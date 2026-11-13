package wanfeng

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPortIsIPBanMessage(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"您的 IP 已被封禁", true}, {"您的ip已被封禁，请稍后", true}, {"ordinary timeout", false}, {"", false},
	} {
		if got := IsIPBanMessage(tc.msg); got != tc.want {
			t.Errorf("IsIPBanMessage(%q)=%v want %v", tc.msg, got, tc.want)
		}
	}
}

func TestPortExplicitCommandsDoNotParseTextAsMedia(t *testing.T) {
	p := DefaultProtocol()
	text := p.TextCommand("peer", "hello")
	legacy, _ := p.BuildCommand(ClientFrame{Act: "touser_peer_name", Msg: "hello"}, "peer")
	if PickString(text.Payload, "localTid") == "" {
		t.Error("TextCommand 缺少 localTid")
	}
	delete(text.Payload, "localTid")
	delete(legacy.Payload, "localTid")
	if !reflect.DeepEqual(text, legacy) {
		t.Errorf("文本 payload 不一致: got=%+v want=%+v", text, legacy)
	}
	literal := p.TextCommand("peer", "[x.jpg]")
	if literal.Media != nil || literal.Payload["type"] != "text" || literal.Payload["content"] != "[x.jpg]" {
		t.Errorf("文本不能成为媒体: %+v", literal)
	}
	media := p.MediaCommand("peer", "image", "x.jpg")
	legacyMedia, _ := p.BuildCommand(ClientFrame{Act: "touser_peer_name", Msg: "[x.jpg]"}, "peer")
	if PickString(media.Payload, "localTid") == "" {
		t.Error("MediaCommand 缺少 localTid")
	}
	delete(media.Payload, "localTid")
	delete(legacyMedia.Payload, "localTid")
	if !reflect.DeepEqual(media, legacyMedia) {
		t.Errorf("媒体 payload 不一致: got=%+v want=%+v", media, legacyMedia)
	}
}

func TestPortClonePreservesRedirectGuard(t *testing.T) {
	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		reached = true
		_, _ = io.WriteString(w, "unexpected")
	}))
	defer srv.Close()
	blocked := errors.New("redirect blocked")
	api := NewAPIClient(APIClientConfig{BaseURL: srv.URL, HTTPClient: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return blocked }}})
	clone := api.Clone()
	if clone.http.CheckRedirect == nil {
		t.Error("Clone 丢失 CheckRedirect")
	}
	resp, err := clone.http.Get(srv.URL + "/start")
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if !errors.Is(err, blocked) || reached {
		t.Fatalf("克隆客户端绕过重定向守卫: reached=%v err=%v", reached, err)
	}
}

func TestPortConnStateReadyLostStopped(t *testing.T) {
	f := newFakeUpstream(t)
	states := make(chan ConnState, 8)
	var client *Client
	client = NewClient(ClientConfig{IdentityID: "port", API: NewAPIClient(APIClientConfig{BaseURL: f.URL(), WSURL: "ws" + strings.TrimPrefix(f.URL(), "http") + "/ws"}), Bindings: NewMemoryBindingStore(), Emitter: &recordingEmitter{}, OnConnState: func(state ConnState, err error) {
		states <- state
		if state == ConnLost {
			client.Close()
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	receive := func(want ConnState) {
		t.Helper()
		select {
		case got := <-states:
			if got != want {
				t.Fatalf("state=%v want=%v", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("未通知连接状态 %v", want)
		}
	}
	receive(ConnReady)
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	_ = conn.Close()
	receive(ConnLost)
	receive(ConnStopped)
	client.Close()
	select {
	case got := <-states:
		t.Fatalf("重复停止事件: %v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

type portFailBindings struct{}

func (portFailBindings) Get(context.Context, string) (*Binding, error) {
	return nil, errors.New("binding unavailable")
}
func (portFailBindings) Save(context.Context, Binding) error { return nil }

func TestPortConnStoppedOnFatalSetup(t *testing.T) {
	states := make(chan ConnState, 4)
	c := NewClient(ClientConfig{Bindings: portFailBindings{}, OnConnState: func(s ConnState, e error) { states <- s }})
	c.Start(context.Background())
	defer c.Close()
	select {
	case s := <-states:
		if s != ConnStopped {
			t.Fatalf("state=%v want ConnStopped", s)
		}
	case <-time.After(time.Second):
		t.Fatal("致命错误后未通知停止")
	}
}

func TestPortCommandFailedOnQueueOverflow(t *testing.T) {
	failed := make(chan OutgoingCommand, 4)
	c := NewClient(ClientConfig{OnCommandFailed: func(cmd OutgoingCommand, err error) {
		if err == nil {
			t.Error("缺少失败原因")
		}
		failed <- cmd
	}})
	defer c.Close()
	for i := 0; i < clientOutboxSize; i++ {
		c.SendCommand(OutgoingCommand{Action: "old"})
	}
	c.SendCommand(OutgoingCommand{Action: "new"})
	select {
	case cmd := <-failed:
		if cmd.Action != "old" {
			t.Fatalf("应报告被丢弃的旧动作，实际 %+v", cmd)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("队列满未通知被丢弃动作")
	}
}

func TestPortCommandFailedOnDeliveryAndMediaError(t *testing.T) {
	for _, media := range []bool{false, true} {
		t.Run(map[bool]string{false: "delivery", true: "media"}[media], func(t *testing.T) {
			failed := make(chan error, 4)
			c := NewClient(ClientConfig{API: NewAPIClient(APIClientConfig{}), Emitter: &recordingEmitter{}, OnCommandFailed: func(cmd OutgoingCommand, err error) { failed <- err }})
			c.ready = true
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			defer c.Close()
			go c.dispatchLoop(ctx)
			cmd := OutgoingCommand{Action: "send_message", Payload: map[string]any{"targetUid": "peer"}}
			if media {
				cmd.Media = &OutgoingMedia{Kind: "image", Path: "x.jpg"}
			}
			c.SendCommand(cmd)
			select {
			case err := <-failed:
				if err == nil {
					t.Fatal("缺少失败原因")
				}
				if media && !strings.Contains(err.Error(), "媒体") {
					t.Fatalf("媒体失败原因: %v", err)
				}
			case <-time.After(300 * time.Millisecond):
				t.Fatal("下发失败未调用 OnCommandFailed")
			}
		})
	}
}
