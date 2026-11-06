//go:build integration

package room_test

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

func connectHello(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	c := dial(t, srv, token)
	if e := readUntil(t, c, realtime.TypeHello, 3*time.Second); e.Type != realtime.TypeHello {
		t.Fatalf("首帧应为 hello")
	}
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

// readUntil 读取消息直到类型为 want，期间忽略 presence/waiting 等无关帧。
func readUntil(t *testing.T, c *websocket.Conn, want realtime.MessageType, timeout time.Duration) realtime.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, time.Until(deadline))
		_, data, err := c.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		var env realtime.Envelope
		_ = json.Unmarshal(data, &env)
		if env.Type == want {
			return env
		}
	}
	t.Fatalf("未在超时内收到 %q", want)
	return realtime.Envelope{}
}

type roomMeta struct {
	roomID pgtype.UUID
	convID pgtype.UUID
	max    int
}

func getRoom(t *testing.T, pool *pgxpool.Pool, slug string) roomMeta {
	t.Helper()
	var m roomMeta
	var maxM int32
	if err := pool.QueryRow(ctx, "SELECT id, conversation_id, max_members FROM rooms WHERE slug=$1", slug).Scan(&m.roomID, &m.convID, &maxM); err != nil {
		t.Fatalf("查询房间失败: %v", err)
	}
	m.max = int(maxM)
	return m
}

type joinAck struct {
	RoomID         string `json:"roomId"`
	ConversationID string `json:"conversationId"`
	MemberID       int32  `json:"memberId"`
	Online         int    `json:"online"`
}

func join(t *testing.T, c *websocket.Conn, roomID pgtype.UUID) joinAck {
	t.Helper()
	send(t, c, map[string]any{"t": realtime.TypeRoomJoin, "id": "join", "d": map[string]any{"roomId": roomID.String()}})
	env := readUntil(t, c, realtime.TypeAck, 3*time.Second)
	var ack joinAck
	_ = json.Unmarshal(env.Data, &ack)
	return ack
}

func strID(u pgtype.UUID) string { return u.String() }

// ---- 测试 ----

func TestListRoomsReturnsSeed(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tok := mkIdentity(t, pool, "alice")

	resp := testutil.Do(t, env.Server, http.MethodGet, "/api/rooms", nil, tok, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var rooms []struct {
		Slug   string `json:"slug"`
		Online int    `json:"online"`
	}
	resp.JSON(t, &rooms)
	if len(rooms) != 6 {
		t.Fatalf("房间数 = %d, 期望 6", len(rooms))
	}
	if rooms[0].Slug != "lobby" {
		t.Errorf("首个房间 = %q, 期望 lobby", rooms[0].Slug)
	}
}

func TestJoinPresenceAndChat(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	room := getRoom(t, pool, "lobby")

	wsA := connectHello(t, env.Server, tokA)
	ackA := join(t, wsA, room.roomID)
	if ackA.ConversationID != strID(room.convID) || ackA.MemberID == 0 {
		t.Fatalf("加入 ack 不符: %+v", ackA)
	}
	wsB := connectHello(t, env.Server, tokB)
	ackB := join(t, wsB, room.roomID)
	if ackB.Online < 1 {
		t.Errorf("B 加入后 online = %d, 期望 >=1", ackB.Online)
	}

	// A 收到在线人数推送。
	_ = readUntil(t, wsA, realtime.TypeRoomPresence, 3*time.Second)

	// B 在房间发言，A 收到 msg.new。
	send(t, wsB, map[string]any{"t": realtime.TypeMsgSend, "id": "s", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "33333333-3333-3333-3333-333333333333", "kind": "text", "body": "大家好",
	}})
	if e := readUntil(t, wsB, realtime.TypeAck, 3*time.Second); e.Type != realtime.TypeAck {
		t.Fatal("B 应收到 ack")
	}
	if e := readUntil(t, wsA, realtime.TypeMsgNew, 3*time.Second); e.Type != realtime.TypeMsgNew {
		t.Fatal("A 应收到 msg.new")
	}
}

func TestRoomFull(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	room := getRoom(t, pool, "gaming")
	if _, err := pool.Exec(ctx, "UPDATE rooms SET max_members=1 WHERE id=$1", room.roomID); err != nil {
		t.Fatal(err)
	}

	wsA := connectHello(t, env.Server, tokA)
	join(t, wsA, room.roomID)
	wsB := connectHello(t, env.Server, tokB)
	send(t, wsB, map[string]any{"t": realtime.TypeRoomJoin, "id": "j", "d": map[string]any{"roomId": room.roomID.String()}})
	e := readUntil(t, wsB, realtime.TypeError, 3*time.Second)
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(e.Data, &ep)
	if ep.Code != realtime.CodeRoomFull {
		t.Fatalf("错误码 = %q, 期望 room_full", ep.Code)
	}
}

func TestLeaveDecreasesOnlineAndCannotSend(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	room := getRoom(t, pool, "lobby")

	wsA := connectHello(t, env.Server, tokA)
	join(t, wsA, room.roomID)
	wsB := connectHello(t, env.Server, tokB)
	join(t, wsB, room.roomID)
	_ = readUntil(t, wsB, realtime.TypeRoomPresence, 3*time.Second)

	// A 退出。
	send(t, wsA, map[string]any{"t": realtime.TypeRoomLeave, "id": "l", "d": map[string]any{"roomId": room.roomID.String()}})
	_ = readUntil(t, wsA, realtime.TypeAck, 3*time.Second)

	// A 退出后不能发言（left_at 已置）。
	send(t, wsA, map[string]any{"t": realtime.TypeMsgSend, "id": "s", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "44444444-4444-4444-4444-444444444444", "kind": "text", "body": "hi",
	}})
	e := readUntil(t, wsA, realtime.TypeError, 3*time.Second)
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(e.Data, &ep)
	if ep.Code != realtime.CodeNotMember {
		t.Fatalf("离开后发言应 not_member, 实际 %q", ep.Code)
	}
}

func TestDisconnectDecreasesOnline(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	room := getRoom(t, pool, "lobby")

	wsA := connectHello(t, env.Server, tokA)
	join(t, wsA, room.roomID)
	wsB := connectHello(t, env.Server, tokB)
	join(t, wsB, room.roomID)
	_ = readUntil(t, wsB, realtime.TypeRoomPresence, 3*time.Second)

	_ = wsA.CloseNow() // A 断线

	// B 收到在线人数降到 1 的推送。
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e := readUntil(t, wsB, realtime.TypeRoomPresence, 3*time.Second)
		var p struct {
			Online int `json:"online"`
		}
		_ = json.Unmarshal(e.Data, &p)
		if p.Online == 1 {
			return
		}
	}
	t.Fatal("未收到在线人数降为 1 的推送")
}

func TestTwoConnectionsOneClosesStillInRoom(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	room := getRoom(t, pool, "lobby")

	ws1 := connectHello(t, env.Server, tokA)
	join(t, ws1, room.roomID)
	ws2 := connectHello(t, env.Server, tokA)
	join(t, ws2, room.roomID)

	_ = ws1.CloseNow() // 关闭一个连接，仍在房间
	time.Sleep(150 * time.Millisecond)

	// 另一个连接仍可发言。
	send(t, ws2, map[string]any{"t": realtime.TypeMsgSend, "id": "s", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "55555555-5555-5555-5555-555555555555", "kind": "text", "body": "still",
	}})
	if e := readUntil(t, ws2, realtime.TypeAck, 3*time.Second); e.Type != realtime.TypeAck {
		t.Fatal("关闭一个连接后另一个仍应能发言")
	}

	// 关闭另一个连接后，成员离开，重连也不能发言。
	_ = ws2.CloseNow()
	time.Sleep(150 * time.Millisecond)
	ws3 := connectHello(t, env.Server, tokA)
	send(t, ws3, map[string]any{"t": realtime.TypeMsgSend, "id": "s", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "66666666-6666-6666-6666-666666666666", "kind": "text", "body": "after",
	}})
	e := readUntil(t, ws3, realtime.TypeError, 3*time.Second)
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(e.Data, &ep)
	if ep.Code != realtime.CodeNotMember {
		t.Fatalf("全部连接关闭后应 not_member, 实际 %q", ep.Code)
	}
}

func TestRoomMessageInterval(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	room := getRoom(t, pool, "lobby")

	ws := connectHello(t, env.Server, tokA)
	join(t, ws, room.roomID)

	send(t, ws, map[string]any{"t": realtime.TypeMsgSend, "id": "s1", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "77777777-7777-7777-7777-777777777777", "kind": "text", "body": "one",
	}})
	_ = readUntil(t, ws, realtime.TypeAck, 3*time.Second)
	// 立刻再发一条应被限流（房间每秒最多 1 条）。
	send(t, ws, map[string]any{"t": realtime.TypeMsgSend, "id": "s2", "d": map[string]any{
		"conversationId": strID(room.convID), "clientMsgId": "88888888-8888-8888-8888-888888888888", "kind": "text", "body": "two",
	}})
	e := readUntil(t, ws, realtime.TypeError, 3*time.Second)
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(e.Data, &ep)
	if ep.Code != realtime.CodeRateLimited {
		t.Fatalf("房间秒内第二条应 rate_limited, 实际 %q", ep.Code)
	}
}

func TestRoomHistoryRetentionWindow(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	a, tokA := mkIdentity(t, pool, "alice")
	room := getRoom(t, pool, "lobby")

	// 一条 8 天前（应被 7 天窗口排除）与一条近期。
	insertMsg(t, pool, room.convID, a.ID, 1, "old", "now() - interval '8 days'")
	insertMsg(t, pool, room.convID, a.ID, 1, "recent", "now()")

	resp := testutil.Do(t, env.Server, http.MethodGet, "/api/rooms/"+room.roomID.String()+"/messages", nil, tokA, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("状态 = %d body=%s", resp.Status, resp.Body)
	}
	var msgs []struct {
		Body string `json:"body"`
	}
	resp.JSON(t, &msgs)
	if len(msgs) != 1 || msgs[0].Body != "recent" {
		t.Fatalf("应只返回 7 天内消息, 实际 %+v", msgs)
	}
}

// insertMsg 用表达式插入 createdAt（直接写库）。
func insertMsg(t *testing.T, pool *pgxpool.Pool, convID, identID pgtype.UUID, memberID int32, body, createdExpr string) {
	t.Helper()
	q := `INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, created_at)
	      VALUES ($1,$2,$3,gen_random_uuid(),'text',$4, ` + createdExpr + `)`
	if _, err := pool.Exec(ctx, q, convID, identID, memberID, body); err != nil {
		t.Fatalf("插入消息失败: %v", err)
	}
}

func TestPresenceThrottled(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	testutil.SeedRooms(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	room := getRoom(t, pool, "lobby")

	wsA := connectHello(t, env.Server, tokA)
	join(t, wsA, room.roomID)
	// 先消费掉 A 自己加入时的在线人数推送（online=1）。
	_ = readUntil(t, wsA, realtime.TypeRoomPresence, 3*time.Second)

	// B、C、D 快速加入。
	for _, name := range []string{"bob", "carol", "dave"} {
		_, tok := mkIdentity(t, pool, name)
		ws := connectHello(t, env.Server, tok)
		join(t, ws, room.roomID)
	}

	// A 收到的 presence 帧应被合并：直到 online==4，帧数不应随加入人数线性增长。
	count := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e := readUntil(t, wsA, realtime.TypeRoomPresence, 3*time.Second)
		count++
		var p struct {
			Online int `json:"online"`
		}
		_ = json.Unmarshal(e.Data, &p)
		if p.Online == 4 {
			break
		}
	}
	if count > 2 {
		t.Fatalf("在线人数推送未合并：收到 %d 帧（期望 <= 2，每秒最多一次）", count)
	}
}
