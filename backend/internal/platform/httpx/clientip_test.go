package httpx

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPResolver_NoTrustedProxies(t *testing.T) {
	res, err := NewClientIPResolver(nil)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if got := res.ClientIP(r); got != "203.0.113.9" {
		t.Errorf("ClientIP = %q, 期望 203.0.113.9（应忽略伪造的 XFF）", got)
	}
}

func TestClientIPResolver_TrustedProxy_UsesRightmostUntrusted(t *testing.T) {
	res, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:443"
	// 客户端 -> 代理A(信任) -> 代理B(信任) -> 我们
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.5, 10.0.0.6")
	if got := res.ClientIP(r); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q, 期望 203.0.113.7", got)
	}
}

func TestClientIPResolver_AllTrustedFallsBackToLeftmost(t *testing.T) {
	res, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "10.0.0.9, 10.0.0.5")
	if got := res.ClientIP(r); got != "10.0.0.9" {
		t.Errorf("ClientIP = %q, 期望 10.0.0.9", got)
	}
}

func TestClientIPResolver_TrustedProxyNoHeader(t *testing.T) {
	res, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:443"
	if got := res.ClientIP(r); got != "10.1.2.3" {
		t.Errorf("ClientIP = %q, 期望 10.1.2.3", got)
	}
}

func TestClientIPResolver_MultipleHeadersAndGarbage(t *testing.T) {
	res, err := NewClientIPResolver([]string{"10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Add("X-Forwarded-For", "garbage, 203.0.113.7")
	r.Header.Add("X-Forwarded-For", "10.0.0.6")
	if got := res.ClientIP(r); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q, 期望 203.0.113.7", got)
	}
}

func TestClientIPResolver_InvalidCIDR(t *testing.T) {
	if _, err := NewClientIPResolver([]string{"not-a-cidr"}); err == nil {
		t.Fatal("非法 CIDR 应返回错误")
	}
}
