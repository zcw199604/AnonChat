//go:build integration

package admin_test

import (
	"context"
	"encoding/json"
	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRateLimitRulesRequireAdminAndPersistImmediately(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	settings := moderation.NewLimitSettings(pool, 1, nil)
	if err := settings.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	env := testutil.MustEnv(t, pool, testutil.Config{LimitSettings: settings})
	denied := testutil.Do(t, env.Server, http.MethodGet, "/api/admin/rate-limit-rules", nil, "", "")
	if denied.Status != http.StatusUnauthorized {
		t.Fatalf("未登录应拒绝: %d", denied.Status)
	}
	if _, err := admin.CreateUser(context.Background(), pool, "limits-admin", "test-password-123"); err != nil {
		t.Fatal(err)
	}
	login := testutil.Do(t, env.Server, http.MethodPost, "/api/admin/login", map[string]string{"username": "limits-admin", "password": "test-password-123"}, "", "")
	if login.Status != http.StatusOK {
		t.Fatalf("登录: %s", login.Body)
	}
	// testutil.Do 仅提取身份Cookie，这里单独取得管理员Cookie。
	req := httptest.NewRequest(http.MethodPost, env.Server.URL+"/api/admin/login", strings.NewReader(`{"username":"limits-admin","password":"test-password-123"}`))
	req.RequestURI = ""
	req.Header.Set("Origin", testutil.DefaultOrigin)
	req.Header.Set("Content-Type", "application/json")
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var cookie *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == admin.SessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("缺少管理员Cookie")
	}
	rules := make([]moderation.Rule, 0)
	for _, meta := range moderation.RuleRegistry() {
		r := settings.Current().Rules[meta.Key]
		if r.Key == moderation.RuleMsgPerMinute {
			r.Limit = 42
		}
		rules = append(rules, r)
	}
	body, _ := json.Marshal(map[string]any{"rules": rules})
	update, _ := http.NewRequest(http.MethodPut, env.Server.URL+"/api/admin/rate-limit-rules", strings.NewReader(string(body)))
	update.Header.Set("Origin", testutil.DefaultOrigin)
	update.Header.Set("Content-Type", "application/json")
	update.AddCookie(cookie)
	result, err := env.Server.Client().Do(update)
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		t.Fatalf("保存失败: %d", result.StatusCode)
	}
	if settings.Current().Rules[moderation.RuleMsgPerMinute].Limit != 42 {
		t.Fatal("快照未立即更新")
	}
	var detail []byte
	if err := pool.QueryRow(context.Background(), "SELECT detail FROM admin_audit_logs WHERE action='update_rate_limit_rules'").Scan(&detail); err != nil {
		t.Fatal(err)
	}
	var changes map[string]any
	if err := json.Unmarshal(detail, &changes); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes["msg_per_minute"] == nil {
		t.Fatalf("审计只能记录变化: %s", detail)
	}
}
