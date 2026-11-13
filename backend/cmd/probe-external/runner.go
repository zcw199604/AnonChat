package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external"
)

type endpoints struct {
	chatRand, chatWS, chatImg, wfBase, wfWS, wfOverrides string
	wfHosts                                              []string
}
type guardFactory func(external.Policy, string) (*external.NetGuard, error)

func standardGuard(p external.Policy, proxy string) (*external.NetGuard, error) {
	return external.NewNetGuard(p, proxy)
}

// Same endpoint environment variables/defaults as the server, without requiring
// DATABASE_URL or initializing any database-backed service.
func loadEndpoints(getenv func(string) string) endpoints {
	value := func(key, fallback string) string {
		if s := strings.TrimSpace(getenv(key)); s != "" {
			return s
		}
		return fallback
	}
	return endpoints{
		chatRand:    value("EXTERNAL_CHAT2019_RAND_SERVER_URL", "http://v1.chat2019.cn/Act/WebService.asmx/getRandServer?ServerInfo=serversdeskry&_="),
		chatWS:      value("EXTERNAL_CHAT2019_FALLBACK_WS", ""),
		chatImg:     value("EXTERNAL_CHAT2019_IMG_SERVER_URL", "http://v1.chat2019.cn/asmx/method.asmx/getImgServer"),
		wfBase:      value("EXTERNAL_WANFENG_BASE_URL", "https://wanfengniming.store"),
		wfWS:        value("EXTERNAL_WANFENG_WS_URL", ""),
		wfOverrides: value("EXTERNAL_WANFENG_PROTOCOL_OVERRIDES", ""),
		wfHosts:     strings.FieldsFunc(value("EXTERNAL_WANFENG_MEDIA_HOSTS", ""), func(r rune) bool { return r == ',' || r == ' ' }),
	}
}

type imageFile struct {
	name, mime string
	data       []byte
}
type probeClient interface {
	startMatch() error
	sendImage(context.Context, string, string, imageFile) error
	close()
}
type event struct {
	frame map[string]any
	ready bool
	err   error
	kind  string
}
type summary struct {
	Type      string `json:"type"`
	Provider  string `json:"provider"`
	Identity  string `json:"identity"`
	Connected bool   `json:"connected"`
	ConnectMS int64  `json:"connectMs"`
	Matched   bool   `json:"matched"`
	MatchMS   int64  `json:"matchMs"`
	ErrorType string `json:"errorType"`
}

func run(ctx context.Context, o options, ep endpoints, guards guardFactory, out io.Writer) error {
	var image imageFile
	if o.sendImage != "" {
		data, err := os.ReadFile(o.sendImage)
		if err != nil {
			return errors.New("无法读取 send-image 文件")
		}
		mime := http.DetectContentType(data)
		switch mime {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return errors.New("send-image 只允许 JPEG/PNG/GIF/WebP 图片")
		}
		image = imageFile{name: filepath.Base(o.sendImage), mime: mime, data: data}
	}
	var store *stateStore
	if o.provider == "wanfeng" {
		var err error
		store, err = openState(o.state)
		if err != nil {
			return err
		}
	}
	output := newOutput(out)
	var wg sync.WaitGroup
	failed := make(chan bool, o.n)
	for i := 1; i <= o.n; i++ {
		id := fmt.Sprintf("probe-%d", i)
		wg.Go(func() {
			s := probe(ctx, o, ep, guards, store, id, image, output)
			output.write(s)
			failed <- s.ErrorType != ""
		})
	}
	wg.Wait()
	close(failed)
	if output.err != nil {
		return errors.New("无法写入探测输出")
	}
	if store != nil && store.Err() != nil {
		return store.Err()
	}
	for fail := range failed {
		if fail {
			return errors.New("部分探测失败，见 summary.errorType")
		}
	}
	return nil
}

func probe(ctx context.Context, o options, ep endpoints, guards guardFactory, store *stateStore, id string, image imageFile, out *output) (s summary) {
	s = summary{Type: "summary", Provider: o.provider, Identity: id}
	events := make(chan event, 128)
	overflow := make(chan struct{}, 1)
	emit := func(e event) {
		select {
		case events <- e:
		default:
			select {
			case overflow <- struct{}{}:
			default:
			}
		}
	}
	start := time.Now()
	life, cancel := context.WithCancel(ctx)
	defer cancel()
	connectCtx, connectCancel := context.WithTimeout(life, 10*time.Second)
	client, err := openClient(connectCtx, life, o, ep, guards, store, id, emit)
	if err != nil {
		connectCancel()
		s.ConnectMS = time.Since(start).Milliseconds()
		s.ErrorType = errorType(err, "connect_failed")
		return
	}
	defer client.close()
	// chat2019 Dial signs in synchronously; wanfeng reports readiness via event.
	if o.provider == "wanfeng" {
		ready := false
		for !ready {
			select {
			case e := <-events:
				if e.err != nil {
					connectCancel()
					s.ErrorType = errorType(e.err, e.kind)
					s.ConnectMS = time.Since(start).Milliseconds()
					return
				}
				if e.frame != nil {
					out.write(map[string]any{"type": "frame", "provider": o.provider, "identity": id, "frame": safeFrame(e.frame)})
				}
				ready = e.ready
			case <-connectCtx.Done():
				connectCancel()
				s.ErrorType = errorType(connectCtx.Err(), "connect_failed")
				s.ConnectMS = time.Since(start).Milliseconds()
				return
			case <-overflow:
				connectCancel()
				s.ErrorType = "frame_overflow"
				return
			}
		}
	}
	connectCancel()
	s.Connected = true
	s.ConnectMS = time.Since(start).Milliseconds()
	if store != nil && store.Err() != nil {
		s.ErrorType = "state_failed"
		return
	}
	matchStart := time.Now()
	if err := client.startMatch(); err != nil {
		s.ErrorType = errorType(err, "send_failed")
		return
	}
	timer := time.NewTimer(o.match)
	defer timer.Stop()
	for {
		select {
		case e := <-events:
			if e.err != nil {
				s.ErrorType = errorType(e.err, e.kind)
				return
			}
			if e.frame == nil {
				continue
			}
			out.write(map[string]any{"type": "frame", "provider": o.provider, "identity": id, "frame": safeFrame(e.frame)})
			// chat2019 can also use code=15 for user info; require a peer ID.
			if !s.Matched && frameCode(e.frame) == 15 {
				peer := stringField(e.frame, "sel_userid")
				if peer == "" {
					continue
				}
				s.Matched = true
				s.MatchMS = time.Since(matchStart).Milliseconds()
				if len(image.data) > 0 {
					// The event loop continues printing inbound frames during uploads.
					wgDone := make(chan struct{})
					go func() {
						defer close(wgDone)
						sendCtx, stop := context.WithTimeout(life, 10*time.Second)
						defer stop()
						if err := client.sendImage(sendCtx, peer, stringField(e.frame, "sel_userNikename"), image); err != nil {
							emit(event{err: err, kind: "media_failed"})
						}
					}()
					defer func() { cancel(); <-wgDone }()
				}
			}
		case <-timer.C:
			if !s.Matched {
				s.ErrorType = "match_timeout"
				s.MatchMS = time.Since(matchStart).Milliseconds()
			}
			return
		case <-ctx.Done():
			s.ErrorType = "cancelled"
			return
		case <-overflow:
			s.ErrorType = "frame_overflow"
			return
		}
	}
}

func frameCode(f map[string]any) int {
	switch v := f["code"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	}
	return 0
}
func stringField(f map[string]any, key string) string { s, _ := f[key].(string); return s }
func errorType(err error, fallback string) string {
	if errors.Is(err, external.ErrAddressRejected) {
		return "address_rejected"
	}
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "timeout"
	}
	if fallback == "" {
		return "disconnected"
	}
	return fallback
}
