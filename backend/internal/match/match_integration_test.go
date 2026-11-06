//go:build integration

package match_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func mkIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "tok-" + name + "-" + time.Now().Format("150405.000000000")
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "seed-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func dial(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", "__Host-anon="+token)
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func send(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
}

func read(t *testing.T, c *websocket.Conn, timeout time.Duration) realtime.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var env realtime.Envelope
	_ = json.Unmarshal(data, &env)
	return env
}

func connectHello(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	c := dial(t, srv, token)
	if env := read(t, c, 3*time.Second); env.Type != realtime.TypeHello {
		t.Fatalf("首帧应为 hello, 实际 %q", env.Type)
	}
	return c
}

func matchStart(t *testing.T, c *websocket.Conn) {
	t.Helper()
	send(t, c, map[string]any{"t": realtime.TypeMatchStart, "id": "start", "d": map[string]any{}})
}

func mustFound(t *testing.T, c *websocket.Conn) string {
	t.Helper()
	var env realtime.Envelope
	for i := 0; i < 5; i++ {
		env = read(t, c, 3*time.Second)
		if env.Type == realtime.TypeMatchWaiting {
			continue
		}
		break
	}
	if env.Type != realtime.TypeMatchFound {
		t.Fatalf("应为 match.found, 实际 %q (%s)", env.Type, env.Data)
	}
	// how.md 3.4：peer 里去掉了 nickname 与 avatarSeed，只保留 memberId 与公开资料。
	var p struct {
		ConversationID string `json:"conversationId"`
		Peer           struct {
			MemberID int32    `json:"memberId"`
			Gender   *string  `json:"gender"`
			Tags     []string `json:"tags"`
		} `json:"peer"`
	}
	if err := json.Unmarshal(env.Data, &p); err != nil {
		t.Fatalf("解析 match.found 失败: %v", err)
	}
	if p.ConversationID == "" || p.Peer.MemberID == 0 {
		t.Fatalf("match.found 载荷不全: %s", env.Data)
	}
	// 防泄漏：事件负载里不得出现任何身份标识字段（how.md 3.8）。
	for _, forbidden := range []string{"identity_id", "identityId", "token", "ip", "account", "phone"} {
		if strings.Contains(string(env.Data), `"`+forbidden+`"`) {
			t.Fatalf("match.found 出现禁止字段 %q: %s", forbidden, env.Data)
		}
	}
	if strings.Contains(string(env.Data), "nickname") || strings.Contains(string(env.Data), "avatarSeed") {
		t.Fatalf("match.found 不应包含 nickname/avatarSeed: %s", env.Data)
	}
	return p.ConversationID
}

func pairTwo(t *testing.T, srv *httptest.Server, tokA, tokB string) (*websocket.Conn, *websocket.Conn, string) {
	t.Helper()
	wsA := connectHello(t, srv, tokA)
	wsB := connectHello(t, srv, tokB)
	matchStart(t, wsA)
	if e := read(t, wsA, 3*time.Second); e.Type != realtime.TypeMatchWaiting {
		t.Fatalf("A 应收到 waiting, 实际 %q", e.Type)
	}
	matchStart(t, wsB)
	convA := mustFound(t, wsA)
	convB := mustFound(t, wsB)
	if convA != convB {
		t.Fatalf("双方会话不一致: %s vs %s", convA, convB)
	}
	return wsA, wsB, convA
}

func TestMatchFoundAndChat(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")

	wsA, wsB, convID := pairTwo(t, env.Server, tokA, tokB)

	// B 发送消息，A 收到 msg.new。
	send(t, wsB, map[string]any{"t": realtime.TypeMsgSend, "id": "s1", "d": map[string]any{
		"conversationId": convID, "clientMsgId": "11111111-1111-1111-1111-111111111111", "kind": "text", "body": "你好",
	}})
	if e := read(t, wsB, 3*time.Second); e.Type != realtime.TypeAck {
		t.Fatalf("B 应收到 ack, 实际 %q", e.Type)
	}
	if e := read(t, wsA, 3*time.Second); e.Type != realtime.TypeMsgNew {
		t.Fatalf("A 应收到 msg.new, 实际 %q", e.Type)
	}
}

func TestMatchNextPeerSkipped(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	wsA, wsB, _ := pairTwo(t, env.Server, tokA, tokB)
	_ = wsA

	send(t, wsB, map[string]any{"t": realtime.TypeMatchNext, "id": "n", "d": map[string]any{}})
	// A 收到 match.ended(peer_skipped)。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e := read(t, wsA, 3*time.Second)
		if e.Type == realtime.TypeMatchEnded {
			var p struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(e.Data, &p)
			if p.Reason != "peer_skipped" {
				t.Fatalf("reason = %q, 期望 peer_skipped", p.Reason)
			}
			return
		}
	}
	t.Fatal("A 未收到 match.ended")
}

func TestMatchLeavePeerLeft(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	wsA, wsB, _ := pairTwo(t, env.Server, tokA, tokB)
	_ = wsB

	send(t, wsA, map[string]any{"t": realtime.TypeMatchLeave, "id": "l", "d": map[string]any{}})
	e := read(t, wsB, 3*time.Second)
	if e.Type != realtime.TypeMatchEnded {
		t.Fatalf("B 应收到 match.ended, 实际 %q", e.Type)
	}
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(e.Data, &p)
	if p.Reason != "peer_left" {
		t.Fatalf("reason = %q, 期望 peer_left", p.Reason)
	}
}

func TestGraceResumeWithinGrace(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		MatchRelaxAfter: 50 * time.Millisecond,
		MatchGrace:      800 * time.Millisecond,
	})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	wsA, wsB, convID := pairTwo(t, env.Server, tokA, tokB)

	// A 断线。
	_ = wsA.CloseNow()
	time.Sleep(200 * time.Millisecond) // < grace
	// 宽限期内重连。
	wsA2 := connectHello(t, env.Server, tokA)
	// 会话仍存活（未被结束）。
	rc := parseUUID(t, convID)
	var endedAt pgtype.Timestamptz
	if err := pool.QueryRow(ctx, "SELECT ended_at FROM conversations WHERE id=$1", rc).Scan(&endedAt); err != nil {
		t.Fatal(err)
	}
	if endedAt.Valid {
		t.Fatal("宽限期内会话不应结束")
	}
	_ = wsB

	// A2 重新订阅会话后仍可接收消息。
	send(t, wsA2, map[string]any{"t": realtime.TypeConvSubscribe, "id": "sub", "d": map[string]any{"conversationIds": []string{convID}}})
	send(t, wsB, map[string]any{"t": realtime.TypeMsgSend, "id": "s1", "d": map[string]any{
		"conversationId": convID, "clientMsgId": "22222222-2222-2222-2222-222222222222", "kind": "text", "body": "still here",
	}})
	_ = read(t, wsB, 3*time.Second) // ack
	if e := read(t, wsA2, 3*time.Second); e.Type != realtime.TypeMsgNew {
		t.Fatalf("重连后 A 应能收到消息, 实际 %q", e.Type)
	}
}

func TestGraceTimeoutPeerLeft(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		MatchRelaxAfter: 50 * time.Millisecond,
		MatchGrace:      300 * time.Millisecond,
	})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	wsA, wsB, _ := pairTwo(t, env.Server, tokA, tokB)

	_ = wsA.CloseNow() // 断线超过宽限
	e := read(t, wsB, 3*time.Second)
	if e.Type != realtime.TypeMatchEnded {
		t.Fatalf("B 应收到 match.ended, 实际 %q", e.Type)
	}
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(e.Data, &p)
	if p.Reason != "peer_left" {
		t.Fatalf("reason = %q, 期望 peer_left", p.Reason)
	}
}

func TestQueueDisconnectRemoved(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchRelaxAfter: 50 * time.Millisecond})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")

	wsA := connectHello(t, env.Server, tokA)
	matchStart(t, wsA)
	_ = read(t, wsA, 3*time.Second) // waiting
	_ = wsA.CloseNow()              // 排队中断线 → 出队

	wsB := connectHello(t, env.Server, tokB)
	matchStart(t, wsB)
	if e := read(t, wsB, 3*time.Second); e.Type != realtime.TypeMatchWaiting {
		t.Fatalf("B 应收到 waiting（A 已出队）, 实际 %q", e.Type)
	}
	var nMatch int
	pool.QueryRow(ctx, "SELECT count(*) FROM conversations WHERE kind='match'").Scan(&nMatch)
	if nMatch != 0 {
		t.Fatalf("排队中断线后不应匹配成会话, 实际 %d", nMatch)
	}
}

func TestRestartCleanupExpiresOpenMatches(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, _ := mkIdentity(t, pool, "alice")
	b, _ := mkIdentity(t, pool, "bob")

	// 直接在库中制造一个未结束的 match 会话。
	svc := conversation.New(pool, nil, nil)
	tx, _ := pool.Begin(ctx)
	conv, _, err := svc.Create(ctx, tx, store.ConversationKindMatch, false, a.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = tx.Commit(ctx)

	if err := env.Deps.Match.CleanupOnStartup(ctx); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	var ended pgtype.Timestamptz
	var r *string
	if err := pool.QueryRow(ctx, "SELECT ended_at, end_reason FROM conversations WHERE id=$1", conv.ID).Scan(&ended, &r); err != nil {
		t.Fatal(err)
	}
	if !ended.Valid || r == nil || *r != "expired" {
		t.Errorf("启动清理应以 expired 结束会话, ended=%v reason=%v", ended.Valid, r)
	}
}

func parseUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("解析 UUID 失败: %v", err)
	}
	return u
}
