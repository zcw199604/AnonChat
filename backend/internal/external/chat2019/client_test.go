package chat2019_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
		var frame map[string]any
		if wsjson.Read(r.Context(), c, &frame) != nil {
			return
		}
		frames <- frame
		// chat2019 先回 code 12 确认登录，之后才处理 random。
		if wsjson.Write(r.Context(), c, map[string]any{"code": 12, "content": "连接服务器成功！"}) != nil {
			return
		}
		for range 2 {
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
	got := make(chan map[string]any, 4)
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
	if ack := receive(t, got); ack["code"] != float64(12) {
		t.Fatalf("未转发登录确认帧: %v", ack)
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
	// 该假上游从不发 code 12，缩短等待以免每个子测试都等满 signAckTimeout。
	restore := chat2019.SetSignAckTimeoutForTest(50 * time.Millisecond)
	defer restore()
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
				// 等 Dial 因 signAckTimeout 返回连接后再断线/踢出，回调才归属调用方。
				time.Sleep(150 * time.Millisecond)
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
		var sign map[string]any
		if wsjson.Read(r.Context(), c, &sign) != nil {
			return
		}
		if wsjson.Write(r.Context(), c, map[string]any{"code": 12, "content": "连接服务器成功！"}) != nil {
			return
		}
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

func TestDialWaitsForSignAckBeforeReturning(t *testing.T) {
	const ackDelay = 200 * time.Millisecond
	frames := make(chan map[string]any, 4)
	ackSent := make(chan time.Time, 1)
	randomAt := make(chan time.Time, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var sign map[string]any
		if wsjson.Read(r.Context(), c, &sign) != nil {
			return
		}
		frames <- sign
		time.Sleep(ackDelay)
		if wsjson.Write(r.Context(), c, map[string]any{"code": 12, "content": "连接服务器成功！"}) != nil {
			return
		}
		sent := time.Now()
		ackSent <- sent
		var random map[string]any
		if wsjson.Read(r.Context(), c, &random) != nil {
			return
		}
		randomAt <- time.Now()
		frames <- random
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
	got := make(chan map[string]any, 4)
	start := time.Now()
	conn, err := chat2019.Dial(context.Background(), cfg, self, func(v map[string]any) { got <- v }, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if elapsed < ackDelay*9/10 {
		t.Fatalf("Dial 未等 sign ack 就返回, 耗时=%v", elapsed)
	}
	// Dial 返回之时, onFrame 必须已经收到 code 12。
	select {
	case frame := <-got:
		if frame["code"] != float64(12) {
			t.Fatalf("Dial 返回前的下行帧=%v", frame)
		}
	default:
		t.Fatal("Dial 返回时 onFrame 尚未收到 code 12")
	}
	if sign := receive(t, frames); sign["act"] != "sign" {
		t.Fatalf("首帧=%v", sign)
	}
	if err := conn.Send(map[string]any{"act": "random", "id": self.UID, "userAge": "0"}); err != nil {
		t.Fatal(err)
	}
	random := receive(t, frames)
	if random["act"] != "random" || random["id"] != self.UID {
		t.Fatalf("random帧=%v", random)
	}
	if ackAt, at := receive(t, ackSent), receive(t, randomAt); !at.After(ackAt) {
		t.Fatalf("random 在 code 12 之前就到达假上游: ack=%v random=%v", ackAt, at)
	}
}

func TestDialReturnsConnWhenSignAckNeverArrives(t *testing.T) {
	restore := chat2019.SetSignAckTimeoutForTest(100 * time.Millisecond)
	defer restore()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		var sign map[string]any
		if wsjson.Read(r.Context(), c, &sign) != nil {
			return
		}
		// 永不回 code 12, 只保持连接。
		_, _, _ = c.Read(r.Context())
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
	start := time.Now()
	conn, err := chat2019.Dial(context.Background(), cfg, self, nil, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("超时不应报错: %v", err)
	}
	defer conn.Close()
	if elapsed < 80*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("未按 signAckTimeout 返回连接, 耗时=%v", elapsed)
	}
	if err := conn.Send(map[string]any{"act": "random", "id": self.UID, "userAge": "0"}); err != nil {
		t.Fatalf("返回的连接不可用: %v", err)
	}
}

func TestDialFailsSilentlyWhenConnectionClosesBeforeSignAck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		var sign map[string]any
		_ = wsjson.Read(r.Context(), c, &sign)
		// 收到 sign 后立刻断开, 且不发 code 12。
		c.CloseNow()
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
	closed := make(chan closeEvent, 2)
	conn, err := chat2019.Dial(context.Background(), cfg, self, nil, func(e bool, err error) { closed <- closeEvent{e, err} })
	if err == nil {
		conn.Close()
		t.Fatal("sign ack 之前断线必须返回错误")
	}
	select {
	case event := <-closed:
		t.Fatalf("Dial 失败时不应回调 onClose: %+v", event)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestReadLoopSkipsNonObjectFrames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		var sign map[string]any
		if wsjson.Read(ctx, c, &sign) != nil {
			return
		}
		if wsjson.Write(ctx, c, map[string]any{"code": 12, "content": "连接服务器成功！"}) != nil {
			return
		}
		// 生产上 chat2019 会在正常帧之间夹带非 JSON 对象的帧：纯文本、数组、字符串、
		// 二进制、null。liao 前端只跳过这些帧，连接必须继续可用。
		for _, raw := range []struct {
			typ  websocket.MessageType
			data string
		}{
			{websocket.MessageText, "hello"},
			{websocket.MessageText, "[1,2]"},
			{websocket.MessageText, `"x"`},
			{websocket.MessageBinary, "\x00\x01"},
			{websocket.MessageText, "null"},
		} {
			if err := c.Write(ctx, raw.typ, []byte(raw.data)); err != nil {
				return
			}
		}
		if wsjson.Write(ctx, c, map[string]any{"code": 15, "sel_userid": "u"}) != nil {
			return
		}
		_, _, _ = c.Read(ctx)
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.FallbackWS = "ws" + strings.TrimPrefix(srv.URL, "http")
	var logs bytes.Buffer
	cfg.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	got := make(chan map[string]any, 8)
	closed := make(chan closeEvent, 2)
	conn, err := chat2019.Dial(context.Background(), cfg, self, func(v map[string]any) { got <- v }, func(e bool, err error) { closed <- closeEvent{e, err} })
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	if ack := receive(t, got); ack["code"] != float64(12) {
		t.Fatalf("未转发登录确认帧: %v", ack)
	}
	frame := receive(t, got)
	if frame["code"] != float64(15) || frame["sel_userid"] != "u" {
		t.Fatalf("非对象帧之后的正常帧丢失: %v", frame)
	}
	select {
	case event := <-closed:
		t.Fatalf("非对象帧不应断开连接: %+v", event)
	case <-time.After(100 * time.Millisecond):
	}
	if out := logs.String(); !strings.Contains(out, "chat2019 忽略非对象帧") {
		t.Fatalf("未记录跳过日志: %s", out)
	} else if !strings.Contains(out, "count=5") {
		t.Fatalf("跳过日志缺少帧长度: %s", out)
	}
	// 日志只记长度，绝不落帧内容。
	for _, content := range []string{"hello", "[1,2]", "sel_userid"} {
		if strings.Contains(logs.String(), content) {
			t.Fatalf("跳过日志泄露帧内容 %q: %s", content, logs.String())
		}
	}
	// 跳帧后连接仍可写。
	if err := conn.Send(map[string]any{"act": "randomOut", "id": self.UID, "msg": "cancel-id"}); err != nil {
		t.Fatalf("跳帧后写入失败: %v", err)
	}
}
