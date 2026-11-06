//go:build integration

package admin_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func mkIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "tok-" + name + "-" + time.Now().Format("150405.000000000")
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "seed-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

type resp struct {
	status  int
	body    []byte
	adminCK string // Set-Cookie 的 __Host-admin 值
}

func do(t *testing.T, srv *httptest.Server, method, path string, body any, adminCookie string) resp {
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
	if adminCookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: adminCookie})
	}
	r, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer r.Body.Close()
	data, _ := io.ReadAll(r.Body)
	out := resp{status: r.StatusCode, body: data}
	for _, ck := range r.Cookies() {
		if ck.Name == admin.SessionCookie {
			out.adminCK = ck.Value
		}
	}
	return out
}

func login(t *testing.T, srv *httptest.Server, user, pass string) string {
	t.Helper()
	r := do(t, srv, http.MethodPost, "/api/admin/login", map[string]string{"username": user, "password": pass}, "")
	if r.status != http.StatusOK {
		t.Fatalf("登录失败: %d %s", r.status, r.body)
	}
	if r.adminCK == "" {
		t.Fatal("登录应下发 __Host-admin Cookie")
	}
	return r.adminCK
}

func setupAdmin(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := admin.CreateUser(ctx, pool, "admin", "supersecret123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
}

func TestLoginAndUnauthorized(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	setupAdmin(t, pool)

	// 未登录访问管理接口 → 401。
	if r := do(t, env.Server, http.MethodGet, "/api/admin/reports", nil, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("未登录应 401, 实际 %d", r.status)
	}
	// 身份 Cookie 无法访问管理接口。
	_, tok := mkIdentity(t, pool, "alice")
	req, _ := http.NewRequest(http.MethodGet, env.Server.URL+"/api/admin/reports", nil)
	req.Header.Set("Origin", testutil.DefaultOrigin)
	req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: tok})
	r, _ := env.Server.Client().Do(req)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("身份 Cookie 访问管理接口应 401, 实际 %d", r.StatusCode)
	}
	r.Body.Close()

	// 正确登录成功。
	cookie := login(t, env.Server, "admin", "supersecret123")
	if r := do(t, env.Server, http.MethodGet, "/api/admin/reports", nil, cookie); r.status != http.StatusOK {
		t.Fatalf("登录后应 200, 实际 %d", r.status)
	}
}

func TestLoginRateLimit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		AdminLoginLimiter: moderation.NewFixedWindow(5, 15*time.Minute),
	})
	setupAdmin(t, pool)

	for i := 0; i < 5; i++ {
		if r := do(t, env.Server, http.MethodPost, "/api/admin/login", map[string]string{"username": "admin", "password": "wrong"}, ""); r.status != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401, 实际 %d", i+1, r.status)
		}
	}
	if r := do(t, env.Server, http.MethodPost, "/api/admin/login", map[string]string{"username": "admin", "password": "wrong"}, ""); r.status != http.StatusTooManyRequests {
		t.Fatalf("连续失败应 429, 实际 %d", r.status)
	}
}

func TestReportsListAndDetail(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	setupAdmin(t, pool)
	cookie := login(t, env.Server, "admin", "supersecret123")

	reporter, _ := mkIdentity(t, pool, "reporter")
	reported, _ := mkIdentity(t, pool, "reported")
	// 被举报者实名（脱敏号码）。
	var acctID pgtype.UUID
	pool.QueryRow(ctx, "INSERT INTO verified_accounts (phone_hmac, phone_enc, phone_masked) VALUES (uuid_send(gen_random_uuid()), uuid_send(gen_random_uuid()), '+86 138****1234') RETURNING id").Scan(&acctID)
	pool.Exec(ctx, "UPDATE identities SET account_id=$1 WHERE id=$2", acctID, reported.ID)

	var reportID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO reports (reporter_identity_id, reported_identity_id, conversation_id, reason, snapshot) VALUES ($1,$2,NULL,'harassment','{\"messages\":[{\"id\":1}]}'::jsonb) RETURNING id", reporter.ID, reported.ID).Scan(&reportID); err != nil {
		t.Fatal(err)
	}

	// 列表。
	lr := do(t, env.Server, http.MethodGet, "/api/admin/reports?status=open", nil, cookie)
	if lr.status != http.StatusOK {
		t.Fatalf("列表状态 = %d", lr.status)
	}
	var list struct {
		Reports []struct {
			ID string `json:"id"`
		} `json:"reports"`
	}
	_ = json.Unmarshal(lr.body, &list)
	if len(list.Reports) != 1 || list.Reports[0].ID != reportID.String() {
		t.Fatalf("举报列表不符: %s", lr.body)
	}

	// 详情含 snapshot 与脱敏号码，且无明文手机号。
	dr := do(t, env.Server, http.MethodGet, "/api/admin/reports/"+reportID.String(), nil, cookie)
	if dr.status != http.StatusOK {
		t.Fatalf("详情状态 = %d %s", dr.status, dr.body)
	}
	var detail struct {
		Snapshot any `json:"snapshot"`
		Reported struct {
			Nickname    string `json:"nickname"`
			PhoneMasked string `json:"phoneMasked"`
		} `json:"reported"`
	}
	_ = json.Unmarshal(dr.body, &detail)
	if detail.Snapshot == nil {
		t.Error("详情应包含 snapshot")
	}
	if detail.Reported.Nickname != "reported" || detail.Reported.PhoneMasked != "+86 138****1234" {
		t.Errorf("被举报者摘要不符: %+v", detail.Reported)
	}
	if strings.Contains(string(dr.body), "13800001234") {
		t.Error("不应返回手机号明文")
	}
}

func TestResolveBanKicksAndRevokeRestores(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	setupAdmin(t, pool)
	cookie := login(t, env.Server, "admin", "supersecret123")

	reporter, _ := mkIdentity(t, pool, "reporter")
	reported, reportedTok := mkIdentity(t, pool, "reported")
	var reportID pgtype.UUID
	pool.QueryRow(ctx, "INSERT INTO reports (reporter_identity_id, reported_identity_id, conversation_id, reason, snapshot) VALUES ($1,$2,NULL,'spam','{}'::jsonb) RETURNING id", reporter.ID, reported.ID).Scan(&reportID)

	// 被举报者在线。
	ws := dialWS(t, env.Server, reportedTok)
	_ = readUntil(t, ws, realtime.TypeHello, 3*time.Second)

	// 处理举报：封禁身份。
	rr := do(t, env.Server, http.MethodPost, "/api/admin/reports/"+reportID.String()+"/resolve", map[string]any{
		"action": "ban",
		"ban":    map[string]any{"subjects": []string{"identity"}, "durationHours": nil, "reason": "spam"},
	}, cookie)
	if rr.status != http.StatusNoContent {
		t.Fatalf("处理举报状态 = %d %s", rr.status, rr.body)
	}
	// 收到 kicked。
	if e := readUntil(t, ws, realtime.TypeKicked, 3*time.Second); e.Type != realtime.TypeKicked {
		t.Fatal("被封禁应收到 kicked")
	}
	// 封禁生效：REST 403 banned。
	meR := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, reportedTok, "")
	if meR.Status != http.StatusForbidden {
		t.Fatalf("封禁后 REST 应 403, 实际 %d", meR.Status)
	}
	// 举报状态为 resolved。
	var status string
	pool.QueryRow(ctx, "SELECT status FROM reports WHERE id=$1", reportID).Scan(&status)
	if status != "resolved" {
		t.Errorf("举报状态应 resolved, 实际 %s", status)
	}

	// 找到该封禁并撤销。
	var banID pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM bans WHERE subject_type='identity' AND subject_value=$1 AND revoked_at IS NULL", reported.ID.String()).Scan(&banID); err != nil {
		t.Fatalf("应存在封禁记录: %v", err)
	}
	if rb := do(t, env.Server, http.MethodDelete, "/api/admin/bans/"+banID.String(), nil, cookie); rb.status != http.StatusNoContent {
		t.Fatalf("撤销封禁状态 = %d", rb.status)
	}
	// 撤销后恢复访问（缓存已失效）。
	meR2 := testutil.Do(t, env.Server, http.MethodGet, "/api/identity/me", nil, reportedTok, "")
	if meR2.Status != http.StatusOK {
		t.Fatalf("撤销封禁后应恢复 200, 实际 %d", meR2.Status)
	}
}

func TestStatsAndAudit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	setupAdmin(t, pool)
	cookie := login(t, env.Server, "admin", "supersecret123")

	sr := do(t, env.Server, http.MethodGet, "/api/admin/stats", nil, cookie)
	if sr.status != http.StatusOK {
		t.Fatalf("stats 状态 = %d", sr.status)
	}
	var stats map[string]any
	_ = json.Unmarshal(sr.body, &stats)
	for _, k := range []string{"online", "activeConversations", "messages24h", "openReports"} {
		if _, ok := stats[k]; !ok {
			t.Errorf("stats 缺少字段 %s", k)
		}
	}

	// 创建封禁 → 应有审计日志。
	reported, _ := mkIdentity(t, pool, "target")
	cb := do(t, env.Server, http.MethodPost, "/api/admin/bans", map[string]any{
		"subjects": []map[string]string{{"type": "identity", "value": reported.ID.String()}},
		"reason":   "test",
	}, cookie)
	if cb.status != http.StatusOK {
		t.Fatalf("创建封禁状态 = %d %s", cb.status, cb.body)
	}
	al := do(t, env.Server, http.MethodGet, "/api/admin/audit-logs", nil, cookie)
	if al.status != http.StatusOK {
		t.Fatalf("审计列表状态 = %d", al.status)
	}
	// 审计日志应包含登录、创建封禁。
	var n int
	pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit_logs WHERE action='create_ban'").Scan(&n)
	if n < 1 {
		t.Error("创建封禁应写入审计日志")
	}
	pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit_logs WHERE action='login_success'").Scan(&n)
	if n < 1 {
		t.Error("登录成功应写入审计日志")
	}
}

func TestBanSubjectValidation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	setupAdmin(t, pool)
	cookie := login(t, env.Server, "admin", "supersecret123")

	// identity 值不是 UUID → 400。
	r := do(t, env.Server, http.MethodPost, "/api/admin/bans", map[string]any{
		"subjects": []map[string]string{{"type": "identity", "value": "not-a-uuid"}},
		"reason":   "x",
	}, cookie)
	if r.status != http.StatusBadRequest {
		t.Fatalf("非法 UUID 应 400, 实际 %d", r.status)
	}
	// ip 值非法 → 400。
	r = do(t, env.Server, http.MethodPost, "/api/admin/bans", map[string]any{
		"subjects": []map[string]string{{"type": "ip", "value": "999.1.1.1"}},
		"reason":   "x",
	}, cookie)
	if r.status != http.StatusBadRequest {
		t.Fatalf("非法 IP 应 400, 实际 %d", r.status)
	}
}

func TestAdminIPAllowlist(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		AllowedOrigins:   []string{testutil.DefaultOrigin},
		AdminIPAllowlist: []string{"10.0.0.0/8"},
	})
	setupAdmin(t, pool)

	r := do(t, env.Server, http.MethodPost, "/api/admin/login", map[string]string{"username": "admin", "password": "supersecret123"}, "")
	if r.status != http.StatusNotFound {
		t.Fatalf("IP 不在白名单应 404, 实际 %d", r.status)
	}
}

// ---- WS 辅助 ----

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", "__Host-anon="+token)
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func readUntil(t *testing.T, c *websocket.Conn, want realtime.MessageType, timeout time.Duration) realtime.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rctx, cancel := context.WithTimeout(ctx, time.Until(deadline))
		_, data, err := c.Read(rctx)
		cancel()
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		var env realtime.Envelope
		_ = json.Unmarshal(data, &env)
		if env.Type == want {
			return env
		}
	}
	t.Fatalf("未收到 %q", want)
	return realtime.Envelope{}
}
