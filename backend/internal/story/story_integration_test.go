//go:build integration

package story_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func newIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "story-tok-" + name
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "s-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", identity.CookieName+"="+token)
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	// 首帧 hello。
	if _, data, err := c.Read(ctx); err != nil || !strings.Contains(string(data), "hello") {
		t.Fatalf("首帧应为 hello: %s (%v)", data, err)
	}
	return c
}

func sendMsg(t *testing.T, c *websocket.Conn, convID, clientID, body string) {
	t.Helper()
	payload := map[string]any{
		"t": realtime.TypeMsgSend, "id": clientID,
		"d": map[string]any{"conversationId": convID, "clientMsgId": clientID, "kind": "text", "body": body},
	}
	data, _ := json.Marshal(payload)
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatalf("发送消息失败: %v", err)
	}
	// 等 ack（跳过 msg.new）。
	for i := 0; i < 4; i++ {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, raw, err := c.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("等待 ack 失败: %v", err)
		}
		if strings.Contains(string(raw), "ack") {
			return
		}
	}
	t.Fatal("未收到 ack")
}

func adminDo(t *testing.T, srv *httptest.Server, method, path string, body any, cookie string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: cookie})
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func loginAdmin(t *testing.T, srv *httptest.Server, pool *pgxpool.Pool) string {
	t.Helper()
	if _, err := admin.CreateUser(ctx, pool, "storyadmin", "supersecret123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"username": "storyadmin", "password": "supersecret123"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testutil.DefaultOrigin)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	defer resp.Body.Close()
	for _, ck := range resp.Cookies() {
		if ck.Name == admin.SessionCookie {
			return ck.Value
		}
	}
	t.Fatal("未获得管理员 Cookie")
	return ""
}

type storyResp struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Opening        string   `json:"opening"`
	Tags           []string `json:"tags"`
	RoleA          string   `json:"roleA"`
	RoleB          string   `json:"roleB"`
	Status         string   `json:"status"`
	Valid          *bool    `json:"valid"`
	HostConsent    bool     `json:"hostConsent"`
	GuestConsent   bool     `json:"guestConsent"`
	ConversationID *string  `json:"conversationId"`
	MyRole         *string  `json:"myRole"`
}

// TestStoryFullFlow 覆盖完整故事书流程（how.md 4）：
// 发起 → 认领 → 双方各发 5 条 → 结束（判定有效）→ 双方同意 → 管理员审核 → 书架与动态可见。
func TestStoryFullFlow(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{StoryMinDuration: time.Second})

	_, tokHost := newIdentity(t, pool, "host")
	_, tokGuest := newIdentity(t, pool, "guest")

	// 发起。
	create := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title": "雨夜书店", "opening": "雨下得很大，书店里只剩下一个客人。",
		"tags": []string{"读书"}, "roleA": "店主", "roleB": "旅人", "hostRole": "a",
	}, tokHost, "")
	if create.Status != http.StatusOK {
		t.Fatalf("发起故事状态 = %d, body=%s", create.Status, create.Body)
	}
	var story storyResp
	create.JSON(t, &story)
	storyID := story.ID
	if story.Status != "waiting" {
		t.Fatalf("新故事状态 = %q, 期望 waiting", story.Status)
	}

	// 列表可见，且不含发起人信息。
	list := testutil.Do(t, env.Server, http.MethodGet, "/api/stories?status=waiting", nil, tokGuest, "")
	if list.Status != http.StatusOK {
		t.Fatalf("列表状态 = %d", list.Status)
	}
	var items []map[string]any
	list.JSON(t, &items)
	if len(items) != 1 {
		t.Fatalf("等待开演列表 = %+v", items)
	}
	if _, leaked := items[0]["hostIdentityId"]; leaked {
		t.Error("列表不应包含发起人身份")
	}
	if items[0]["openRole"] != "旅人" {
		t.Errorf("开放角色 = %v, 期望 旅人", items[0]["openRole"])
	}

	// 认领。
	join := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID+"/join", nil, tokGuest, "")
	if join.Status != http.StatusOK {
		t.Fatalf("认领状态 = %d, body=%s", join.Status, join.Body)
	}
	var joined struct {
		ConversationID string `json:"conversationId"`
	}
	join.JSON(t, &joined)
	convID := joined.ConversationID

	// 双方通过 WS 各发 5 条；故事会话的 nickname 应为角色名。
	wsHost := dialWS(t, env.Server, tokHost)
	wsGuest := dialWS(t, env.Server, tokGuest)
	for i := 0; i < 5; i++ {
		sendMsg(t, wsHost, convID, "1111111"+string(rune('1'+i))+"-1111-1111-1111-111111111111", "店主发言"+string(rune('a'+i)))
		sendMsg(t, wsGuest, convID, "2222222"+string(rune('1'+i))+"-2222-2222-2222-222222222222", "旅人发言"+string(rune('a'+i)))
	}

	// 消息历史里昵称应为角色名（不是真实昵称）。
	msgs := testutil.Do(t, env.Server, http.MethodGet, "/api/conversations/"+convID+"/messages", nil, tokHost, "")
	var history []struct {
		Nickname string `json:"nickname"`
		MemberID int32  `json:"memberId"`
	}
	msgs.JSON(t, &history)
	if len(history) < 10 {
		t.Fatalf("历史消息数 = %d, 期望 ≥ 10", len(history))
	}
	roles := map[int32]string{}
	for _, m := range history {
		roles[m.MemberID] = m.Nickname
	}
	if roles[1] != "店主" || roles[2] != "旅人" {
		t.Fatalf("角色名不符: %+v", roles)
	}

	// 等待时长阈值（1 秒）后再结束。
	time.Sleep(1200 * time.Millisecond)
	end := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID+"/end", nil, tokHost, "")
	if end.Status != http.StatusOK {
		t.Fatalf("结束状态 = %d, body=%s", end.Status, end.Body)
	}
	var ended storyResp
	end.JSON(t, &ended)
	if ended.Status != "ended" || ended.Valid == nil || !*ended.Valid {
		t.Fatalf("结束结果 = %+v（期望 ended 且 valid=true）", ended)
	}

	// 双方同意。
	consentHost := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID+"/consent", map[string]bool{"agree": true}, tokHost, "")
	if consentHost.Status != http.StatusOK {
		t.Fatalf("发起人同意状态 = %d, body=%s", consentHost.Status, consentHost.Body)
	}
	var afterHost storyResp
	consentHost.JSON(t, &afterHost)
	if !afterHost.HostConsent || afterHost.GuestConsent {
		t.Fatalf("同意状态不符: %+v", afterHost)
	}
	consentGuest := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID+"/consent", map[string]bool{"agree": true}, tokGuest, "")
	if consentGuest.Status != http.StatusOK {
		t.Fatalf("参与者同意状态 = %d, body=%s", consentGuest.Status, consentGuest.Body)
	}

	// 双方都同意后生成 pending 作品。
	var workUUID pgtype.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM story_works WHERE story_id = $1`, pgUUID(t, storyID)).Scan(&workUUID); err != nil {
		t.Fatalf("作品未生成: %v", err)
	}
	workID := workUUID.String()
	if workID == "" {
		t.Fatal("作品未生成")
	}

	// 待审核作品对非参与者不可见。
	other := testutil.Do(t, env.Server, http.MethodGet, "/api/works/"+workID, nil, tokHost, "")
	if other.Status != http.StatusOK {
		t.Fatalf("参与者应可查看 pending 作品: %d %s", other.Status, other.Body)
	}
	var work struct {
		Status string `json:"status"`
		Lines  []struct {
			Role string `json:"role"`
			Text string `json:"text"`
		} `json:"lines"`
		Mine *string `json:"mine"`
	}
	other.JSON(t, &work)
	if work.Status != "pending" || work.Mine == nil || *work.Mine != "host" {
		t.Fatalf("作品状态不符: %+v", work)
	}
	if len(work.Lines) < 10 {
		t.Fatalf("作品正文条数 = %d, 期望 ≥ 10", len(work.Lines))
	}
	if work.Lines[0].Role != "店主" {
		t.Errorf("作品首行角色 = %q, 期望 店主", work.Lines[0].Role)
	}

	// 管理员审核通过后出现在书架与匿名动态里。
	cookie := loginAdmin(t, env.Server, pool)
	status, data := adminDo(t, env.Server, http.MethodPost, "/api/admin/works/"+workID+"/approve", nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("审核通过状态 = %d, body=%s", status, data)
	}

	shelf := testutil.Do(t, env.Server, http.MethodGet, "/api/works", nil, tokGuest, "")
	var shelfItems []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	shelf.JSON(t, &shelfItems)
	if len(shelfItems) != 1 || shelfItems[0].Title != "雨夜书店" {
		t.Fatalf("书架 = %+v", shelfItems)
	}

	feedResp := testutil.Do(t, env.Server, http.MethodGet, "/api/feed", nil, tokGuest, "")
	var feed []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	feedResp.JSON(t, &feed)
	found := false
	for _, item := range feed {
		if item.Type == "work" && strings.Contains(item.Text, "雨夜书店") {
			found = true
		}
	}
	if !found {
		t.Fatalf("匿名动态应包含作品: %+v", feed)
	}

	// 点赞幂等：连点两次仍然只有 1。
	l1 := testutil.Do(t, env.Server, http.MethodPost, "/api/works/"+workID+"/like", nil, tokGuest, "")
	l2 := testutil.Do(t, env.Server, http.MethodPost, "/api/works/"+workID+"/like", nil, tokGuest, "")
	if l1.Status != http.StatusOK || l2.Status != http.StatusOK {
		t.Fatalf("点赞状态 = %d, %d", l1.Status, l2.Status)
	}
	var likeCount struct {
		LikeCount int `json:"likeCount"`
	}
	l2.JSON(t, &likeCount)
	if likeCount.LikeCount != 1 {
		t.Errorf("点赞数 = %d, 期望 1（幂等）", likeCount.LikeCount)
	}
}

// TestStoryJoinConcurrentOnlyOnce 验证并发认领同一个故事只能成功一次。
func TestStoryJoinConcurrentOnlyOnce(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{StoryMinDuration: time.Second})

	_, tokHost := newIdentity(t, pool, "chost")
	create := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title": "并发测试", "opening": "这是一个用于并发认领测试的开场白内容。",
		"tags": []string{}, "roleA": "甲", "roleB": "乙", "hostRole": "a",
	}, tokHost, "")
	if create.Status != http.StatusOK {
		t.Fatalf("发起失败: %d %s", create.Status, create.Body)
	}
	var story storyResp
	create.JSON(t, &story)

	const n = 5
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		_, tokens[i] = newIdentity(t, pool, "guest-"+string(rune('a'+i)))
	}
	var success int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			r := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+story.ID+"/join", nil, tok, "")
			if r.Status == http.StatusOK {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(tokens[i])
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("并发认领成功次数 = %d, 期望 1", success)
	}
}

// TestStoryConsentRequiresValid 验证未达有效条件的故事不能申请收录。
func TestStoryConsentRequiresValid(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{StoryMinDuration: time.Minute})

	_, tokHost := newIdentity(t, pool, "vhost")
	_, tokGuest := newIdentity(t, pool, "vguest")
	create := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title": "无效场次", "opening": "这段开场足够长，可以通过校验的检查。",
		"tags": []string{}, "roleA": "甲", "roleB": "乙", "hostRole": "a",
	}, tokHost, "")
	var story storyResp
	create.JSON(t, &story)
	join := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+story.ID+"/join", nil, tokGuest, "")
	if join.Status != http.StatusOK {
		t.Fatalf("认领失败: %d %s", join.Status, join.Body)
	}
	end := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+story.ID+"/end", nil, tokHost, "")
	var ended storyResp
	end.JSON(t, &ended)
	if ended.Valid == nil || *ended.Valid {
		t.Fatalf("时长不足时 valid 应为 false: %+v", ended)
	}
	consent := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+story.ID+"/consent", map[string]bool{"agree": true}, tokHost, "")
	if consent.Status != http.StatusConflict {
		t.Fatalf("无效场次收录状态 = %d, 期望 409, body=%s", consent.Status, consent.Body)
	}
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	consent.JSON(t, &e)
	if e.Error.Code != "story_not_valid" {
		t.Errorf("错误码 = %q, 期望 story_not_valid", e.Error.Code)
	}
}

// ---- 小工具 ----

func pgUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("UUID 解析失败: %v", err)
	}
	return u
}

func workPtr(dst *string) *string { return dst }

// TestStoryEventsNoIdentityLeak 验证 story.started 事件不含任何身份标识字段。
func TestStoryEventsNoIdentityLeak(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{StoryMinDuration: time.Second})

	_, tokHost := newIdentity(t, pool, "leakhhost")
	_, tokGuest := newIdentity(t, pool, "leakhguest")
	wsHost := dialWS(t, env.Server, tokHost)

	create := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title": "事件泄漏测试", "opening": "用于验证事件负载不含身份信息的开场白。",
		"tags": []string{}, "roleA": "甲", "roleB": "乙", "hostRole": "a",
	}, tokHost, "")
	var story storyResp
	create.JSON(t, &story)

	join := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+story.ID+"/join", nil, tokGuest, "")
	if join.Status != http.StatusOK {
		t.Fatalf("认领失败: %d %s", join.Status, join.Body)
	}

	// 发起人应收到 story.started。
	got := false
	for i := 0; i < 5 && !got; i++ {
		rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		_, raw, err := wsHost.Read(rctx)
		cancel()
		if err != nil {
			break
		}
		if strings.Contains(string(raw), string(realtime.TypeStoryStarted)) {
			got = true
			for _, forbidden := range []string{"identity_id", "identityId", "token", "ip", "account", "phone"} {
				if strings.Contains(string(raw), `"`+forbidden+`"`) {
					t.Fatalf("story.started 出现禁止字段 %q: %s", forbidden, raw)
				}
			}
		}
	}
	if !got {
		t.Fatal("未收到 story.started 事件")
	}
}

// storyMaskWord / storyBlockWord 是测试用词条：mask 级被打码，block 级被拦截（审查意见 R1、R2）。
const (
	storyMaskWord  = "示例替换词"
	storyBlockWord = "禁止词"
)

// storyFilter 返回含一个打码级词与一个拦截级词的过滤器。
func storyFilter() *moderation.Moderator {
	return moderation.NewModerator([]moderation.Entry{
		{Word: storyMaskWord, Level: moderation.LevelMask},
		{Word: storyBlockWord, Level: moderation.LevelBlock},
	})
}

// TestStoryContentMasked 验证标题、开场与角色名保存的是打码后的文本（审查意见 R1、R2）。
func TestStoryContentMasked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{Filter: storyFilter()})
	_, tokHost := newIdentity(t, pool, "maskhost")

	create := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title":    storyMaskWord + "的书店",
		"opening":  "雨下得很大，" + storyMaskWord + "走进书店，这是一段足够长的开场白。",
		"tags":     []string{"读书"},
		"roleA":    storyMaskWord,
		"roleB":    "旅人",
		"hostRole": "a",
	}, tokHost, "")
	if create.Status != http.StatusOK {
		t.Fatalf("发起故事状态 = %d, body=%s", create.Status, create.Body)
	}
	var created storyResp
	create.JSON(t, &created)

	if want := "*****的书店"; created.Title != want {
		t.Errorf("标题 = %q, 期望 %q（打码后的文本）", created.Title, want)
	}
	if want := "雨下得很大，*****走进书店，这是一段足够长的开场白。"; created.Opening != want {
		t.Errorf("开场 = %q, 期望 %q", created.Opening, want)
	}
	if want := "*****"; created.RoleA != want {
		t.Errorf("角色名 A = %q, 期望 %q", created.RoleA, want)
	}

	// 落库的也必须是打码后的文本。
	var storedTitle, storedRoleA string
	if err := pool.QueryRow(ctx, `SELECT title, role_a FROM stories WHERE id = $1`, created.ID).Scan(&storedTitle, &storedRoleA); err != nil {
		t.Fatalf("查询故事失败: %v", err)
	}
	if storedTitle != "*****的书店" || storedRoleA != "*****" {
		t.Errorf("库中标题/角色名 = %q/%q, 期望打码后的文本", storedTitle, storedRoleA)
	}
}

// TestStoryRoleBlockedAndTagsValidated 验证角色名命中拦截级词被拒绝、标签必须合法且去重（审查意见 R2）。
func TestStoryRoleBlockedAndTagsValidated(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{Filter: storyFilter()})
	_, tokHost := newIdentity(t, pool, "taghost")

	base := func() map[string]any {
		return map[string]any{
			"title":    "雨夜书店",
			"opening":  "雨下得很大，书店里只剩下一个客人，故事从这里开始。",
			"tags":     []string{"读书"},
			"roleA":    "店主",
			"roleB":    "旅人",
			"hostRole": "a",
		}
	}

	// 非法标签 → invalid_request。
	badTag := base()
	badTag["tags"] = []string{"不存在的标签"}
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", badTag, tokHost, "")
	if r.Status != http.StatusBadRequest {
		t.Fatalf("非法标签状态 = %d, 期望 400, body=%s", r.Status, r.Body)
	}
	assertErrCode(t, r.Body, "invalid_request")

	// 角色名命中拦截级词 → content_blocked。
	badRole := base()
	badRole["roleB"] = storyBlockWord
	r = testutil.Do(t, env.Server, http.MethodPost, "/api/stories", badRole, tokHost, "")
	if r.Status != http.StatusUnprocessableEntity {
		t.Fatalf("角色名命中拦截词状态 = %d, 期望 422, body=%s", r.Status, r.Body)
	}
	assertErrCode(t, r.Body, "content_blocked")

	// 重复标签应去重（不视为非法）。
	dup := base()
	dup["tags"] = []string{"读书", "读书", "音乐"}
	r = testutil.Do(t, env.Server, http.MethodPost, "/api/stories", dup, tokHost, "")
	if r.Status != http.StatusOK {
		t.Fatalf("重复标签状态 = %d, 期望 200, body=%s", r.Status, r.Body)
	}
	var created storyResp
	r.JSON(t, &created)
	if len(created.Tags) != 2 || created.Tags[0] != "读书" || created.Tags[1] != "音乐" {
		t.Errorf("去重后标签 = %+v, 期望 [读书 音乐]", created.Tags)
	}
}

// TestStoryJoinRejectedWhenBlocked 验证存在任一方向拉黑时认领失败（审查意见 R4）。
func TestStoryJoinRejectedWhenBlocked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	host, tokHost := newIdentity(t, pool, "blockhost")
	guest, tokGuest := newIdentity(t, pool, "blockguest")

	// 情形一：发起人拉黑认领者。
	if _, err := pool.Exec(ctx, `INSERT INTO blocks (blocker_identity_id, blocked_identity_id) VALUES ($1,$2)`, host.ID, guest.ID); err != nil {
		t.Fatalf("写入拉黑关系失败: %v", err)
	}
	storyID := createWaitingStory(t, env, tokHost, "拉黑场景一")
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID+"/join", nil, tokGuest, "")
	if r.Status != http.StatusConflict {
		t.Fatalf("发起人拉黑后认领状态 = %d, 期望 409, body=%s", r.Status, r.Body)
	}
	assertErrCode(t, r.Body, "story_taken")

	// 情形二：认领者拉黑发起人（反方向）。
	if _, err := pool.Exec(ctx, `DELETE FROM blocks WHERE blocker_identity_id = $1 AND blocked_identity_id = $2`, host.ID, guest.ID); err != nil {
		t.Fatalf("清理拉黑关系失败: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO blocks (blocker_identity_id, blocked_identity_id) VALUES ($1,$2)`, guest.ID, host.ID); err != nil {
		t.Fatalf("写入反向拉黑关系失败: %v", err)
	}
	// 清掉第一个故事，避免触发「同时只能有 1 个进行中的故事」的配额。
	if _, err := pool.Exec(ctx, `DELETE FROM stories WHERE id = $1`, storyID); err != nil {
		t.Fatalf("清理故事失败: %v", err)
	}
	storyID2 := createWaitingStory(t, env, tokHost, "拉黑场景二")
	r = testutil.Do(t, env.Server, http.MethodPost, "/api/stories/"+storyID2+"/join", nil, tokGuest, "")
	if r.Status != http.StatusConflict {
		t.Fatalf("认领者拉黑后认领状态 = %d, 期望 409, body=%s", r.Status, r.Body)
	}
	assertErrCode(t, r.Body, "story_taken")
}

// createWaitingStory 以给定身份发起一个等待开演的故事，返回故事 ID。
func createWaitingStory(t *testing.T, env *testutil.Env, token, title string) string {
	t.Helper()
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/stories", map[string]any{
		"title": title, "opening": "雨下得很大，书店里只剩下一个客人，故事从这里开始。",
		"tags": []string{"读书"}, "roleA": "店主", "roleB": "旅人", "hostRole": "a",
	}, token, "")
	if r.Status != http.StatusOK {
		t.Fatalf("发起故事状态 = %d, body=%s", r.Status, r.Body)
	}
	var created storyResp
	r.JSON(t, &created)
	return created.ID
}

// assertErrCode 断言错误响应的错误码。
func assertErrCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("解析错误响应失败: %v, body=%s", err, body)
	}
	if e.Error.Code != want {
		t.Errorf("错误码 = %q, 期望 %q", e.Error.Code, want)
	}
}
