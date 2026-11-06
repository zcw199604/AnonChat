//go:build integration

package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

// forbiddenFields 是任何用户侧响应里都不应出现的字段名（how.md 3.8）。
var forbiddenFields = []string{
	"identity_id",
	"identityId",
	"token_hash",
	"tokenHash",
	"created_ip",
	"createdIp",
	"phone",
	"phoneMasked",
	"vip_until",
}

// assertNoLeak 断言响应体里没有出现身份 UUID 与禁止字段。
func assertNoLeak(t *testing.T, label string, body []byte, identityID string) {
	t.Helper()
	s := string(body)
	if identityID != "" && strings.Contains(s, identityID) {
		t.Errorf("%s 泄漏了身份 UUID: %s", label, s)
	}
	for _, f := range forbiddenFields {
		if strings.Contains(s, `"`+f+`"`) {
			t.Errorf("%s 出现了禁止字段 %q: %s", label, f, s)
		}
	}
}

// TestNoIdentityLeakInUserResponses 遍历全部用户侧接口（含本包新增），断言不泄漏身份信息。
func TestNoIdentityLeakInUserResponses(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{StoryMinDuration: time.Second})

	// 创建身份，取回明文令牌。
	created := testutil.Do(t, env.Server, "POST", "/api/identity", nil, "", "")
	if created.Status != 200 || created.Token == "" {
		t.Fatalf("创建身份失败: %d", created.Status)
	}
	token := created.Token

	// 从库中查出该身份的 UUID（不应出现在任何响应里）。
	ident, err := store.New(pool).GetIdentityByTokenHash(ctx, identity.HashToken(token))
	if err != nil {
		t.Fatalf("查询身份失败: %v", err)
	}
	idStr := ident.ID.String()

	// 预置数据：种子房间 + 一张广播卡。
	testutil.SeedRooms(t, pool)
	if _, err := pool.Exec(ctx, `UPDATE identities SET broadcast_cards = 1 WHERE id = $1`, ident.ID); err != nil {
		t.Fatalf("发放广播卡失败: %v", err)
	}

	checks := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/identity", nil},
		{"GET", "/api/identity/me", nil},
		{"PATCH", "/api/identity/profile", map[string]any{"gender": "female", "age": 24, "province": "浙江", "provincePublic": true, "tags": []string{"音乐"}}},
		{"POST", "/api/identity/nickname/shuffle", nil},
		{"PUT", "/api/identity/nickname", map[string]string{"nickname": "测试昵称"}},
		{"GET", "/api/conversations", nil},
		{"GET", "/api/rooms", nil},
		{"GET", "/api/bottles/mine", nil},
		{"POST", "/api/bottles", map[string]string{"content": "hello"}},
		{"POST", "/api/private-links", map[string]any{"ttlHours": 24}},
		{"GET", "/api/stats/online", nil},
		{"GET", "/api/announcements", nil},
		{"GET", "/api/broadcasts/current", nil},
		{"POST", "/api/broadcasts", map[string]string{"content": "我的广播"}},
		{"GET", "/api/broadcasts/mine", nil},
		{"GET", "/api/feed", nil},
		{"GET", "/api/stories?status=waiting", nil},
		{"POST", "/api/stories", map[string]any{
			"title": "泄漏测试", "opening": "这是一段用于泄漏测试的开场白。", "tags": []string{},
			"roleA": "甲", "roleB": "乙", "hostRole": "a",
		}},
		{"GET", "/api/works", nil},
	}
	for _, c := range checks {
		r := testutil.Do(t, env.Server, c.method, c.path, c.body, token, "")
		if r.Status >= 500 {
			t.Fatalf("%s %s 返回 %d, body=%s", c.method, c.path, r.Status, r.Body)
		}
		assertNoLeak(t, c.method+" "+c.path, r.Body, idStr)
	}

	// 会话详情接口也不应泄漏身份 UUID。
	link := testutil.Do(t, env.Server, "POST", "/api/private-links", map[string]any{"ttlHours": 24}, token, "")
	if link.Status != 200 {
		t.Fatalf("创建私密链接失败: %d", link.Status)
	}
	var linkInfo struct {
		ConversationID string `json:"conversationId"`
	}
	link.JSON(t, &linkInfo)
	detail := testutil.Do(t, env.Server, "GET", "/api/conversations/"+linkInfo.ConversationID, nil, token, "")
	if detail.Status != 200 {
		t.Fatalf("会话详情状态 = %d", detail.Status)
	}
	assertNoLeak(t, "conversation detail", detail.Body, idStr)
}
