//go:build integration

package external_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestExternalSettingsAPI(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	cache := env.Deps.ExternalSettings
	var notifications atomic.Int32
	cache.OnChange(func(s external.Settings) {
		if cache.Plan() != s {
			t.Error("通知时内存快照尚未生效")
		}
		notifications.Add(1)
	})
	const path = "/api/admin/external/settings"
	request := func(method string, body map[string]any, cookie *http.Cookie) (int, map[string]any) {
		t.Helper()
		data, _ := json.Marshal(body)
		req, err := http.NewRequest(method, env.Server.URL+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", testutil.DefaultOrigin)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		resp, err := env.Server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			t.Fatalf("响应不是 JSON: status=%d body=%s", resp.StatusCode, raw)
		}
		return resp.StatusCode, result
	}
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		status, _ := request(method, nil, nil)
		if status != http.StatusUnauthorized {
			t.Fatalf("未登录 %s 应 401，got %d", method, status)
		}
	}
	adminID, err := admin.CreateUser(context.Background(), pool, "external-admin", "test-password-123")
	if err != nil {
		t.Fatal(err)
	}
	login, _ := http.NewRequest(http.MethodPost, env.Server.URL+"/api/admin/login", bytes.NewBufferString(`{"username":"external-admin","password":"test-password-123"}`))
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
		t.Fatal("没有管理员 Cookie")
	}
	status, got := request(http.MethodGet, nil, cookie)
	if status != 200 {
		t.Fatalf("GET: %d %v", status, got)
	}
	defaults := map[string]any{"enabled": false, "chat2019Enabled": false, "wanfengEnabled": false, "onlineThreshold": float64(5), "localWaitSeconds": float64(10), "externalMatchSeconds": float64(10), "chat2019MaxSessions": float64(20), "wanfengMaxSessions": float64(20)}
	for k, v := range defaults {
		if got[k] != v {
			t.Errorf("默认 %s=%v, want %v", k, got[k], v)
		}
	}
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM external_settings").Scan(&count); err != nil || count != 1 {
		t.Fatalf("默认行: count=%d %v", count, err)
	}
	for field, values := range map[string][]int{"onlineThreshold": {-1, 100001}, "localWaitSeconds": {-1, 601}, "externalMatchSeconds": {2, 121}, "chat2019MaxSessions": {0, 1001}, "wanfengMaxSessions": {0, 1001}} {
		for _, v := range values {
			body := clone(defaults)
			body[field] = v
			status, got := request(http.MethodPut, body, cookie)
			if status != 400 {
				t.Fatalf("%s=%d 应 400, got %d %v", field, v, status, got)
			}
		}
	}
	if cache.Plan().Enabled || notifications.Load() != 0 {
		t.Fatal("无效 PUT 不应更新快照或通知")
	}
	for _, values := range []map[string]any{
		{"onlineThreshold": 0, "localWaitSeconds": 0, "externalMatchSeconds": 3, "chat2019MaxSessions": 1, "wanfengMaxSessions": 1},
		{"onlineThreshold": 100000, "localWaitSeconds": 600, "externalMatchSeconds": 120, "chat2019MaxSessions": 1000, "wanfengMaxSessions": 1000},
	} {
		body := clone(defaults)
		body["enabled"] = true
		body["chat2019Enabled"] = true
		body["wanfengEnabled"] = true
		for k, v := range values {
			body[k] = v
		}
		status, got := request(http.MethodPut, body, cookie)
		if status != 200 {
			t.Fatalf("PUT: %d %v", status, got)
		}
		_, got = request(http.MethodGet, nil, cookie)
		plan := cache.Plan()
		if !plan.Enabled || !plan.Chat2019Enabled || !plan.WanfengEnabled || plan.OnlineThreshold != values["onlineThreshold"] || plan.LocalWait != time.Duration(values["localWaitSeconds"].(int))*time.Second || plan.ExternalMatchTimeout != time.Duration(values["externalMatchSeconds"].(int))*time.Second || plan.Chat2019MaxSessions != values["chat2019MaxSessions"] || plan.WanfengMaxSessions != values["wanfengMaxSessions"] {
			t.Fatalf("PUT 后 Plan 未立即更新: %+v", plan)
		}
		for k, v := range body {
			if n, ok := v.(int); ok {
				v = float64(n)
			}
			if got[k] != v {
				t.Fatalf("PUT 后 %s=%v, want %v", k, got[k], v)
			}
		}
	}
	var updatedBy string
	if notifications.Load() != 2 {
		t.Fatalf("通知次数 = %d", notifications.Load())
	}
	reloaded, err := external.NewSettingsCache(context.Background(), pool)
	if err != nil || reloaded.Plan() != cache.Plan() {
		t.Fatalf("重建缓存未复用已保存的设置: %v", err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT updated_by::text FROM external_settings WHERE id=1").Scan(&updatedBy); err != nil || updatedBy != adminID.ID.String() {
		t.Fatalf("updated_by: %s %v", updatedBy, err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM admin_audit_logs WHERE action='external.settings.update' AND admin_id=$1", adminID.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("审计数量: %d %v", count, err)
	}
}

func clone(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func TestExternalSettingsSaveSurvivesRequestCancellation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	cache, err := external.NewSettingsCache(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var notified bool
	cache.OnChange(func(s external.Settings) { notified = s.Enabled })
	in := external.UpdateInput{Enabled: true, OnlineThreshold: 5, LocalWaitSeconds: 10, ExternalMatchSeconds: 10, Chat2019MaxSessions: 20, WanfengMaxSessions: 20}
	if _, err := cache.Update(ctx, in, pgtype.UUID{}); err != nil {
		t.Fatalf("已接受的设置保存不应随请求取消: %v", err)
	}
	if !cache.Plan().Enabled || !notified {
		t.Fatal("缓存或通知未生效")
	}
	var enabled bool
	if err := pool.QueryRow(context.Background(), "SELECT enabled FROM external_settings WHERE id=1").Scan(&enabled); err != nil || !enabled {
		t.Fatalf("数据库与缓存不一致: %v", err)
	}
}

func TestExternalSettingsConcurrentPlanAndSave(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	cache, err := external.NewSettingsCache(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	done := make(chan struct{})
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
					s := cache.Plan()
					if s.Chat2019MaxSessions != 20 || s.ExternalMatchTimeout != 10*time.Second {
						t.Error("读取到不完整的快照")
						return
					}
				}
			}
		}()
	}
	defer func() { close(done); readers.Wait() }()
	var writers sync.WaitGroup
	for i := range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			_, err := cache.Update(context.Background(), external.UpdateInput{OnlineThreshold: i, LocalWaitSeconds: 10, ExternalMatchSeconds: 10, Chat2019MaxSessions: 20, WanfengMaxSessions: 20}, pgtype.UUID{})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	writers.Wait()
	var threshold int
	if err := pool.QueryRow(context.Background(), "SELECT online_threshold FROM external_settings WHERE id=1").Scan(&threshold); err != nil || cache.Plan().OnlineThreshold != threshold {
		t.Fatalf("并发保存后数据库与缓存不一致: %v", err)
	}
}
