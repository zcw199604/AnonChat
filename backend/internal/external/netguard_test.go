package external

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/coder/websocket"
)

type netGuardResolver func(context.Context, string) ([]net.IPAddr, error)

func (f netGuardResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return f(ctx, host)
}

func mustGuard(t *testing.T, p Policy, proxy string, opts ...NetGuardOption) *NetGuard {
	t.Helper()
	g, err := NewNetGuard(p, proxy, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestNetGuardRejectsNonPublicAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.1.1", "100.64.0.1", "100.127.255.254", "0.0.0.0", "224.0.0.1", "::1", "::", "fe80::1", "fc00::1", "ff02::1", "::ffff:127.0.0.1"} {
		t.Run(ip, func(t *testing.T) {
			var dialed bool
			g := mustGuard(t, Policy{}, "", WithNetResolver(netGuardResolver(func(context.Context, string) ([]net.IPAddr, error) { return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil })), WithNetDialContext(func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected dial")
			}))
			_, err := g.HTTPClient(time.Second).Get("http://public.test/")
			if !errors.Is(err, ErrAddressRejected) || dialed {
				t.Fatalf("ip=%s err=%v dialed=%v", ip, err, dialed)
			}
		})
	}
}

func TestNetGuardDialsCheckedIPWithoutSecondResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "public.test" {
			t.Errorf("Host=%q", r.Host)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()
	var calls atomic.Int32
	g := mustGuard(t, Policy{}, "", WithNetResolver(netGuardResolver(func(context.Context, string) ([]net.IPAddr, error) {
		if calls.Add(1) > 1 {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	})), WithNetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:80" {
			return nil, fmt.Errorf("拨号没有使用已检查 IP: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
	}))
	res, err := g.HTTPClient(time.Second).Get("http://public.test/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if string(data) != "ok" || calls.Load() != 1 {
		t.Fatalf("body=%q DNS次数=%d", data, calls.Load())
	}
}

func TestNetGuardRejectsMixedDNSAndEmptyAnswers(t *testing.T) {
	for _, ips := range [][]net.IPAddr{nil, {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}} {
		var dialed bool
		g := mustGuard(t, Policy{}, "", WithNetResolver(netGuardResolver(func(context.Context, string) ([]net.IPAddr, error) { return ips, nil })), WithNetDialContext(func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("dialed")
		}))
		_, err := g.HTTPClient(time.Second).Get("http://public.test")
		if !errors.Is(err, ErrAddressRejected) || dialed {
			t.Fatalf("err=%v dialed=%v", err, dialed)
		}
	}
}

func TestNetGuardRedirectPolicy(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "ok") }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	g := mustGuard(t, Policy{AllowPrivate: true, NoRedirect: true}, "")
	_, err := g.HTTPClient(time.Second).Get(redirect.URL)
	if !errors.Is(err, ErrAddressRejected) || hits.Load() != 0 {
		t.Fatalf("禁止重定向失效: err=%v hits=%d", err, hits.Load())
	}

	g = mustGuard(t, Policy{AllowPrivate: true, AllowedHosts: []string{"localhost"}}, "")
	_, err = g.HTTPClient(time.Second).Get(strings.Replace(redirect.URL, "127.0.0.1", "localhost", 1))
	if !errors.Is(err, ErrAddressRejected) || hits.Load() != 0 {
		t.Fatalf("白名单外重定向未拒绝: err=%v hits=%d", err, hits.Load())
	}

	// 初始域名通过白名单，重定向到白名单中的内网域名仍须由拨号守卫拒绝。
	var resolutions atomic.Int32
	g = mustGuard(t, Policy{AllowedHosts: []string{"public.test", "private.test"}}, "", WithNetResolver(netGuardResolver(func(_ context.Context, host string) ([]net.IPAddr, error) {
		resolutions.Add(1)
		if host == "private.test" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	})), WithNetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		left, right := net.Pipe()
		go func() {
			defer right.Close()
			buf := make([]byte, 4096)
			_, _ = right.Read(buf)
			_, _ = io.WriteString(right, "HTTP/1.1 302 Found\r\nLocation: http://private.test/\r\nContent-Length: 0\r\n\r\n")
		}()
		return left, nil
	}))
	_, err = g.HTTPClient(time.Second).Get("http://public.test/")
	if !errors.Is(err, ErrAddressRejected) || resolutions.Load() != 2 {
		t.Fatalf("内网重定向未拒绝: %v DNS=%d", err, resolutions.Load())
	}
}

func TestNetGuardProxyRequiresAndEnforcesAllowlist(t *testing.T) {
	for _, proxy := range []string{"http://127.0.0.1:1", "https://proxy.test:443", "socks5://127.0.0.1:1080"} {
		if _, err := NewNetGuard(Policy{}, proxy); err == nil {
			t.Errorf("代理缺少白名单未拒绝: %s", proxy)
		}
	}
	var hits atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Host != "allowed.test" {
			t.Errorf("代理目标=%q", r.URL.Host)
		}
		_, _ = io.WriteString(w, "proxied")
	}))
	defer proxy.Close()
	g := mustGuard(t, Policy{AllowedHosts: []string{"allowed.test"}}, proxy.URL)
	client := g.HTTPClient(time.Second)
	res, err := client.Get("http://allowed.test/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	_, err = client.Get("http://forbidden.test/")
	if !errors.Is(err, ErrAddressRejected) || hits.Load() != 1 {
		t.Fatalf("代理白名单失效: err=%v hits=%d", err, hits.Load())
	}
	for _, raw := range []string{"file://allowed.test/x", "ftp://allowed.test/x", "http://allowed.test.evil/x", "http://user@allowed.test/x"} {
		u, _ := url.Parse(raw)
		if !errors.Is(g.CheckURL(u), ErrAddressRejected) {
			t.Errorf("未拒绝 %s", raw)
		}
	}
}

func TestNetGuardRedirectLimitAndSchemes(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/again", http.StatusFound)
	}))
	defer server.Close()
	g := mustGuard(t, Policy{AllowPrivate: true}, "")
	_, err := g.HTTPClient(time.Second).Get(server.URL)
	if !errors.Is(err, ErrAddressRejected) || hits.Load() != 4 {
		t.Fatalf("重定向上限错误: err=%v hits=%d", err, hits.Load())
	}
	for _, location := range []string{"ftp://allowed.test/a", "file:///etc/hosts"} {
		u, _ := url.Parse(location)
		if err := g.HTTPClient(time.Second).CheckRedirect(&http.Request{URL: u}, nil); !errors.Is(err, ErrAddressRejected) {
			t.Errorf("重定向协议未拒绝: %s err=%v", location, err)
		}
	}
}

func TestNetGuardWebSocketDialersSharePolicy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
	}))
	defer server.Close()
	for _, kind := range []string{"coder", "gorilla"} {
		t.Run(kind, func(t *testing.T) {
			var calls atomic.Int32
			g := mustGuard(t, Policy{}, "", WithNetResolver(netGuardResolver(func(context.Context, string) ([]net.IPAddr, error) {
				calls.Add(1)
				return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
			})), WithNetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
				if address != "8.8.8.8:80" {
					return nil, fmt.Errorf("未拨已检查IP: %s", address)
				}
				return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "http://"))
			}))
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if kind == "coder" {
				conn, err := g.WSDialContext(ctx, "ws://public.test/")
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.CloseNow()
			} else {
				conn, _, err := g.GorillaDialer().DialContext(ctx, "ws://public.test/", nil)
				if err != nil {
					t.Fatal(err)
				}
				_ = conn.Close()
			}
			if calls.Load() != 1 {
				t.Fatalf("DNS次数=%d", calls.Load())
			}
			// 代理模式由目标主机白名单拒绝请求，不能尝试连接代理。
			var dialed bool
			proxy := mustGuard(t, Policy{AllowedHosts: []string{"allowed.test"}}, "http://127.0.0.1:1", WithNetDialContext(func(context.Context, string, string) (net.Conn, error) {
				dialed = true
				return nil, errors.New("unexpected proxy dial")
			}))
			var err error
			if kind == "coder" {
				_, err = proxy.WSDialContext(ctx, "ws://forbidden.test/")
			} else {
				_, _, err = proxy.GorillaDialer().DialContext(ctx, "ws://forbidden.test/", nil)
			}
			if !errors.Is(err, ErrAddressRejected) || dialed {
				t.Fatalf("err=%v dialed=%v", err, dialed)
			}
		})
	}
}

func TestNetGuardTLSKeepsOriginalServerName(t *testing.T) {
	var serverName string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "tls") }))
	server.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) { serverName = hello.ServerName; return nil, nil }}
	server.StartTLS()
	defer server.Close()
	g := mustGuard(t, Policy{}, "", WithNetResolver(netGuardResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	})), WithNetDialContext(func(ctx context.Context, network, address string) (net.Conn, error) {
		if address != "8.8.8.8:443" {
			return nil, fmt.Errorf("address=%s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(server.URL, "https://"))
	}))
	// 本地假服务证书和目标域名不同；这里只验证拨号后 TLS 的 SNI。
	g.transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	res, err := g.HTTPClient(time.Second).Get("https://public.test/")
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if serverName != "public.test" {
		t.Fatalf("TLS SNI=%q", serverName)
	}
}

func TestNetGuardWanfengCloneKeepsRedirectGuard(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "secret") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer server.Close()
	g := mustGuard(t, Policy{AllowPrivate: true, NoRedirect: true}, "")
	api := wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: server.URL, HTTPClient: g.HTTPClient(time.Second)})
	_, _, err := api.Clone().DownloadMedia(context.Background(), server.URL+"/media.png")
	if !errors.Is(err, ErrAddressRejected) || hits.Load() != 0 {
		t.Fatalf("Clone重定向守卫失效: err=%v hits=%d", err, hits.Load())
	}
}

func TestNetGuardGorillaHTTPSProxyUsesTLSConnect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		_, _, _ = conn.Read(r.Context())
	}))
	defer target.Close()
	var connects atomic.Int32
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "allowed.test:80" || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
			t.Errorf("CONNECT request: %s %s auth=%q", r.Method, r.Host, r.Header.Get("Proxy-Authorization"))
			http.Error(w, "bad connect", http.StatusBadRequest)
			return
		}
		connects.Add(1)
		upstream, err := net.Dial("tcp", strings.TrimPrefix(target.URL, "http://"))
		if err != nil {
			t.Error(err)
			return
		}
		defer upstream.Close()
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		copied := make(chan struct{})
		go func() { _, _ = io.Copy(upstream, conn); close(copied) }()
		_, _ = io.Copy(conn, upstream)
		_ = conn.Close()
		<-copied
	}))
	defer proxy.Close()
	proxyURL := strings.Replace(proxy.URL, "https://", "https://user:pass@", 1)
	g := mustGuard(t, Policy{AllowedHosts: []string{"allowed.test"}}, proxyURL)
	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(proxy.Certificate())
	g.transport.TLSClientConfig = &tls.Config{RootCAs: rootCAs, MinVersion: tls.VersionTLS12}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := g.GorillaDialer().DialContext(ctx, "ws://allowed.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	if connects.Load() != 1 {
		t.Fatalf("CONNECT次数=%d", connects.Load())
	}
}
