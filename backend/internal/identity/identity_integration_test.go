//go:build integration

package identity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// responseKeys 返回 JSON 对象的键集合，用于断言没有多余字段（尤其是身份 ID）。
func responseKeys(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, body)
	}
	return m
}

// TestCreateSetsCookieAndStoresHash 验证：创建身份时下发 __Host-anon Cookie，
// 库里只存令牌哈希，响应不含身份 ID。
func TestCreateSetsCookieAndStoresHash(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})

	resp := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if resp.Status != http.StatusOK {
		t.Fatalf("状态码 = %d, 期望 200, body=%s", resp.Status, resp.Body)
	}
	if resp.Token == "" {
		t.Fatal("未下发 __Host-anon Cookie")
	}

	// Cookie 属性校验。
	c := resp.SetCookie
	if c.Name != identity.CookieName {
		t.Errorf("Cookie 名 = %q, 期望 %q", c.Name, identity.CookieName)
	}
	if !c.HttpOnly {
		t.Error("Cookie 应为 HttpOnly")
	}
	if !c.Secure {
		t.Error("Cookie 应为 Secure")
	}
	if c.Path != "/" {
		t.Errorf("Cookie Path = %q, 期望 /", c.Path)
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie SameSite = %v, 期望 Lax", c.SameSite)
	}
	if c.MaxAge != identity.CookieMaxAge {
		t.Errorf("Cookie MaxAge = %d, 期望 %d", c.MaxAge, identity.CookieMaxAge)
	}
	if c.Domain != "" {
		t.Errorf("__Host- Cookie 不应有 Domain, 实际 %q", c.Domain)
	}

	// 响应字段：只应有 nickname/avatarSeed/verified，不含身份 ID。
	keys := responseKeys(t, resp.Body)
	for _, k := range []string{"id", "identityId", "identity_id"} {
		if _, ok := keys[k]; ok {
			t.Errorf("响应不应包含字段 %q", k)
		}
	}
	for _, k := range []string{"nickname", "avatarSeed", "verified"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("响应缺少字段 %q", k)
		}
	}

	// 数据库只存哈希，且哈希与令牌匹配。
	var storedHash []byte
	var nickname string
	if err := pool.QueryRow(context.Background(),
		"SELECT token_hash, nickname FROM identities").Scan(&storedHash, &nickname); err != nil {
		t.Fatalf("查询身份失败: %v", err)
	}
	if !bytes.Equal(storedHash, identity.HashToken(resp.Token)) {
		t.Error("库中 token_hash 不等于 sha256(令牌)")
	}
	if bytes.Equal(storedHash, []byte(resp.Token)) {
		t.Error("库中不应存明文令牌")
	}
	if nickname == "" {
		t.Error("昵称为空")
	}
}

// TestMeReturnsFieldsAndNoID 验证 /me 返回昵称、头像种子、verified 与 verificationMode。
func TestMeReturnsFieldsAndNoID(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{VerificationMode: "off"})

	created := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if created.Status != http.StatusOK {
		t.Fatalf("创建身份失败: %d %s", created.Status, created.Body)
	}

	me := testutil.Do(t, srv, http.MethodGet, "/api/identity/me", nil, created.Token, "")
	if me.Status != http.StatusOK {
		t.Fatalf("/me 状态码 = %d, 期望 200, body=%s", me.Status, me.Body)
	}
	var out struct {
		Nickname         string `json:"nickname"`
		AvatarSeed       string `json:"avatarSeed"`
		Verified         bool   `json:"verified"`
		VerificationMode string `json:"verificationMode"`
	}
	me.JSON(t, &out)
	if out.Nickname == "" || out.AvatarSeed == "" {
		t.Error("昵称或头像种子为空")
	}
	if out.Verified {
		t.Error("新身份不应已验证")
	}
	if out.VerificationMode != "off" {
		t.Errorf("verificationMode = %q, 期望 off", out.VerificationMode)
	}
	if _, ok := responseKeys(t, me.Body)["id"]; ok {
		t.Error("/me 响应不应包含身份 ID")
	}
}

// TestMeWithoutCookie 验证无 Cookie 时返回 401 unauthorized。
func TestMeWithoutCookie(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})

	resp := testutil.Do(t, srv, http.MethodGet, "/api/identity/me", nil, "", "")
	if resp.Status != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d, 期望 401", resp.Status)
	}
	if code := responseKeys(t, resp.Body); string(code["error"]) == "" {
		t.Error("缺少 error 字段")
	}
}

// TestResetInvalidatesOldToken 验证换身份后旧令牌失效、新令牌可用。
func TestResetInvalidatesOldToken(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})

	created := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if created.Status != http.StatusOK {
		t.Fatalf("创建身份失败: %d", created.Status)
	}
	oldToken := created.Token

	reset := testutil.Do(t, srv, http.MethodPost, "/api/identity/reset", nil, oldToken, "")
	if reset.Status != http.StatusOK {
		t.Fatalf("reset 状态码 = %d, 期望 200, body=%s", reset.Status, reset.Body)
	}
	if reset.Token == "" || reset.Token == oldToken {
		t.Fatal("reset 应下发新的令牌")
	}

	// 旧令牌失效。
	if got := testutil.Do(t, srv, http.MethodGet, "/api/identity/me", nil, oldToken, ""); got.Status != http.StatusUnauthorized {
		t.Errorf("旧令牌应 401, 实际 %d", got.Status)
	}
	// 新令牌可用。
	if got := testutil.Do(t, srv, http.MethodGet, "/api/identity/me", nil, reset.Token, ""); got.Status != http.StatusOK {
		t.Errorf("新令牌应 200, 实际 %d", got.Status)
	}

	// 旧身份 revoked_at 已置位。
	var revokedCount int
	if err := pool.QueryRow(context.Background(),
		"SELECT count(*) FROM identities WHERE revoked_at IS NOT NULL").Scan(&revokedCount); err != nil {
		t.Fatal(err)
	}
	if revokedCount != 1 {
		t.Errorf("已吊销身份数 = %d, 期望 1", revokedCount)
	}
}

// TestCreateRateLimited 验证同一 IP 每小时超过 10 次创建返回 rate_limited。
func TestCreateRateLimited(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	// 使用真实固定窗口：每 IP 每小时 10 次。
	srv := testutil.MustServer(t, pool, testutil.Config{
		IdentityLimiter: moderation.NewFixedWindow(10, time.Hour),
	})

	for i := 0; i < 10; i++ {
		resp := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
		if resp.Status != http.StatusOK {
			t.Fatalf("第 %d 次创建应成功, 实际 %d (%s)", i+1, resp.Status, resp.Body)
		}
	}
	over := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if over.Status != http.StatusTooManyRequests {
		t.Fatalf("第 11 次应 429, 实际 %d", over.Status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	over.JSON(t, &body)
	if body.Error.Code != "rate_limited" {
		t.Errorf("错误码 = %q, 期望 rate_limited", body.Error.Code)
	}
}

// TestOriginRequired 验证修改类请求缺少/错误 Origin 时返回 403 forbidden_origin。
func TestOriginRequired(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})

	resp := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "http://evil.example")
	if resp.Status != http.StatusForbidden {
		t.Fatalf("错误 Origin 应 403, 实际 %d", resp.Status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	resp.JSON(t, &body)
	if body.Error.Code != "forbidden_origin" {
		t.Errorf("错误码 = %q, 期望 forbidden_origin", body.Error.Code)
	}
}

// TestResetUsesSharedCreateLimit 验证换身份与创建身份共用同一限流 key（R1 修复）：
// create + 9 次 reset 共 10 次后，再 reset 应 429，且被拒的 reset 不吊销旧身份。
func TestResetUsesSharedCreateLimit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{
		IdentityLimiter: moderation.NewFixedWindow(10, time.Hour),
	})

	created := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if created.Status != http.StatusOK {
		t.Fatalf("创建身份失败: %d", created.Status)
	}
	token := created.Token

	// 9 次 reset 成功（累计 10 次操作）。
	for i := 0; i < 9; i++ {
		r := testutil.Do(t, srv, http.MethodPost, "/api/identity/reset", nil, token, "")
		if r.Status != http.StatusOK {
			t.Fatalf("第 %d 次 reset 应 200, 实际 %d (%s)", i+1, r.Status, r.Body)
		}
		token = r.Token
	}

	// 第 10 次 reset（总第 11 次操作）应被限流。
	over := testutil.Do(t, srv, http.MethodPost, "/api/identity/reset", nil, token, "")
	if over.Status != http.StatusTooManyRequests {
		t.Fatalf("超限 reset 应 429, 实际 %d", over.Status)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	over.JSON(t, &body)
	if body.Error.Code != "rate_limited" {
		t.Errorf("错误码 = %q, 期望 rate_limited", body.Error.Code)
	}

	// 被拒的 reset 不应吊销当前身份（事务/限流前置保证旧身份仍有效）。
	if me := testutil.Do(t, srv, http.MethodGet, "/api/identity/me", nil, token, ""); me.Status != http.StatusOK {
		t.Errorf("被限流后旧身份应仍有效, /me 实际 %d", me.Status)
	}
}
