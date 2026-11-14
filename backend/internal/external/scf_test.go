package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/gorilla/websocket"
)

// 隧道模式的一切测试只使用本地假上游：不连真实腾讯云，也不连真实晚风。

func tunnelGuard(t *testing.T, hosts []string, dial func(context.Context, string, string) (net.Conn, error)) *NetGuard {
	t.Helper()
	return mustGuard(t, Policy{AllowedHosts: hosts}, "", WithTunnelDial(dial))
}

// TestNetGuardTunnelCarriesHTTP 确认 HTTP 请求全部经隧道，且隧道收到的是原始主机名。
func TestNetGuardTunnelCarriesHTTP(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "wanfeng.test" {
			t.Errorf("Host=%q", r.Host)
		}
		_, _ = io.WriteString(w, "tunneled "+r.URL.Path)
	}))
	defer upstream.Close()
	var addrs []string
	var mu sync.Mutex
	g := tunnelGuard(t, []string{"wanfeng.test"}, func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		addrs = append(addrs, addr)
		mu.Unlock()
		if network != "tcp" {
			return nil, fmt.Errorf("unexpected network %q", network)
		}
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(upstream.URL, "http://"))
	})
	res, err := g.HTTPClient(time.Second).Get("http://wanfeng.test/media")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || string(body) != "tunneled /media" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 1 || addrs[0] != "wanfeng.test:80" {
		t.Fatalf("隧道拨号地址不是主机名:端口: %v", addrs)
	}
}

// TestNetGuardTunnelCarriesGorillaWebSocket 确认晚风 WS 拨号同样使用隧道。
func TestNetGuardTunnelCarriesGorillaWebSocket(t *testing.T) {
	upgrader := websocket.Upgrader{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			kind, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(kind, data); err != nil {
				return
			}
		}
	}))
	defer upstream.Close()
	var addrs []string
	var mu sync.Mutex
	g := tunnelGuard(t, []string{"socket.test"}, func(ctx context.Context, network, addr string) (net.Conn, error) {
		mu.Lock()
		addrs = append(addrs, addr)
		mu.Unlock()
		return (&net.Dialer{}).DialContext(ctx, network, strings.TrimPrefix(upstream.URL, "http://"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := g.GorillaDialer().DialContext(ctx, "ws://socket.test/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil || string(data) != "ping" {
		t.Fatalf("回显失败: %q err=%v", data, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(addrs) != 1 || addrs[0] != "socket.test:80" {
		t.Fatalf("WS 隧道拨号地址不是主机名:端口: %v", addrs)
	}
}

// TestNetGuardTunnelRejectsOutsideAllowlist 确认白名单外主机在拨隧道之前就被拒绝。
func TestNetGuardTunnelRejectsOutsideAllowlist(t *testing.T) {
	var calls atomic.Int32
	g := tunnelGuard(t, []string{"wanfeng.test"}, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("unexpected tunnel dial")
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := g.HTTPClient(time.Second).Get("http://forbidden.test/"); !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("HTTP 未被拒绝: %v", err)
	}
	if _, _, err := g.GorillaDialer().DialContext(ctx, "ws://forbidden.test/ws", nil); !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("WS 未被拒绝: %v", err)
	}
	// 直接调用拨号器（例如 WS 升级所需）也必须拒绝。
	if _, err := g.transport.DialContext(ctx, "tcp", "forbidden.test:443"); !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("拨号未被拒绝: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatalf("白名单外主机调用了隧道 %d 次", calls.Load())
	}
}

// TestNetGuardTunnelNeverFallsBackToDirect 确认隧道失败时不会直连到真实上游。
func TestNetGuardTunnelNeverFallsBackToDirect(t *testing.T) {
	var directHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	// AllowPrivate 仅用于本地测试：让字面量回环地址通过 IP 规则，检查重点是不回退直连。
	g := mustGuard(t, Policy{AllowPrivate: true, AllowedHosts: []string{"127.0.0.1"}}, "", WithTunnelDial(func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("隧道拒绝")
	}))
	res, err := g.HTTPClient(time.Second).Get(upstream.URL + "/x")
	if res != nil {
		res.Body.Close()
	}
	if err == nil || directHits.Load() != 0 {
		t.Fatalf("隧道失败后回退到了直连: hits=%d err=%v", directHits.Load(), err)
	}
}

// TestNetGuardTunnelRejectsNonPublicLiteralIP 确认隧道模式仍然拒绝内网字面量 IP。
func TestNetGuardTunnelRejectsNonPublicLiteralIP(t *testing.T) {
	var calls atomic.Int32
	g := tunnelGuard(t, []string{"127.0.0.1"}, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, errors.New("unexpected tunnel dial")
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := g.transport.DialContext(ctx, "tcp", "127.0.0.1:80"); !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("内网字面量 IP 未被拒绝: %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("内网字面量 IP 调用了隧道")
	}
}

// TestNetGuardTunnelRejectsProxyAndMissingAllowlist 确认隧道模式的两个构造前提。
func TestNetGuardTunnelRejectsProxyAndMissingAllowlist(t *testing.T) {
	dial := func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("unused") }
	if g, err := NewNetGuard(Policy{AllowedHosts: []string{"wanfeng.test"}}, "http://proxy.test:8080", WithTunnelDial(dial)); err == nil || g != nil || !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("隧道与代理同时设置未被拒绝: g=%v err=%v", g, err)
	}
	if g, err := NewNetGuard(Policy{}, "", WithTunnelDial(dial)); err == nil || g != nil || !errors.Is(err, ErrAddressRejected) {
		t.Fatalf("隧道模式缺少白名单未被拒绝: g=%v err=%v", g, err)
	}
}

// writeSCFTestPool 生成与 scfproxy 池文件格式一致的三节点固定池。
func writeSCFTestPool(t *testing.T, size int) string {
	t.Helper()
	entries := []map[string]any{
		{"url": "ws://127.0.0.1:19001", "region": "ap-guangzhou", "function": "anonchat-main", "egress_ip": "192.0.2.1"},
		{"url": "ws://127.0.0.1:19002", "region": "ap-shanghai", "function": "anonchat-standby", "egress_ip": "192.0.2.2"},
		{"url": "ws://127.0.0.1:19003", "region": "ap-beijing", "function": "anonchat-spare", "egress_ip": "192.0.2.3"},
	}
	data, err := json.Marshal(map[string]any{"version": 1, "active": entries[:size], "retired": []any{}, "banned_ips": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "pool.json")
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

// TestSCFEgressSwitchesPoolOnceForConcurrentReports 确认并发上报只切换一次，健康备用不被轮换。
func TestSCFEgressSwitchesPoolOnceForConcurrentReports(t *testing.T) {
	poolFile := writeSCFTestPool(t, 3)
	_, egress, cleanup, err := newWanfengSCF(&config.Config{ExternalWanfengSCFEnabled: true, SCFProxyPoolFile: poolFile, SCFProxyToken: "test-only-token"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if egress.Current() != "ws://127.0.0.1:19001" {
		t.Fatalf("初始出口错误: %s", egress.Current())
	}
	old := egress.Current()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := egress.Switch(old); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if got := egress.Current(); got != "ws://127.0.0.1:19002" {
		t.Fatalf("过期上报把刚切好的备用又切走了: %s", got)
	}
	data, err := os.ReadFile(poolFile)
	if err != nil {
		t.Fatal(err)
	}
	var pool struct {
		Active    []json.RawMessage `json:"active"`
		Retired   []json.RawMessage `json:"retired"`
		BannedIPs []json.RawMessage `json:"banned_ips"`
	}
	if err := json.Unmarshal(data, &pool); err != nil {
		t.Fatal(err)
	}
	if len(pool.Active) != 2 || len(pool.Retired) != 1 || len(pool.BannedIPs) != 1 {
		t.Fatalf("池状态异常: %s", data)
	}
}

// TestConfiguredBridgeSurvivesSCFFailure 确认隧道初始化失败只停用晚风，chat2019 与启动不受影响。
func TestConfiguredBridgeSurvivesSCFFailure(t *testing.T) {
	cfg := &config.Config{
		ExternalTimeout:               time.Second,
		ExternalChat2019RandServerURL: "http://chat2019.test/getRandServer",
		ExternalChat2019ImgServerURL:  "http://chat2019.test/getImgServer",
		ExternalWanfengBaseURL:        "https://wanfeng.test",
		ExternalWanfengSCFEnabled:     true,
		SCFProxyPoolFile:              filepath.Join(t.TempDir(), "missing", "pool.json"),
		SCFProxyToken:                 "test-only-token",
	}
	b, err := NewConfiguredBridge(BridgeDeps{}, cfg)
	if err != nil {
		t.Fatalf("隧道失败不应阻止启动: %v", err)
	}
	defer b.Close()
	if b.deps.Wanfeng != nil || b.deps.WanfengDialer != nil {
		t.Fatal("隧道失败后晚风仍然被装配")
	}
	if b.deps.Chat2019.HTTPClient == nil || b.deps.Chat2019.WSDialer == nil {
		t.Fatal("隧道失败波及了 chat2019 装配")
	}
	for _, p := range b.Status().Providers {
		if p.Name == "wanfeng" && p.Available {
			t.Fatal("缺少隧道时晚风不应可用")
		}
	}
}

// fakeSCFTunnel 是本地假 SCF 协议对端：完成带内握手后把字节转发到 upstream。
// 它只验证客户端侧装配，不模拟腾讯云函数。
func fakeSCFTunnel(t *testing.T, upstreamURL string) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer ws.Close()
		var hello struct {
			Target string `json:"target"`
			Sign   string `json:"sign"`
		}
		if err := ws.ReadJSON(&hello); err != nil {
			t.Error(err)
			return
		}
		if hello.Target != "wanfeng.test:80" || hello.Sign == "" {
			t.Errorf("握手 target/sign 异常: %q", hello.Target)
			return
		}
		conn, err := net.DialTimeout("tcp", strings.TrimPrefix(upstreamURL, "http://"), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if err := ws.WriteJSON(map[string]any{"ok": true}); err != nil {
			return
		}
		go func() {
			defer ws.Close()
			buf := make([]byte, 8192)
			for {
				n, err := conn.Read(buf)
				if n > 0 {
					if err := ws.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		for {
			kind, reader, err := ws.NextReader()
			if err != nil {
				return
			}
			if kind != websocket.BinaryMessage {
				t.Errorf("意外帧类型: %d", kind)
				return
			}
			if _, err := io.Copy(conn, reader); err != nil {
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestConfiguredBridgeRoutesWanfengThroughSCF 用本地假隧道对端验证晚风流量确实走隧道。
func TestConfiguredBridgeRoutesWanfengThroughSCF(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "tunneled")
	}))
	defer upstream.Close()
	tunnel := fakeSCFTunnel(t, upstream.URL)
	poolFile := filepath.Join(t.TempDir(), "pool.json")
	pool := map[string]any{"version": 1, "active": []map[string]any{
		{"url": "ws" + strings.TrimPrefix(tunnel.URL, "http"), "region": "ap-guangzhou", "function": "anonchat-main", "egress_ip": "192.0.2.1"},
		{"url": "ws" + strings.TrimPrefix(tunnel.URL, "http"), "region": "ap-shanghai", "function": "anonchat-standby", "egress_ip": "192.0.2.2"},
	}, "retired": []any{}, "banned_ips": []any{}}
	data, err := json.Marshal(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(poolFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		ExternalTimeout:           time.Second,
		ExternalWanfengBaseURL:    "http://wanfeng.test",
		ExternalWanfengSCFEnabled: true,
		SCFProxyPoolFile:          poolFile,
		SCFProxyToken:             "test-only-token",
	}
	b, err := NewConfiguredBridge(BridgeDeps{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if b.deps.Wanfeng == nil || b.deps.WanfengDialer == nil {
		t.Fatal("隧道成功时未装配晚风")
	}
	// 环境代理不得把请求引到隧道之外。
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	if _, _, err := b.deps.Wanfeng.DownloadMedia(context.Background(), "http://wanfeng.test/media/x.jpg"); err != nil {
		t.Fatalf("晚风请求未经过隧道: %v", err)
	}
}
