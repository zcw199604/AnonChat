package chat2019_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestMediaUploadDownloadHeadersCacheAndPortFallback(t *testing.T) {
	var discovery atomic.Int32
	var mu sync.Mutex
	var ports []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/getImgServer":
			discovery.Add(1)
			_, _ = io.WriteString(w, `{"state":"OK","msg":{"server":"images.test"}}`)
		case "/asmx/upload.asmx/ProcessRequest":
			if r.URL.Query().Get("act") != "uploadImgRandom" || r.URL.Query().Get("userid") != self.UID {
				t.Error("上传query错误")
			}
			if r.Header.Get("Origin") != "http://v1.chat2019.cn" || r.Header.Get("Referer") != "http://v1.chat2019.cn/randomdeskrynew4m1phj.html?v=4m1phj" {
				t.Error("上传headers错误")
			}
			if !regexp.MustCompile(`^` + self.UID + `_` + self.Nickname + `_[0-9]+_[a-z0-9]{6}$`).MatchString(r.Header.Get("Cookie")) {
				t.Errorf("Cookie格式错误: %q", r.Header.Get("Cookie"))
			}
			f, h, err := r.FormFile("upload_file")
			if err != nil {
				t.Error(err)
				http.Error(w, "missing", http.StatusBadRequest)
				return
			}
			defer f.Close()
			b, _ := io.ReadAll(f)
			if h.Filename != "photo.webp" || !bytes.Equal(b, []byte("original-webp")) {
				t.Error("未保留原文件")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"state": "OK", "msg": "day/photo.webp"})
		case "/img/Upload/day/photo.webp":
			if r.Header.Get("Referer") == "" || r.Header.Get("Origin") == "" {
				t.Error("下载headers缺失")
			}
			mu.Lock()
			ports = append(ports, r.Header.Get("Test-Port"))
			mu.Unlock()
			if r.Header.Get("Test-Port") != "7004" {
				http.Error(w, "no file", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "image/webp")
			_, _ = w.Write([]byte("file"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cfg := guardConfig(t)
	cfg.ImgServerURL = srv.URL + "/getImgServer"
	base := cfg.HTTPClient.Transport
	destination, _ := url.Parse(srv.URL)
	cfg.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		next := r.Clone(r.Context())
		u := *r.URL
		next.URL = &u
		if u.Hostname() == "images.test" {
			next.Header.Set("Test-Port", u.Port())
			next.URL.Host = destination.Host
		}
		return base.RoundTrip(next)
	})
	media := chat2019.NewMedia(cfg, 1024)
	path, err := media.Upload(context.Background(), self, "photo.webp", []byte("original-webp"))
	if err != nil {
		t.Fatalf("Upload未实现: %v", err)
	}
	if path != "day/photo.webp" {
		t.Fatal(path)
	}
	for range 2 {
		data, kind, err := media.Download(context.Background(), path)
		if err != nil || string(data) != "file" || kind != "image/webp" {
			t.Fatalf("Download=%q %s %v", data, kind, err)
		}
	}
	if discovery.Load() != 1 {
		t.Fatalf("图片服务器未缓存: %d", discovery.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(ports, []string{"7001", "7004", "7004"}) {
		t.Fatalf("端口顺序=%v", ports)
	}
}
func TestMediaRejectsNonRelativePathsBeforeNetwork(t *testing.T) {
	cfg := guardConfig(t)
	var requests atomic.Int32
	cfg.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, fmt.Errorf("不应访问网络")
	})
	media := chat2019.NewMedia(cfg, 10)
	for _, path := range []string{"", "/a.jpg", "../a.jpg", "a/../b.jpg", "https://evil.test/a", "//evil.test/a", "a\\b.jpg", "%2e%2e/a.jpg", "a?x=1", "a#frag", "a%252f..%252fb"} {
		if _, _, err := media.Download(context.Background(), path); err == nil {
			t.Errorf("非法路径被接受: %q", path)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("非法路径触发网络: %d", requests.Load())
	}
}
func TestMediaRejectsInvalidHostsOversizeAndUpstreamFailure(t *testing.T) {
	for _, host := range []string{"", "http://images.test/a", "images.test/path", "user@images.test", "images.test?x=1"} {
		t.Run("host="+host, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(map[string]any{"state": "OK", "msg": map[string]string{"server": host}})
			}))
			defer srv.Close()
			cfg := guardConfig(t)
			cfg.ImgServerURL = srv.URL
			if _, err := chat2019.NewMedia(cfg, 10).Upload(context.Background(), self, "a", []byte("a")); err == nil {
				t.Fatal("非法图片主机被接受")
			}
		})
	}
	cfg := guardConfig(t)
	media := chat2019.NewMedia(cfg, 2)
	if _, err := media.Upload(context.Background(), self, "a", []byte("big")); err == nil {
		t.Fatal("过大上传被接受")
	}
	for _, state := range []string{"ERR", "OK"} {
		t.Run(state, func(t *testing.T) {
			var srv *httptest.Server
			srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/host" {
					_ = json.NewEncoder(w).Encode(map[string]any{"state": "OK", "msg": map[string]string{"server": strings.TrimPrefix(srv.URL, "http://")}})
					return
				}
				if r.Method == http.MethodPost {
					_ = json.NewEncoder(w).Encode(map[string]string{"state": state, "msg": "../bad"})
					return
				}
				_, _ = io.WriteString(w, "oversized")
			}))
			defer srv.Close()
			cfg := guardConfig(t)
			cfg.ImgServerURL = srv.URL + "/host"
			base := cfg.HTTPClient.Transport
			u, _ := url.Parse(srv.URL)
			cfg.HTTPClient.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				n := r.Clone(r.Context())
				v := *r.URL
				v.Host = u.Host
				n.URL = &v
				return base.RoundTrip(n)
			})
			m := chat2019.NewMedia(cfg, 2)
			if _, err := m.Upload(context.Background(), self, "a", []byte("a")); err == nil {
				t.Fatal("失败或非法上传路径被接受")
			}
			if _, _, err := m.Download(context.Background(), "a.jpg"); err == nil {
				t.Fatal("过大下载被接受")
			}
		})
	}
}
