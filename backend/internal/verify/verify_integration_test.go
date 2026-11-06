//go:build integration

package verify_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/anonchat/anonchat/backend/internal/verify"
)

const (
	testPhone     = "13800001234"
	testPhoneE164 = "+8613800001234"
)

// testKeys 返回 32 字节的加密与 HMAC 密钥。
func testKeys() (enc, hmac []byte) {
	enc = make([]byte, 32)
	hmac = make([]byte, 32)
	for i := range enc {
		enc[i] = byte(i + 1)
		hmac[i] = byte(i + 101)
	}
	return enc, hmac
}

// handle 汇总一次集成测试所需的句柄。
type handle struct {
	srv  *httptest.Server
	pool *pgxpool.Pool
	sms  *testutil.CapturingSMS
	enc  []byte
	hmac []byte
}

// newServer 构建带验证配置的服务与记录型短信。extra 用于覆盖默认配置。
func newServer(t *testing.T, extra func(*testutil.Config)) *handle {
	t.Helper()
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sms := testutil.NewCapturingSMS()
	enc, hmac := testKeys()
	cfg := testutil.Config{
		VerificationMode: "required",
		EncKey:           enc,
		HmacKey:          hmac,
		SMS:              sms,
	}
	if extra != nil {
		extra(&cfg)
	}
	srv := testutil.MustServer(t, pool, cfg)
	return &handle{srv: srv, pool: pool, sms: sms, enc: enc, hmac: hmac}
}

// createToken 创建一个身份并返回令牌。
func (h *handle) createToken(t *testing.T) string {
	t.Helper()
	resp := testutil.Do(t, h.srv, http.MethodPost, "/api/identity", nil, "", "")
	if resp.Status != http.StatusOK {
		t.Fatalf("创建身份失败: %d %s", resp.Status, resp.Body)
	}
	return resp.Token
}

// sendCode 发送验证码。
func (h *handle) sendCode(t *testing.T, token, phone string) testutil.Response {
	t.Helper()
	return testutil.Do(t, h.srv, http.MethodPost, "/api/verify/sms/send",
		map[string]string{"phone": phone}, token, "")
}

// confirmCode 确认验证码。
func (h *handle) confirmCode(t *testing.T, token, phone, code string) testutil.Response {
	t.Helper()
	return testutil.Do(t, h.srv, http.MethodPost, "/api/verify/sms/confirm",
		map[string]string{"phone": phone, "code": code}, token, "")
}

func TestSendAndConfirmFlow(t *testing.T) {
	h := newServer(t, nil)
	token := h.createToken(t)

	if r := h.sendCode(t, token, testPhone); r.Status != http.StatusNoContent {
		t.Fatalf("发送验证码状态码 = %d, 期望 204, body=%s", r.Status, r.Body)
	}
	code, ok := h.sms.Last(testPhoneE164)
	if !ok {
		t.Fatal("未记录到验证码")
	}

	if r := h.confirmCode(t, token, testPhone, code); r.Status != http.StatusOK {
		t.Fatalf("确认验证码状态码 = %d, 期望 200, body=%s", r.Status, r.Body)
	}

	// /me 应显示已验证，verificationMode=required。
	me := testutil.Do(t, h.srv, http.MethodGet, "/api/identity/me", nil, token, "")
	var meOut struct {
		Verified         bool   `json:"verified"`
		VerificationMode string `json:"verificationMode"`
	}
	me.JSON(t, &meOut)
	if !meOut.Verified {
		t.Error("确认后 /me 应显示 verified=true")
	}
	if meOut.VerificationMode != "required" {
		t.Errorf("verificationMode = %q, 期望 required", meOut.VerificationMode)
	}

	// 数据库：手机号以密文 + HMAC + 脱敏形式存储。
	var phoneEnc, phoneHmac []byte
	var masked string
	if err := h.pool.QueryRow(context.Background(),
		"SELECT phone_enc, phone_hmac, phone_masked FROM verified_accounts").Scan(&phoneEnc, &phoneHmac, &masked); err != nil {
		t.Fatalf("查询 verified_accounts 失败: %v", err)
	}
	if bytes.Contains(phoneEnc, []byte(testPhoneE164)) {
		t.Error("phone_enc 不应包含明文号码")
	}
	dec, err := verify.Decrypt(h.enc, phoneEnc)
	if err != nil {
		t.Fatalf("解密 phone_enc 失败: %v", err)
	}
	if string(dec) != testPhoneE164 {
		t.Errorf("解密结果 = %q, 期望 %q", dec, testPhoneE164)
	}
	if !bytes.Equal(phoneHmac, verify.HMACSHA256(h.hmac, []byte(testPhoneE164))) {
		t.Error("phone_hmac 与预期不符")
	}
	if masked != "+86 138****1234" {
		t.Errorf("phone_masked = %q, 期望 +86 138****1234", masked)
	}

	var bound int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM identities WHERE account_id IS NOT NULL").Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != 1 {
		t.Errorf("已绑定账号的身份数 = %d, 期望 1", bound)
	}
	var verifyLogs int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM access_logs WHERE event = 'verify'").Scan(&verifyLogs); err != nil {
		t.Fatal(err)
	}
	if verifyLogs != 1 {
		t.Errorf("verify 审计日志数 = %d, 期望 1", verifyLogs)
	}
}

func TestWrongCodeRejectedThenMaxAttempts(t *testing.T) {
	h := newServer(t, nil)
	token := h.createToken(t)
	_ = h.sendCode(t, token, testPhone)
	code, _ := h.sms.Last(testPhoneE164)

	for i := 0; i < 5; i++ {
		if r := h.confirmCode(t, token, testPhone, wrongCode(code)); r.Status != http.StatusBadRequest {
			t.Fatalf("第 %d 次错误码应 400, 实际 %d", i+1, r.Status)
		}
	}
	if r := h.confirmCode(t, token, testPhone, code); r.Status != http.StatusBadRequest {
		t.Fatalf("尝试超过 5 次后应拒绝, 实际 %d", r.Status)
	}
}

func TestCodeExpires(t *testing.T) {
	h := newServer(t, nil)
	token := h.createToken(t)
	_ = h.sendCode(t, token, testPhone)
	code, _ := h.sms.Last(testPhoneE164)

	// 把最新验证码置为已过期（避免真实等待 5 分钟）。
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE sms_codes SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatalf("设置过期失败: %v", err)
	}

	if r := h.confirmCode(t, token, testPhone, code); r.Status != http.StatusBadRequest {
		t.Fatalf("过期验证码应 400, 实际 %d", r.Status)
	}
}

func TestResendCooldown(t *testing.T) {
	h := newServer(t, nil) // 默认冷却限流：每手机号 60 秒 1 次
	token := h.createToken(t)

	if r := h.sendCode(t, token, testPhone); r.Status != http.StatusNoContent {
		t.Fatalf("首次发送应 204, 实际 %d (%s)", r.Status, r.Body)
	}
	if r := h.sendCode(t, token, testPhone); r.Status != http.StatusTooManyRequests {
		t.Fatalf("60 秒内重发应 429, 实际 %d", r.Status)
	}
}

func TestRebindReusesAccount(t *testing.T) {
	all := moderation.AllowAll()
	h := newServer(t, func(c *testutil.Config) {
		c.VerifyCooldown = all
		c.VerifyPhoneDay = all
		c.VerifyIPDay = all
	})

	tokenA := h.createToken(t)
	_ = h.sendCode(t, tokenA, testPhone)
	codeA, _ := h.sms.Last(testPhoneE164)
	if r := h.confirmCode(t, tokenA, testPhone, codeA); r.Status != http.StatusOK {
		t.Fatalf("身份 A 确认失败: %d %s", r.Status, r.Body)
	}

	tokenB := h.createToken(t)
	_ = h.sendCode(t, tokenB, testPhone)
	codeB, _ := h.sms.Last(testPhoneE164)
	if r := h.confirmCode(t, tokenB, testPhone, codeB); r.Status != http.StatusOK {
		t.Fatalf("身份 B 确认失败: %d %s", r.Status, r.Body)
	}

	var accounts int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(*) FROM verified_accounts").Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if accounts != 1 {
		t.Errorf("verified_accounts 记录数 = %d, 期望 1（同一手机号应复用）", accounts)
	}
	var distinctAccounts int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT count(DISTINCT account_id) FROM identities WHERE account_id IS NOT NULL").Scan(&distinctAccounts); err != nil {
		t.Fatal(err)
	}
	if distinctAccounts != 1 {
		t.Errorf("不同账号数 = %d, 期望 1", distinctAccounts)
	}
}

func TestInvalidPhoneRejected(t *testing.T) {
	h := newServer(t, nil)
	token := h.createToken(t)
	if r := h.sendCode(t, token, "abc"); r.Status != http.StatusBadRequest {
		t.Fatalf("非法手机号应 400, 实际 %d", r.Status)
	}
}

// wrongCode 返回一个与原验证码不同的数字串。
func wrongCode(code string) string {
	if code[0] == '0' {
		return "9" + code[1:]
	}
	return "0" + code[1:]
}

// TestConcurrentWrongAttemptsBounded 验证并发错误验证码不会绕过 5 次上限（R4 修复）。
func TestConcurrentWrongAttemptsBounded(t *testing.T) {
	h := newServer(t, nil)
	token := h.createToken(t)
	_ = h.sendCode(t, token, testPhone)
	code, _ := h.sms.Last(testPhoneE164)
	wrong := wrongCode(code)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			payload := fmt.Sprintf(`{"phone":%q,"code":%q}`, testPhone, wrong)
			req, err := http.NewRequest(http.MethodPost, h.srv.URL+"/api/verify/sms/confirm", strings.NewReader(payload))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", testutil.DefaultOrigin)
			req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: token})
			resp, err := h.srv.Client().Do(req)
			if err != nil {
				return
			}
			_ = resp.Body.Close()
		}()
	}
	wg.Wait()

	var attempts int
	if err := h.pool.QueryRow(context.Background(),
		"SELECT attempts FROM sms_codes ORDER BY id DESC LIMIT 1").Scan(&attempts); err != nil {
		t.Fatalf("查询 attempts 失败: %v", err)
	}
	if attempts > 5 {
		t.Fatalf("并发后尝试次数 = %d, 不应超过 5", attempts)
	}

	// 并发耗尽尝试机会后，正确验证码也应被拒绝。
	if r := h.confirmCode(t, token, testPhone, code); r.Status != http.StatusBadRequest {
		t.Fatalf("尝试用尽后正确验证码应 400, 实际 %d", r.Status)
	}
}

// TestVerifyRoutesDisabledWhenOff 验证 VERIFICATION_MODE=off 时不注册 /api/verify/* 路由（R3 修复）。
func TestVerifyRoutesDisabledWhenOff(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{VerificationMode: "off"})

	created := testutil.Do(t, srv, http.MethodPost, "/api/identity", nil, "", "")
	if created.Status != http.StatusOK {
		t.Fatalf("创建身份失败: %d", created.Status)
	}
	for _, path := range []string{"/api/verify/sms/send", "/api/verify/sms/confirm"} {
		r := testutil.Do(t, srv, http.MethodPost, path,
			map[string]string{"phone": testPhone, "code": "123456"}, created.Token, "")
		if r.Status != http.StatusNotFound {
			t.Errorf("%s 在 off 模式应 404, 实际 %d", path, r.Status)
		}
	}
}
