//go:build integration

package bottle_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
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

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	return n
}

func postBottle(t *testing.T, srv *httptest.Server, token, content string) string {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/bottles", map[string]string{"content": content}, token, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("投放状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var out struct {
		ID string `json:"id"`
	}
	resp.JSON(t, &out)
	return out.ID
}

func pickBottle(t *testing.T, srv *httptest.Server, token string) (int, string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/bottles/pick", nil, token, "")
	if resp.Status != http.StatusOK {
		return resp.Status, string(resp.Body)
	}
	var out struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	resp.JSON(t, &out)
	return resp.Status, out.ID
}

func TestPostTooLongAndDailyLimit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		BottlePostLimiter: moderation.NewFixedWindow(10, 24*time.Hour),
	})
	_, tok := mkIdentity(t, pool, "alice")

	// 超过 500 字。
	long := strings.Repeat("字", 501)
	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles", map[string]string{"content": long}, tok, "")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("超长应 400, 实际 %d", resp.Status)
	}

	// 每日上限 10 次。
	for i := 0; i < 10; i++ {
		if r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles", map[string]string{"content": "hi"}, tok, ""); r.Status != http.StatusOK {
			t.Fatalf("第 %d 次投放应成功, 实际 %d", i+1, r.Status)
		}
	}
	if r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles", map[string]string{"content": "hi"}, tok, ""); r.Status != http.StatusTooManyRequests {
		t.Fatalf("第 11 次应 429, 实际 %d", r.Status)
	}
}

func TestPickNotOwnAndEmptySea(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")

	// A 只有自己的瓶子 → empty_sea。
	postBottle(t, env.Server, tokA, "my own")
	status, body := pickBottle(t, env.Server, tokA)
	if status != http.StatusNotFound {
		t.Fatalf("只有自己的瓶子应 404 empty_sea, 实际 %d %s", status, body)
	}

	// B 投放后 A 可捞到 B 的。
	bid := postBottle(t, env.Server, tokB, "hello from bob")
	status, pickedID := pickBottle(t, env.Server, tokA)
	if status != http.StatusOK {
		t.Fatalf("捞取应成功, 实际 %d", status)
	}
	if pickedID != bid {
		t.Errorf("捞到 = %s, 期望 %s", pickedID, bid)
	}
}

func TestHeldTimeoutOthersCanPick(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")

	bid := postBottle(t, env.Server, tokB, "x")
	// A 捞起。
	if status, _ := pickBottle(t, env.Server, tokA); status != http.StatusOK {
		t.Fatal("A 应捞到")
	}
	// 模拟持有超时。
	if _, err := pool.Exec(ctx, "UPDATE bottles SET held_until = now() - interval '1 minute' WHERE id=$1", bid); err != nil {
		t.Fatal(err)
	}
	// C 可捞到（B 是投放者不能捞自己，用 C）。
	_, tokC := mkIdentity(t, pool, "carol")
	status, picked := pickBottle(t, env.Server, tokC)
	if status != http.StatusOK || picked != bid {
		t.Fatalf("持有超时后他人应可捞到, status=%d id=%s", status, picked)
	}
}

func TestRePickReleasesPrevious(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")

	b1 := postBottle(t, env.Server, tokB, "one")
	b2 := postBottle(t, env.Server, tokB, "two")
	// A 捞第一个。
	if _, id := pickBottle(t, env.Server, tokA); id == "" {
		t.Fatal("首次捞取失败")
	}
	// A 再捞 → 前一个应放回海里。
	status, id2 := pickBottle(t, env.Server, tokA)
	if status != http.StatusOK {
		t.Fatalf("再次捞取失败: %d", status)
	}
	_ = id2
	// b1、b2 中至少一个处于 floating（被放回的那个）。
	var floating int
	pool.QueryRow(ctx, "SELECT count(*) FROM bottles WHERE id IN ($1,$2) AND status='floating' AND held_by IS NULL", b1, b2).Scan(&floating)
	if floating < 1 {
		t.Errorf("再次捞取应放回前一个瓶子, floating=%d", floating)
	}
}

func TestThrowBackAndReply(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	bIdent, tokB := mkIdentity(t, pool, "bob")
	_, tokA := mkIdentity(t, pool, "alice")

	bid := postBottle(t, env.Server, tokB, "reply me")
	if status, _ := pickBottle(t, env.Server, tokA); status != http.StatusOK {
		t.Fatal("A 应捞到")
	}

	// B 在线，准备接收 bottle.reply。
	wsB := dialWS(t, env.Server, tokB)
	_ = readUntil(t, wsB, realtime.TypeHello, 3*time.Second)

	// A 回复。
	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles/"+bid+"/reply", map[string]string{"content": "hello back"}, tokA, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("回复状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var out struct {
		ConversationID string `json:"conversationId"`
	}
	resp.JSON(t, &out)
	if out.ConversationID == "" {
		t.Fatal("应返回 conversationId")
	}
	// 会话类型为 bottle、两名成员。
	if n := count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1 AND kind='bottle'", out.ConversationID); n != 1 {
		t.Error("应创建 kind=bottle 的会话")
	}

	// 投放者收到 bottle.reply。
	env2 := readUntil(t, wsB, realtime.TypeBottleReply, 3*time.Second)
	var br struct {
		BottleID       string `json:"bottleId"`
		ConversationID string `json:"conversationId"`
	}
	_ = json.Unmarshal(env2.Data, &br)
	if br.BottleID != bid || br.ConversationID != out.ConversationID {
		t.Errorf("bottle.reply 载荷不符: %+v", br)
	}

	// /mine 未读数正确（B 是投放者，A 回复了一条）。
	mine := testutil.Do(t, env.Server, http.MethodGet, "/api/bottles/mine", nil, tokB, "")
	var bots []struct {
		Replies []struct {
			ConversationID string `json:"conversationId"`
			Unread         int64  `json:"unread"`
		} `json:"replies"`
	}
	mine.JSON(t, &bots)
	foundUnread := false
	_ = bIdent
	for _, b := range bots {
		for _, rr := range b.Replies {
			if rr.ConversationID == out.ConversationID && rr.Unread >= 1 {
				foundUnread = true
			}
		}
	}
	if !foundUnread {
		t.Error("/mine 回复未读数应 >= 1")
	}

	// 同一 Pick 不能再回复。
	// A 已不是当前持有者（回复后放回海里），再次回复应失败。
	if r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles/"+bid+"/reply", map[string]string{"content": "again"}, tokA, ""); r.Status == http.StatusOK {
		t.Error("重复回复应失败")
	}
}

func TestReplyOncePerPicker(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokB := mkIdentity(t, pool, "bob")
	_, tokA := mkIdentity(t, pool, "alice")

	bid := postBottle(t, env.Server, tokB, "x")
	if s, _ := pickBottle(t, env.Server, tokA); s != http.StatusOK {
		t.Fatal("捞取失败")
	}
	// 回复一次成功。
	if r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles/"+bid+"/reply", map[string]string{"content": "r1"}, tokA, ""); r.Status != http.StatusOK {
		t.Fatalf("首次回复应成功, 实际 %d %s", r.Status, r.Body)
	}
	// 再次回复：A 已不是持有者 → 失败。
	if r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles/"+bid+"/reply", map[string]string{"content": "r2"}, tokA, ""); r.Status == http.StatusOK {
		t.Error("每人每个瓶子只能回复一次")
	}
}

func TestConcurrentPickNoDuplicate(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokPoster := mkIdentity(t, pool, "poster")
	for i := 0; i < 5; i++ {
		postBottle(t, env.Server, tokPoster, fmt.Sprintf("b%d", i))
	}

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	ids := map[string]int{}
	wg.Add(n)
	for i := 0; i < n; i++ {
		_, tok := mkIdentity(t, pool, fmt.Sprintf("p%d", i))
		go func(tok string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, env.Server.URL+"/api/bottles/pick", nil)
			req.Header.Set("Origin", testutil.DefaultOrigin)
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: tok})
			resp, err := env.Server.Client().Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
			data, _ := io.ReadAll(resp.Body)
			var out struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(data, &out)
			mu.Lock()
			ids[out.ID]++
			mu.Unlock()
		}(tok)
	}
	wg.Wait()

	if len(ids) != 5 {
		t.Fatalf("不同瓶子数 = %d, 期望 5", len(ids))
	}
	for id, c := range ids {
		if c != 1 {
			t.Errorf("瓶子 %s 被重复持有 %d 次", id, c)
		}
	}
}

// ---- WS 辅助 ----

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
	t.Fatalf("未收到 %q", want)
	return realtime.Envelope{}
}
