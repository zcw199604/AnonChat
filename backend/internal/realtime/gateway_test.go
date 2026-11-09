package realtime_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"sync/atomic"
)

const testOrigin = "http://test.local"

// ---- 测试替身 ----

type fakeResolver struct {
	mu      sync.Mutex
	byToken map[string]*store.Identity
}

func newFakeResolver() *fakeResolver {
	return &fakeResolver{byToken: make(map[string]*store.Identity)}
}

func (f *fakeResolver) add(token string, id *store.Identity) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byToken[token] = id
}

func (f *fakeResolver) Resolve(_ context.Context, token string) (*store.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.byToken[token]; ok {
		return id, nil
	}
	return nil, identity.ErrUnauthorized
}

type fakeBans struct{ banned map[pgtype.UUID]bool }

func (b *fakeBans) IsBanned(_ context.Context, id *store.Identity, _ string) bool {
	return b.banned[id.ID]
}

type accessEvent struct {
	identityID pgtype.UUID
	event      string
	ip         string
}

type fakeAccess struct {
	mu     sync.Mutex
	events []accessEvent
}

func (a *fakeAccess) LogAccess(_ context.Context, identityID pgtype.UUID, event, ip, _ string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, accessEvent{identityID, event, ip})
}

func (a *fakeAccess) count(event string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, e := range a.events {
		if e.event == event {
			n++
		}
	}
	return n
}

func mkUUID(n byte) pgtype.UUID {
	var b [16]byte
	b[15] = n
	return pgtype.UUID{Bytes: b, Valid: true}
}

func mkIdentity(n byte, nickname string) *store.Identity {
	return &store.Identity{ID: mkUUID(n), Nickname: nickname, AvatarSeed: "seed-" + nickname}
}

// ---- 环境搭建 ----

type envOpts struct {
	settings   *moderation.LimitSettings
	observer   realtime.ConnObserver
	bans       realtime.BanChecker
	maxFrame   int64
	idle       time.Duration
	sendBuffer int
}

type testEnv struct {
	gw  *realtime.Gateway
	srv *httptest.Server
	acc *fakeAccess
}

func newTestEnv(t *testing.T, resolver realtime.IdentityResolver, o envOpts) *testEnv {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ipResolver, err := httpx.NewClientIPResolver(nil)
	if err != nil {
		t.Fatalf("构造 ClientIPResolver 失败: %v", err)
	}
	acc := &fakeAccess{}
	gw := realtime.NewGateway(realtime.Options{
		AllowedOrigins: []string{testOrigin},
		LimitSettings:  o.settings,
		MaxFrameBytes:  o.maxFrame,
		IdleTimeout:    o.idle,
		SendBuffer:     o.sendBuffer,
	}, logger, resolver, o.bans, acc, ipResolver)

	if o.observer != nil {
		gw.AddConnObserver(o.observer)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws", gw.ServeWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &testEnv{gw: gw, srv: srv, acc: acc}
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

// dial 建立连接（不读取 hello）。
func (e *testEnv) dial(t *testing.T, token, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	h := http.Header{}
	if origin != "" {
		h.Set("Origin", origin)
	}
	if token != "" {
		h.Set("Cookie", identity.CookieName+"="+token)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return websocket.Dial(ctx, wsURL(e.srv), &websocket.DialOptions{HTTPHeader: h})
}

// connect 建立连接并读取 hello。
func (e *testEnv) connect(t *testing.T, token string) (*websocket.Conn, realtime.Envelope) {
	t.Helper()
	c, _, err := e.dial(t, token, testOrigin)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	hello := readEnvelope(t, c, 3*time.Second)
	if hello.Type != realtime.TypeHello {
		t.Fatalf("首帧应为 hello, 实际 %q", hello.Type)
	}
	return c, hello
}

func readEnvelope(t *testing.T, c *websocket.Conn, timeout time.Duration) realtime.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("读取消息失败: %v", err)
	}
	var env realtime.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("解析信封失败: %v, data=%s", err, data)
	}
	return env
}

func writeEnvelope(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}

// ---- 握手测试 ----

func TestHandshakeRejectedWithoutCookie(t *testing.T) {
	res := newFakeResolver()
	e := newTestEnv(t, res, envOpts{})
	c, resp, err := e.dial(t, "", testOrigin)
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("无 Cookie 应握手失败")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("期望 401, 实际 %v", statusOf(resp))
	}
}

func TestHandshakeRejectedBadOrigin(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(1, "a"))
	e := newTestEnv(t, res, envOpts{})
	c, resp, err := e.dial(t, "tok", "http://evil.example")
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("非法 Origin 应握手失败")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("期望 403, 实际 %v", statusOf(resp))
	}
}

func TestHandshakeRejectedBanned(t *testing.T) {
	id := mkIdentity(2, "banned")
	res := newFakeResolver()
	res.add("tok", id)
	bans := &fakeBans{banned: map[pgtype.UUID]bool{id.ID: true}}
	e := newTestEnv(t, res, envOpts{bans: bans})
	c, resp, err := e.dial(t, "tok", testOrigin)
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("被封禁应握手失败")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("期望 403, 实际 %v", statusOf(resp))
	}
}

func statusOf(resp *http.Response) any {
	if resp == nil {
		return "nil"
	}
	return resp.StatusCode
}

func TestHelloOnConnect(t *testing.T) {
	id := mkIdentity(3, "迷路的猫")
	res := newFakeResolver()
	res.add("tok", id)
	e := newTestEnv(t, res, envOpts{})

	_, hello := e.connect(t, "tok")
	var p realtime.HelloPayload
	if err := json.Unmarshal(hello.Data, &p); err != nil {
		t.Fatalf("解析 hello 失败: %v", err)
	}
	if p.Nickname != "迷路的猫" || p.AvatarSeed != id.AvatarSeed {
		t.Errorf("hello 负载不符: %+v", p)
	}
	if p.Resumable == nil {
		t.Error("resumable 应为数组（可为空）")
	}
	if hello.TS == 0 {
		t.Error("服务端消息应包含 ts")
	}
}

func TestPingPong(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(4, "p"))
	e := newTestEnv(t, res, envOpts{})
	c, _ := e.connect(t, "tok")

	writeEnvelope(t, c, realtime.Envelope{Type: realtime.TypePing, ID: "req-1"})
	pong := readEnvelope(t, c, 3*time.Second)
	if pong.Type != realtime.TypePong {
		t.Fatalf("应收到 pong, 实际 %q", pong.Type)
	}
	if pong.ID != "req-1" {
		t.Errorf("pong.id = %q, 期望回显 req-1", pong.ID)
	}
}

func TestUnknownTypeInvalidRequest(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(5, "u"))
	e := newTestEnv(t, res, envOpts{})
	c, _ := e.connect(t, "tok")

	writeEnvelope(t, c, map[string]any{"t": "bogus", "id": "x"})
	env := readEnvelope(t, c, 3*time.Second)
	if env.Type != realtime.TypeError {
		t.Fatalf("应收到 error, 实际 %q", env.Type)
	}
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(env.Data, &ep)
	if ep.Code != realtime.CodeInvalidRequest {
		t.Errorf("错误码 = %q, 期望 invalid_request", ep.Code)
	}

	// 连接应保持存活。
	writeEnvelope(t, c, realtime.Envelope{Type: realtime.TypePing, ID: "alive"})
	if pong := readEnvelope(t, c, 3*time.Second); pong.Type != realtime.TypePong {
		t.Fatal("未知类型后连接应仍可用")
	}
}

func TestBadJSONInvalidRequest(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(6, "j"))
	e := newTestEnv(t, res, envOpts{})
	c, _ := e.connect(t, "tok")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageText, []byte("not json")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	env := readEnvelope(t, c, 3*time.Second)
	if env.Type != realtime.TypeError {
		t.Fatalf("应收到 error, 实际 %q", env.Type)
	}
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(env.Data, &ep)
	if ep.Code != realtime.CodeInvalidRequest {
		t.Errorf("错误码 = %q, 期望 invalid_request", ep.Code)
	}
	// 连接仍可用。
	writeEnvelope(t, c, realtime.Envelope{Type: realtime.TypePing, ID: "alive"})
	if pong := readEnvelope(t, c, 3*time.Second); pong.Type != realtime.TypePong {
		t.Fatal("JSON 解析失败后连接应仍可用")
	}
}

func TestOversizeFrameCloses(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(7, "o"))
	e := newTestEnv(t, res, envOpts{maxFrame: 1024})
	c, _ := e.connect(t, "tok")

	big := strings.Repeat("a", 2048)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageText, []byte(big))

	// 服务端应以 1009（消息过大）关闭连接。
	readCtx, readCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer readCancel()
	_, _, err := c.Read(readCtx)
	if err == nil {
		t.Fatal("超大帧应导致连接断开")
	}
	if cs := websocket.CloseStatus(err); cs != websocket.StatusMessageTooBig {
		t.Errorf("关闭码 = %d, 期望 %d", cs, websocket.StatusMessageTooBig)
	}
}

func TestIdleTimeoutCloses(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(8, "i"))
	e := newTestEnv(t, res, envOpts{idle: 300 * time.Millisecond})
	c, _ := e.connect(t, "tok")

	// 不再发送任何帧，服务端应在空闲超时后断开。
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("空闲超时后读应失败/连接已关闭")
	}
}

// ---- Kick 与 Presence ----

func TestKickClosesAllConnsOfIdentity(t *testing.T) {
	id := mkIdentity(9, "k")
	res := newFakeResolver()
	res.add("tok", id)
	e := newTestEnv(t, res, envOpts{})

	c1, _ := e.connect(t, "tok")
	c2, _ := e.connect(t, "tok")
	if got := e.gw.Hub().ConnCount(id.ID); got != 2 {
		t.Fatalf("同一身份连接数 = %d, 期望 2", got)
	}

	e.gw.Hub().Kick(id.ID, realtime.KickReasonBanned)

	for i, c := range []*websocket.Conn{c1, c2} {
		kicked := readEnvelope(t, c, 3*time.Second)
		if kicked.Type != realtime.TypeKicked {
			t.Fatalf("连接 %d 应收到 kicked, 实际 %q", i+1, kicked.Type)
		}
		var kp realtime.KickedPayload
		_ = json.Unmarshal(kicked.Data, &kp)
		if kp.Reason != realtime.KickReasonBanned {
			t.Errorf("kicked.reason = %q, 期望 banned", kp.Reason)
		}
		// 之后连接应被关闭。
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if _, _, err := c.Read(ctx); err == nil {
			t.Errorf("连接 %d 收到 kicked 后应被关闭", i+1)
		}
		cancel()
	}
}

func TestGracefulCloseAll(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(10, "g"))
	e := newTestEnv(t, res, envOpts{})
	c, _ := e.connect(t, "tok")

	e.gw.CloseAll()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if err == nil {
		t.Fatal("CloseAll 后连接应被关闭")
	}
	if cs := websocket.CloseStatus(err); cs != websocket.StatusGoingAway {
		t.Errorf("关闭码 = %d, 期望 %d", cs, websocket.StatusGoingAway)
	}
}

func TestAccessLogOnConnectAndDisconnect(t *testing.T) {
	res := newFakeResolver()
	res.add("tok", mkIdentity(11, "l"))
	e := newTestEnv(t, res, envOpts{})
	c, _ := e.connect(t, "tok")
	// 关闭客户端，等待服务端写断开日志。
	_ = c.CloseNow()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if e.acc.count("ws_connect") >= 1 && e.acc.count("ws_disconnect") >= 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("访问日志不足: connect=%d disconnect=%d", e.acc.count("ws_connect"), e.acc.count("ws_disconnect"))
}

type admissionObserver struct {
	opened atomic.Int32
	closed atomic.Int32
}

func (o *admissionObserver) OnConnOpened(*realtime.Conn) { o.opened.Add(1) }
func (o *admissionObserver) OnConnClosed(*realtime.Conn) { o.closed.Add(1) }

// TestRejectedAdmissionSkipsServe 验证拒绝分支不发送hello、不记录访问、不通知观察者。
func TestRejectedAdmissionSkipsServe(t *testing.T) {
	resolver := newFakeResolver()
	for n := byte(1); n <= 4; n++ {
		resolver.add(string(rune('a'+n)), mkIdentity(n, "用户"))
	}
	observer := &admissionObserver{}
	env := newTestEnv(t, resolver, envOpts{settings: moderation.NewLimitSettings(nil, 1, nil), observer: observer})
	for n := byte(1); n <= 3; n++ {
		env.connect(t, string(rune('a'+n)))
	}
	c, _, err := env.dial(t, "e", testOrigin)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	rejected := readEnvelope(t, c, time.Second)
	if rejected.Type != realtime.TypeKicked {
		t.Fatalf("首帧=%s，期望kicked", rejected.Type)
	}
	var payload realtime.KickedPayload
	if err := json.Unmarshal(rejected.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Reason != "ip_conn_limit" {
		t.Fatalf("拒绝原因=%s", payload.Reason)
	}
	if observer.opened.Load() != 3 || env.acc.count("ws_connect") != 3 {
		t.Fatal("拒绝连接触发了serve副作用")
	}
	if env.gw.Hub().ConnCount(mkUUID(4)) != 0 {
		t.Fatal("拒绝连接已登记Hub")
	}
}
