//go:build integration

package moderation_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"crypto/rand"
	"fmt"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func mustCreateIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "tok-" + name + "-" + time.Now().Format("150405.000000")
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash:  identity.HashToken(token),
		Nickname:   name,
		AvatarSeed: "seed-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func mustCreateConv(t *testing.T, pool *pgxpool.Pool, kind store.ConversationKind, ids ...pgtype.UUID) store.Conversation {
	t.Helper()
	svc := conversation.New(pool, testLogger(), nil)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	conv, _, err := svc.Create(ctx, tx, kind, false, ids...)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return conv
}

func memberID(t *testing.T, pool *pgxpool.Pool, convID, identID pgtype.UUID) int32 {
	t.Helper()
	var mid int32
	if err := pool.QueryRow(ctx, "SELECT member_id FROM conversation_members WHERE conversation_id=$1 AND identity_id=$2", convID, identID).Scan(&mid); err != nil {
		t.Fatalf("查询 member_id 失败: %v", err)
	}
	return mid
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	return n
}

// historyTokens 通过 REST 历史接口取消息发件人昵称列表。
func historyNames(t *testing.T, srv *httptest.Server, token string, convID pgtype.UUID) []string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodGet, "/api/conversations/"+convID.String()+"/messages", nil, token, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("历史接口状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var out []struct {
		Nickname string `json:"nickname"`
	}
	resp.JSON(t, &out)
	names := make([]string, 0, len(out))
	for _, m := range out {
		names = append(names, m.Nickname)
	}
	return names
}

// sendViaWS 用 WS 让某连接发送一条消息，并等待其 ack。
func sendViaWS(t *testing.T, c *websocket.Conn, convID pgtype.UUID, body string) {
	t.Helper()
	// 每次用唯一请求 id，并等待与之一致的 ack：这样能确保服务端已完成该消息的落库，
	// 避免因连接同时订阅了会话、广播的 msg.new 与 ack 交错导致的读取错位竞态。
	reqID := "s-" + newUUIDStr()
	data, _ := json.Marshal(map[string]any{
		"t": realtime.TypeMsgSend, "id": reqID,
		"d": map[string]any{"conversationId": convID.String(), "clientMsgId": newUUIDStr(), "kind": "text", "body": body},
	})
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		t.Fatalf("发送失败: %v", err)
	}
	for {
		env := readEnv(t, c)
		if env.ID != reqID {
			continue // 跳过交错到达的广播等无关帧
		}
		if env.Type == realtime.TypeError {
			t.Fatalf("发送返回错误: %s", env.Data)
		}
		if env.Type == realtime.TypeAck {
			return
		}
	}
}

func readEnv(t *testing.T, c *websocket.Conn) realtime.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, d, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	var env realtime.Envelope
	_ = json.Unmarshal(d, &env)
	return env
}

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
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

func subscribeWS(t *testing.T, c *websocket.Conn, convID pgtype.UUID) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"t": realtime.TypeConvSubscribe, "id": "sub",
		"d": map[string]any{"conversationIds": []string{convID.String()}},
	})
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = c.Write(wctx, websocket.MessageText, data)
}

func newUUIDStr() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ---- 屏蔽 ----

func TestBlockPeerEndsConversationAndMutualInvisibility(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, tokB := mustCreateIdentity(t, pool, "bob")
	conv := mustCreateConv(t, pool, store.ConversationKindMatch, a.ID, b.ID)
	aMid := memberID(t, pool, conv.ID, a.ID)
	bMid := memberID(t, pool, conv.ID, b.ID)

	// 双方各发一条（直接写库以规避订阅时序）。
	insertMsg(t, pool, conv.ID, a.ID, aMid, "from alice")
	insertMsg(t, pool, conv.ID, b.ID, bMid, "from bob")

	// A 屏蔽对方（1v1），会结束会话。
	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/conversations/"+conv.ID.String()+"/block", nil, tokA, "")
	if resp.Status != http.StatusNoContent {
		t.Fatalf("屏蔽状态 = %d, 期望 204, body=%s", resp.Status, resp.Body)
	}
	if n := count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1 AND ended_at IS NOT NULL", conv.ID); n != 1 {
		t.Error("屏蔽 1v1 后会话应结束")
	}
	if n := count(t, pool, "SELECT count(*) FROM blocks WHERE blocker_identity_id=$1 AND blocked_identity_id=$2", a.ID, b.ID); n != 1 {
		t.Error("应存在 A 屏蔽 B 的记录")
	}

	// 双向不可见：各自历史里只剩自己的消息；1v1 会话透传发送者真实昵称。
	namesA := historyNames(t, env.Server, tokA, conv.ID)
	if len(namesA) != 1 || namesA[0] != a.Nickname {
		t.Errorf("A 历史应只含自己的 1 条消息且昵称为 %q, 实际 %v", a.Nickname, namesA)
	}
	namesB := historyNames(t, env.Server, tokB, conv.ID)
	if len(namesB) != 1 || namesB[0] != b.Nickname {
		t.Errorf("B 历史应只含自己的 1 条消息且昵称为 %q, 实际 %v", b.Nickname, namesB)
	}
}

// insertMsg 直接插入一条文本消息（测试用）。
func insertMsg(t *testing.T, pool *pgxpool.Pool, convID, identID pgtype.UUID, memberID int32, body string) {
	t.Helper()
	q := `INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body)
	      VALUES ($1,$2,$3,gen_random_uuid(),'text',$4)`
	if _, err := pool.Exec(ctx, q, convID, identID, memberID, body); err != nil {
		t.Fatalf("插入消息失败: %v", err)
	}
}

func TestBlockPeerOnRoomRejected(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, tokB := mustCreateIdentity(t, pool, "bob")
	_ = tokB
	conv := mustCreateConv(t, pool, store.ConversationKindRoom, a.ID, b.ID)

	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/conversations/"+conv.ID.String()+"/block", nil, tokA, "")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("房间上一键屏蔽应 400, 实际 %d", resp.Status)
	}
}

func TestBlockMemberInRoom(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, _ := mustCreateIdentity(t, pool, "bob")
	conv := mustCreateConv(t, pool, store.ConversationKindRoom, a.ID, b.ID)
	bMid := memberID(t, pool, conv.ID, b.ID)

	path := "/api/conversations/" + conv.ID.String() + "/members/" + itoa(int64(bMid)) + "/block"
	resp := testutil.Do(t, env.Server, http.MethodPost, path, nil, tokA, "")
	if resp.Status != http.StatusNoContent {
		t.Fatalf("屏蔽成员状态 = %d, 期望 204, body=%s", resp.Status, resp.Body)
	}
	if n := count(t, pool, "SELECT count(*) FROM blocks WHERE blocker_identity_id=$1 AND blocked_identity_id=$2", a.ID, b.ID); n != 1 {
		t.Error("应存在屏蔽成员记录")
	}
	// 房间会话不结束。
	if n := count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1 AND ended_at IS NULL", conv.ID); n != 1 {
		t.Error("屏蔽房间成员不应结束会话")
	}
}

// ---- 举报 ----

func TestReportCreatesSnapshot(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, tokB := mustCreateIdentity(t, pool, "bob")
	conv := mustCreateConv(t, pool, store.ConversationKindMatch, a.ID, b.ID)
	bMid := memberID(t, pool, conv.ID, b.ID)

	// B 发 3 条。
	wsB := dialWS(t, env.Server, tokB)
	readEnv(t, wsB)
	subscribeWS(t, wsB, conv.ID)
	sendViaWS(t, wsB, conv.ID, "msg1")
	sendViaWS(t, wsB, conv.ID, "msg2")
	sendViaWS(t, wsB, conv.ID, "msg3")

	// 取中间那条的 id。
	var msgIDs []int64
	rows, _ := pool.Query(ctx, "SELECT id FROM messages WHERE conversation_id=$1 ORDER BY id", conv.ID)
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		msgIDs = append(msgIDs, id)
	}
	rows.Close()
	reported := msgIDs[1]

	out := testutil.Do(t, env.Server, http.MethodPost, "/api/reports", map[string]any{
		"conversationId": conv.ID.String(),
		"memberId":       bMid,
		"messageIds":     []int64{reported},
		"reason":         "harassment",
		"note":           "很讨厌",
	}, tokA, "")
	if out.Status != http.StatusOK {
		t.Fatalf("举报状态 = %d, body=%s", out.Status, out.Body)
	}
	var resp struct {
		ID string `json:"id"`
	}
	out.JSON(t, &resp)
	if resp.ID == "" {
		t.Fatal("举报应返回 id")
	}

	var snapJSON []byte
	if err := pool.QueryRow(ctx, "SELECT snapshot FROM reports WHERE id=$1", resp.ID).Scan(&snapJSON); err != nil {
		t.Fatalf("查询快照失败: %v", err)
	}
	var snap []struct {
		ID       int64 `json:"id"`
		Reported bool  `json:"reported"`
		Body     string
	}
	if err := json.Unmarshal(snapJSON, &snap); err != nil {
		t.Fatalf("解析快照失败: %v", err)
	}
	if len(snap) != 3 {
		t.Errorf("快照应含前后上下文共 3 条, 实际 %d", len(snap))
	}
	// 升序 + 标记被举报消息。
	found := 0
	for i, s := range snap {
		if i > 0 && snap[i-1].ID >= s.ID {
			t.Error("快照应按 id 升序")
		}
		if s.ID == reported {
			if !s.Reported {
				t.Error("被举报消息应标记 reported=true")
			}
			found++
		}
	}
	if found != 1 {
		t.Errorf("被举报消息应恰好出现一次, 实际 %d", found)
	}
	// access_logs 记录 report。
	if n := count(t, pool, "SELECT count(*) FROM access_logs WHERE event='report'"); n != 1 {
		t.Errorf("应写入 report 审计日志, 实际 %d", n)
	}
}

func TestReportValidation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, _ := mustCreateIdentity(t, pool, "bob")
	conv := mustCreateConv(t, pool, store.ConversationKindMatch, a.ID, b.ID)
	bMid := memberID(t, pool, conv.ID, b.ID)

	// 非法原因。
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/reports", map[string]any{
		"conversationId": conv.ID.String(), "memberId": bMid, "messageIds": []int64{1}, "reason": "bad",
	}, tokA, "")
	if r.Status != http.StatusBadRequest {
		t.Errorf("非法原因应 400, 实际 %d", r.Status)
	}

	// 非成员举报。
	c, tokC := mustCreateIdentity(t, pool, "carol")
	_ = c
	r = testutil.Do(t, env.Server, http.MethodPost, "/api/reports", map[string]any{
		"conversationId": conv.ID.String(), "memberId": bMid, "messageIds": []int64{1}, "reason": "spam",
	}, tokC, "")
	if r.Status != http.StatusForbidden {
		t.Errorf("非成员举报应 403, 实际 %d", r.Status)
	}
}

// ---- 封禁 ----

func TestBanIdentityKicksAndBlocksREST(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")

	ws := dialWS(t, env.Server, tokA)
	readEnv(t, ws) // hello

	// 封禁身份。
	bans, err := env.Deps.Ban.Create(ctx, []moderation.BanSubject{{Type: moderation.SubjectIdentity, Value: a.ID.String()}}, 0, "test", pgtype.UUID{})
	if err != nil {
		t.Fatalf("创建封禁失败: %v", err)
	}

	// WS 收到 kicked 并被关闭。
	kicked := readEnv(t, ws)
	if kicked.Type != realtime.TypeKicked {
		t.Fatalf("应收到 kicked, 实际 %q", kicked.Type)
	}

	// REST 被拒 403 banned。
	r := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, tokA, "")
	if r.Status != http.StatusForbidden {
		t.Fatalf("被封身份 REST 应 403, 实际 %d", r.Status)
	}

	// 新 WS 握手被拒（403）。
	u := "ws" + strings.TrimPrefix(env.Server.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", "__Host-anon="+tokA)
	c, resp, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err == nil {
		_ = c.CloseNow()
		t.Fatal("被封身份不应能建立 WS 连接")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("WS 握手应 403, 实际 %v", resp)
	}

	// 撤销后立即恢复（Revoke 会失效缓存）。
	if err := env.Deps.Ban.Revoke(ctx, bans[0].ID); err != nil {
		t.Fatalf("撤销封禁失败: %v", err)
	}
	r = testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, tokA, "")
	if r.Status != http.StatusOK {
		t.Fatalf("撤销封禁后应恢复 200, 实际 %d", r.Status)
	}
}

func TestBanIPBlocksNewIdentity(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})

	// 封禁本机 IP（httptest 客户端来源 127.0.0.1）。
	if _, err := env.Deps.Ban.Create(ctx, []moderation.BanSubject{{Type: moderation.SubjectIP, Value: "127.0.0.1"}}, 0, "test", pgtype.UUID{}); err != nil {
		t.Fatalf("创建 IP 封禁失败: %v", err)
	}
	// 新身份创建（不带 cookie）应被拒。
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/identity", nil, "", "")
	if r.Status != http.StatusForbidden {
		t.Fatalf("IP 被封后创建身份应 403, 实际 %d, body=%s", r.Status, r.Body)
	}
}

func TestBanAccountBlocksLinkedIdentity(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, tokB := mustCreateIdentity(t, pool, "bob")

	// 让 A、B 绑定同一实名账号。
	var acctID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO verified_accounts (phone_hmac, phone_enc, phone_masked) VALUES (uuid_send(gen_random_uuid()), uuid_send(gen_random_uuid()), '+86 138****0000') RETURNING id").Scan(&acctID); err != nil {
		t.Fatal(err)
	}
	pool.Exec(ctx, "UPDATE identities SET account_id=$1 WHERE id IN ($2,$3)", acctID, a.ID, b.ID)

	if _, err := env.Deps.Ban.Create(ctx, []moderation.BanSubject{{Type: moderation.SubjectAccount, Value: acctID.String()}}, 0, "test", pgtype.UUID{}); err != nil {
		t.Fatalf("创建账号封禁失败: %v", err)
	}
	// 两个身份都应被封（同一手机号绑定的另一个身份也被拒绝）。
	for _, tok := range []string{tokA, tokB} {
		r := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, tok, "")
		if r.Status != http.StatusForbidden {
			t.Errorf("账号封禁后 REST 应 403, 实际 %d (token=%s)", r.Status, tok)
		}
	}
}

func TestTemporaryBanExpires(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	env := testutil.MustEnv(t, pool, testutil.Config{Clock: func() time.Time { return now }})
	a, _ := mustCreateIdentity(t, pool, "alice")

	if _, err := env.Deps.Ban.Create(ctx, []moderation.BanSubject{{Type: moderation.SubjectIdentity, Value: a.ID.String()}}, 10*time.Second, "temp", pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	if !env.Deps.Ban.IsBanned(ctx, &a, "") {
		t.Fatal("有效期内应封禁")
	}
	// 越过有效期（同时越过缓存 TTL 上限）。
	now = now.Add(40 * time.Second)
	if env.Deps.Ban.IsBanned(ctx, &a, "") {
		t.Error("临时封禁到期后应自动失效")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestContentFilterInPipeline(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	moderator := moderation.NewModerator([]moderation.Entry{
		{Word: "badword", Level: moderation.LevelBlock},
		{Word: "sneaky", Level: moderation.LevelMask},
	})
	env := testutil.MustEnv(t, pool, testutil.Config{Filter: moderator})
	a, tokA := mustCreateIdentity(t, pool, "alice")
	b, _ := mustCreateIdentity(t, pool, "bob")
	conv := mustCreateConv(t, pool, store.ConversationKindMatch, a.ID, b.ID)

	ws := dialWS(t, env.Server, tokA)
	readEnv(t, ws) // hello

	send := func(body string) realtime.Envelope {
		data, _ := json.Marshal(map[string]any{
			"t": realtime.TypeMsgSend, "id": "s",
			"d": map[string]any{"conversationId": conv.ID.String(), "clientMsgId": newUUIDStr(), "kind": "text", "body": body},
		})
		wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		_ = ws.Write(wctx, websocket.MessageText, data)
		return readEnv(t, ws)
	}

	// 拦截级：返回 content_blocked，不落库。
	if e := send("这里 badword 出现"); e.Type != realtime.TypeError {
		t.Fatalf("应返回 error, 实际 %q", e.Type)
	} else {
		var ep realtime.ErrorPayload
		_ = json.Unmarshal(e.Data, &ep)
		if ep.Code != realtime.CodeContentBlocked {
			t.Errorf("错误码 = %q, 期望 content_blocked", ep.Code)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM messages WHERE conversation_id=$1", conv.ID); n != 0 {
		t.Errorf("被拦截消息不应落库, 实际 %d", n)
	}

	// 打码级：照常发送，但正文被替换为 *。
	if e := send("this is sneaky text"); e.Type != realtime.TypeAck {
		t.Fatalf("打码消息应回 ack, 实际 %q", e.Type)
	}
	var body string
	if err := pool.QueryRow(ctx, "SELECT body FROM messages WHERE conversation_id=$1", conv.ID).Scan(&body); err != nil {
		t.Fatalf("查询消息失败: %v", err)
	}
	if strings.Contains(body, "sneaky") {
		t.Errorf("正文应被打码, 实际 %q", body)
	}
	if !strings.Contains(body, "******") {
		t.Errorf("正文应含打码符号, 实际 %q", body)
	}
}
