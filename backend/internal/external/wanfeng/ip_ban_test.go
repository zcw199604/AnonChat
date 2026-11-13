package wanfeng

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHandshakeRecognizesExplicitIPBan(t *testing.T) {
	for _, body := range []string{
		`{"success":false,"code":"IP_BANNED","message":"blocked"}`,
		`{"success":false,"code":40300,"message":"您的 IP 已被封禁（同 IP 短时间批量注册（自动风控））"}`,
		`{"success":false,"code":"USER_BANNED","message":"您的 IP 已被封禁"}`,
		`{"success":false,"code":40310,"message":"您的 IP 已被封禁"}`,
		`<html>您的 IP 已被封禁</html>`,
		`{"success":false,"message":"您的 IP 已被封禁（同 IP 短时间批量注册）"}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			api := NewAPIClient(APIClientConfig{BaseURL: server.URL})
			_, err := api.Session(context.Background())
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.Code != "IP_BANNED" {
				t.Fatalf("explicit IP ban was not recognized: %v", err)
			}
		})
	}
}

type testBanEgress struct {
	mu       sync.Mutex
	active   string
	switches int
	err      error
}

func (e *testBanEgress) Current() string { e.mu.Lock(); defer e.mu.Unlock(); return e.active }
func (e *testBanEgress) Switch(expected string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return e.err
	}
	if e.active == expected {
		e.switches++
		e.active = "new"
	}
	return nil
}

func TestIPBanHTTPPathsSwitchWithoutReplayingRequest(t *testing.T) {
	for _, path := range []string{"handshake", "upload", "download"} {
		t.Run(path, func(t *testing.T) {
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"success":false,"code":"IP_BANNED","message":"blocked"}`))
			}))
			defer server.Close()
			egress := &testBanEgress{active: "old"}
			api := NewAPIClient(APIClientConfig{BaseURL: server.URL, Egress: egress}).Clone()
			var err error
			switch path {
			case "handshake":
				_, err = api.Session(context.Background())
			case "upload":
				_, err = api.UploadMedia(context.Background(), "a.png", "image/png", []byte("data"), "image")
			case "download":
				_, _, err = api.DownloadMedia(context.Background(), "/a.png")
			}
			if !errors.Is(err, ErrIPBanSwitched) || egress.Current() != "new" || requests.Load() != 1 {
				t.Fatalf("switch/replay contract failed: err=%v egress=%s requests=%d", err, egress.Current(), requests.Load())
			}
		})
	}
}

func TestIPBanDoesNotConfuseOtherErrors(t *testing.T) {
	for _, raw := range []string{
		`{"success":false,"code":"USER_BANNED","message":"用户已被封禁"}`,
		`{"success":false,"code":"IDENTITY_INVALID","message":"identity expired"}`,
		`{"success":false,"code":"DEVICE_DUPLICATE"}`,
		`{"success":false,"code":40300,"message":"请求错误，请刷新页面"}`,
		`{"success":false,"code":40310,"message":"session expired"}`,
		`{"success":false,"code":"HANDSHAKE_FAILED","message":"timeout"}`,
		`<html>403 Forbidden</html>`,
		`{"success":true,"message":"IP_BANNED"}`,
	} {
		egress := &testBanEgress{active: "old"}
		api := NewAPIClient(APIClientConfig{Egress: egress})
		if err := api.checkIPBan([]byte(raw), "old"); err != nil || egress.Current() != "old" {
			t.Errorf("unrelated error triggered rotation: %s, err=%v", raw, err)
		}
	}
}

func TestIPBanWebSocketPreservesIdentityAndReconnects(t *testing.T) {
	for _, mode := range []string{"error frame", "close reason"} {
		t.Run(mode, func(t *testing.T) {
			f := newFakeUpstream(t)
			egress := &testBanEgress{active: "old"}
			api := NewAPIClient(APIClientConfig{BaseURL: f.URL(), Egress: egress})
			store := NewMemoryBindingStore()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// A preexisting identity avoids issuing another registration after rotation.
			if err := store.Save(ctx, Binding{IdentityID: "me", UpstreamUID: "existing", DeviceID: "device", IdentityToken: "token"}); err != nil {
				t.Fatal(err)
			}
			client := NewClient(ClientConfig{IdentityID: "me", API: api, Bindings: store, Emitter: &recordingEmitter{}})
			client.Start(ctx)
			defer client.Close()
			select {
			case <-f.connectedSent:
			case <-ctx.Done():
				t.Fatal("initial WS login timed out")
			}
			if mode == "error frame" {
				if err := f.pushFrame(DefaultProtocol().EventError, map[string]any{"code": "HANDSHAKE_FAILED", "message": "您的 IP 已被封禁"}); err != nil {
					t.Fatal(err)
				}
			} else {
				f.mu.Lock()
				conn := f.conn
				f.mu.Unlock()
				if err := conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "您的 IP 已被封禁"), time.Now().Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-f.connectedSent:
			case <-ctx.Done():
				t.Fatal("WS did not reconnect after IP rotation")
			}
			if egress.Current() != "new" {
				t.Fatal("WS did not rotate the egress")
			}
			binding, err := store.Get(ctx, "me")
			if err != nil || binding.UpstreamUID != "existing" || binding.DeviceID != "device" {
				t.Fatalf("rotation changed identity: %+v %v", binding, err)
			}
		})
	}
}

func TestIPBanInWebSocketUpgradeResponseSwitchesEgress(t *testing.T) {
	f := newFakeUpstream(t)
	handler := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("您的 IP 已被封禁"))
			return
		}
		handler.ServeHTTP(w, r)
	})
	egress := &testBanEgress{active: "old"}
	api := NewAPIClient(APIClientConfig{BaseURL: f.URL(), Egress: egress})
	client := NewClient(ClientConfig{IdentityID: "me", API: api, Bindings: NewMemoryBindingStore(), Emitter: &recordingEmitter{}})
	fatal, err := client.serveOnce(context.Background())
	if fatal || !errors.Is(err, ErrIPBanSwitched) || egress.Current() != "new" {
		t.Fatalf("WS upgrade ban did not switch: fatal=%v err=%v egress=%s", fatal, err, egress.Current())
	}
}

func TestIPBanWithoutStandbyKeepsFatalWebSocketBan(t *testing.T) {
	for _, code := range []string{"IP_BANNED", "HANDSHAKE_FAILED", "IDENTITY_INVALID", ""} {
		t.Run(code, func(t *testing.T) {
			egress := &testBanEgress{active: "old", err: errors.New("no standby")}
			emitter := &recordingEmitter{}
			api := NewAPIClient(APIClientConfig{Egress: egress})
			client := NewClient(ClientConfig{IdentityID: "me", API: api, Emitter: emitter})
			client.egressID = "old"
			raw, _ := json.Marshal(map[string]any{"type": DefaultProtocol().EventError, "data": map[string]any{"code": code, "message": "您的 IP 已被封禁"}})
			if !client.handleFrame(raw) {
				t.Fatal("failed rotation must not hide the fatal IP ban")
			}
			if len(emitter.withCode(codeForbidden)) != 1 || egress.Current() != "old" {
				t.Fatal("missing ban notice or unexpected egress change")
			}
		})
	}
}

func TestIPBanDuringInitialIdentitySetupRemainsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"success":false,"message":"您的 IP 已被封禁（同 IP 短时间批量注册（自动风控））"}`))
	}))
	defer server.Close()
	api := NewAPIClient(APIClientConfig{BaseURL: server.URL, Egress: &testBanEgress{active: "old"}})
	client := NewClient(ClientConfig{IdentityID: "me", API: api, Bindings: NewMemoryBindingStore(), Emitter: &recordingEmitter{}})
	fatal, err := client.serveOnce(context.Background())
	if fatal || !errors.Is(err, ErrIPBanSwitched) {
		t.Fatalf("initial ban stopped recovery: fatal=%v err=%v", fatal, err)
	}
}
