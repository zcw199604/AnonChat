//go:build integration

package privatechat_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
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

type createdLink struct {
	Token          string `json:"token"`
	ConversationID string `json:"conversationId"`
	ExpiresAt      string `json:"expiresAt"`
}

func createLink(t *testing.T, srv *httptest.Server, token string, password string, ttl int, burn bool) (int, createdLink) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/private-links", map[string]any{
		"password": password, "ttlHours": ttl, "burnAfterRead": burn,
	}, token, "")
	var out createdLink
	if resp.Status == http.StatusOK {
		resp.JSON(t, &out)
	}
	return resp.Status, out
}

func joinLink(t *testing.T, srv *httptest.Server, token, linkToken, password string) (int, string) {
	t.Helper()
	resp := testutil.Do(t, srv, http.MethodPost, "/api/private-links/"+linkToken+"/join", map[string]string{"password": password}, token, "")
	if resp.Status != http.StatusOK {
		return resp.Status, string(resp.Body)
	}
	var out struct {
		ConversationID string `json:"conversationId"`
	}
	resp.JSON(t, &out)
	return resp.Status, out.ConversationID
}

func TestCreateTTLRange(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tok := mkIdentity(t, pool, "alice")

	for _, ttl := range []int{200, -1} {
		if status, _ := createLink(t, env.Server, tok, "", ttl, false); status != http.StatusBadRequest {
			t.Errorf("ttl=%d 应 400, 实际 %d", ttl, status)
		}
	}
	if status, _ := createLink(t, env.Server, tok, "", 0, false); status != http.StatusOK {
		t.Errorf("ttl=0 应使用默认值成功, 实际 %d", status)
	}
}

func TestTokenStoredAsHashOnly(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tok := mkIdentity(t, pool, "alice")

	status, link := createLink(t, env.Server, tok, "", 24, false)
	if status != http.StatusOK {
		t.Fatal("创建失败")
	}
	var stored []byte
	if err := pool.QueryRow(ctx, "SELECT token_hash FROM private_links").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(link.Token))
	if string(stored) != string(sum[:]) {
		t.Error("库中应存 sha256(token)")
	}
	if string(stored) == link.Token {
		t.Error("库中不应存明文令牌")
	}
}

func TestJoinSecondThirdAndCreatorIdempotent(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokCreator := mkIdentity(t, pool, "creator")
	_, tokB := mkIdentity(t, pool, "bob")
	_, tokC := mkIdentity(t, pool, "carol")

	_, link := createLink(t, env.Server, tokCreator, "", 24, false)

	// 创建者本人加入是幂等的。
	if status, convID := joinLink(t, env.Server, tokCreator, link.Token, ""); status != http.StatusOK || convID != link.ConversationID {
		t.Fatalf("创建者幂等加入失败: %d %s", status, convID)
	}
	// 第二人加入成功。
	if status, convID := joinLink(t, env.Server, tokB, link.Token, ""); status != http.StatusOK || convID != link.ConversationID {
		t.Fatalf("第二人加入失败: %d", status)
	}
	// 第三人 link_full。
	if status, _ := joinLink(t, env.Server, tokC, link.Token, ""); status != http.StatusConflict {
		t.Fatalf("第三人应 409 link_full, 实际 %d", status)
	}
}

func TestPasswordAndRateLimit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		PasswordLimiter: moderation.NewFixedWindow(5, 10*time.Minute),
	})
	_, tokCreator := mkIdentity(t, pool, "creator")
	_, tokB := mkIdentity(t, pool, "bob")

	_, link := createLink(t, env.Server, tokCreator, "secret", 24, false)
	// 正确密码加入成功（用另一个链接避免锁吃满）。
	_, link2 := createLink(t, env.Server, tokCreator, "secret", 24, false)
	if status, _ := joinLink(t, env.Server, tokB, link2.Token, "secret"); status != http.StatusOK {
		t.Fatalf("正确密码应加入成功, 实际 %d", status)
	}

	// 错误密码：前 5 次 bad_password(403)，第 6 次 rate_limited(429)。
	_, tokC := mkIdentity(t, pool, "carol")
	for i := 0; i < 5; i++ {
		if status, _ := joinLink(t, env.Server, tokC, link.Token, "wrong"); status != http.StatusForbidden {
			t.Fatalf("第 %d 次错误密码应 403 bad_password, 实际 %d", i+1, status)
		}
	}
	if status, _ := joinLink(t, env.Server, tokC, link.Token, "wrong"); status != http.StatusTooManyRequests {
		t.Fatalf("连续错误应 429 rate_limited, 实际 %d", status)
	}
}

func TestBlockedJoinReturnsLinkFull(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	creator, tokCreator := mkIdentity(t, pool, "creator")
	third, tokThird := mkIdentity(t, pool, "third")

	_, link := createLink(t, env.Server, tokCreator, "", 24, false)
	if _, err := pool.Exec(ctx, "INSERT INTO blocks (blocker_identity_id, blocked_identity_id) VALUES ($1,$2)", creator.ID, third.ID); err != nil {
		t.Fatal(err)
	}
	if status, _ := joinLink(t, env.Server, tokThird, link.Token, ""); status != http.StatusConflict {
		t.Fatalf("存在屏蔽应 409 link_full, 实际 %d", status)
	}
}

func TestExpiredLinkAndScan(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokCreator := mkIdentity(t, pool, "creator")
	_, tokB := mkIdentity(t, pool, "bob")

	_, link := createLink(t, env.Server, tokCreator, "", 24, false)
	// 置为过期。
	if _, err := pool.Exec(ctx, "UPDATE private_links SET expires_at = now() - interval '1 minute' WHERE conversation_id = $1", link.ConversationID); err != nil {
		t.Fatal(err)
	}
	if status, _ := joinLink(t, env.Server, tokB, link.Token, ""); status != http.StatusConflict {
		t.Fatalf("过期链接加入应 409 link_expired, 实际 %d", status)
	}
	// 过期扫描结束会话。
	if err := env.Deps.Private.ScanExpired(ctx); err != nil {
		t.Fatalf("扫描失败: %v", err)
	}
	var ended pgtype.Timestamptz
	var reason *string
	if err := pool.QueryRow(ctx, "SELECT ended_at, end_reason FROM conversations WHERE id=$1", link.ConversationID).Scan(&ended, &reason); err != nil {
		t.Fatal(err)
	}
	if !ended.Valid || reason == nil || *reason != "expired" {
		t.Errorf("过期扫描应以 expired 结束会话, ended=%v reason=%v", ended.Valid, reason)
	}
}

func TestDestroyNotifiesHistoryEnded(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokCreator := mkIdentity(t, pool, "creator")
	_, tokB := mkIdentity(t, pool, "bob")

	_, link := createLink(t, env.Server, tokCreator, "", 24, false)
	if status, _ := joinLink(t, env.Server, tokB, link.Token, ""); status != http.StatusOK {
		t.Fatal("第二人加入失败")
	}

	// 双方 WS 订阅会话，准备接收 conv.ended。
	wsC := dialWS(t, env.Server, tokCreator)
	_ = readUntil(t, wsC, realtime.TypeHello, 3*time.Second)
	subscribe(t, wsC, link.ConversationID)
	syncWS(t, wsC)
	wsB := dialWS(t, env.Server, tokB)
	_ = readUntil(t, wsB, realtime.TypeHello, 3*time.Second)
	subscribe(t, wsB, link.ConversationID)
	syncWS(t, wsB)

	// 销毁。
	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/private-links/"+link.Token+"/destroy", nil, tokCreator, "")
	if resp.Status != http.StatusNoContent {
		t.Fatalf("销毁状态 = %d", resp.Status)
	}
	for i, ws := range []*websocket.Conn{wsC, wsB} {
		e := readUntil(t, ws, realtime.TypeConvEnded, 3*time.Second)
		var p struct {
			Reason string `json:"reason"`
		}
		_ = json.Unmarshal(e.Data, &p)
		if p.Reason != "destroyed" {
			t.Errorf("连接 %d conv.ended reason = %q, 期望 destroyed", i, p.Reason)
		}
	}

	// 历史接口返回 conversation_ended（409）。
	h := testutil.Do(t, env.Server, http.MethodGet, "/api/conversations/"+link.ConversationID+"/messages", nil, tokB, "")
	if h.Status != http.StatusConflict {
		t.Fatalf("销毁后历史应 409 conversation_ended, 实际 %d", h.Status)
	}
}

func TestConcurrentJoinOnlyOne(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tokCreator := mkIdentity(t, pool, "creator")
	_, link := createLink(t, env.Server, tokCreator, "", 24, false)

	const n = 5
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		_, tok := mkIdentity(t, pool, fmt.Sprintf("j%d", i))
		go func(tok string) {
			defer wg.Done()
			req, _ := http.NewRequest(http.MethodPost, env.Server.URL+"/api/private-links/"+link.Token+"/join", strings.NewReader(`{}`))
			req.Header.Set("Origin", testutil.DefaultOrigin)
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: tok})
			resp, err := env.Server.Client().Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(tok)
	}
	wg.Wait()

	if success != 1 {
		t.Fatalf("并发加入成功数 = %d, 期望 1", success)
	}
	var members int
	pool.QueryRow(ctx, "SELECT count(*) FROM conversation_members WHERE conversation_id=$1 AND left_at IS NULL", link.ConversationID).Scan(&members)
	if members != 2 {
		t.Fatalf("会话成员数 = %d, 期望 2", members)
	}
}

func TestBurnAfterReadFlag(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, tok := mkIdentity(t, pool, "alice")
	status, link := createLink(t, env.Server, tok, "", 24, true)
	if status != http.StatusOK {
		t.Fatal("创建失败")
	}
	var burn bool
	if err := pool.QueryRow(ctx, "SELECT burn_after_read FROM conversations WHERE id=$1", link.ConversationID).Scan(&burn); err != nil {
		t.Fatal(err)
	}
	if !burn {
		t.Error("burnAfterRead=true 应对应会话 burn_after_read=true")
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

func subscribe(t *testing.T, c *websocket.Conn, convID string) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"t": realtime.TypeConvSubscribe, "id": "sub", "d": map[string]any{"conversationIds": []string{convID}}})
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_ = c.Write(wctx, websocket.MessageText, data)
}

func syncWS(t *testing.T, c *websocket.Conn) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{"t": realtime.TypePing, "id": "p"})
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		t.Fatalf("ping 写入失败: %v", err)
	}
	_ = readUntil(t, c, realtime.TypePong, 3*time.Second)
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
