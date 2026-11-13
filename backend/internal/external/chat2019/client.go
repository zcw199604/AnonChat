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

type Conn struct {
	ws        *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	writeMu   sync.Mutex
	closeOnce sync.Once
	onFrame   func(map[string]any)
	onClose   func(bool, error)
}

// Dial discovers the WS endpoint and signs in. ctx bounds connection establishment;
// the established connection lives until Close or a transport failure.
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
	c := &Conn{ws: ws, ctx: life, cancel: cancel, onFrame: onFrame, onClose: onClose}
	sign := map[string]any{"act": "sign", "id": self.UID, "name": self.Nickname, "userSex": self.Sex, "address_show": "false", "randomhealthmode": "0", "randomvipsex": "0", "randomvipaddress": "0", "userip": "", "useraddree": "", "randomvipcode": ""}
	if err := c.Send(sign); err != nil {
		cancel()
		_ = ws.CloseNow()
		return nil, err
	}
	go c.readLoop()
	go c.pingLoop()
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
	won := false
	c.closeOnce.Do(func() { won = true; c.cancel(); _ = c.ws.CloseNow() })
	// Invoke outside Once so callbacks may themselves close the connection.
	if won && c.onClose != nil {
		c.onClose(expected, err)
	}
}
func (c *Conn) readLoop() {
	for {
		ctx, cancel := context.WithTimeout(c.ctx, 700*time.Second)
		var frame map[string]any
		err := wsjson.Read(ctx, c.ws, &frame)
		cancel()
		if err != nil {
			c.finish(false, err)
			return
		}
		if frame["code"] == float64(-3) && frame["forceout"] == true {
			c.finish(false, ErrForceout)
			return
		}
		if c.onFrame != nil {
			c.onFrame(frame)
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
