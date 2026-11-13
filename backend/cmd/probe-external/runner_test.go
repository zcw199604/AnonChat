package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/gorilla/websocket"
)

func writeImage(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"), 0600); err != nil {
		t.Fatal(err)
	}
}

func localGuard(p external.Policy, proxy string) (*external.NetGuard, error) {
	p.AllowPrivate = true // only tests can admit the httptest loopback listener.
	return external.NewNetGuard(p, proxy)
}

func records(t *testing.T, b *bytes.Buffer) []map[string]any {
	t.Helper()
	var result []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var v map[string]any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			t.Fatalf("not JSON: %s %v", line, err)
		}
		result = append(result, v)
	}
	return result
}

func TestProbeChat2019(t *testing.T) {
	var sign, matches, uploads, images atomic.Int32
	var serverURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/rand", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "OK", "msg": map[string]any{"server": "ws" + strings.TrimPrefix(serverURL, "http") + "/ws"}})
	})
	mux.HandleFunc("/img", func(w http.ResponseWriter, r *http.Request) {
		u, _ := url.Parse(serverURL)
		_ = json.NewEncoder(w).Encode(map[string]any{"state": "OK", "msg": map[string]any{"server": u.Host}})
	})
	mux.HandleFunc("/asmx/upload.asmx/ProcessRequest", func(w http.ResponseWriter, r *http.Request) {
		file, _, err := r.FormFile("upload_file")
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		_ = file.Close()
		uploads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"State": "OK", "Msg": "out.png"})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var f map[string]any
			if err := conn.ReadJSON(&f); err != nil {
				return
			}
			switch f["act"] {
			case "sign":
				sign.Add(1)
				if f["userip"] != "" || f["useraddree"] != "" {
					t.Error("sign exposed location")
				}
			case "random":
				matches.Add(1)
				_ = conn.WriteJSON(map[string]any{"code": 15, "sel_userid": "peer", "sel_userNikename": "陌生人", "token": "secret-token"})
				_ = conn.WriteJSON(map[string]any{"code": 7, "msg": strings.Repeat("中", 230), "identityToken": "secret-token"})
			case "touser_peer_陌生人":
				if f["msg"] == "[out.png]" {
					images.Add(1)
				}
			}
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	serverURL = srv.URL
	image := filepath.Join(t.TempDir(), "picture.png")
	writeImage(t, image)
	var out bytes.Buffer
	err := run(context.Background(), options{provider: "chat2019", n: 2, match: 200 * time.Millisecond, sendImage: image}, endpoints{chatRand: srv.URL + "/rand", chatImg: srv.URL + "/img"}, localGuard, &out)
	if err != nil {
		t.Fatal(err)
	}
	if sign.Load() != 2 || matches.Load() != 2 || uploads.Load() != 2 || images.Load() != 2 {
		t.Fatalf("sign=%d match=%d upload=%d image=%d", sign.Load(), matches.Load(), uploads.Load(), images.Load())
	}
	var summaries int
	for _, v := range records(t, &out) {
		if v["type"] == "summary" {
			summaries++
			if v["matched"] != true || v["connected"] != true || v["errorType"] != "" || v["connectMs"] == nil || v["matchMs"] == nil {
				t.Errorf("summary=%v", v)
			}
		}
	}
	if summaries != 2 || strings.Contains(out.String(), "secret-token") || strings.Contains(out.String(), strings.Repeat("中", 201)) {
		t.Fatalf("invalid probe output: %s", out.String())
	}
}

func TestProbeTimeoutAndGuard(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()
	ep := endpoints{chatWS: "ws" + strings.TrimPrefix(srv.URL, "http")}
	for _, tc := range []struct {
		name  string
		guard guardFactory
		want  string
	}{{"unmatched", localGuard, "match_timeout"}, {"private-rejected", standardGuard, "address_rejected"}} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(context.Background(), options{provider: "chat2019", n: 1, match: 30 * time.Millisecond}, ep, tc.guard, &out)
			if err == nil {
				t.Fatal("expected failure")
			}
			recs := records(t, &out)
			last := recs[len(recs)-1]
			if last["type"] != "summary" || last["matched"] != false || last["errorType"] != tc.want {
				t.Fatalf("summary=%v", last)
			}
		})
	}
}
