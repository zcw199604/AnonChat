package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/google/uuid"
)

func openClient(connectCtx, life context.Context, o options, ep endpoints, guards guardFactory, store *stateStore, id string, emit func(event)) (probeClient, error) {
	// The ported client logs upstream errors/data; probe output must pass through
	// our redactor only, so library logs are deliberately discarded.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if o.provider == "chat2019" {
		guard, err := guards(external.Policy{NoRedirect: true}, "")
		if err != nil {
			return nil, err
		}
		cfg := chat2019.Config{RandServerURL: ep.chatRand, FallbackWS: ep.chatWS, ImgServerURL: ep.chatImg, HTTPClient: guard.HTTPClient(10 * time.Second), WSDialer: guard.WSDialContext, Logger: logger}
		self := chat2019.Self{UID: uuid.NewString(), Nickname: "匿名用户", Sex: "男"}
		conn, err := chat2019.Dial(connectCtx, cfg, self, func(f map[string]any) { emit(event{frame: f}) }, func(expected bool, err error) {
			if !expected {
				if err == nil {
					err = errors.New("connection closed")
				}
				emit(event{err: err, kind: "disconnected"})
			}
		})
		if err != nil {
			return nil, err
		}
		return &chatProbe{conn: conn, self: self, media: chat2019.NewMedia(cfg, 100<<20)}, nil
	}
	base, err := url.Parse(ep.wfBase)
	if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") {
		return nil, errors.New("晚风 BaseURL 非法")
	}
	hosts := append([]string{base.Hostname()}, ep.wfHosts...)
	if ep.wfWS != "" {
		u, err := url.Parse(ep.wfWS)
		if err != nil || u.Hostname() == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
			return nil, errors.New("晚风 WSURL 非法")
		}
		hosts = append(hosts, u.Hostname())
	}
	guard, err := guards(external.Policy{AllowedHosts: hosts}, o.proxy)
	if err != nil {
		return nil, err
	}
	proto, err := wanfeng.DefaultProtocol().ApplyOverrides(ep.wfOverrides)
	if err != nil {
		return nil, errors.New("晚风协议覆盖非法")
	}
	api := wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: ep.wfBase, WSURL: ep.wfWS, HTTPClient: guard.HTTPClient(10 * time.Second), Protocol: proto, Logger: logger})
	hidden := false
	s := &wfProbe{api: api, life: life, id: id}
	s.client = wanfeng.NewClient(wanfeng.ClientConfig{
		IdentityID: id, API: api, Bindings: store, Dialer: guard.GorillaDialer(), Logger: logger,
		Profile: wanfeng.Profile{Nickname: "匿名用户", Gender: "male", ShowCity: &hidden},
		Media:   s,
		Emitter: probeEmitter{emit: emit},
		OnConnState: func(state wanfeng.ConnState, err error) {
			if state == wanfeng.ConnReady {
				binding, err := store.Get(life, id)
				if err != nil || binding == nil || binding.UpstreamUID == "" {
					emit(event{err: errors.New("missing persisted binding"), kind: "state_failed"})
					return
				}
				api.SetUploadUserID(binding.UpstreamUID)
				// As in the server, hide location before issuing join_match.
				if err := s.client.PushShowCity(false); err != nil {
					emit(event{err: err, kind: "send_failed"})
					return
				}
				emit(event{ready: true})
			} else {
				if err == nil {
					err = errors.New("connection stopped")
				}
				emit(event{err: err, kind: "disconnected"})
			}
		},
		OnCommandFailed: func(cmd wanfeng.OutgoingCommand, err error) {
			kind := "send_failed"
			if cmd.Media != nil {
				kind = "media_failed"
			}
			emit(event{err: err, kind: kind})
		},
	})
	s.client.Start(life)
	return s, nil
}

type chatProbe struct {
	conn  *chat2019.Conn
	self  chat2019.Self
	media *chat2019.Media
}

func (s *chatProbe) startMatch() error {
	return s.conn.Send(map[string]any{"act": "random", "id": s.self.UID, "userAge": "0"})
}
func (s *chatProbe) sendImage(ctx context.Context, peer, nick string, image imageFile) error {
	path, err := s.media.Upload(ctx, s.self, image.name, image.data)
	if err != nil {
		return err
	}
	return s.conn.Send(map[string]any{"act": fmt.Sprintf("touser_%s_%s", peer, nick), "id": s.self.UID, "msg": "[" + path + "]"})
}
func (s *chatProbe) close() {
	_ = s.conn.Send(map[string]any{"act": "randomOut", "id": s.self.UID, "msg": uuid.NewString()})
	s.conn.Close()
}

type probeEmitter struct{ emit func(event) }

func (e probeEmitter) Emit(_ string, f map[string]any) { e.emit(event{frame: f}) }
func (e probeEmitter) EmitFatal(_ string, f map[string]any) {
	e.emit(event{frame: f})
	e.emit(event{err: errors.New("upstream fatal"), kind: "upstream_fatal"})
}

type wfProbe struct {
	client *wanfeng.Client
	api    *wanfeng.APIClient
	life   context.Context
	id     string
	mu     sync.Mutex
	image  imageFile
	token  string
}

func (s *wfProbe) startMatch() error {
	frame, err := wanfeng.ParseClientFrame(`{"act":"random"}`)
	if err != nil {
		return err
	}
	cmd, ok := s.api.Protocol().BuildCommand(frame, "")
	if !ok {
		return errors.New("unsupported match command")
	}
	s.client.SendCommand(cmd)
	return nil
}
func (s *wfProbe) sendImage(_ context.Context, peer, _ string, image imageFile) error {
	token := uuid.NewString()
	s.mu.Lock()
	s.image = image
	s.token = token
	s.mu.Unlock()
	s.client.SendCommand(s.api.Protocol().MediaCommand(peer, "image", token))
	return nil
}
func (s *wfProbe) MirrorOutbound(id, kind, token string) (string, error) {
	s.mu.Lock()
	if id != s.id || kind != "image" || token == "" || token != s.token {
		s.mu.Unlock()
		return "", errors.New("unknown probe media")
	}
	image := s.image
	s.token = ""
	s.image = imageFile{}
	s.mu.Unlock()
	ctx, stop := context.WithTimeout(s.life, 10*time.Second)
	defer stop()
	result, err := s.api.UploadMedia(ctx, image.name, image.mime, image.data, s.api.Protocol().BucketImage)
	if err != nil {
		return "", err
	}
	return result.URL, nil
}
func (s *wfProbe) MirrorInbound(_, _, _ string) (string, error) {
	return "", errors.New("probe does not download inbound media")
}
func (s *wfProbe) close() { s.client.Close() }
