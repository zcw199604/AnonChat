//go:build integration

package bot_test

import (
	"net/http"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// TestP7BotFields 覆盖双核设置的创建、更新、读取及类型用量的窗口统计。
func TestP7BotFields(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir()})
	cookie := loginAdmin(t, pool, env.Server)
	create := map[string]any{"label": "双核测试", "coreMode": "dual", "reasonModel": " reason-custom ", "memoryEnabled": false}
	status, body := doJSON(t, env.Server, http.MethodPost, "/api/admin/bots", create, cookie)
	if status != http.StatusCreated {
		t.Fatalf("创建新字段: %d %s", status, body)
	}
	var got map[string]any
	mustJSON(t, body, &got)
	id := got["id"].(string)
	assertP7BotFields(t, got, "dual", "reason-custom", false)
	for _, mode := range []string{"", "unknown", "DUAL", " dual "} {
		create["coreMode"] = mode
		status, body = doJSON(t, env.Server, http.MethodPost, "/api/admin/bots", create, cookie)
		if status != http.StatusBadRequest {
			t.Fatalf("非法创建 coreMode=%q: %d %s", mode, status, body)
		}
	}
	update := map[string]any{
		"label": "双核测试", "model": "test-model", "temperature": 0.7, "maxOutputTokens": 200,
		"maxActiveMatches": 2, "windowSeconds": 600, "bottleIntervalSeconds": 60,
		"profile":  map[string]any{"gender": "secret", "tags": []string{}},
		"coreMode": "single", "reasonModel": "reason-next", "memoryEnabled": true,
	}
	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/bots/"+id, update, cookie)
	if status != http.StatusOK {
		t.Fatalf("更新新字段: %d %s", status, body)
	}
	mustJSON(t, body, &got)
	assertP7BotFields(t, got, "single", "reason-next", true)
	update["coreMode"] = "bad"
	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/bots/"+id, update, cookie)
	if status != http.StatusBadRequest {
		t.Fatalf("非法更新模式: %d %s", status, body)
	}
	peer, _ := mkIdentity(t, pool, "记忆对象")
	if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_memories(bot_id,peer_identity_id) VALUES($1,$2)`, id, peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bot_usage(bot_id,prompt_tokens,completion_tokens,kind,created_at) VALUES
 ($1,10,3,'reason',now()),($1,20,4,'memory',now()),($1,50,0,'reason',now()-interval '1 hour'),($1,7,2,'reply',now())`, id); err != nil {
		t.Fatal(err)
	}
	status, body = doJSON(t, env.Server, http.MethodGet, "/api/admin/bots", nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取列表: %d %s", status, body)
	}
	var rows []map[string]any
	mustJSON(t, body, &rows)
	if len(rows) != 1 {
		t.Fatalf("bot 数量=%d", len(rows))
	}
	got = rows[0]
	assertP7BotFields(t, got, "single", "reason-next", true)
	usage := got["usage"].(map[string]any)
	if usage["windowReasonTokens"] != float64(13) || usage["windowMemoryTokens"] != float64(24) || usage["windowTokens"] != float64(46) || got["memoryCount"] != float64(1) {
		t.Fatalf("窗口统计错误: %s", body)
	}
	status, body = doJSON(t, env.Server, http.MethodGet, "/api/admin/bots/"+id, nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取详情: %d %s", status, body)
	}
	mustJSON(t, body, &got)
	assertP7BotFields(t, got, "single", "reason-next", true)
	// 旧客户端省略新字段时，更新保留既有值。
	delete(update, "coreMode")
	delete(update, "reasonModel")
	delete(update, "memoryEnabled")
	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/bots/"+id, update, cookie)
	if status != http.StatusOK {
		t.Fatalf("兼容更新: %d %s", status, body)
	}
	mustJSON(t, body, &got)
	assertP7BotFields(t, got, "single", "reason-next", true)
	status, body = doJSON(t, env.Server, http.MethodPost, "/api/admin/bots", map[string]any{"label": "默认设置"}, cookie)
	if status != http.StatusCreated {
		t.Fatalf("默认创建: %d %s", status, body)
	}
	mustJSON(t, body, &got)
	assertP7BotFields(t, got, "single", "", true)
}

func assertP7BotFields(t *testing.T, got map[string]any, mode, model string, memory bool) {
	t.Helper()
	if got["coreMode"] != mode || got["reasonModel"] != model || got["memoryEnabled"] != memory {
		t.Fatalf("bot 新字段不符: %#v", got)
	}
}
