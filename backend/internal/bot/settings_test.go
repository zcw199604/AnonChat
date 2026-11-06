package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/platform/secretbox"
)

// TestUpdateInputValidate 覆盖设置校验：URL、时区、取值范围。
func TestUpdateInputValidate(t *testing.T) {
	valid := UpdateInput{
		BaseURL:              "https://api.example.com/v1",
		TimeoutSeconds:       60,
		MatchFallbackSeconds: 10,
		BottleMinAgeSeconds:  1800,
		Timezone:             "Asia/Shanghai",
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("合法设置不应报错: %v", err)
	}
	// Base URL 允许留空（未配置视为不可用）。
	empty := valid
	empty.BaseURL = ""
	if err := empty.Validate(); err != nil {
		t.Errorf("Base URL 为空应允许保存（只是不可用）: %v", err)
	}

	cases := []struct {
		name string
		mod  func(in *UpdateInput)
		want string
	}{
		{"非法 URL 协议", func(in *UpdateInput) { in.BaseURL = "ftp://example.com" }, "Base URL"},
		{"URL 缺少 host", func(in *UpdateInput) { in.BaseURL = "http://" }, "Base URL"},
		{"URL 不是绝对地址", func(in *UpdateInput) { in.BaseURL = "api.example.com/v1" }, "Base URL"},
		{"超时过小", func(in *UpdateInput) { in.TimeoutSeconds = 4 }, "超时"},
		{"超时过大", func(in *UpdateInput) { in.TimeoutSeconds = 301 }, "超时"},
		{"兜底过小", func(in *UpdateInput) { in.MatchFallbackSeconds = 2 }, "匹配兜底"},
		{"兜底过大", func(in *UpdateInput) { in.MatchFallbackSeconds = 601 }, "匹配兜底"},
		{"瓶龄为负", func(in *UpdateInput) { in.BottleMinAgeSeconds = -1 }, "瓶龄"},
		{"瓶龄过大", func(in *UpdateInput) { in.BottleMinAgeSeconds = 604801 }, "瓶龄"},
		{"时区为空", func(in *UpdateInput) { in.Timezone = "  " }, "时区"},
		{"时区无法识别", func(in *UpdateInput) { in.Timezone = "Mars/Olympus" }, "时区"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := valid
			c.mod(&in)
			err := in.Validate()
			if err == nil {
				t.Fatalf("期望报错，实际为 nil")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("错误信息 %q 未包含 %q", err.Error(), c.want)
			}
		})
	}
}

// TestMaskAPIKey 验证 mask 只保留末 4 位。
func TestMaskAPIKey(t *testing.T) {
	if got := MaskAPIKey("sk-1234567890abcd"); got != "••••abcd" {
		t.Errorf("MaskAPIKey = %q", got)
	}
	if got := MaskAPIKey("abc"); got != "••••" {
		t.Errorf("短 key 应只显示掩码: %q", got)
	}
	if got := MaskAPIKey("   "); got != "" {
		t.Errorf("空 key 掩码应为空: %q", got)
	}
}

// TestResolveAPIKeyPriority 验证生效 key 的优先级：数据库 > 环境变量 > 无
// （且必须与当前 Base URL 绑定一致）。
func TestResolveAPIKeyPriority(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	const base = "https://api.example.com/v1"
	c := &SettingsCache{logger: quietLogger(), encKey: key, env: EnvSettings{
		APIKey:  "sk-env-9999",
		BaseURL: "https://env.example.com/v1",
	}}

	sealed, err := secretbox.Encrypt(key, []byte("sk-db-1234"))
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}

	// 1) 数据库 key 且绑定地址一致 → db 优先。
	got, source, hint, stale := c.resolveAPIKey(sealed, "", base, base)
	if got != "sk-db-1234" || source != APIKeySourceDB || hint != "••••1234" || stale {
		t.Fatalf("db 优先不符: key=%q source=%q hint=%q stale=%v", got, source, hint, stale)
	}
	// 2) 数据库 key 但绑定地址不同 → 不使用（stale），环境变量也不匹配 → none。
	got, source, hint, stale = c.resolveAPIKey(sealed, "", "https://other.example/v1", base)
	if got != "" || source != APIKeySourceNone || !stale {
		t.Fatalf("绑定地址不同的 db key 不应使用: key=%q source=%q stale=%v", got, source, stale)
	}
	if hint == "" {
		t.Error("stale 时应保留 hint 供提示")
	}
	// 3) 数据库为空、环境变量 key 的 base 与当前一致 → env。
	got, source, _, stale = c.resolveAPIKey(nil, "", "", "https://env.example.com/v1/")
	if got != "sk-env-9999" || source != APIKeySourceEnv || stale {
		t.Fatalf("环境变量 key 绑定一致时应可用: key=%q source=%q stale=%v", got, source, stale)
	}
	// 4) 数据库为空、环境变量 key 的 base 与当前不同 → 视为没有 key。
	got, source, _, stale = c.resolveAPIKey(nil, "", "", base)
	if got != "" || source != APIKeySourceNone || !stale {
		t.Fatalf("环境变量 key 绑定地址不同时应不可用: key=%q source=%q stale=%v", got, source, stale)
	}
	// 5) 都没有 → none 且不是 stale。
	empty := &SettingsCache{logger: quietLogger(), encKey: key, env: EnvSettings{BaseURL: base}}
	if got, source, _, stale := empty.resolveAPIKey(nil, "", "", base); got != "" || source != APIKeySourceNone || stale {
		t.Fatalf("无 key 时应返回 none: key=%q source=%q stale=%v", got, source, stale)
	}
	// 6) 未配置 SETTINGS_ENC_KEY 时无法解密 → stale，回退到匹配的环境变量 key（若有）。
	noKey := &SettingsCache{logger: quietLogger(), env: EnvSettings{APIKey: "sk-env-0001", BaseURL: base}}
	if got, source, _, _ := noKey.resolveAPIKey(sealed, "", base, base); got != "sk-env-0001" || source != APIKeySourceEnv {
		t.Fatalf("无法解密时应回退环境变量: key=%q source=%q", got, source)
	}
}

// TestNormalizeBaseURL 验证 Base URL 规范化（去空白与末尾斜杠）。
func TestNormalizeBaseURL(t *testing.T) {
	cases := map[string]string{
		"  https://a.example/v1/  ": "https://a.example/v1",
		"https://a.example/v1":      "https://a.example/v1",
		"https://a.example/v1///":   "https://a.example/v1",
		"":                          "",
	}
	for in, want := range cases {
		if got := NormalizeBaseURL(in); got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestEvaluateReady 验证"可用/不可用"的判定。
func TestEvaluateReady(t *testing.T) {
	full := Settings{Enabled: true, BaseURL: "https://x/v1", Model: "m", APIKeySet: true}
	if ok, reason := evaluateReady(full, false); !ok {
		t.Fatalf("完整配置应可用: %s", reason)
	}
	closed := full
	closed.Enabled = false
	if ok, reason := evaluateReady(closed, false); ok || !strings.Contains(reason, "关闭") {
		t.Fatalf("关闭时应不可用: ok=%v reason=%q", ok, reason)
	}
	noKey := full
	noKey.APIKeySet = false
	if ok, reason := evaluateReady(noKey, false); ok || !strings.Contains(reason, "API Key") {
		t.Fatalf("缺 key 应不可用: ok=%v reason=%q", ok, reason)
	}
	// key 绑定地址与当前 Base URL 不一致：给出"重新填写"的提示。
	stale := full
	stale.APIKeySet = false
	stale.APIKeyStale = true
	if ok, reason := evaluateReady(stale, false); ok || reason != notReadyURLChanged {
		t.Fatalf("key 绑定不一致时应不可用并提示重填: ok=%v reason=%q", ok, reason)
	}
	noModel := full
	noModel.Model = ""
	if ok, _ := evaluateReady(noModel, false); ok {
		t.Fatal("缺模型应不可用")
	}
	noBase := full
	noBase.BaseURL = ""
	if ok, _ := evaluateReady(noBase, false); ok {
		t.Fatal("缺 Base URL 应不可用")
	}
	// 注入了假模型客户端时只要求开关打开。
	if ok, reason := evaluateReady(noKey, true); !ok {
		t.Fatalf("注入假 LLM 时不应要求 key: %s", reason)
	}
}

// TestSettingsEncryptRoundTrip 验证密文可以解回原文，且密文不含明文。
func TestSettingsEncryptRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	sealed, err := secretbox.Encrypt(key, []byte("sk-roundtrip-abcd"))
	if err != nil {
		t.Fatalf("加密失败: %v", err)
	}
	if strings.Contains(string(sealed), "sk-roundtrip") {
		t.Fatal("密文中不应包含明文")
	}
	plain, err := secretbox.Decrypt(key, sealed)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if string(plain) != "sk-roundtrip-abcd" {
		t.Fatalf("解密结果 = %q", plain)
	}
}

// TestSanitizeTestError 验证测试连接的错误文案会被截断且不含 key。
func TestSanitizeTestError(t *testing.T) {
	msg := sanitizeTestError("调用失败: 401 无效密钥 sk-secret-1234", "sk-secret-1234")
	if strings.Contains(msg, "sk-secret-1234") {
		t.Fatalf("错误文案不应包含 key: %q", msg)
	}
	long := strings.Repeat("错", testConnectionErrorMax+50)
	if got := sanitizeTestError(long, ""); len([]rune(got)) > testConnectionErrorMax+1 {
		t.Errorf("错误文案未截断: %d", len([]rune(got)))
	}
	if got := sanitizeTestError("   ", ""); got != "调用失败" {
		t.Errorf("空错误应回退为通用文案: %q", got)
	}
}

// TestClampSettings 验证首次初始化时对取值区间的收敛。
func TestClampSettings(t *testing.T) {
	if got := clampInt32(1, minTimeoutSeconds, maxTimeoutSeconds); got != minTimeoutSeconds {
		t.Errorf("低于下限应被抬到下限: %d", got)
	}
	if got := clampInt32(9999, minFallbackSecs, maxFallbackSecs); got != maxFallbackSecs {
		t.Errorf("高于上限应被压到上限: %d", got)
	}
	if got := clampDuration(-time.Minute, 0, maxBottleAgeSecs); got != 0 {
		t.Errorf("负数时长应变 0: %v", got)
	}
}
