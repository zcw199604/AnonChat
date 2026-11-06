//go:build integration

package bot_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/bot"
	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// expectNoFound 在给定时间内断言没有收到 match.found。
func expectNoFound(t *testing.T, c *websocket.Conn, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return
		}
		rctx, cancel := context.WithTimeout(ctx, remaining)
		_, data, err := c.Read(rctx)
		cancel()
		if err != nil {
			return // 超时：没有收到 match.found
		}
		var env realtime.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			t.Fatalf("解析信封失败: %v", err)
		}
		if env.Type == realtime.TypeMatchFound {
			t.Fatalf("超限的 bot 不应被选中: %s", env.Data)
		}
	}
}

// TestMatchFallbackEndToEnd 覆盖 §13 第 1 条：
// 排队 → bot 兜底 → match.found（peer 字段集合与真人配对一致）→ 用户发言 →
// 假 LLM 返回文本 + 图片 → 收到 msg.new（图片带 mediaUrl）→ 用户可取到媒体、他人 404。
func TestMatchFallbackEndToEnd(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()

	seeded := seedBot(t, pool, root, seedBotOptions{
		enabled: true, serveMatch: true, serveBottle: true,
		withMedia: true, mediaTags: []string{"自拍"},
	})
	llm := newFakeLLM(promptReply)
	env := newEnv(t, pool, llm, root)

	// 真人配对：用于取得 peer 字段集合的基准。
	_, tokA := mkIdentity(t, pool, "alice")
	_, tokB := mkIdentity(t, pool, "bob")
	wsA := dialWS(t, env.Server, tokA)
	_ = readEnvelope(t, wsA, 3*time.Second) // hello
	wsB := dialWS(t, env.Server, tokB)
	_ = readEnvelope(t, wsB, 3*time.Second)

	sendWS(t, wsA, map[string]any{"t": realtime.TypeMatchStart, "id": "a", "d": map[string]any{}})
	sendWS(t, wsB, map[string]any{"t": realtime.TypeMatchStart, "id": "b", "d": map[string]any{}})
	foundA := parseFound(t, readUntil(t, wsA, realtime.TypeMatchFound, 5*time.Second))
	foundB := parseFound(t, readUntil(t, wsB, realtime.TypeMatchFound, 5*time.Second))
	realPeer := foundA.Peer
	if len(realPeer) == 0 {
		realPeer = foundB.Peer
	}
	wantKeys := peerKeys(realPeer)
	if len(wantKeys) == 0 {
		t.Fatal("真人配对的 peer 字段为空")
	}
	// 退出真人会话，清空队列。
	sendWS(t, wsA, map[string]any{"t": realtime.TypeMatchLeave, "id": "leave-a", "d": map[string]any{}})
	sendWS(t, wsB, map[string]any{"t": realtime.TypeMatchLeave, "id": "leave-b", "d": map[string]any{}})
	waitFor(t, "真实配对会话结束", 3*time.Second, func() bool {
		return countOpenMatchConversations(t, pool) == 0
	})

	// 用户排队 → 200ms 后由 bot 兜底。
	user, userToken := mkIdentity(t, pool, "carol")
	wsUser := dialWS(t, env.Server, userToken)
	_ = readEnvelope(t, wsUser, 3*time.Second)
	botFound := parseFound(t, startMatch(t, wsUser))
	if botFound.ConversationID == "" {
		t.Fatal("bot 兜底的 match.found 缺少 conversationId")
	}
	// peer 字段集合必须与真人配对完全一致（不得有任何可区分的字段）。
	if got := peerKeys(botFound.Peer); !equalStrings(got, wantKeys) {
		t.Fatalf("bot 配对 peer 字段集合 = %v, 真人配对 = %v", got, wantKeys)
	}
	// peer 里的 memberId 必须有效（与真人一致）。
	if id, ok := botFound.Peer["memberId"].(float64); !ok || id == 0 {
		t.Fatalf("peer.memberId 缺失或为 0: %v", botFound.Peer)
	}

	// 用户发一条消息 → bot 回复（文本 + 图片）。
	sendMessage(t, wsUser, botFound.ConversationID, "在吗")

	var textSeen, imageSeen bool
	var imageURL string
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) && !(textSeen && imageSeen) {
		envRead := readEnvelope(t, wsUser, time.Until(deadline)+time.Second)
		if envRead.Type != realtime.TypeMsgNew {
			continue
		}
		var p msgNewPayload
		if err := json.Unmarshal(envRead.Data, &p); err != nil {
			t.Fatalf("解析 msg.new 失败: %v", err)
		}
		switch p.Message.Kind {
		case "text":
			textSeen = true
		case "image":
			imageSeen = true
			imageURL = p.Message.MediaURL
		}
		// bot 的消息与真人消息走同一条管道：载荷里不能出现身份字段。
		for _, forbidden := range []string{"identity_id", "identityId", "token"} {
			if strings.Contains(string(envRead.Data), `"`+forbidden+`"`) {
				t.Fatalf("msg.new 出现禁止字段 %q: %s", forbidden, envRead.Data)
			}
		}
	}
	if !textSeen || !imageSeen {
		t.Fatalf("应依次收到文本与图片消息: text=%v image=%v", textSeen, imageSeen)
	}
	if !strings.HasPrefix(imageURL, "/api/media/") {
		t.Fatalf("图片消息的 mediaUrl 不符: %q", imageURL)
	}

	// 用户可访问媒体；其他身份 404。
	okResp := doFile(t, env.Server, imageURL, userToken)
	if okResp.StatusCode != http.StatusOK {
		t.Fatalf("会话成员访问媒体状态 = %d", okResp.StatusCode)
	}
	_ = okResp.Body.Close()
	_, otherToken := mkIdentity(t, pool, "dave")
	notMember := doFile(t, env.Server, imageURL, otherToken)
	if notMember.StatusCode != http.StatusNotFound {
		t.Fatalf("非成员访问媒体状态 = %d, 期望 404", notMember.StatusCode)
	}
	_ = notMember.Body.Close()

	// 默认是 user 注入方式（兼容会丢弃 system 的网关）：不发送任何 system 消息，
	// 角色设定块（到"【设定结束】"为止）在所有调用（开场 + 回复）里逐字节相同，保证缓存前缀稳定。
	calls := llm.snapshot()
	if len(calls) < 2 {
		t.Fatalf("假 LLM 应至少被调用 2 次（开场 + 回复），实际 %d 次", len(calls))
	}
	setupOf := func(m bot.ChatMessage) string {
		i := strings.Index(m.Content, "【设定结束】")
		if m.Role != "user" || i < 0 {
			t.Fatalf("第 1 条消息应为包含角色设定块的 user 消息: %+v", m)
		}
		return m.Content[:i]
	}
	firstSetup := setupOf(calls[0].Messages[0])
	if !strings.Contains(firstSetup, "角色名：") {
		t.Fatalf("角色设定块缺少角色名:\n%s", firstSetup)
	}
	for i, call := range calls {
		for _, m := range call.Messages {
			if m.Role == "system" {
				t.Fatalf("第 %d 次调用不应包含 system 消息: %+v", i+1, m)
			}
		}
		if got := setupOf(call.Messages[0]); got != firstSetup {
			t.Fatalf("第 %d 次调用的角色设定块与首次不一致:\n%s\n%s", i+1, firstSetup, got)
		}
	}
	// 用量落库：bot_usage 有记录且累加了 used_tokens。
	waitFor(t, "写入 bot 用量", 3*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM bot_usage WHERE bot_id=$1", seeded.BotID).Scan(&n)
		return n >= 2
	})
	var usedTokens int64
	if err := pool.QueryRow(ctx, "SELECT used_tokens FROM bots WHERE id=$1", seeded.BotID).Scan(&usedTokens); err != nil {
		t.Fatal(err)
	}
	if usedTokens <= 0 {
		t.Errorf("used_tokens 应累加, 实际 %d", usedTokens)
	}
	_ = user
}

// TestConversationReplyLimitEndsMatch 覆盖 §13 第 2 条：对话回复上限触发后会话以 peer_left 结束。
func TestConversationReplyLimitEndsMatch(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	// 开场按 §7.3 记 1 条回复，所以把上限设为 2：开场 1 条 + 一次回复 1 条。
	seedBot(t, pool, root, seedBotOptions{enabled: true, serveMatch: true, convReplyLimit: 2})
	llm := newFakeLLM(promptReply)
	env := newEnv(t, pool, llm, root)

	_, token := mkIdentity(t, pool, "erin")
	ws := dialWS(t, env.Server, token)
	_ = readEnvelope(t, ws, 3*time.Second)
	found := parseFound(t, startMatch(t, ws))

	// 等开场完成（开场记为第 1 条回复），不改动任何用量数据。
	waitFor(t, "开场回复完成", 5*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, "SELECT count(*) FROM bot_usage WHERE conversation_id=$1", mustUUID(t, found.ConversationID)).Scan(&n)
		return n > 0
	})

	// 第一条用户消息：正常回复（现在是第 2 条回复，达到 conv_reply_limit）。
	sendMessage(t, ws, found.ConversationID, "你好")
	readUntil(t, ws, realtime.TypeMsgNew, 8*time.Second)
	waitFor(t, "第一次回复的用量写入", 5*time.Second, func() bool {
		var replies int64
		_ = pool.QueryRow(ctx,
			"SELECT COALESCE(sum(replies),0) FROM bot_usage WHERE conversation_id=$1",
			mustUUID(t, found.ConversationID)).Scan(&replies)
		return replies >= 2
	})

	// 第二条用户消息：已达到 conv_reply_limit，会话以 peer_left 结束。
	sendMessage(t, ws, found.ConversationID, "再聊会儿")
	ended := readUntil(t, ws, realtime.TypeMatchEnded, 8*time.Second)
	var payload struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ended.Data, &payload)
	if payload.Reason != "peer_left" {
		t.Fatalf("结束原因 = %q, 期望 peer_left", payload.Reason)
	}
	var reason *string
	if err := pool.QueryRow(ctx, "SELECT end_reason FROM conversations WHERE id=$1", mustUUID(t, found.ConversationID)).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil || *reason != "peer_left" {
		t.Fatalf("库里 end_reason = %v, 期望 peer_left", reason)
	}
}

// TestTotalReplyLimitStopsFallback 覆盖 §13 第 3 条：总上限用尽后不再被选中。
func TestTotalReplyLimitStopsFallback(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	seedBot(t, pool, root, seedBotOptions{
		enabled: true, serveMatch: true,
		totalReplyLimit: 1, usedReplies: 1,
	})
	llm := newFakeLLM(promptReply)
	env := newEnv(t, pool, llm, root)

	_, token := mkIdentity(t, pool, "frank")
	ws := dialWS(t, env.Server, token)
	_ = readEnvelope(t, ws, 3*time.Second)
	sendWS(t, ws, map[string]any{"t": realtime.TypeMatchStart, "id": "m1", "d": map[string]any{}})
	waiting := readUntil(t, ws, realtime.TypeMatchWaiting, 5*time.Second)
	if waiting.Type != realtime.TypeMatchWaiting {
		t.Fatalf("应先收到 match.waiting")
	}
	// 兜底时间已过（200ms）且扫描很快，但 bot 已超限：不应配对。
	expectNoFound(t, ws, 900*time.Millisecond)
	if calls := llm.callCount(); calls != 0 {
		t.Errorf("超限的 bot 不应调用模型, 实际 %d 次", calls)
	}
}

// TestBottleBotReply 覆盖 §13 第 4 条：bot 回复漂流瓶，投瓶人收到 bottle.reply，
// 并能在"我的瓶子"里看到，且之后在会话里再发消息时 bot 会回复。
func TestBottleBotReply(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	seedBot(t, pool, root, seedBotOptions{enabled: true, serveBottle: true, serveMatch: false})
	llm := newFakeLLM(promptReply)
	env := newEnv(t, pool, llm, root)

	_, token := mkIdentity(t, pool, "gina")
	ws := dialWS(t, env.Server, token)
	_ = readEnvelope(t, ws, 3*time.Second)

	r := testutil.Do(t, env.Server, http.MethodPost, "/api/bottles", map[string]string{"content": "今天有点累"}, token, "")
	if r.Status != http.StatusOK {
		t.Fatalf("投放漂流瓶失败: %d %s", r.Status, r.Body)
	}
	var posted struct {
		ID string `json:"id"`
	}
	r.JSON(t, &posted)

	// 等 bot 回复（bottle.reply）。
	reply := readUntil(t, ws, realtime.TypeBottleReply, 8*time.Second)
	var replyPayload struct {
		BottleID       string `json:"bottleId"`
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(reply.Data, &replyPayload); err != nil {
		t.Fatalf("解析 bottle.reply 失败: %v", err)
	}
	if replyPayload.ConversationID == "" {
		t.Fatalf("bottle.reply 缺少 conversationId: %s", reply.Data)
	}

	// 我的瓶子里能看到这条回复。
	mine := testutil.Do(t, env.Server, http.MethodGet, "/api/bottles/mine", nil, token, "")
	if mine.Status != http.StatusOK {
		t.Fatalf("我的瓶子失败: %d", mine.Status)
	}
	var minePayload []struct {
		ID      string `json:"id"`
		Replies []struct {
			ConversationID string `json:"conversationId"`
		} `json:"replies"`
	}
	mine.JSON(t, &minePayload)
	found := false
	for _, b := range minePayload {
		if b.ID != posted.ID {
			continue
		}
		found = true
		if len(b.Replies) == 0 || b.Replies[0].ConversationID != replyPayload.ConversationID {
			t.Fatalf("我的瓶子里的回复不符: %+v", b.Replies)
		}
	}
	if !found {
		t.Fatalf("我的瓶子里找不到刚投的瓶子: %s", mine.Body)
	}

	// 投瓶人在会话里再发一条消息，bot 会回复。
	sendMessage(t, ws, replyPayload.ConversationID, "谢谢你")
	readUntil(t, ws, realtime.TypeMsgNew, 8*time.Second)

	// 用量记录带上了新的会话 ID。
	waitFor(t, "漂流瓶回复的用量记录", 3*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx,
			"SELECT count(*) FROM bot_usage WHERE conversation_id=$1", mustUUID(t, replyPayload.ConversationID)).Scan(&n)
		return n >= 1
	})
}

// ---- 未启用/停用 ----

// TestDisabledBotStopsMatchingButKeepsBottleConversation 验证 bot 停用时的处理（B3 复查补充）：
// 匹配会话以 peer_left 结束，漂流瓶会话保持沉默。
func TestDisabledBotStopsMatchingButKeepsBottleConversation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	seeded := seedBot(t, pool, root, seedBotOptions{enabled: false, serveMatch: true, serveBottle: true})
	llm := newFakeLLM(promptReply)
	env := newEnv(t, pool, llm, root)
	if env.Deps.BotEngine == nil {
		t.Fatal("应启用 bot 引擎")
	}

	user, _ := mkIdentity(t, pool, "henry")

	// 匹配会话：停用的 bot 应立即以 peer_left 结束。
	matchConv := insertConversation(t, pool, "match", user.ID, seeded.IdentityID)
	env.Deps.BotEngine.OnMessage(botMessageEvent(matchConv, user.ID, store.ConversationKindMatch))
	waitFor(t, "匹配会话结束", 3*time.Second, func() bool {
		var reason *string
		_ = pool.QueryRow(ctx, "SELECT end_reason FROM conversations WHERE id=$1", matchConv).Scan(&reason)
		return reason != nil && *reason == "peer_left"
	})

	// 漂流瓶会话：保持沉默，不会结束也不会发消息。
	bottleConv := insertConversation(t, pool, "bottle", user.ID, seeded.IdentityID)
	env.Deps.BotEngine.OnMessage(botMessageEvent(bottleConv, user.ID, store.ConversationKindBottle))
	// 轮询等待"沉默"窗口：期间只要出现消息或会话结束就失败。
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		var ended pgtype.Timestamptz
		if err := pool.QueryRow(ctx, "SELECT ended_at FROM conversations WHERE id=$1", bottleConv).Scan(&ended); err != nil {
			t.Fatal(err)
		}
		if ended.Valid {
			t.Fatal("漂流瓶会话不应被结束")
		}
		var msgCount int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM messages WHERE conversation_id=$1 AND sender_identity_id=$2",
			bottleConv, seeded.IdentityID).Scan(&msgCount); err != nil {
			t.Fatal(err)
		}
		if msgCount != 0 {
			t.Fatalf("停用的 bot 不应发送消息, 实际 %d 条", msgCount)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls := llm.callCount(); calls != 0 {
		t.Errorf("停用的 bot 不应调用模型, 实际 %d 次", calls)
	}
}

// ---- 后台接口 ----

// TestAdminBotAndMediaLifecycle 覆盖 §13 第 5 条：角色卡与 bot 的增删改查、
// 媒体文件夹的上传/改标签/删除，以及未登录访问返回 401。
func TestAdminBotAndMediaLifecycle(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	env := newEnv(t, pool, newFakeLLM(promptReply), root)

	// 未登录访问后台 → 401。
	for _, path := range []string{"/api/admin/character-cards", "/api/admin/bots"} {
		if status, _ := doJSON(t, env.Server, http.MethodGet, path, nil, ""); status != http.StatusUnauthorized {
			t.Fatalf("%s 未登录应 401, 实际 %d", path, status)
		}
	}
	cookie := loginAdmin(t, pool, env.Server)

	// 角色卡 CRUD。
	status, body := doJSON(t, env.Server, http.MethodPost, "/api/admin/character-cards", map[string]string{
		"name": "小雅", "personality": "温柔", "firstMessage": "在吗",
	}, cookie)
	if status != http.StatusCreated {
		t.Fatalf("新建角色卡失败: %d %s", status, body)
	}
	var card struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	mustJSON(t, body, &card)
	if card.ID == "" {
		t.Fatal("角色卡 ID 为空")
	}
	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/character-cards/"+card.ID, map[string]string{
		"name": "小雅2", "personality": "活泼",
	}, cookie)
	if status != http.StatusOK {
		t.Fatalf("更新角色卡失败: %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, http.MethodGet, "/api/admin/character-cards", nil, cookie)
	if status != http.StatusOK || !strings.Contains(string(body), "小雅2") {
		t.Fatalf("角色卡列表不符: %d %s", status, body)
	}

	// 媒体文件夹：创建 → 上传 png（合法）→ 上传 txt（被拒）→ 改标签 → 删除。
	status, body = doJSON(t, env.Server, http.MethodPost, "/api/admin/media-folders", map[string]string{"name": "自拍"}, cookie)
	if status != http.StatusCreated {
		t.Fatalf("新建文件夹失败: %d %s", status, body)
	}
	var folder struct {
		ID string `json:"id"`
	}
	mustJSON(t, body, &folder)

	status, body = uploadFile(t, env.Server, folder.ID, cookie, "a.png", pngBytes, "自拍,微笑", "")
	if status != http.StatusCreated {
		t.Fatalf("上传 png 失败: %d %s", status, body)
	}
	var items []struct {
		ID  string `json:"id"`
		URL string `json:"fileUrl"`
	}
	mustJSON(t, body, &items)
	if len(items) != 1 {
		t.Fatalf("上传返回 %d 个条目", len(items))
	}
	itemPath := filepath.Join(root, folder.ID, items[0].ID+".png")
	if _, err := os.Stat(itemPath); err != nil {
		t.Fatalf("上传后磁盘文件不存在: %v", err)
	}
	status, body = uploadFile(t, env.Server, folder.ID, cookie, "evil.png", []byte("不是图片"), "", "")
	if status != http.StatusUnsupportedMediaType {
		t.Fatalf("上传 txt 应 415, 实际 %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, http.MethodPatch, "/api/admin/media-items/"+items[0].ID,
		map[string]any{"tags": []string{"夜景"}, "caption": "夜色"}, cookie)
	if status != http.StatusOK || !strings.Contains(string(body), "夜景") {
		t.Fatalf("修改标签失败: %d %s", status, body)
	}
	if status, _ := doJSON(t, env.Server, http.MethodDelete, "/api/admin/media-folders/"+folder.ID, nil, cookie); status != http.StatusNoContent {
		t.Fatalf("删除文件夹失败: %d", status)
	}
	if _, err := os.Stat(itemPath); !os.IsNotExist(err) {
		t.Errorf("删除文件夹后磁盘文件应消失, err=%v", err)
	}

	// Bot CRUD。
	status, body = doJSON(t, env.Server, http.MethodPost, "/api/admin/bots", map[string]any{
		"label": "客服 bot", "nickname": "小助手", "cardId": card.ID, "enabled": true,
	}, cookie)
	if status != http.StatusCreated {
		t.Fatalf("新建 bot 失败: %d %s", status, body)
	}
	var created struct {
		ID      string `json:"id"`
		CardID  string `json:"cardId"`
		Profile struct {
			Nickname string `json:"nickname"`
		} `json:"profile"`
	}
	mustJSON(t, body, &created)
	if created.ID == "" || created.CardID != card.ID || created.Profile.Nickname != "小助手" {
		t.Fatalf("新建 bot 响应不符: %s", body)
	}

	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/bots/"+created.ID, map[string]any{
		"label": "客服 bot", "cardId": card.ID, "enabled": false,
		"model": "test-model", "temperature": 0.7, "maxOutputTokens": 200,
		"serveMatch": true, "serveBottle": false, "maxActiveMatches": 2,
		"totalTokenLimit": 0, "totalReplyLimit": 5, "windowSeconds": 600,
		"windowTokenLimit": 0, "windowReplyLimit": 0,
		"convReplyLimit": 3, "convMinIntervalSeconds": 0, "convMediaLimit": 1,
		"replyDelayMinMs": 0, "replyDelayMaxMs": 0, "typingMsPerChar": 0, "bottleIntervalSeconds": 60,
		"profile": map[string]any{"nickname": "小助手", "gender": "female", "age": 24, "tags": []string{"音乐"}, "cleanMode": false},
	}, cookie)
	if status != http.StatusOK {
		t.Fatalf("更新 bot 失败: %d %s", status, body)
	}
	var updated struct {
		Enabled    bool  `json:"enabled"`
		ReplyLimit int64 `json:"totalReplyLimit"`
		Profile    struct {
			Gender string   `json:"gender"`
			Tags   []string `json:"tags"`
		} `json:"profile"`
	}
	mustJSON(t, body, &updated)
	if updated.Enabled || updated.ReplyLimit != 5 || updated.Profile.Gender != "female" {
		t.Fatalf("更新 bot 响应不符: %s", body)
	}

	status, body = doJSON(t, env.Server, http.MethodGet, "/api/admin/bots", nil, cookie)
	if status != http.StatusOK || !strings.Contains(string(body), created.ID) {
		t.Fatalf("bot 列表不符: %d %s", status, body)
	}
	if status, _ := doJSON(t, env.Server, http.MethodPost, "/api/admin/bots/"+created.ID+"/reset-usage", nil, cookie); status != http.StatusOK {
		t.Fatalf("重置用量失败: %d", status)
	}
	if status, _ := doJSON(t, env.Server, http.MethodDelete, "/api/admin/bots/"+created.ID, nil, cookie); status != http.StatusNoContent {
		t.Fatalf("删除 bot 失败: %d", status)
	}
	// 删除 bot 时吊销了身份。
	waitFor(t, "bot 身份被吊销", 3*time.Second, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM identities WHERE revoked_at IS NOT NULL`).Scan(&n)
		return n >= 1
	})
	// 角色卡删除。
	if status, _ := doJSON(t, env.Server, http.MethodDelete, "/api/admin/character-cards/"+card.ID, nil, cookie); status != http.StatusNoContent {
		t.Fatalf("删除角色卡失败: %d", status)
	}
}

// ---- 小工具 ----

// botMessageEvent 构造一条用户消息的观察事件。
func botMessageEvent(convID, senderID pgtype.UUID, kind store.ConversationKind) conversation.MessageEvent {
	return conversation.MessageEvent{
		ConversationID:   convID,
		ConversationKind: kind,
		MessageID:        1,
		SenderIdentityID: senderID,
		SenderMemberID:   1,
		Kind:             "text",
	}
}

// insertConversation 直接插入一个两人会话（用户 + bot）。
func insertConversation(t *testing.T, pool *pgxpool.Pool, kind string, userID, botID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var convID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO conversations (kind) VALUES ($1) RETURNING id", kind).Scan(&convID); err != nil {
		t.Fatalf("插入会话失败: %v", err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO conversation_members (conversation_id, member_id, identity_id) VALUES ($1,1,$2), ($1,2,$3)",
		convID, userID, botID); err != nil {
		t.Fatalf("插入成员失败: %v", err)
	}
	return convID
}

// countOpenMatchConversations 返回未结束的匹配会话数。
func countOpenMatchConversations(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM conversations WHERE kind='match' AND ended_at IS NULL").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// mustUUID 解析 UUID 字符串。
func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		t.Fatalf("解析 UUID 失败: %v", err)
	}
	return id
}

// mustJSON 解析 JSON。
func mustJSON(t *testing.T, body []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("解析 JSON 失败: %v, body=%s", err, body)
	}
}

// equalStrings 判断两个字符串切片是否相同。
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// doFile 以身份 Cookie 请求媒体文件。
func doFile(t *testing.T, srv *httptest.Server, path, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", testutil.DefaultOrigin)
	req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: token})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求媒体失败: %v", err)
	}
	return resp
}

// uploadFile 以 multipart 上传一个文件。
func uploadFile(t *testing.T, srv *httptest.Server, folderID, cookie, name string, content []byte, tags, caption string) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	w, err := mw.CreateFormFile("files", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	if tags != "" {
		_ = mw.WriteField("tags", tags)
	}
	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/media-folders/"+folderID+"/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", testutil.DefaultOrigin)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: cookie})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}
