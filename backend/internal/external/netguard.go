package external

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	gorilla "github.com/gorilla/websocket"
)

// Policy 约束外站 HTTP 和 WebSocket 的目标地址。
type Policy struct {
	AllowPrivate bool // 仅供本地测试使用。
	AllowedHosts []string
	NoRedirect   bool
}

var ErrAddressRejected = errors.New("外站地址不被允许")

// NetResolver 可由部署环境提供解析器；拨号始终使用它返回并检查过的 IP。
type NetResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type NetGuardOption func(*NetGuard)

// NetGuard 的配置在构造后不可变，可供多个 HTTP/WS 客户端共享。
type NetGuard struct {
	policy    Policy
	allowed   map[string]bool
	proxy     *url.URL
	resolver  NetResolver
	dial      func(context.Context, string, string) (net.Conn, error)
	transport *http.Transport
}

func WithNetResolver(r NetResolver) NetGuardOption {
	return func(g *NetGuard) { g.resolver = r }
}

func WithNetDialContext(d func(context.Context, string, string) (net.Conn, error)) NetGuardOption {
	return func(g *NetGuard) { g.dial = d }
}

func NewNetGuard(p Policy, proxyURL string, opts ...NetGuardOption) (*NetGuard, error) {
	g := &NetGuard{
		policy: p, allowed: make(map[string]bool), resolver: net.DefaultResolver,
		dial: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	}
	for _, host := range p.AllowedHosts {
		host = canonicalHost(host)
		_, ipErr := netip.ParseAddr(host)
		if host == "" || strings.ContainsAny(host, "/@?#[]") || (strings.Contains(host, ":") && ipErr != nil) {
			return nil, fmt.Errorf("%w: 非法白名单主机", ErrAddressRejected)
		}
		g.allowed[host] = true
	}
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
			return nil, fmt.Errorf("%w: 非法代理地址", ErrAddressRejected)
		}
		if len(g.allowed) == 0 {
			return nil, fmt.Errorf("%w: 代理模式需要主机白名单", ErrAddressRejected)
		}
		g.proxy = u
	}
	for _, opt := range opts {
		opt(g)
	}
	if g.resolver == nil || g.dial == nil {
		return nil, errors.New("地址守卫需要解析器和拨号器")
	}
	// 不从环境继承代理；直连一定经过受控解析与拨号。
	g.transport = http.DefaultTransport.(*http.Transport).Clone()
	g.transport.Proxy = nil
	g.transport.DialContext = g.dialDirect
	g.transport.DialTLSContext = nil
	if g.proxy != nil {
		g.transport.Proxy = http.ProxyURL(g.proxy)
		// 代理地址来自运维配置，不按外站目标 IP 规则检查。
		g.transport.DialContext = g.dial
	}
	return g, nil
}

func canonicalHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

// CheckURL 检查协议和目标主机；直连的 DNS/IP 检查在实际拨号时进行。
func (g *NetGuard) CheckURL(u *url.URL) error {
	if u == nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return ErrAddressRejected
	}
	switch u.Scheme {
	case "http", "https", "ws", "wss":
	default:
		return ErrAddressRejected
	}
	host := canonicalHost(u.Hostname())
	if len(g.allowed) > 0 && !g.allowed[host] {
		return ErrAddressRejected
	}
	if ip, err := netip.ParseAddr(host); err == nil && !g.policy.AllowPrivate && !publicIP(ip) {
		return ErrAddressRejected
	}
	return nil
}

func publicIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip)
}

func (g *NetGuard) dialDirect(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if len(g.allowed) > 0 && !g.allowed[canonicalHost(host)] {
		return nil, ErrAddressRejected
	}
	ips, err := g.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, ErrAddressRejected
	}
	// 检查全部答案之后才拨号，拒绝混合公网/内网的 DNS 回答。
	addresses := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || ip.Zone != "" || (!g.policy.AllowPrivate && !publicIP(addr)) {
			return nil, ErrAddressRejected
		}
		addresses = append(addresses, net.JoinHostPort(addr.Unmap().String(), port))
	}
	var dialErr error
	for _, checked := range addresses {
		conn, err := g.dial(ctx, network, checked)
		if err == nil {
			return conn, nil
		}
		dialErr = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, dialErr
}

type guardedTransport struct {
	guard *NetGuard
	inner http.RoundTripper
}

func (t guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		return nil, ErrAddressRejected
	}
	if err := t.guard.CheckURL(r.URL); err != nil {
		return nil, err
	}
	return t.inner.RoundTrip(r)
}

func (g *NetGuard) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout, Transport: guardedTransport{guard: g, inner: g.transport},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if g.policy.NoRedirect || len(via) > 3 || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
				return ErrAddressRejected
			}
			return g.CheckURL(req.URL)
		},
	}
}

func (g *NetGuard) WSDialContext(ctx context.Context, rawURL string) (*websocket.Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "ws" && u.Scheme != "wss") {
		return nil, ErrAddressRejected
	}
	if err := g.CheckURL(u); err != nil {
		return nil, err
	}
	conn, response, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{HTTPClient: g.HTTPClient(0)})
	if err != nil && response != nil && response.Body != nil {
		_ = response.Body.Close()
	}
	return conn, err
}

// GorillaDialer 给晚风使用，和 HTTP/coder WS 共用目标检查及直连拨号规则。
func (g *NetGuard) GorillaDialer() *gorilla.Dialer {
	d := &gorilla.Dialer{HandshakeTimeout: 10 * time.Second, NetDialContext: g.dialDirect}
	if g.proxy != nil {
		d.NetDialContext = g.dial
	}
	d.Proxy = func(req *http.Request) (*url.URL, error) {
		if err := g.CheckURL(req.URL); err != nil {
			return nil, err
		}
		if g.proxy != nil && g.proxy.Scheme == "https" {
			return nil, nil
		}
		return g.proxy, nil
	}
	// gorilla 内置代理仅支持 HTTP 和 SOCKS5，HTTPS 代理显式建立 TLS CONNECT。
	if g.proxy != nil && g.proxy.Scheme == "https" {
		d.NetDialContext = g.dialHTTPSProxy
	}
	return d
}

func (g *NetGuard) dialHTTPSProxy(ctx context.Context, network, target string) (net.Conn, error) {
	address := g.proxy.Host
	if g.proxy.Port() == "" {
		address = net.JoinHostPort(g.proxy.Hostname(), "443")
	}
	raw, err := g.dial(ctx, network, address)
	if err != nil {
		return nil, err
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if g.transport.TLSClientConfig != nil {
		tlsConfig = g.transport.TLSClientConfig.Clone()
	}
	tlsConfig.ServerName = g.proxy.Hostname()
	conn := tls.Client(raw, tlsConfig)
	if err := conn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	req := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if user := g.proxy.User; user != nil {
		password, _ := user.Password()
		req.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user.Username()+":"+password)))
	}
	if err := req.Write(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	reader := bufio.NewReader(conn)
	res, err := http.ReadResponse(reader, req)
	if err != nil || res.StatusCode != http.StatusOK {
		_ = conn.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("代理 CONNECT 失败: %s", res.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	return &bufferedProxyConn{Conn: conn, reader: reader}, nil
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
