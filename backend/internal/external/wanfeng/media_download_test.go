package wanfeng

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWithThumbParam 校验补 thumb 参数的规则（对方 view 接口不带该参数会 403）。
func TestWithThumbParam(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://x.com/api/tools/niming-file/view/abc", "https://x.com/api/tools/niming-file/view/abc?thumb=1"},
		{"https://x.com/api/tools/niming-file/view/abc?thumb=1", "https://x.com/api/tools/niming-file/view/abc?thumb=1"},
		{"https://x.com/api/tools/niming-file/view/abc?a=1", "https://x.com/api/tools/niming-file/view/abc?a=1&thumb=1"},
		{"/api/tools/niming-file/view/abc", "/api/tools/niming-file/view/abc?thumb=1"},
	}
	for _, c := range cases {
		if got := WithThumbParam(c.in); got != c.want {
			t.Fatalf("WithThumbParam(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
	if !hasThumbParam("https://x/a?b=2&thumb=1") {
		t.Fatal("hasThumbParam 应识别 thumb 参数")
	}
	if hasThumbParam("https://x/a?b=2") {
		t.Fatal("hasThumbParam 误判")
	}
}

// TestDownloadMediaRetriesWithThumb 校验：裸地址 403 时会自动补 thumb=1 重试并成功。
func TestDownloadMediaRetriesWithThumb(t *testing.T) {
	var sawRetry bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("thumb") != "1" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"success":false,"code":403,"message":"禁止访问","data":null}`))
			return
		}
		sawRetry = true
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png-bytes"))
	}))
	defer srv.Close()

	api := NewAPIClient(APIClientConfig{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Protocol:   DefaultProtocol(),
	})
	data, contentType, err := api.DownloadMedia(context.Background(), srv.URL+"/api/tools/niming-file/view/abc")
	if err != nil {
		t.Fatalf("补 thumb 重试后应成功: %v", err)
	}
	if !sawRetry || string(data) != "png-bytes" || !strings.HasPrefix(contentType, "image/png") {
		t.Fatalf("重试结果不符: sawRetry=%v data=%q ct=%s", sawRetry, data, contentType)
	}
}

// TestDownloadMediaNoDoubleThumb 校验地址已带 thumb 时不会重复追加。
func TestDownloadMediaNoDoubleThumb(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Query().Get("thumb") != "1" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	api := NewAPIClient(APIClientConfig{BaseURL: srv.URL, HTTPClient: srv.Client(), Protocol: DefaultProtocol()})
	if _, _, err := api.DownloadMedia(context.Background(), srv.URL+"/api/tools/niming-file/view/abc?thumb=1"); err != nil {
		t.Fatalf("已带 thumb 应一次成功: %v", err)
	}
	if hits != 1 {
		t.Fatalf("不应重复请求，实际 %d 次", hits)
	}
}
