package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthcheckURL(t *testing.T) {
	cases := []struct{ addr, want string }{
		{":8080", "http://127.0.0.1:8080/healthz"},
		{"0.0.0.0:9090", "http://127.0.0.1:9090/healthz"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080/healthz"},
		{"[::]:8080", "http://127.0.0.1:8080/healthz"},
	}
	for _, c := range cases {
		if got := healthcheckURL(c.addr); got != c.want {
			t.Errorf("healthcheckURL(%q) = %q, 期望 %q", c.addr, got, c.want)
		}
	}
}

func TestProbeHealth(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	if err := probeHealth(ok.URL+"/healthz", time.Second); err != nil {
		t.Fatalf("200 应视为健康: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer bad.Close()
	if err := probeHealth(bad.URL+"/healthz", time.Second); err == nil {
		t.Fatal("503 应视为不健康")
	}
}

func TestCmdHealthcheck(t *testing.T) {
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ok.Close()
	addr := strings.TrimPrefix(ok.URL, "http://")
	if err := cmdHealthcheck([]string{"--addr", addr}); err != nil {
		t.Fatalf("健康服务应返回 nil, 实际: %v", err)
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()
	badAddr := strings.TrimPrefix(bad.URL, "http://")
	if err := cmdHealthcheck([]string{"--addr", badAddr}); err == nil {
		t.Fatal("不健康服务应返回错误")
	}
}
