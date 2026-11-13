package main

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/gorilla/websocket"
)

type wfUpstream struct {
	t                           *testing.T
	srv                         *httptest.Server
	codec                       *wanfeng.APIClient
	mu                          sync.Mutex
	session                     *wanfeng.Session
	logins                      []map[string]any
	generated, uploaded, images atomic.Int32
	disconnect                  bool
	rejectUpload                bool
}

func newWFUpstream(t *testing.T, disconnect, rejectUpload bool) *wfUpstream {
	p := wanfeng.DefaultProtocol()
	f := &wfUpstream{t: t, disconnect: disconnect, rejectUpload: rejectUpload, codec: wanfeng.NewAPIClient(wanfeng.APIClientConfig{Protocol: p})}
	mux := http.NewServeMux()
	mux.HandleFunc(p.HandshakePath, func(w http.ResponseWriter, r *http.Request) {
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
		raw, err := base64.StdEncoding.DecodeString(req.Pub)
		if err != nil {
			t.Error(err)
			return
		}
		pub, err := ecdh.P256().NewPublicKey(raw)
		if err != nil {
			t.Error(err)
			return
		}
		shared, err := kp.ECDH(pub)
		if err != nil {
			t.Error(err)
			return
		}
		key, err := hkdf.Key(sha256.New, shared, []byte("probe-session"), p.HKDFInfo, 32)
		if err != nil {
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.session = &wanfeng.Session{ID: "probe-session", Key: key}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"sid": "probe-session", "pub": base64.StdEncoding.EncodeToString(kp.PublicKey().Bytes()), "exp": time.Now().Add(time.Hour).UnixMilli()}})
	})
	mux.HandleFunc(p.GenerateIdentityPath, func(w http.ResponseWriter, r *http.Request) {
		f.generated.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"uid": "remote"}})
	})
	mux.HandleFunc(p.IdentityCookiePath, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "probe=secret-cookie; Path=/")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
	})
	mux.HandleFunc(p.UploadPath, func(w http.ResponseWriter, r *http.Request) {
		if f.rejectUpload {
			http.Error(w, "secret-token", http.StatusServiceUnavailable)
			return
		}
		file, _, err := r.FormFile(p.UploadFileField)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		_ = file.Close()
		if r.FormValue(p.UploadUserField) != "remote" || r.FormValue(p.UploadBucketField) != p.BucketImage || !strings.Contains(r.Header.Get("Cookie"), "probe=secret-cookie") {
			t.Error("upload identity/bucket/cookie missing")
		}
		f.uploaded.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"filePath": "/files/out.png"}})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		f.mu.Lock()
		sess := f.session
		f.mu.Unlock()
		push := func(kind string, data map[string]any) {
			plain, err := json.Marshal(data)
			if err != nil {
				t.Error(err)
				return
			}
			sealed, err := f.codec.SealFrameData(sess, plain)
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.WriteJSON(map[string]any{"type": kind, "data": sealed})
		}
		for {
			var frame struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			plain, err := f.codec.OpenFrameData(sess, frame.Data)
			if err != nil {
				t.Error(err)
				return
			}
			var data map[string]any
			if err := json.Unmarshal(plain, &data); err != nil {
				t.Error(err)
				return
			}
			switch frame.Type {
			case p.ActionLogin:
				f.mu.Lock()
				f.logins = append(f.logins, data)
				f.mu.Unlock()
				push(p.EventConnected, map[string]any{"uid": "remote", "state": p.StateIdle, "identityToken": "secret-token"})
			case p.ActionJoinMatch:
				push(p.EventMatchResult, map[string]any{"targetUid": "peer", "partner": map[string]any{"uid": "peer", "nickname": "陌生人"}})
				if f.disconnect {
					return
				}
			case p.ActionSendMessage:
				if data["type"] != "image" || data["targetUid"] != "peer" || data["content"] != f.srv.URL+"/files/out.png" {
					t.Errorf("incorrect media command: %v", data)
				}
				f.images.Add(1)
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestProbeWanfengReusesStateAndSendsImage(t *testing.T) {
	f := newWFUpstream(t, false, false)
	path := filepath.Join(t.TempDir(), "private", "state.json")
	image := filepath.Join(t.TempDir(), "image.png")
	writeImage(t, image)
	o := options{provider: "wanfeng", n: 1, match: 200 * time.Millisecond, state: path, sendImage: image}
	ep := endpoints{wfBase: f.srv.URL, wfWS: "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"}
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := run(context.Background(), o, ep, localGuard, &out); err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, &out)
		}
		recs := records(t, &out)
		s := recs[len(recs)-1]
		if s["matched"] != true || s["connected"] != true || s["errorType"] != "" || strings.Contains(out.String(), "secret-") {
			t.Fatalf("invalid output: %s", &out)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.generated.Load() != 1 || f.uploaded.Load() != 2 || f.images.Load() != 2 || len(f.logins) != 2 {
		t.Fatalf("generated=%d uploaded=%d images=%d logins=%d", f.generated.Load(), f.uploaded.Load(), f.images.Load(), len(f.logins))
	}
	if f.logins[1]["identityToken"] != "secret-token" || f.logins[0]["deviceId"] == "" || f.logins[0]["deviceId"] != f.logins[1]["deviceId"] || f.logins[0]["uid"] != f.logins[1]["uid"] {
		t.Error("identity token/uid/device was not reused")
	}
}

func TestProbeWanfengFailureSummaries(t *testing.T) {
	for _, tc := range []struct {
		name               string
		disconnect, reject bool
		want               string
	}{{"disconnect", true, false, "disconnected"}, {"upload-failed", false, true, "media_failed"}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newWFUpstream(t, tc.disconnect, tc.reject)
			image := filepath.Join(t.TempDir(), "image.png")
			writeImage(t, image)
			var out bytes.Buffer
			err := run(context.Background(), options{provider: "wanfeng", n: 1, match: time.Second, state: filepath.Join(t.TempDir(), "private", "state.json"), sendImage: image}, endpoints{wfBase: f.srv.URL, wfWS: "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws"}, localGuard, &out)
			if err == nil {
				t.Fatal("expected probe failure")
			}
			recs := records(t, &out)
			s := recs[len(recs)-1]
			if s["errorType"] != tc.want || strings.Contains(out.String(), "secret-") {
				t.Fatalf("invalid summary: %s", &out)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.logins) != 1 {
				t.Errorf("unexpected reconnects: %d", len(f.logins))
			}
		})
	}
}

func TestProbeWanfengThroughLocalProxy(t *testing.T) {
	f := newWFUpstream(t, false, false)
	target, err := url.Parse(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	forward := httputil.NewSingleHostReverseProxy(target)
	var connects atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			forward.ServeHTTP(w, r)
			return
		}
		if r.Host != "probe.invalid:80" {
			t.Errorf("unexpected proxy target: %s", r.Host)
			w.WriteHeader(http.StatusForbidden)
			return
		}
		up, err := net.Dial("tcp", target.Host)
		if err != nil {
			t.Error(err)
			return
		}
		defer up.Close()
		down, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer down.Close()
		connects.Add(1)
		_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		_ = rw.Flush()
		go func() { _, _ = io.Copy(up, rw); _ = up.Close() }()
		_, _ = io.Copy(down, up)
	}))
	defer proxy.Close()
	var out bytes.Buffer
	err = run(context.Background(), options{provider: "wanfeng", n: 1, match: 100 * time.Millisecond, state: filepath.Join(t.TempDir(), "private", "state.json"), proxy: proxy.URL}, endpoints{wfBase: "http://probe.invalid", wfWS: "ws://probe.invalid/ws"}, standardGuard, &out)
	if err != nil || connects.Load() != 1 {
		t.Fatalf("proxy connects=%d err=%v output=%s", connects.Load(), err, &out)
	}
	recs := records(t, &out)
	if recs[len(recs)-1]["matched"] != true {
		t.Fatalf("probe did not match: %s", &out)
	}
}
