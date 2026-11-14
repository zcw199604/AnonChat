//go:build integration

package conversation_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return pgtype.UUID{Bytes: b, Valid: true}
}

// createIdentity 直接向库中插入身份，返回身份与明文令牌。
func createIdentity(t *testing.T, pool *pgxpool.Pool, nickname string) (store.Identity, string) {
	t.Helper()
	token := "tok-" + nickname + "-" + newUUID(t).String()
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash:  identity.HashToken(token),
		Nickname:   nickname,
		AvatarSeed: "seed-" + nickname,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func newService(pool *pgxpool.Pool, broker realtime.Broker) *conversation.Service {
	return conversation.New(pool, testLogger(), broker,
		conversation.WithBlocker(conversation.NewSQLBlockChecker(pool)),
	)
}

func createConv(t *testing.T, pool *pgxpool.Pool, svc *conversation.Service, kind store.ConversationKind, burn bool, ids ...pgtype.UUID) store.Conversation {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("开启事务失败: %v", err)
	}
	defer tx.Rollback(ctx)
	conv, _, err := svc.Create(ctx, tx, kind, burn, ids...)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	return conv
}

// ---- 测试替身 ----

type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(e string) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

type fakeClient struct {
	ident *store.Identity
	rec   *recorder
	acks  []conversation.AckPayload
	errs  []string
}

func (f *fakeClient) Identity() *store.Identity { return f.ident }
func (f *fakeClient) Ack(_ string, p conversation.AckPayload) {
	f.acks = append(f.acks, p)
	f.rec.add("ack")
}
func (f *fakeClient) Error(_ string, code, _ string) {
	f.errs = append(f.errs, code)
	f.rec.add("error:" + code)
}

type broadcast struct {
	convID  pgtype.UUID
	env     realtime.Envelope
	exclude map[pgtype.UUID]struct{}
}

type fakeBroker struct {
	rec        *recorder
	broadcasts []broadcast
}

func (b *fakeBroker) Broadcast(convID pgtype.UUID, env realtime.Envelope, exclude map[pgtype.UUID]struct{}) {
	b.broadcasts = append(b.broadcasts, broadcast{convID, env, exclude})
	if b.rec != nil {
		b.rec.add("broadcast")
	}
}
func (b *fakeBroker) SendToIdentity(pgtype.UUID, realtime.Envelope) {}

func sendEnv(t *testing.T, convID, clientMsgID pgtype.UUID, body string) realtime.Envelope {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"conversationId": convID.String(),
		"clientMsgId":    clientMsgID.String(),
		"kind":           "text",
		"body":           body,
	})
	if err != nil {
		t.Fatal(err)
	}
	return realtime.Envelope{Type: realtime.TypeMsgSend, ID: "req-1", Data: data}
}

// ---- 发送管道 ----

func TestSendInsertsAcksThenBroadcasts(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	rec := &recorder{}
	broker := &fakeBroker{rec: rec}
	svc := newService(pool, broker)

	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	cl := &fakeClient{ident: &a, rec: rec}
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), "你好"))

	if len(cl.acks) != 1 {
		t.Fatalf("ack 数量 = %d, 期望 1", len(cl.acks))
	}
	if cl.acks[0].MessageID == 0 {
		t.Error("ack.messageId 不应为 0")
	}
	if len(broker.broadcasts) != 1 {
		t.Fatalf("广播数量 = %d, 期望 1", len(broker.broadcasts))
	}
	// ack 必须在广播之前。
	if ev := rec.snapshot(); len(ev) < 2 || ev[0] != "ack" || ev[1] != "broadcast" {
		t.Fatalf("事件顺序 = %v, 期望 ack 先于 broadcast", ev)
	}

	// 落库。
	var cnt int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE conversation_id=$1", conv.ID).Scan(&cnt); err != nil {
		t.Fatal(err)
	}
	if cnt != 1 {
		t.Fatalf("消息数 = %d, 期望 1", cnt)
	}

	// 广播载荷：msg.new 且不含 identity_id。
	b0 := broker.broadcasts[0]
	if b0.env.Type != realtime.TypeMsgNew {
		t.Errorf("广播类型 = %q, 期望 msg.new", b0.env.Type)
	}
	if strings.Contains(string(b0.env.Data), a.ID.String()) {
		t.Error("对外载荷不应包含发送者 identity_id")
	}
	var payload struct {
		ConversationID string `json:"conversationId"`
		Message        struct {
			ID         int64  `json:"id"`
			MemberID   int32  `json:"memberId"`
			Nickname   string `json:"nickname"`
			AvatarSeed string `json:"avatarSeed"`
		} `json:"message"`
	}
	if err := json.Unmarshal(b0.env.Data, &payload); err != nil {
		t.Fatalf("广播载荷解析失败: %v", err)
	}
	// match 会话属于 1v1：现在同样透传发送者的真实昵称与头像种子。
	if payload.Message.Nickname != a.Nickname || payload.Message.AvatarSeed != a.AvatarSeed || payload.Message.MemberID == 0 {
		t.Errorf("广播载荷字段不符（1v1 应透传真实昵称与头像）: %+v", payload)
	}
}

func TestSendIdempotent(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	broker := &fakeBroker{}
	svc := newService(pool, broker)

	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	cl := &fakeClient{ident: &a, rec: &recorder{}}
	clientMsgID := newUUID(t)

	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, clientMsgID, "hi"))
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, clientMsgID, "hi"))

	if len(cl.acks) != 2 {
		t.Fatalf("ack 数量 = %d, 期望 2", len(cl.acks))
	}
	if cl.acks[0].MessageID != cl.acks[1].MessageID {
		t.Errorf("重复 clientMsgId 应返回同一 messageId: %d vs %d", cl.acks[0].MessageID, cl.acks[1].MessageID)
	}
	if len(broker.broadcasts) != 1 {
		t.Errorf("重复消息不应重复广播, 广播数 = %d", len(broker.broadcasts))
	}
	var cnt int
	pool.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&cnt)
	if cnt != 1 {
		t.Errorf("消息数 = %d, 期望 1", cnt)
	}
}

func TestSendNotMember(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	c, _ := createIdentity(t, pool, "carol")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	cl := &fakeClient{ident: &c, rec: &recorder{}}
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), "hi"))
	if len(cl.errs) != 1 || cl.errs[0] != "not_member" {
		t.Fatalf("错误 = %v, 期望 not_member", cl.errs)
	}
	var cnt int
	pool.QueryRow(ctx, "SELECT count(*) FROM messages").Scan(&cnt)
	if cnt != 0 {
		t.Errorf("非成员不应落库")
	}
}

func TestSendConversationEnded(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	if err := svc.End(ctx, conv.ID, "peer_left"); err != nil {
		t.Fatal(err)
	}
	cl := &fakeClient{ident: &a, rec: &recorder{}}
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), "hi"))
	if len(cl.errs) != 1 || cl.errs[0] != "conversation_ended" {
		t.Fatalf("错误 = %v, 期望 conversation_ended", cl.errs)
	}
}

func TestSendTooLong(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	cl := &fakeClient{ident: &a, rec: &recorder{}}
	body := strings.Repeat("字", 1001)
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), body))
	if len(cl.errs) != 1 || cl.errs[0] != "invalid_request" {
		t.Fatalf("错误 = %v, 期望 invalid_request", cl.errs)
	}
}

// ---- 屏蔽 ----

func TestBlockedSenderExcludedFromBroadcastAndHistory(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	broker := &fakeBroker{}
	svc := newService(pool, broker)
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	// A 屏蔽 B。
	if _, err := pool.Exec(ctx, "INSERT INTO blocks (blocker_identity_id, blocked_identity_id) VALUES ($1,$2)", a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	// B 发送 → 广播排除 A。
	cl := &fakeClient{ident: &b, rec: &recorder{}}
	svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), "from bob"))
	if len(broker.broadcasts) != 1 {
		t.Fatalf("广播数 = %d", len(broker.broadcasts))
	}
	if _, blocked := broker.broadcasts[0].exclude[a.ID]; !blocked {
		t.Error("广播应排除屏蔽了发送者(A)的身份")
	}

	// A 读历史：看不到 B 的消息。
	msgs := listMessages(t, svc, conv.ID, &a)
	if len(msgs) != 0 {
		t.Errorf("A 不应看到被屏蔽者(B)的消息, 实际 %d 条", len(msgs))
	}
	// B 读历史：能看到自己的消息。
	msgsB := listMessages(t, svc, conv.ID, &b)
	if len(msgsB) != 1 {
		t.Errorf("B 应看到自己的消息, 实际 %d 条", len(msgsB))
	}
}

// ---- 历史分页 ----

type msgItem struct {
	ID        int64  `json:"id"`
	MemberID  int32  `json:"memberId"`
	Nickname  string `json:"nickname"`
	Kind      string `json:"kind"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
}

// listMessages 以某身份调用历史 handler。
func listMessages(t *testing.T, svc *conversation.Service, convID pgtype.UUID, ident *store.Identity) []msgItem {
	return listMessagesQuery(t, svc, convID, ident, "")
}

func listMessagesQuery(t *testing.T, svc *conversation.Service, convID pgtype.UUID, ident *store.Identity, rawQuery string) []msgItem {
	t.Helper()
	url := "/api/conversations/" + convID.String() + "/messages"
	if rawQuery != "" {
		url += "?" + rawQuery
	}
	req := httptest.NewRequest("GET", url, nil)
	req.SetPathValue("id", convID.String())
	req = req.WithContext(identity.WithIdentity(req.Context(), ident))
	rec := httptest.NewRecorder()
	svc.ListMessagesHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("历史接口状态 = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out []msgItem
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析历史失败: %v, body=%s", err, rec.Body.String())
	}
	return out
}

func TestHistoryPagination(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	cl := &fakeClient{ident: &a, rec: &recorder{}}
	for i := 0; i < 5; i++ {
		svc.HandleSend(ctx, cl, sendEnv(t, conv.ID, newUUID(t), "m"+string(rune('0'+i))))
	}

	all := listMessages(t, svc, conv.ID, &a)
	if len(all) != 5 {
		t.Fatalf("全部消息 = %d, 期望 5", len(all))
	}
	// 升序。
	for i := 1; i < len(all); i++ {
		if all[i-1].ID >= all[i].ID {
			t.Error("历史应按 id 升序")
		}
	}
	// before：取前两条（id < 第 3 条）。
	beforeRes := listMessagesQuery(t, svc, conv.ID, &a, "before="+itoa(all[2].ID))
	if len(beforeRes) != 2 || beforeRes[1].ID != all[1].ID {
		t.Errorf("before 分页结果不符: %d 条", len(beforeRes))
	}
	// after：取后两条。
	afterRes := listMessagesQuery(t, svc, conv.ID, &a, "after="+itoa(all[2].ID))
	if len(afterRes) != 2 || afterRes[0].ID != all[3].ID {
		t.Errorf("after 分页结果不符: %d 条", len(afterRes))
	}
	// limit。
	limRes := listMessagesQuery(t, svc, conv.ID, &a, "limit=2")
	if len(limRes) != 2 {
		t.Errorf("limit=2 结果 = %d, 期望 2", len(limRes))
	}
}

// ---- 阅后即焚 ----

func TestBurnAfterRead(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	broker := &fakeBroker{}
	svc := conversation.New(pool, testLogger(), broker,
		conversation.WithBlocker(conversation.NewSQLBlockChecker(pool)),
		conversation.WithBurnTiming(10*time.Second, time.Second),
	)
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindPrivate, true, a.ID, b.ID)

	// A 发送一条。
	clA := &fakeClient{ident: &a, rec: &recorder{}}
	svc.HandleSend(ctx, clA, sendEnv(t, conv.ID, newUUID(t), "secret"))
	if len(clA.acks) != 1 {
		t.Fatal("发送应有 ack")
	}

	// B 已读。
	readData, _ := json.Marshal(map[string]any{"conversationId": conv.ID.String(), "messageId": clA.acks[0].MessageID})
	clB := &fakeClient{ident: &b, rec: &recorder{}}
	svc.HandleRead(ctx, clB, realtime.Envelope{Type: realtime.TypeMsgRead, ID: "r", Data: readData})
	var readAtSet int
	pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE read_at IS NOT NULL").Scan(&readAtSet)
	if readAtSet != 1 {
		t.Fatalf("msg.read 后应有 1 条 read_at 非空, 实际 %d", readAtSet)
	}

	// 模拟已过 10 秒。
	if _, err := pool.Exec(ctx, "UPDATE messages SET read_at = now() - interval '20 seconds' WHERE read_at IS NOT NULL"); err != nil {
		t.Fatal(err)
	}
	if err := svc.BurnOnce(ctx); err != nil {
		t.Fatalf("BurnOnce 失败: %v", err)
	}
	// 广播 msg.burned。
	var burnedEnv *realtime.Envelope
	for i := range broker.broadcasts {
		if broker.broadcasts[i].env.Type == realtime.TypeMsgBurned {
			burnedEnv = &broker.broadcasts[i].env
		}
	}
	if burnedEnv == nil {
		t.Fatal("应推送 msg.burned")
	}
	// 历史不再返回已焚毁消息。
	if msgs := listMessages(t, svc, conv.ID, &b); len(msgs) != 0 {
		t.Errorf("已焚毁消息不应出现在历史, 实际 %d 条", len(msgs))
	}
}

// ---- 会话列表 ----

func TestListConversationsUnread(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	clB := &fakeClient{ident: &b, rec: &recorder{}}
	svc.HandleSend(ctx, clB, sendEnv(t, conv.ID, newUUID(t), "hi"))
	svc.HandleSend(ctx, clB, sendEnv(t, conv.ID, newUUID(t), "again"))

	req := httptest.NewRequest("GET", "/api/conversations", nil)
	req = req.WithContext(identity.WithIdentity(req.Context(), &a))
	rec := httptest.NewRecorder()
	svc.ListConversationsHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("状态 = %d body=%s", rec.Code, rec.Body.String())
	}
	var out []struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		UnreadCount int64  `json:"unreadCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(out) != 1 || out[0].UnreadCount != 2 {
		t.Fatalf("会话列表不符: %+v", out)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestHistoryLimitNotReducedByBlocked 验证屏蔽者在结果中间穿插时，limit=N 仍返回 N 条（P2 修复）。
func TestHistoryLimitNotReducedByBlocked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	c, _ := createIdentity(t, pool, "carol")
	conv := createConv(t, pool, svc, store.ConversationKindRoom, false, a.ID, b.ID, c.ID)

	clB := &fakeClient{ident: &b, rec: &recorder{}}
	clC := &fakeClient{ident: &c, rec: &recorder{}}
	// B 与 C 交错发送各 5 条。
	for i := 0; i < 5; i++ {
		svc.HandleSend(ctx, clB, sendEnv(t, conv.ID, newUUID(t), "b"))
		svc.HandleSend(ctx, clC, sendEnv(t, conv.ID, newUUID(t), "c"))
	}

	// A 屏蔽 B。
	if _, err := pool.Exec(ctx, "INSERT INTO blocks (blocker_identity_id, blocked_identity_id) VALUES ($1,$2)", a.ID, b.ID); err != nil {
		t.Fatal(err)
	}

	// limit=5 应返回 5 条，全部来自 C（不含被屏蔽的 B）。
	got := listMessagesQuery(t, svc, conv.ID, &a, "limit=5")
	if len(got) != 5 {
		t.Fatalf("limit=5 返回 %d 条, 期望 5（屏蔽不应减少页大小）", len(got))
	}
	for _, m := range got {
		if m.Nickname == "bob" {
			t.Error("不应返回被屏蔽者(bob)的消息")
		}
	}
}

// TestConversationDetail 验证 GET /api/conversations/{id} 返回 myMemberId 与 peer（H1）。
func TestConversationDetail(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})
	a, tokA := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	_, tokC := createIdentity(t, pool, "carol")
	convSvc := newService(pool, nil)
	conv := createConv(t, pool, convSvc, store.ConversationKindMatch, false, a.ID, b.ID)

	r := testutil.Do(t, srv, "GET", "/api/conversations/"+conv.ID.String(), nil, tokA, "")
	if r.Status != 200 {
		t.Fatalf("成员应 200, 实际 %d %s", r.Status, r.Body)
	}
	var detail struct {
		ID            string `json:"id"`
		Kind          string `json:"kind"`
		MyMemberID    int32  `json:"myMemberId"`
		BurnAfterRead bool   `json:"burnAfterRead"`
		Peer          *struct {
			MemberID   int32  `json:"memberId"`
			Nickname   string `json:"nickname"`
			AvatarSeed string `json:"avatarSeed"`
		} `json:"peer"`
	}
	r.JSON(t, &detail)
	if detail.MyMemberID == 0 {
		t.Error("应返回 myMemberId")
	}
	if detail.Peer == nil || detail.Peer.Nickname != "bob" {
		t.Errorf("应返回对方 peer: %+v", detail.Peer)
	}
	if detail.Kind != "match" {
		t.Errorf("kind = %q, 期望 match", detail.Kind)
	}

	// 非成员 403 not_member
	if rr := testutil.Do(t, srv, "GET", "/api/conversations/"+conv.ID.String(), nil, tokC, ""); rr.Status != 403 {
		t.Fatalf("非成员应 403, 实际 %d", rr.Status)
	}
}
