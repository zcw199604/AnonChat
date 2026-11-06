//go:build integration

package feed_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func newIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "feed-tok-" + name
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "s-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

func adminDo(t *testing.T, srv *httptest.Server, method, path string, body any, cookie string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
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
	if _, err := admin.CreateUser(ctx, pool, "feedadmin", "supersecret123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	status, data := adminDo(t, srv, http.MethodPost, "/api/admin/login", map[string]string{
		"username": "feedadmin", "password": "supersecret123",
	}, "")
	if status != http.StatusOK {
		t.Fatalf("管理员登录失败: %d %s", status, data)
	}
	// 重新登录一次以拿到 Cookie 值。
	body, _ := json.Marshal(map[string]string{"username": "feedadmin", "password": "supersecret123"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testutil.DefaultOrigin)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
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

// TestBroadcastCardFlow 验证广播扣卡、审核通过后可见、驳回退卡（how.md 3.5）。
func TestBroadcastCardFlow(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	id, token := newIdentity(t, pool, "bc")

	// 没有广播卡时提交 → no_broadcast_card。
	r := testutil.Do(t, env.Server, http.MethodPost, "/api/broadcasts", map[string]string{"content": "你好"}, token, "")
	if r.Status != http.StatusConflict {
		t.Fatalf("无卡提交状态 = %d, 期望 409, body=%s", r.Status, r.Body)
	}
	assertErrCode(t, r.Body, "no_broadcast_card")

	// 给 2 张广播卡。
	if _, err := pool.Exec(ctx, `UPDATE identities SET broadcast_cards = 2 WHERE id = $1`, id.ID); err != nil {
		t.Fatalf("发放广播卡失败: %v", err)
	}

	sub := testutil.Do(t, env.Server, http.MethodPost, "/api/broadcasts", map[string]string{"content": "测试广播"}, token, "")
	if sub.Status != http.StatusOK {
		t.Fatalf("提交广播状态 = %d, body=%s", sub.Status, sub.Body)
	}
	mine := testutil.Do(t, env.Server, http.MethodGet, "/api/broadcasts/mine", nil, token, "")
	var list []struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	mine.JSON(t, &list)
	if len(list) != 1 || list[0].Status != "pending" {
		t.Fatalf("我的广播 = %+v", list)
	}
	broadcastID := list[0].ID

	// 剩余卡数 = 1（事务内扣 1 张）。
	me := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, token, "")
	var meBody struct {
		BroadcastCards int32 `json:"broadcastCards"`
	}
	me.JSON(t, &meBody)
	if meBody.BroadcastCards != 1 {
		t.Fatalf("剩余广播卡 = %d, 期望 1", meBody.BroadcastCards)
	}

	// 审核前 current 为空。
	cur := testutil.Do(t, env.Server, http.MethodGet, "/api/broadcasts/current", nil, token, "")
	var curList []map[string]any
	cur.JSON(t, &curList)
	if len(curList) != 0 {
		t.Fatalf("审核前不应有展示中的广播: %+v", curList)
	}

	// 管理员通过审核。
	cookie := loginAdmin(t, env.Server, pool)
	status, data := adminDo(t, env.Server, http.MethodPost, "/api/admin/broadcasts/"+broadcastID+"/approve", nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("审核通过状态 = %d, body=%s", status, data)
	}

	cur = testutil.Do(t, env.Server, http.MethodGet, "/api/broadcasts/current", nil, token, "")
	cur.JSON(t, &curList)
	if len(curList) != 1 || curList[0]["content"] != "测试广播" {
		t.Fatalf("通过后应出现在展示中: %+v", curList)
	}

	// 匿名动态应包含这条广播。
	feedResp := testutil.Do(t, env.Server, http.MethodGet, "/api/feed", nil, token, "")
	var feed []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	feedResp.JSON(t, &feed)
	found := false
	for _, item := range feed {
		if item.Type == "broadcast" && item.Text == "测试广播" {
			found = true
		}
	}
	if !found {
		t.Fatalf("匿名动态应包含广播: %+v", feed)
	}

	// 再提交一条并驳回：广播卡应退还。
	sub2 := testutil.Do(t, env.Server, http.MethodPost, "/api/broadcasts", map[string]string{"content": "会被驳回"}, token, "")
	if sub2.Status != http.StatusOK {
		t.Fatalf("第二次提交状态 = %d, body=%s", sub2.Status, sub2.Body)
	}
	mine = testutil.Do(t, env.Server, http.MethodGet, "/api/broadcasts/mine", nil, token, "")
	mine.JSON(t, &list)
	var pendingID string
	for _, b := range list {
		if b.Status == "pending" {
			pendingID = b.ID
		}
	}
	status, data = adminDo(t, env.Server, http.MethodPost, "/api/admin/broadcasts/"+pendingID+"/reject",
		map[string]string{"reason": "不合规"}, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("驳回状态 = %d, body=%s", status, data)
	}
	me = testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, token, "")
	me.JSON(t, &meBody)
	if meBody.BroadcastCards != 1 {
		t.Fatalf("驳回后广播卡 = %d, 期望 1（退还）", meBody.BroadcastCards)
	}
}

func TestAnnouncementVisibleAndDeleted(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, token := newIdentity(t, pool, "ann")
	cookie := loginAdmin(t, env.Server, pool)

	status, data := adminDo(t, env.Server, http.MethodPost, "/api/admin/announcements",
		map[string]any{"content": "今晚维护"}, cookie)
	if status != http.StatusOK {
		t.Fatalf("创建公告状态 = %d, body=%s", status, data)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("解析公告失败: %v", err)
	}

	list := testutil.Do(t, env.Server, http.MethodGet, "/api/announcements", nil, token, "")
	var anns []struct {
		Content string `json:"content"`
	}
	list.JSON(t, &anns)
	if len(anns) != 1 || anns[0].Content != "今晚维护" {
		t.Fatalf("公告列表 = %+v", anns)
	}

	status, data = adminDo(t, env.Server, http.MethodDelete, "/api/admin/announcements/"+created.ID, nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("删除公告状态 = %d, body=%s", status, data)
	}
	list = testutil.Do(t, env.Server, http.MethodGet, "/api/announcements", nil, token, "")
	list.JSON(t, &anns)
	if len(anns) != 0 {
		t.Fatalf("删除后公告应为空: %+v", anns)
	}
}

func TestStatsOnline(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, token := newIdentity(t, pool, "stats")

	r := testutil.Do(t, env.Server, http.MethodGet, "/api/stats/online", nil, token, "")
	if r.Status != http.StatusOK {
		t.Fatalf("在线统计状态 = %d, body=%s", r.Status, r.Body)
	}
	var stats struct {
		Online   int `json:"online"`
		Matching int `json:"matching"`
		InRooms  int `json:"inRooms"`
	}
	r.JSON(t, &stats)
	if stats.Online < 0 || stats.Matching < 0 || stats.InRooms < 0 {
		t.Fatalf("统计值异常: %+v", stats)
	}
}

func TestAdminLookupIdentityByCode(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	id, _ := newIdentity(t, pool, "lookup")
	ident, err := store.New(pool).GetIdentityByID(ctx, id.ID)
	if err != nil {
		t.Fatalf("查询身份失败: %v", err)
	}
	cookie := loginAdmin(t, env.Server, pool)
	status, data := adminDo(t, env.Server, http.MethodGet, "/api/admin/identities/by-code/"+ident.PublicCode, nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("按匿名编号查身份状态 = %d, body=%s", status, data)
	}
	var out struct {
		IdentityID string `json:"identityId"`
		PublicCode string `json:"publicCode"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("解析响应失败: %v", err)
	}
	if out.IdentityID != id.ID.String() || out.PublicCode != ident.PublicCode {
		t.Fatalf("查询结果不符: %+v", out)
	}
}

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

// maskWord / blockWord 是测试用词条：mask 级被打码，block 级被拦截（审查意见 R1）。
const (
	maskWord  = "示例替换词"
	blockWord = "禁止词"
)

// maskFilter 返回含一个打码级词与一个拦截级词的过滤器。
func maskFilter() *moderation.Moderator {
	return moderation.NewModerator([]moderation.Entry{
		{Word: maskWord, Level: moderation.LevelMask},
		{Word: blockWord, Level: moderation.LevelBlock},
	})
}

// TestBroadcastContentMasked 验证打码级敏感词在广播入库前被替换（审查意见 R1）。
func TestBroadcastContentMasked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{Filter: maskFilter()})
	id, token := newIdentity(t, pool, "bmask")
	if _, err := pool.Exec(ctx, `UPDATE identities SET broadcast_cards = 1 WHERE id = $1`, id.ID); err != nil {
		t.Fatalf("发放广播卡失败: %v", err)
	}

	sub := testutil.Do(t, env.Server, http.MethodPost, "/api/broadcasts",
		map[string]string{"content": "今天聊" + maskWord + "吧"}, token, "")
	if sub.Status != http.StatusOK {
		t.Fatalf("提交广播状态 = %d, body=%s", sub.Status, sub.Body)
	}
	// 我的广播里读到的必须是打码后的文本。
	mine := testutil.Do(t, env.Server, http.MethodGet, "/api/broadcasts/mine", nil, token, "")
	var list []struct {
		Content string `json:"content"`
	}
	mine.JSON(t, &list)
	if len(list) != 1 {
		t.Fatalf("我的广播 = %+v", list)
	}
	if want := "今天聊*****吧"; list[0].Content != want {
		t.Errorf("我的广播内容 = %q, 期望 %q（打码后的文本）", list[0].Content, want)
	}

	// 落库的也必须是打码后的文本，避免审核通过后原样公开。
	var stored string
	if err := pool.QueryRow(ctx, `SELECT content FROM broadcasts WHERE identity_id = $1`, id.ID).Scan(&stored); err != nil {
		t.Fatalf("查询广播失败: %v", err)
	}
	if want := "今天聊*****吧"; stored != want {
		t.Errorf("库中广播内容 = %q, 期望 %q", stored, want)
	}

	// 拦截级词仍然返回 content_blocked。
	if _, err := pool.Exec(ctx, `UPDATE identities SET broadcast_cards = 1 WHERE id = $1`, id.ID); err != nil {
		t.Fatalf("发放广播卡失败: %v", err)
	}
	blocked := testutil.Do(t, env.Server, http.MethodPost, "/api/broadcasts",
		map[string]string{"content": "含有" + blockWord + "的内容"}, token, "")
	if blocked.Status != http.StatusUnprocessableEntity {
		t.Fatalf("拦截级词状态 = %d, 期望 422, body=%s", blocked.Status, blocked.Body)
	}
	assertErrCode(t, blocked.Body, "content_blocked")
}
