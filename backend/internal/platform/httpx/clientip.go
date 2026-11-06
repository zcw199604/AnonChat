package httpx

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// ClientIPResolver 依据可信代理列表解析客户端真实 IP。
// 只有当直连来源（RemoteAddr）属于可信代理时才采信 X-Forwarded-For，
// 否则一律使用 RemoteAddr，避免伪造头绕过限流与封禁。
type ClientIPResolver struct {
	trusted []netip.Prefix
}

// NewClientIPResolver 用 CIDR 列表构造解析器。非法 CIDR 返回错误。
func NewClientIPResolver(cidrs []string) (*ClientIPResolver, error) {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, c := range cidrs {
		p, err := netip.ParsePrefix(strings.TrimSpace(c))
		if err != nil {
			return nil, fmt.Errorf("非法的可信代理 CIDR %q: %w", c, err)
		}
		prefixes = append(prefixes, p)
	}
	return &ClientIPResolver{trusted: prefixes}, nil
}

// isTrusted 报告给定地址是否落在可信代理范围内。
func (res *ClientIPResolver) isTrusted(ip netip.Addr) bool {
	for _, p := range res.trusted {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP 返回请求的客户端 IP 字符串。
// 规则：直连来源不可信 → 直接返回 RemoteAddr；可信 → 从 X-Forwarded-For 右侧向左取第一个非可信地址。
func (res *ClientIPResolver) ClientIP(r *http.Request) string {
	remote, ok := remoteIP(r)
	if !ok {
		return ""
	}
	if !res.isTrusted(remote) {
		return remote.String()
	}

	addrs := forwardedAddrs(r)
	// 从右向左（最靠近我们的代理在右边）寻找第一个非可信地址。
	for i := len(addrs) - 1; i >= 0; i-- {
		ip, err := netip.ParseAddr(addrs[i])
		if err != nil {
			continue
		}
		if !res.isTrusted(ip) {
			return ip.String()
		}
	}
	// 全部可信（或头缺失/非法）：最左侧是原始客户端，否则退回 RemoteAddr。
	if len(addrs) > 0 {
		if ip, err := netip.ParseAddr(addrs[0]); err == nil {
			return ip.String()
		}
	}
	return remote.String()
}

// remoteIP 从 RemoteAddr 中解析出主机 IP。
func remoteIP(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	// 去掉 IPv6 可能带的方括号。
	host = strings.Trim(host, "[]")
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return ip, true
}

// forwardedAddrs 汇总所有 X-Forwarded-For 头（可能多个头、多个逗号分隔地址）。
func forwardedAddrs(r *http.Request) []string {
	var out []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, part := range strings.Split(h, ",") {
			if s := strings.TrimSpace(part); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
