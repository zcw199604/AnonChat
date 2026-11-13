//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

func TestExternalStatusIsAuthenticatedAndSettingsReachBridge(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	getStatus := func(cookie *http.Cookie) (int, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, env.Server.URL+"/api/admin/external/status", nil)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := env.Server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var result map[string]any
		_ = json.Unmarshal(body, &result)
		return resp.StatusCode, result
	}
	if code, _ := getStatus(nil); code != http.StatusUnauthorized {
		t.Fatalf("未登录status=%d want401", code)
	}
	_, err := admin.CreateUser(context.Background(), pool, "bridge-admin", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	login, _ := http.NewRequest(http.MethodPost, env.Server.URL+"/api/admin/login", bytes.NewBufferString(`{"username":"bridge-admin","password":"test-password-123"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", testutil.DefaultOrigin)
	resp, err := env.Server.Client().Do(login)
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
		t.Fatal("管理员登录失败")
	}
	code, snapshot := getStatus(cookie)
	if code != http.StatusOK {
		t.Fatalf("已登录status=%d body=%v", code, snapshot)
	}
	if _, ok := snapshot["providers"].([]any); !ok {
		t.Fatalf("providers应是数组: %v", snapshot)
	}
	if _, ok := snapshot["online"]; !ok {
		t.Fatal("缺少online")
	}
	if _, ok := snapshot["queue"]; !ok {
		t.Fatal("缺少queue")
	}
	providers, _ := snapshot["providers"].([]any)
	for _, raw := range providers {
		provider, _ := raw.(map[string]any)
		if provider["enabled"] != false {
			t.Fatalf("默认外站站点应关闭: %v", provider)
		}
	}
	payload := `{"enabled":true,"chat2019Enabled":true,"wanfengEnabled":false,"onlineThreshold":5,"localWaitSeconds":10,"externalMatchSeconds":10,"chat2019MaxSessions":20,"wanfengMaxSessions":20}`
	update, _ := http.NewRequest(http.MethodPut, env.Server.URL+"/api/admin/external/settings", bytes.NewBufferString(payload))
	update.Header.Set("Content-Type", "application/json")
	update.Header.Set("Origin", testutil.DefaultOrigin)
	update.AddCookie(cookie)
	resp, err = env.Server.Client().Do(update)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatal(resp.Status)
	}
	code, snapshot = getStatus(cookie)
	providers, _ = snapshot["providers"].([]any)
	var chatEnabled bool
	for _, raw := range providers {
		provider, _ := raw.(map[string]any)
		if provider["name"] == "chat2019" {
			chatEnabled = provider["enabled"] == true
		}
	}
	if code != http.StatusOK || !chatEnabled {
		t.Fatalf("设置未立即到达Bridge: %d %v", code, snapshot)
	}
}
