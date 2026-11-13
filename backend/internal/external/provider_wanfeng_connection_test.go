package external

import (
	"context"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/gorilla/websocket"
)

type providerWFFrame struct {
	action  string
	payload map[string]any
}

func TestProviderWanfengWaitsReadyHidesCityAndStopsOnFirstLost(t *testing.T) {
	proto := wanfeng.DefaultProtocol()
	bindings := wanfeng.NewMemoryBindingStore()
	if err := bindings.Save(context.Background(), wanfeng.Binding{IdentityID: "local", UpstreamUID: "remote", DeviceID: "device", Nickname: "随机绑定昵称"}); err != nil {
		t.Fatal(err)
	}
	loggedIn := make(chan struct{})
	releaseReady := make(chan struct{})
	frames := make(chan providerWFFrame, 20)
	serverConn := make(chan *websocket.Conn, 1)
	var connections atomic.Int32
	var key []byte
	const sid = "provider-test-session"
	mux := http.NewServeMux()
	mux.HandleFunc(proto.HandshakePath, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pub string `json:"pub"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		kp, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		pubRaw, err := base64.StdEncoding.DecodeString(req.Pub)
		if err != nil {
			t.Error(err)
			return
		}
		pub, err := ecdh.P256().NewPublicKey(pubRaw)
		if err != nil {
			t.Error(err)
			return
		}
		shared, err := kp.ECDH(pub)
		if err != nil {
			t.Error(err)
			return
		}
		key, err = hkdf.Key(sha256.New, shared, []byte(sid), proto.HKDFInfo, 32)
		if err != nil {
			t.Error(err)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"sid": sid, "pub": base64.StdEncoding.EncodeToString(kp.PublicKey().Bytes()), "exp": time.Now().Add(time.Hour).UnixMilli()}})
	})
	api := wanfeng.NewAPIClient(wanfeng.APIClientConfig{Protocol: proto})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		connections.Add(1)
		serverConn <- conn
		first := true
		for {
			var raw struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if err := conn.ReadJSON(&raw); err != nil {
				return
			}
			plain, err := api.OpenFrameData(&wanfeng.Session{ID: sid, Key: key}, raw.Data)
			if err != nil {
				t.Error(err)
				return
			}
			var payload map[string]any
			if err := json.Unmarshal(plain, &payload); err != nil {
				t.Error(err)
				return
			}
			frames <- providerWFFrame{raw.Type, payload}
			if first {
				first = false
				close(loggedIn)
				<-releaseReady
				if err := conn.WriteJSON(map[string]any{"type": proto.EventConnected, "data": map[string]any{"uid": "remote", "state": proto.StateIdle}}); err != nil {
					return
				}
			}
		}
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	guard, err := NewNetGuard(Policy{AllowPrivate: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	api = wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: server.URL, WSURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws", Protocol: proto, HTTPClient: guard.HTTPClient(time.Second)})
	states := make(chan wanfeng.ConnState, 10)
	openCtx, cancelOpen := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelOpen()
	type result struct {
		s   *wanfengSession
		err error
	}
	opened := make(chan result, 1)
	go func() {
		s, err := connectWanfeng(openCtx, wanfeng.ClientConfig{IdentityID: "local", Profile: wanfeng.Profile{Nickname: "随机绑定昵称", Gender: "female", Age: 25}, API: api, Bindings: bindings, Dialer: guard.GorillaDialer(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), OnConnState: func(state wanfeng.ConnState, _ error) { states <- state }})
		opened <- result{s, err}
	}()
	select {
	case <-loggedIn:
	case <-openCtx.Done():
		t.Fatal("没有收到登录")
	}
	select {
	case r := <-opened:
		t.Fatalf("尚未Ready便返回: %+v", r)
	default:
	}
	close(releaseReady)
	var s *wanfengSession
	select {
	case r := <-opened:
		if r.err != nil {
			t.Fatal(r.err)
		}
		s = r.s
	case <-openCtx.Done():
		t.Fatal("未返回就绪连接")
	}
	defer s.close()
	if s.selfID() != "local" {
		t.Fatalf("回显识别应使用 ClientConfig.IdentityID: selfID=%q, want local", s.selfID())
	}
	if s.uid != "remote" {
		t.Fatalf("协议命令应保留远端 UID: uid=%q", s.uid)
	}
	cancelOpen()
	if err := s.startMatch(); err != nil {
		t.Fatal(err)
	}
	if err := s.sendText("peer", "对方", "[private.jpg]"); err != nil {
		t.Fatal(err)
	}
	hiddenBeforeMatch := false
	gotText := false
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for !gotText {
		select {
		case frame := <-frames:
			switch frame.action {
			case proto.ActionLogin:
				if frame.payload["nickname"] != "随机绑定昵称" || frame.payload["gender"] != "female" || frame.payload["age"] != float64(25) {
					t.Fatalf("登录资料=%v", frame.payload)
				}
				if _, ok := frame.payload["matchPreference"]; ok {
					t.Fatal("不应传匹配偏好")
				}
			case proto.ActionUpdateProfile:
				if frame.payload["showCity"] == false {
					hiddenBeforeMatch = true
				}
			case proto.ActionJoinMatch:
				if !hiddenBeforeMatch {
					t.Fatal("匹配前尚未隐藏城市")
				}
			case proto.ActionSendMessage:
				if frame.payload["type"] != "text" || frame.payload["content"] != "[private.jpg]" {
					t.Fatalf("方括号文本被解释为媒体: %v", frame.payload)
				}
				gotText = true
			}
		case <-deadline.C:
			t.Fatal("未收到文本命令")
		}
	}
	conn := <-serverConn
	_ = conn.Close()
	stopped := false
	stopTimer := time.NewTimer(time.Second)
	defer stopTimer.Stop()
	for !stopped {
		select {
		case state := <-states:
			stopped = state == wanfeng.ConnStopped
		case <-stopTimer.C:
			t.Fatal("首次断线后未停止客户端")
		}
	}
	if connections.Load() != 1 {
		t.Fatalf("出现内部重连: %d", connections.Load())
	}
}

func TestProviderWanfengBindingResetDeletesInsteadOfReregistering(t *testing.T) {
	for _, b := range []wanfeng.Binding{{IdentityID: "local", UpstreamUID: "remote"}, {IdentityID: "local", DeviceID: "device"}} {
		memory := wanfeng.NewMemoryBindingStore()
		deleted := false
		var reported error
		store := wanfengLinkBindings{BindingStore: memory, reset: func(context.Context) error { deleted = true; return nil }, onReset: func(err error) { reported = err }}
		if err := store.Save(context.Background(), b); err != nil {
			t.Fatal(err)
		}
		if !deleted || !errors.Is(reported, ErrIdentityReset) {
			t.Fatalf("重置未删除/上报: deleted=%v err=%v", deleted, reported)
		}
		row, _ := memory.Get(context.Background(), "local")
		if row != nil {
			t.Fatal("重置写回了待自动注册的空绑定")
		}
	}
}
