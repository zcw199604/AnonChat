//go:build integration

package bot_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// TestAdminMemories 验证分页、隔离、详情审计、删除级联以及后台权限。
func TestAdminMemories(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir()})
	cookie := loginAdmin(t, pool, env.Server)
	b := seedBot(t, pool, t.TempDir(), seedBotOptions{})
	other := seedBot(t, pool, t.TempDir(), seedBotOptions{})
	peer, token := mkIdentity(t, pool, "记忆对方")
	peer2, _ := mkIdentity(t, pool, "较旧对方")
	for _, id := range []string{b.BotID.String(), other.BotID.String()} {
		if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_memories(bot_id,peer_identity_id,profile,affinity,mood_label) VALUES($1,$2,'{"basic_info":{"称呼":{"v":"画像秘密","t":"2026-11-09T00:00:00Z"}}}',12,'开心')`, id, peer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_events(bot_id,peer_identity_id,gist) VALUES($1,$2,'事件秘密')`, id, peer.ID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_memories(bot_id,peer_identity_id,last_interaction_at) VALUES($1,$2,now()-interval '1 day')`, b.BotID, peer2.ID); err != nil {
		t.Fatal(err)
	}
	base := "/api/admin/bots/" + b.BotID.String() + "/memories"
	detail := base + "/" + peer.ID.String()
	for _, c := range []string{"", token} {
		for _, req := range []struct{ method, path string }{{"GET", base}, {"GET", detail}, {"DELETE", detail}, {"DELETE", base}} {
			status, body := doJSON(t, env.Server, req.method, req.path, nil, c)
			if status != 401 && status != 403 {
				t.Fatalf("非管理员 %s %s: %d %s", req.method, req.path, status, body)
			}
		}
	}
	status, body := doJSON(t, env.Server, "GET", base+"?limit=1&offset=0", nil, cookie)
	if status != 200 {
		t.Fatalf("列表: %d %s", status, body)
	}
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0]["identityId"] != peer.ID.String() || list[0]["nickname"] != peer.Nickname || list[0]["publicCode"] != peer.PublicCode || list[0]["affinity"] != float64(12) || list[0]["eventCount"] != float64(1) || list[0]["lastInteractionAt"] == "" {
		t.Fatalf("列表字段: %s", body)
	}
	status, body = doJSON(t, env.Server, "GET", base+"?limit=1&offset=1", nil, cookie)
	if status != 200 || !strings.Contains(string(body), peer2.ID.String()) {
		t.Fatalf("分页: %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, "GET", detail, nil, cookie)
	if status != 200 || !strings.Contains(string(body), "画像秘密") || !strings.Contains(string(body), "事件秘密") {
		t.Fatalf("详情: %d %s", status, body)
	}
	missing := "00000000-0000-0000-0000-000000000001"
	for _, req := range []struct{ method, path string }{{"GET", base + "/" + missing}, {"DELETE", base + "/" + missing}, {"GET", "/api/admin/bots/" + missing + "/memories"}, {"DELETE", "/api/admin/bots/" + missing + "/memories"}} {
		status, body = doJSON(t, env.Server, req.method, req.path, nil, cookie)
		if status != 404 {
			t.Fatalf("缺失对象: %d %s", status, body)
		}
	}
	for _, query := range []string{"?limit=0", "?offset=-1", "?limit=no"} {
		status, body = doJSON(t, env.Server, "GET", base+query, nil, cookie)
		if status != 400 {
			t.Fatalf("非法分页: %d %s", status, body)
		}
	}
	status, body = doJSON(t, env.Server, "DELETE", detail, nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("删除: %d %s", status, body)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bot_peer_events WHERE bot_id=$1`, b.BotID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("事件未级联: %d %v", n, err)
	}
	status, body = doJSON(t, env.Server, "DELETE", base, nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("清空: %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, "GET", base, nil, cookie)
	if status != 200 || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("清空后列表: %d %s", status, body)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bot_peer_memories WHERE bot_id=$1`, other.BotID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("跨 bot 隔离失败: %d %v", n, err)
	}
	rows, err := pool.Query(ctx, `SELECT action,detail FROM admin_audit_logs WHERE action LIKE 'bot.memory.%' ORDER BY created_at`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	actions := map[string]int{}
	for rows.Next() {
		var action string
		var raw []byte
		if err := rows.Scan(&action, &raw); err != nil {
			t.Fatal(err)
		}
		actions[action]++
		var d map[string]any
		if err := json.Unmarshal(raw, &d); err != nil {
			t.Fatal(err)
		}
		for k := range d {
			if k != "id" && k != "identityId" && k != "count" {
				t.Fatalf("审计额外字段: %s", raw)
			}
		}
		if strings.Contains(string(raw), "秘密") {
			t.Fatalf("审计泄露: %s", raw)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"bot.memory.view", "bot.memory.delete", "bot.memory.clear"} {
		if actions[action] != 1 {
			t.Fatalf("审计 %s=%d", action, actions[action])
		}
	}
}
