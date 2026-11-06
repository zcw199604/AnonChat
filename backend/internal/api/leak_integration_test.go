//go:build integration

package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

// TestNoIdentityLeakInUserResponses 遍历普通用户接口，断言响应中不出现当前身份的 UUID。
func TestNoIdentityLeakInUserResponses(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})

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

	// 预置数据：一个公开房间会话的可读历史（用种子房间即可）。
	testutil.SeedRooms(t, pool)

	checks := []struct {
		method string
		path   string
		body   any
	}{
		{"POST", "/api/identity", nil},
		{"GET", "/api/identity/me", nil},
		{"GET", "/api/conversations", nil},
		{"GET", "/api/rooms", nil},
		{"GET", "/api/bottles/mine", nil},
		{"POST", "/api/bottles", map[string]string{"content": "hello"}},
		{"GET", "/api/bottles/mine", nil},
		{"POST", "/api/private-links", map[string]any{"ttlHours": 24}},
	}
	for _, c := range checks {
		r := testutil.Do(t, env.Server, c.method, c.path, c.body, token, "")
		if r.Status >= 500 {
			t.Fatalf("%s %s 返回 %d", c.method, c.path, r.Status)
		}
		if strings.Contains(string(r.Body), idStr) {
			t.Errorf("%s %s 响应泄漏了身份 UUID: %s", c.method, c.path, r.Body)
		}
	}

	// 会话详情接口也不应泄漏身份 UUID。
	link := testutil.Do(t, env.Server, "POST", "/api/private-links", map[string]any{"ttlHours": 24}, token, "")
	if link.Status != 200 {
		t.Fatalf("创建私密链接失败: %d", link.Status)
	}
	var linkInfo struct {
		ConversationID string `json:"conversationId"`
	}
	if err := json.Unmarshal(link.Body, &linkInfo); err != nil || linkInfo.ConversationID == "" {
		t.Fatalf("解析 conversationId 失败: %v", err)
	}
	detail := testutil.Do(t, env.Server, "GET", "/api/conversations/"+linkInfo.ConversationID, nil, token, "")
	if detail.Status != 200 {
		t.Fatalf("会话详情状态 = %d", detail.Status)
	}
	if strings.Contains(string(detail.Body), idStr) {
		t.Errorf("会话详情响应泄漏了身份 UUID: %s", detail.Body)
	}
}
