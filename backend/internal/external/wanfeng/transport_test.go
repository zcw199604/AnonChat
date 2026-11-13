package wanfeng

import (
	"context"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHTTPAndWebSocketShareTunnelDialer(t *testing.T) {
	f := newFakeUpstream(t)
	upstreamHandler := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/files/proxy-test.png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("downloaded image"))
			return
		}
		upstreamHandler.ServeHTTP(w, r)
	})
	var dials atomic.Int64
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if addr != "wanfeng.invalid:80" {
			t.Errorf("unexpected tunnel target: %s", addr)
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(f.URL(), "http://"))
	}
	defer transport.CloseIdleConnections()
	api := NewAPIClient(APIClientConfig{
		BaseURL:    "http://wanfeng.invalid",
		HTTPClient: &http.Client{Transport: transport, Timeout: 2 * time.Second},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Media clients retain the tunnel while isolating identity cookies/sessions.
	clone := api.Clone()
	if clone.http.Jar == api.http.Jar {
		t.Fatal("identity clients must have separate cookie jars")
	}
	if _, err := clone.Session(ctx); err != nil {
		t.Fatalf("HTTP handshake through tunnel: %v", err)
	}
	if _, err := clone.UploadMedia(ctx, "image.png", "image/png", []byte("image data"), "image"); err != nil {
		t.Fatalf("media upload through tunnel: %v", err)
	}
	data, _, err := clone.DownloadMedia(ctx, "/files/proxy-test.png")
	if err != nil || string(data) != "downloaded image" {
		t.Fatalf("media download through tunnel: data=%q err=%v", data, err)
	}
	client := NewClient(ClientConfig{
		IdentityID: "proxy-test", API: api, Bindings: NewMemoryBindingStore(), Emitter: &recordingEmitter{},
	})
	client.Start(ctx)
	defer client.Close()
	select {
	case <-f.connectedSent:
	case <-ctx.Done():
		t.Fatal("WebSocket bypassed the configured HTTP tunnel dialer")
	}
	if dials.Load() < 2 {
		t.Fatal("HTTP and WebSocket should both open tunnel connections")
	}
}

func TestWSSUsesHTTP1WhileAPIKeepsHTTP2(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			_, _ = io.WriteString(w, r.Proto)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		kind, data, err := conn.ReadMessage()
		if err == nil {
			_ = conn.WriteMessage(kind, data)
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	transport := server.Client().Transport.(*http.Transport).Clone()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig.RootCAs = roots
	transport.TLSClientConfig.InsecureSkipVerify = false
	transport.ForceAttemptHTTP2 = true
	transport.Proxy = nil
	var dials atomic.Int64
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	checkHTTP2 := func() {
		t.Helper()
		resp, err := httpClient.Get(server.URL + "/api")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.ProtoMajor != 2 || string(body) != "HTTP/2.0" {
			t.Fatalf("API lost HTTP/2: body=%q err=%v", body, err)
		}
	}
	// http.Transport lazily adds h2 to TLSClientConfig on the first API call.
	checkHTTP2()
	api := NewAPIClient(APIClientConfig{BaseURL: server.URL, HTTPClient: httpClient})
	client := NewClient(ClientConfig{API: api})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := client.dialer.DialContext(ctx, "wss"+strings.TrimPrefix(server.URL, "https")+"/ws", nil)
	if err != nil {
		t.Fatalf("WSS upgrade must use HTTP/1.1: %v", err)
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, []byte("through proxy dialer")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "through proxy dialer" {
		t.Fatalf("WSS echo failed: %q %v", data, err)
	}
	if dials.Load() < 2 {
		t.Fatal("WSS bypassed the shared network dialer")
	}
	// Force a fresh API TLS handshake: reusing an existing HTTP/2 connection
	// would hide accidental changes to the transport's ALPN configuration.
	transport.CloseIdleConnections()
	checkHTTP2()
}
