package chat2019_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var self = chat2019.Self{UID: strings.Repeat("a", 32), Nickname: "随机名字", Sex: "女"}

func guardConfig(t *testing.T) chat2019.Config {
	t.Helper()
	g, err := external.NewNetGuard(external.Policy{AllowPrivate: true, NoRedirect: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	return chat2019.Config{HTTPClient: g.HTTPClient(time.Second), WSDialer: g.WSDialContext}
}
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("等待假上游超时")
		var zero T
		return zero
	}
}

type closeEvent struct {
	expected bool
	err      error
}

func TestDialSignsPrivatelyAndSendsMatchingFrames(t *testing.T) {
	frames := make(chan map[string]any, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		for range 3 {
			var frame map[string]any
			if wsjson.Read(r.Context(), c, &frame) != nil {
				return
			}
			frames <- frame
		}
		_ = wsjson.Write(r.Context(), c, map[string]any{"code": 15, "content": "matched"})
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	rand := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "OK", "msg": map[string]string{"server": "ws" + strings.TrimPrefix(srv.URL, "http")}})
	}))
	defer rand.Close()
	cfg := guardConfig(t)
	cfg.RandServerURL = rand.URL
	got := make(chan map[string]any, 1)
	closed := make(chan closeEvent, 2)
	conn, err := chat2019.Dial(context.Background(), cfg, self, func(v map[string]any) { got <- v }, func(e bool, err error) { closed <- closeEvent{e, err} })
	if err != nil {
		t.Fatalf("Dial未实现: %v", err)
	}
	defer conn.Close()
	sign := receive(t, frames)
	want := map[string]any{"act": "sign", "id": self.UID, "name": self.Nickname, "userSex": "女", "address_show": "false", "randomhealthmode": "0", "randomvipsex": "0", "randomvipaddress": "0", "userip": "", "useraddree": "", "randomvipcode": ""}
	a, _ := json.Marshal(sign)
	b, _ := json.Marshal(want)
	if string(a) != string(b) {
		t.Fatalf("sign=%s want=%s", a, b)
	}
	for _, frame := range []map[string]any{{"act": "random", "id": self.UID, "userAge": "23"}, {"act": "randomOut", "id": self.UID, "msg": "cancel-id"}} {
		if err := conn.Send(frame); err != nil {
			t.Fatal(err)
		}
		actual := receive(t, frames)
		a, _ := json.Marshal(frame)
		b, _ := json.Marshal(actual)
		if string(a) != string(b) {
			t.Fatalf("frame=%s want=%s", b, a)
		}
	}
	if receive(t, got)["code"] != float64(15) {
		t.Fatal("未转发下行帧")
	}
	conn.Close()
	conn.Close()
	if event := receive(t, closed); !event.expected {
		t.Fatalf("主动关闭=%+v", event)
	}
	select {
	case event := <-closed:
		t.Fatalf("重复onClose: %+v", event)
	case <-time.After(20 * time.Millisecond):
	}
	if err := conn.Send(map[string]any{"act": "random"}); err == nil {
		t.Fatal("关闭后写入必须返回错误")
	}
}
func TestForceoutAndUnexpectedDisconnect(t *testing.T) {
	for _, force := range []bool{true, false} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer c.CloseNow()
				var sign any
				if wsjson.Read(r.Context(), c, &sign) != nil {
					return
				}
				if force {
					_ = wsjson.Write(r.Context(), c, map[string]any{"code": -3, "forceout": true})
				}
			}))
			defer srv.Close()
			cfg := guardConfig(t)
			cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
			closed := make(chan closeEvent, 2)
			c, err := chat2019.Dial(context.Background(), cfg, self, nil, func(e bool, err error) { closed <- closeEvent{e, err} })
			if err != nil {
				t.Fatalf("Dial: %v", err)
			}
			defer c.Close()
			event := receive(t, closed)
			if event.expected || event.err == nil {
				t.Fatalf("关闭信息=%+v", event)
			}
			if force && !errors.Is(event.err, chat2019.ErrForceout) {
				t.Fatalf("forceout=%v", event.err)
			}
			c.Close()
			select {
			case <-closed:
				t.Fatal("onClose重复")
			case <-time.After(10 * time.Millisecond):
			}
		})
	}
}
func TestDialRejectsInvalidWSAndFallsBackOnDiscoveryFailure(t *testing.T) {
	for _, address := range []string{"http://example.com", "file:///tmp/socket", "ws:///empty", "ws://user:pass@example.com", "ws://example.com/#fragment"} {
		t.Run(address, func(t *testing.T) {
			cfg := guardConfig(t)
			var called atomic.Bool
			cfg.WSDialer = func(context.Context, string) (*websocket.Conn, error) {
				called.Store(true)
				return nil, errors.New("called")
			}
			cfg.FallbackWS = address
			if _, err := chat2019.Dial(context.Background(), cfg, self, nil, nil); err == nil {
				t.Fatal("非法WS地址被接受")
			}
			if called.Load() {
				t.Fatal("非法WS地址到达拨号器")
			}
		})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", http.StatusServiceUnavailable) }))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.RandServerURL = srv.URL
	cfg.FallbackWS = "ws://fallback.test/"
	sentinel := errors.New("fallback dial")
	cfg.WSDialer = func(_ context.Context, u string) (*websocket.Conn, error) {
		if u != cfg.FallbackWS {
			t.Errorf("fallback=%s", u)
		}
		return nil, sentinel
	}
	if _, err := chat2019.Dial(context.Background(), cfg, self, nil, nil); !errors.Is(err, sentinel) {
		t.Fatalf("未使用fallback: %v", err)
	}
}

func TestConnectionSendsWebSocketPing(t *testing.T) {
	ping := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OnPingReceived: func(context.Context, []byte) bool {
			select {
			case ping <- struct{}{}:
			default:
			}
			return true
		}})
		if err != nil {
			return
		}
		defer c.CloseNow()
		_, _, _ = c.Read(r.Context())
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
	c, err := chat2019.Dial(context.Background(), cfg, self, nil, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()
	select {
	case <-ping:
	case <-time.After(33 * time.Second):
		t.Fatal("30秒内未发送WS ping")
	}
}
