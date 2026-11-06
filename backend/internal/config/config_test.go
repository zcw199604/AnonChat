package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
	"time"
)

// setEnv 是一个测试辅助函数，用于在测试期间设置环境变量并在结束后恢复。
func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

// unsetEnv 在测试期间取消设置环境变量，并在测试结束后恢复原值。
// 用于验证"必填项缺失"的场景，不依赖调用方的环境是否干净。
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	os.Unsetenv(key)
	t.Cleanup(func() {
		if had {
			os.Setenv(key, old)
		}
	})
}

// validEnv 返回一组能通过校验的最小必填环境变量。
func validEnv() map[string]string {
	return map[string]string{
		"APP_ENV":      "development",
		"DATABASE_URL": "postgres://anonchat@127.0.0.1:55432/anonchat_dev?sslmode=disable",
	}
}

func TestLoadAppliesDefaults(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}

	if cfg.Env != "development" {
		t.Errorf("Env = %q, 期望 development", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q, 期望 :8080", cfg.HTTPAddr)
	}
	if cfg.DatabaseURL == "" {
		t.Error("DatabaseURL 不应为空")
	}
	if cfg.MessageRetention != 168*time.Hour {
		t.Errorf("MessageRetention = %v, 期望 168h", cfg.MessageRetention)
	}
	if cfg.AuditRetention != 4320*time.Hour {
		t.Errorf("AuditRetention = %v, 期望 4320h", cfg.AuditRetention)
	}
	if cfg.BottleTTL != 168*time.Hour {
		t.Errorf("BottleTTL = %v, 期望 168h", cfg.BottleTTL)
	}
	if cfg.VerificationMode != "off" {
		t.Errorf("VerificationMode = %q, 期望 off", cfg.VerificationMode)
	}
	if cfg.SMSProvider != "console" {
		t.Errorf("SMSProvider = %q, 期望 console", cfg.SMSProvider)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{
			name:    "缺少 DATABASE_URL",
			env:     map[string]string{"APP_ENV": "development"},
			wantSub: "DATABASE_URL",
		},
		{
			name:    "DATABASE_URL 为空字符串",
			env:     map[string]string{"APP_ENV": "development", "DATABASE_URL": "  "},
			wantSub: "DATABASE_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 先确保环境里没有残留的 DATABASE_URL，避免被调用方环境干扰。
			unsetEnv(t, "DATABASE_URL")
			setEnv(t, tt.env)
			_, err := Load()
			if err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("错误信息 %q 未包含 %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestLoadInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{
			name:    "非法 APP_ENV",
			env:     map[string]string{"APP_ENV": "staging", "DATABASE_URL": "postgres://x"},
			wantSub: "APP_ENV",
		},
		{
			name:    "非法 VERIFICATION_MODE",
			env:     map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://x", "VERIFICATION_MODE": "maybe"},
			wantSub: "VERIFICATION_MODE",
		},
		{
			name:    "非法 MESSAGE_RETENTION",
			env:     map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://x", "MESSAGE_RETENTION": "not-a-duration"},
			wantSub: "MESSAGE_RETENTION",
		},
		{
			name:    "非法 HTTP_ADDR 端口",
			env:     map[string]string{"APP_ENV": "development", "DATABASE_URL": "postgres://x", "HTTP_ADDR": ":notaport"},
			wantSub: "HTTP_ADDR",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, tt.env)
			_, err := Load()
			if err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("错误信息 %q 未包含 %q", err.Error(), tt.wantSub)
			}
		})
	}
}

func TestLoadProductionForbidsConsoleSMSWhenVerificationEnabled(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":           "production",
		"DATABASE_URL":      "postgres://anonchat@db/anonchat",
		"SMS_PROVIDER":      "console",
		"VERIFICATION_MODE": "required",
		"ALLOWED_ORIGINS":   "https://anonchat.example",
		"WORDLIST_PATH":     "/tmp/words.txt",
		"PHONE_ENC_KEY":     b64(32),
		"PHONE_HMAC_KEY":    b64(32),
	})

	_, err := Load()
	if err == nil {
		t.Fatal("生产环境 VERIFICATION_MODE≠off 时使用 console 短信应报错，实际为 nil")
	}
	if !strings.Contains(err.Error(), "SMS_PROVIDER") {
		t.Errorf("错误信息 %q 未包含 SMS_PROVIDER", err.Error())
	}
}

// TestLoadProductionOffAllowsConsoleSMS 验证 VERIFICATION_MODE=off 时生产环境允许默认 console（S1）。
func TestLoadProductionOffAllowsConsoleSMS(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":           "production",
		"DATABASE_URL":      "postgres://anonchat@db/anonchat",
		"VERIFICATION_MODE": "off",
		"ALLOWED_ORIGINS":   "https://anonchat.example",
		"WORDLIST_PATH":     "/tmp/words.txt",
	})
	unsetEnv(t, "SMS_PROVIDER")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("VERIFICATION_MODE=off 的生产环境应通过（默认 console）: %v", err)
	}
	if cfg.SMSProvider != "console" {
		t.Errorf("SMSProvider = %q, 期望 console", cfg.SMSProvider)
	}
}

// TestLoadProductionRequiredDefaultConsoleFails 验证 required + 未设置 SMS_PROVIDER（默认 console）应报错。
func TestLoadProductionRequiredDefaultConsoleFails(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":           "production",
		"DATABASE_URL":      "postgres://anonchat@db/anonchat",
		"VERIFICATION_MODE": "required",
		"ALLOWED_ORIGINS":   "https://anonchat.example",
		"WORDLIST_PATH":     "/tmp/words.txt",
		"PHONE_ENC_KEY":     b64(32),
		"PHONE_HMAC_KEY":    b64(32),
	})
	unsetEnv(t, "SMS_PROVIDER")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SMS_PROVIDER") {
		t.Fatalf("required 生产环境默认 console 应报错, 实际: %v", err)
	}
}

func TestLoadProductionRequiresAllowedOrigins(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":      "production",
		"DATABASE_URL": "postgres://anonchat@db/anonchat",
		"SMS_PROVIDER": "aliyun",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("生产环境缺少 ALLOWED_ORIGINS 时期望返回错误，实际为 nil")
	}
	if !strings.Contains(err.Error(), "ALLOWED_ORIGINS") {
		t.Errorf("错误信息 %q 未包含 ALLOWED_ORIGINS", err.Error())
	}
}

func TestIsProduction(t *testing.T) {
	setEnv(t, map[string]string{"APP_ENV": "production", "DATABASE_URL": "postgres://x", "SMS_PROVIDER": "aliyun", "ALLOWED_ORIGINS": "https://a.example", "WORDLIST_PATH": "/tmp/words.txt"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	if !cfg.IsProduction() {
		t.Error("IsProduction() = false, 期望 true")
	}
}

// b64 生成 n 字节的 base64 字符串，用于测试密钥配置。
func b64(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func TestLoadDevelopmentDefaultsAllowedOrigins(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":      "development",
		"DATABASE_URL": "postgres://x",
	})
	unsetEnv(t, "ALLOWED_ORIGINS")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	want := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	if len(cfg.AllowedOrigins) != len(want) {
		t.Fatalf("AllowedOrigins = %v, 期望 %v", cfg.AllowedOrigins, want)
	}
	for i := range want {
		if cfg.AllowedOrigins[i] != want[i] {
			t.Errorf("AllowedOrigins[%d] = %q, 期望 %q", i, cfg.AllowedOrigins[i], want[i])
		}
	}
}

func TestLoadVerificationModeRequiresPhoneKeys(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":           "test",
		"DATABASE_URL":      "postgres://x",
		"VERIFICATION_MODE": "required",
	})
	unsetEnv(t, "PHONE_ENC_KEY")
	unsetEnv(t, "PHONE_HMAC_KEY")

	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PHONE_ENC_KEY") {
		t.Fatalf("缺少 PHONE_ENC_KEY 时应报错，实际: %v", err)
	}

	// 长度不对也要报错。
	setEnv(t, map[string]string{"PHONE_ENC_KEY": b64(16), "PHONE_HMAC_KEY": b64(32)})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "PHONE_ENC_KEY") {
		t.Fatalf("PHONE_ENC_KEY 非 32 字节时应报错，实际: %v", err)
	}

	// 正确的 32 字节密钥应通过，并能正确解码。
	setEnv(t, map[string]string{"PHONE_ENC_KEY": b64(32), "PHONE_HMAC_KEY": b64(32)})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("合法密钥下 Load() 返回错误: %v", err)
	}
	if len(cfg.PhoneEncKey) != 32 || len(cfg.PhoneHmacKey) != 32 {
		t.Errorf("密钥长度错误: enc=%d hmac=%d", len(cfg.PhoneEncKey), len(cfg.PhoneHmacKey))
	}
}

func TestLoadOffModeAllowsMissingPhoneKeys(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":           "test",
		"DATABASE_URL":      "postgres://x",
		"VERIFICATION_MODE": "off",
	})
	unsetEnv(t, "PHONE_ENC_KEY")
	unsetEnv(t, "PHONE_HMAC_KEY")

	if _, err := Load(); err != nil {
		t.Fatalf("off 模式允许缺少密钥，实际报错: %v", err)
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":         "test",
		"DATABASE_URL":    "postgres://x",
		"TRUSTED_PROXIES": "127.0.0.1/32, 10.0.0.0/8",
	})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() 返回错误: %v", err)
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[1] != "10.0.0.0/8" {
		t.Errorf("TrustedProxies = %v", cfg.TrustedProxies)
	}

	// 非法 CIDR 报错。
	setEnv(t, map[string]string{"TRUSTED_PROXIES": "not-a-cidr"})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("非法 CIDR 应报错，实际: %v", err)
	}
}

func TestLoadProductionRequiresWordlistPath(t *testing.T) {
	setEnv(t, map[string]string{
		"APP_ENV":         "production",
		"DATABASE_URL":    "postgres://x",
		"SMS_PROVIDER":    "aliyun",
		"ALLOWED_ORIGINS": "https://a.example",
	})
	unsetEnv(t, "WORDLIST_PATH")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "WORDLIST_PATH") {
		t.Fatalf("生产环境缺少 WORDLIST_PATH 应报错, 实际: %v", err)
	}
}

func TestLoadRateLimitMultiplier(t *testing.T) {
	base := map[string]string{"APP_ENV": "test", "DATABASE_URL": "postgres://x"}
	setEnv(t, base)

	// 默认 1
	unsetEnv(t, "RATE_LIMIT_MULTIPLIER")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("默认应通过: %v", err)
	}
	if cfg.RateLimitMultiplier != 1 {
		t.Errorf("默认 multiplier = %d, 期望 1", cfg.RateLimitMultiplier)
	}

	// 非生产可放宽
	setEnv(t, map[string]string{"RATE_LIMIT_MULTIPLIER": "100"})
	if cfg, err = Load(); err != nil || cfg.RateLimitMultiplier != 100 {
		t.Fatalf("test 环境 multiplier=100 应通过, err=%v", err)
	}

	// 非法值
	setEnv(t, map[string]string{"RATE_LIMIT_MULTIPLIER": "0"})
	if _, err := Load(); err == nil {
		t.Fatal("multiplier=0 应报错")
	}
	setEnv(t, map[string]string{"RATE_LIMIT_MULTIPLIER": "abc"})
	if _, err := Load(); err == nil {
		t.Fatal("multiplier 非整数应报错")
	}

	// 生产必须为 1
	setEnv(t, map[string]string{
		"APP_ENV": "production", "DATABASE_URL": "postgres://x", "SMS_PROVIDER": "aliyun",
		"ALLOWED_ORIGINS": "https://a.example", "WORDLIST_PATH": "/tmp/w.txt", "RATE_LIMIT_MULTIPLIER": "100",
	})
	if _, err := Load(); err == nil {
		t.Fatal("生产环境 multiplier!=1 应报错")
	}
}

// botEnvDefaults 清空 AI 机器人与媒体相关的环境变量，保证默认值用例不受调用方环境影响。
func botEnvDefaults(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BOT_ENABLED", "LLM_BASE_URL", "LLM_API_KEY", "LLM_MODEL", "LLM_TIMEOUT",
		"LLM_SEND_CACHE_KEY", "BOT_MATCH_FALLBACK_AFTER", "BOT_BOTTLE_MIN_AGE",
		"MEDIA_ROOT", "MEDIA_MAX_UPLOAD_MB", "BOT_TIMEZONE",
	} {
		unsetEnv(t, k)
	}
}

// TestLoadBotDefaults 验证 bot 与媒体配置的默认值。
func TestLoadBotDefaults(t *testing.T) {
	setEnv(t, validEnv())
	botEnvDefaults(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("默认值应通过校验: %v", err)
	}
	if cfg.BotEnabled {
		t.Error("BotEnabled 默认应为 false")
	}
	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Errorf("LLMBaseURL = %q, 期望 https://api.openai.com/v1", cfg.LLMBaseURL)
	}
	if cfg.LLMTimeout != 60*time.Second {
		t.Errorf("LLMTimeout = %v, 期望 60s", cfg.LLMTimeout)
	}
	if !cfg.LLMSendCacheKey {
		t.Error("LLMSendCacheKey 默认应为 true")
	}
	if cfg.BotMatchFallbackAfter != 10*time.Second {
		t.Errorf("BotMatchFallbackAfter = %v, 期望 10s", cfg.BotMatchFallbackAfter)
	}
	if cfg.BotBottleMinAge != 30*time.Minute {
		t.Errorf("BotBottleMinAge = %v, 期望 30m", cfg.BotBottleMinAge)
	}
	if cfg.MediaRoot != "./data/media" {
		t.Errorf("MediaRoot = %q, 期望 ./data/media", cfg.MediaRoot)
	}
	if cfg.MediaMaxUploadMB != 100 {
		t.Errorf("MediaMaxUploadMB = %d, 期望 100", cfg.MediaMaxUploadMB)
	}
	if cfg.BotTimezone != "Asia/Shanghai" {
		t.Errorf("BotTimezone = %q, 期望 Asia/Shanghai", cfg.BotTimezone)
	}
}

// TestLoadBotEnabledDoesNotRequireLLM 验证 BOT_ENABLED=true 时不再强制要求 key 或 model。
// how.md §15 起，模型接入配置可以在管理后台补填（bot_settings），因此启动校验只要求
// LLM_BASE_URL 非空；key 与 model 留空是允许的。
func TestLoadBotEnabledDoesNotRequireLLM(t *testing.T) {
	setEnv(t, validEnv())
	botEnvDefaults(t)
	setEnv(t, map[string]string{"BOT_ENABLED": "true"})
	unsetEnv(t, "LLM_API_KEY")
	unsetEnv(t, "LLM_MODEL")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("BOT_ENABLED=true 且未配置 key/model 时应允许启动（后台可补填）: %v", err)
	}
	if !cfg.BotEnabled || cfg.LLMAPIKey != "" || cfg.LLMModel != "" {
		t.Errorf("bot 配置解析错误: enabled=%v key=%q model=%q", cfg.BotEnabled, cfg.LLMAPIKey, cfg.LLMModel)
	}

	// 给了 key/model 时应正确解析（用于首次初始化与 key 回退）。
	setEnv(t, map[string]string{"LLM_API_KEY": "sk-test", "LLM_MODEL": "deepseek-chat"})
	cfg, err = Load()
	if err != nil {
		t.Fatalf("配置齐备时不应报错: %v", err)
	}
	if cfg.LLMAPIKey != "sk-test" || cfg.LLMModel != "deepseek-chat" {
		t.Errorf("LLM 配置解析错误: key=%q model=%q", cfg.LLMAPIKey, cfg.LLMModel)
	}

	// LLM_BASE_URL 有默认值，留空即用默认地址。
	unsetEnv(t, "LLM_BASE_URL")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("LLM_BASE_URL 留空应使用默认值: %v", err)
	}
	if cfg.LLMBaseURL != "https://api.openai.com/v1" {
		t.Errorf("LLMBaseURL = %q", cfg.LLMBaseURL)
	}
}

// TestLoadSettingsEncKey 验证 SETTINGS_ENC_KEY 的长度校验。
func TestLoadSettingsEncKey(t *testing.T) {
	setEnv(t, validEnv())
	botEnvDefaults(t)

	// 未配置：允许（后台不能保存 API Key，生产只打 warn）。
	unsetEnv(t, "SETTINGS_ENC_KEY")
	if _, err := Load(); err != nil {
		t.Fatalf("未配置 SETTINGS_ENC_KEY 不应报错: %v", err)
	}
	// 32 字节：通过并解码。
	setEnv(t, map[string]string{"SETTINGS_ENC_KEY": b64(32)})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("32 字节 SETTINGS_ENC_KEY 应通过: %v", err)
	}
	if len(cfg.SettingsEncKey) != 32 {
		t.Errorf("SettingsEncKey 长度 = %d, 期望 32", len(cfg.SettingsEncKey))
	}
	// 长度不对：报错。
	setEnv(t, map[string]string{"SETTINGS_ENC_KEY": b64(16)})
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "SETTINGS_ENC_KEY") {
		t.Fatalf("SETTINGS_ENC_KEY 非 32 字节应报错, 实际: %v", err)
	}
}

// TestLoadBotInvalidValues 验证新增配置项的非法取值。
func TestLoadBotInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{name: "非法 BOT_ENABLED", env: map[string]string{"BOT_ENABLED": "yes please"}, wantSub: "BOT_ENABLED"},
		{name: "非法 LLM_SEND_CACHE_KEY", env: map[string]string{"LLM_SEND_CACHE_KEY": "maybe"}, wantSub: "LLM_SEND_CACHE_KEY"},
		{name: "非法 LLM_TIMEOUT", env: map[string]string{"LLM_TIMEOUT": "0"}, wantSub: "LLM_TIMEOUT"},
		{name: "非法 BOT_MATCH_FALLBACK_AFTER", env: map[string]string{"BOT_MATCH_FALLBACK_AFTER": "-1s"}, wantSub: "BOT_MATCH_FALLBACK_AFTER"},
		{name: "非法 BOT_BOTTLE_MIN_AGE", env: map[string]string{"BOT_BOTTLE_MIN_AGE": "-30m"}, wantSub: "BOT_BOTTLE_MIN_AGE"},
		{name: "非法 MEDIA_MAX_UPLOAD_MB", env: map[string]string{"MEDIA_MAX_UPLOAD_MB": "0"}, wantSub: "MEDIA_MAX_UPLOAD_MB"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, validEnv())
			botEnvDefaults(t)
			setEnv(t, tt.env)
			_, err := Load()
			if err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
			if !strings.Contains(err.Error(), tt.wantSub) {
				t.Errorf("错误信息 %q 未包含 %q", err.Error(), tt.wantSub)
			}
		})
	}

	// 瓶子的最小年龄允许为 0（测试里立刻让 bot 捞瓶子）。
	setEnv(t, validEnv())
	botEnvDefaults(t)
	setEnv(t, map[string]string{"BOT_BOTTLE_MIN_AGE": "0"})
	cfg, err := Load()
	if err != nil {
		t.Fatalf("BOT_BOTTLE_MIN_AGE=0 应通过: %v", err)
	}
	if cfg.BotBottleMinAge != 0 {
		t.Errorf("BotBottleMinAge = %v, 期望 0", cfg.BotBottleMinAge)
	}
}

// TestGetBool 直接验证 getBool 的解析行为。
func TestGetBool(t *testing.T) {
	tests := []struct {
		raw     string
		def     bool
		want    bool
		wantErr bool
	}{
		{raw: "", def: true, want: true},
		{raw: "", def: false, want: false},
		{raw: "true", def: false, want: true},
		{raw: "1", def: false, want: true},
		{raw: "false", def: true, want: false},
		{raw: "0", def: true, want: false},
		{raw: "not-bool", def: true, wantErr: true},
	}
	for _, tt := range tests {
		unsetEnv(t, "BOT_TEST_BOOL")
		if tt.raw != "" {
			setEnv(t, map[string]string{"BOT_TEST_BOOL": tt.raw})
		}
		got, err := getBool("BOT_TEST_BOOL", tt.def)
		if tt.wantErr {
			if err == nil {
				t.Errorf("raw=%q 期望报错，实际 err=nil", tt.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("raw=%q 不应报错: %v", tt.raw, err)
			continue
		}
		if got != tt.want {
			t.Errorf("raw=%q getBool = %v, 期望 %v", tt.raw, got, tt.want)
		}
	}
}
