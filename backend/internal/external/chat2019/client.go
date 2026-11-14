// Package chat2019 implements one chat2019 connection without automatic reconnect.
package chat2019

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type Config struct {
	RandServerURL string
	FallbackWS    string
	ImgServerURL  string
	// HTTPClient and WSDialer must come from the address guard. There is no unguarded default.
	HTTPClient *http.Client
	WSDialer   func(context.Context, string) (*websocket.Conn, error)
	Logger     *slog.Logger
}
type Self struct{ UID, Nickname, Sex string }

var ErrForceout = errors.New("chat2019: forced out")

// ErrSignAckClosed reports that the connection went away while Dial was still
// waiting for the server's sign-in confirmation (frame code 12).
var ErrSignAckClosed = errors.New("chat2019: connection closed before sign ack")

// signAckCode is the code of the frame chat2019 sends once the sign-in frame has
// been processed ("连接服务器成功！"). Frames sent before it, such as a match
// request, are dropped by the server.
const signAckCode = float64(12)

// signAckTimeout bounds how long Dial waits for the sign ack. A timeout is not an
// error: the connection is returned anyway, so a server that never sends the ack
// stays usable. Tests lower it via SetSignAckTimeoutForTest. 必须小于调用方 match
// 包的 fallbackTimeout（5s，包含取服务器地址和拨号耗时），否则超时兜底来不及生效。
var signAckTimeout = 3 * time.Second

type Conn struct {
	ws        *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	writeMu   sync.Mutex
	closeOnce sync.Once
	onFrame   func(map[string]any)
	onClose   func(bool, error)
	// logger 可为 nil（Dial 时取自 cfg.Logger）。只用于 debug 级诊断，且只能记录
	// 长度之类的元数据：外站帧内容属于对方用户消息，不得落日志。
	logger *slog.Logger
	// signed is closed once the sign ack arrived; signedOnce guards close(signed).
	signed     chan struct{}
	signedOnce sync.Once
	// dialing is true until Dial hands the connection to the caller. While it is
	// set, finish must not invoke onClose: the caller has no Conn yet, so an
	// "unexpected disconnect" callback would be a phantom event.
	dialing atomic.Bool
}

// Dial discovers the WS endpoint and signs in. ctx bounds connection establishment;
// the established connection lives until Close or a transport failure.
//
// Dial only returns once chat2019 has confirmed the sign-in with a code 12 frame
// (or signAckTimeout elapsed). The server drops frames that arrive before it has
// processed the sign frame — in particular the "random" match request the caller
// sends right after Dial — so returning earlier made matching impossible.
func Dial(ctx context.Context, cfg Config, self Self, onFrame func(map[string]any), onClose func(bool, error)) (*Conn, error) {
	if cfg.HTTPClient == nil || cfg.WSDialer == nil {
		return nil, errors.New("chat2019: guarded HTTP client and WS dialer required")
	}
	endpoint := cfg.FallbackWS
	if cfg.RandServerURL != "" {
		var result struct {
			State string `json:"state"`
			Msg   struct {
				Server string `json:"server"`
			} `json:"msg"`
		}
		if err := getJSON(ctx, cfg.HTTPClient, cfg.RandServerURL, &result); err == nil && result.State == "OK" && strings.TrimSpace(result.Msg.Server) != "" {
			endpoint = strings.TrimSpace(result.Msg.Server)
		}
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil || u.Fragment != "" {
		return nil, errors.New("chat2019: invalid WS URL")
	}
	ws, err := cfg.WSDialer(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	c := &Conn{ws: ws, ctx: life, cancel: cancel, onFrame: onFrame, onClose: onClose, logger: cfg.Logger, signed: make(chan struct{})}
	c.dialing.Store(true)
	sign := map[string]any{"act": "sign", "id": self.UID, "name": self.Nickname, "userSex": self.Sex, "address_show": "false", "randomhealthmode": "0", "randomvipsex": "0", "randomvipaddress": "0", "userip": "", "useraddree": "", "randomvipcode": ""}
	if err := c.Send(sign); err != nil {
		cancel()
		_ = ws.CloseNow()
		return nil, err
	}
	go c.readLoop()
	go c.pingLoop()
	// Wait for the server's sign-in confirmation before handing the connection out.
	select {
	case <-c.signed:
	case <-c.ctx.Done():
		// readLoop/pingLoop gave up: the connection is already torn down and onClose
		// stayed silent because dialing is still set.
		return nil, ErrSignAckClosed
	case <-ctx.Done():
		c.finish(true, nil)
		return nil, ctx.Err()
	case <-time.After(signAckTimeout):
		// Keep the old behaviour: a server that never confirms still gets a connection.
	}
	c.dialing.Store(false)
	return c, nil
}

func (c *Conn) Send(frame map[string]any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(ctx, c.ws, frame)
}
func (c *Conn) Close() { c.finish(true, nil) }
func (c *Conn) finish(expected bool, err error) {
	notify := false
	c.closeOnce.Do(func() {
		c.cancel()
		_ = c.ws.CloseNow()
		// While Dial is still waiting the caller has no Conn yet, so reporting an
		// "unexpected disconnect" would be a phantom event for the bridge.
		notify = !c.dialing.Load()
	})
	// Invoke outside Once so callbacks may themselves close the connection.
	if notify && c.onClose != nil {
		c.onClose(expected, err)
	}
}
func (c *Conn) readLoop() {
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 700*time.Second)
		typ, data, err := c.ws.Read(ctx)
		cancel()
		if err != nil {
			// 只有传输层错误才结束连接：读超时、网络/关闭错误都在这里。
			c.finish(false, err)
			return
		}
		var frame map[string]any
		// chat2019 服务端会夹带非对象帧（纯文本、数组、字符串、数字、null、二进制）。
		// liao 的前端对每帧 JSON.parse 用 try/catch 包住、解析失败只跳过该帧，这里保持
		// 同样的容忍度：单帧无法解析不是断线原因，不让它 finish 整条连接。
		// json.Unmarshal("null") 会返回 nil map 而不报错，因此也要判空。
		if typ != websocket.MessageText || json.Unmarshal(data, &frame) != nil || frame == nil {
			if c.logger != nil {
				// 只记录帧长度，绝不记录内容：可能是对方用户消息。
				// 用 Info 而非 Debug：生产日志级别是 Info，Debug 级会被直接丢弃，跳帧等于静默。
				c.logger.Info("chat2019 忽略非对象帧", "count", len(data))
			}
			continue
		}
		if frame["code"] == float64(-3) && frame["forceout"] == true {
			c.finish(false, ErrForceout)
			return
		}
		if c.onFrame != nil {
			c.onFrame(frame)
		}
		// Signal the sign ack only after forwarding it, so a caller released by Dial
		// has already seen the frame.
		if frame["code"] == signAckCode {
			c.signedOnce.Do(func() { close(c.signed) })
		}
	}
}
func (c *Conn) pingLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				c.finish(false, err)
				return
			}
		}
	}
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("chat2019: HTTP status %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out)
}
