// Package config 负责从环境变量加载并校验应用配置。
// 校验失败时返回错误，由调用方决定如何处理（通常是记录日志后退出）。
package config

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config 保存应用运行所需的全部配置。
type Config struct {
	// Env 是运行环境：development | test | production。
	Env string
	// HTTPAddr 是 HTTP 服务监听地址，例如 :8080。
	HTTPAddr string
	// DatabaseURL 是 PostgreSQL 连接字符串（必填）。
	DatabaseURL string
	// AllowedOrigins 是允许的 Origin 白名单，用于 WebSocket 握手和修改类请求。
	AllowedOrigins []string
	// TrustedProxies 是可信反向代理的 CIDR 列表；仅当 RemoteAddr 属于其中之一时才采信 X-Forwarded-For。
	TrustedProxies []string

	// MessageRetention 是消息保留期，默认 7 天。
	MessageRetention time.Duration
	// AuditRetention 是审计日志和举报快照的保留期，默认 180 天。
	AuditRetention time.Duration
	// BottleTTL 是漂流瓶和私密链接的保留期，默认 7 天。
	BottleTTL time.Duration
	// RetentionInterval 是保留期清理任务的执行间隔，默认 10 分钟。
	RetentionInterval time.Duration

	// MatchRelaxAfter 是排队多久后对“最近聊过”限制放宽，默认 15 秒。
	MatchRelaxAfter time.Duration
	// MatchGrace 是匹配会话中一方断线后的宽限期，默认 30 秒。
	MatchGrace time.Duration
	// StoryMinDuration 是故事书有效场次的最短时长，默认 5 分钟；
	// 只在 APP_ENV=test 时允许小于 5 分钟（how.md 3.5）。
	StoryMinDuration time.Duration

	// VerificationMode 控制实名验证开关：off | optional | required。
	VerificationMode string
	// SMSProvider 是短信服务商实现：console | <vendor>。
	SMSProvider string

	// PhoneEncKey 是手机号 AES-256-GCM 加密密钥（32 字节）。
	PhoneEncKey []byte
	// PhoneHmacKey 是手机号 HMAC-SHA256 去重密钥（32 字节）。
	PhoneHmacKey []byte

	// WordlistPath 是敏感词库文件路径（可选；生产环境必填）。
	WordlistPath string

	// AdminIPAllowlist 是允许访问管理后台的 CIDR 列表（可选；为空表示不限制）。
	AdminIPAllowlist []string

	// RateLimitMultiplier 把所有限流上限乘以该系数（默认 1；E2E 用 100 放宽）。
	RateLimitMultiplier int
}

// 允许的枚举值。
var (
	allowedEnvs             = []string{"development", "test", "production"}
	allowedVerificationMode = []string{"off", "optional", "required"}
)

// Load 从环境变量读取配置并校验，返回不可变配置或明确的错误。
func Load() (*Config, error) {
	cfg := &Config{
		Env:              getEnv("APP_ENV", "development"),
		HTTPAddr:         getEnv("HTTP_ADDR", ":8080"),
		DatabaseURL:      strings.TrimSpace(os.Getenv("DATABASE_URL")),
		VerificationMode: getEnv("VERIFICATION_MODE", "off"),
		SMSProvider:      getEnv("SMS_PROVIDER", "console"),
	}

	// 解析时长类配置。
	var err error
	if cfg.MessageRetention, err = getDuration("MESSAGE_RETENTION", 168*time.Hour); err != nil {
		return nil, err
	}
	if cfg.AuditRetention, err = getDuration("AUDIT_RETENTION", 4320*time.Hour); err != nil {
		return nil, err
	}
	if cfg.BottleTTL, err = getDuration("BOTTLE_TTL", 168*time.Hour); err != nil {
		return nil, err
	}
	if cfg.RetentionInterval, err = getDuration("RETENTION_INTERVAL", 10*time.Minute); err != nil {
		return nil, err
	}
	if cfg.MatchRelaxAfter, err = getDuration("MATCH_RELAX_AFTER", 15*time.Second); err != nil {
		return nil, err
	}
	if cfg.MatchGrace, err = getDuration("MATCH_GRACE", 30*time.Second); err != nil {
		return nil, err
	}
	if cfg.StoryMinDuration, err = getDuration("STORY_MIN_DURATION", 5*time.Minute); err != nil {
		return nil, err
	}

	// 解析 Origin 白名单（逗号分隔）。
	cfg.AllowedOrigins = splitList(os.Getenv("ALLOWED_ORIGINS"))
	// 开发环境下未显式配置时使用本地开发服务器的默认 Origin，保持同源体验。
	if cfg.Env == "development" && len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	}

	// 解析可信代理 CIDR 与手机号密钥。
	cfg.TrustedProxies = splitList(os.Getenv("TRUSTED_PROXIES"))
	cfg.WordlistPath = strings.TrimSpace(os.Getenv("WORDLIST_PATH"))
	cfg.AdminIPAllowlist = splitList(os.Getenv("ADMIN_IP_ALLOWLIST"))
	if cfg.RateLimitMultiplier, err = getInt("RATE_LIMIT_MULTIPLIER", 1); err != nil {
		return nil, err
	}
	if cfg.PhoneEncKey, err = getBase64Key("PHONE_ENC_KEY"); err != nil {
		return nil, err
	}
	if cfg.PhoneHmacKey, err = getBase64Key("PHONE_HMAC_KEY"); err != nil {
		return nil, err
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// validate 校验必填项和取值范围。
func (c *Config) validate() error {
	if !contains(allowedEnvs, c.Env) {
		return fmt.Errorf("APP_ENV 取值非法: %q（允许: %s）", c.Env, strings.Join(allowedEnvs, ", "))
	}
	if c.DatabaseURL == "" {
		return fmt.Errorf("DATABASE_URL 为必填项，不能为空")
	}
	if err := validateAddr(c.HTTPAddr); err != nil {
		return fmt.Errorf("HTTP_ADDR 非法: %w", err)
	}
	if !contains(allowedVerificationMode, c.VerificationMode) {
		return fmt.Errorf("VERIFICATION_MODE 取值非法: %q（允许: %s）", c.VerificationMode, strings.Join(allowedVerificationMode, ", "))
	}
	if c.MessageRetention <= 0 {
		return fmt.Errorf("MESSAGE_RETENTION 必须为正数")
	}
	if c.AuditRetention <= 0 {
		return fmt.Errorf("AUDIT_RETENTION 必须为正数")
	}
	if c.BottleTTL <= 0 {
		return fmt.Errorf("BOTTLE_TTL 必须为正数")
	}
	if c.RetentionInterval <= 0 {
		return fmt.Errorf("RETENTION_INTERVAL 必须为正数")
	}
	if c.MatchRelaxAfter <= 0 {
		return fmt.Errorf("MATCH_RELAX_AFTER 必须为正数")
	}
	if c.MatchGrace <= 0 {
		return fmt.Errorf("MATCH_GRACE 必须为正数")
	}
	if c.StoryMinDuration <= 0 {
		return fmt.Errorf("STORY_MIN_DURATION 必须为正数")
	}
	// 有效场次的最短时长默认 5 分钟，只有测试环境允许调短（how.md 3.5）。
	if c.Env != "test" && c.StoryMinDuration < 5*time.Minute {
		return fmt.Errorf("STORY_MIN_DURATION 不得小于 5 分钟（仅 APP_ENV=test 允许）")
	}
	if c.RateLimitMultiplier < 1 {
		return fmt.Errorf("RATE_LIMIT_MULTIPLIER 必须为正整数")
	}
	if c.Env == "production" && c.RateLimitMultiplier != 1 {
		return fmt.Errorf("生产环境 RATE_LIMIT_MULTIPLIER 必须为 1")
	}

	// 校验管理后台 IP 白名单 CIDR。
	for _, cidr := range c.AdminIPAllowlist {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("ADMIN_IP_ALLOWLIST 含非法 CIDR %q: %w", cidr, err)
		}
	}

	// 校验可信代理 CIDR。
	for _, cidr := range c.TrustedProxies {
		if _, err := netip.ParsePrefix(cidr); err != nil {
			return fmt.Errorf("TRUSTED_PROXIES 含非法 CIDR %q: %w", cidr, err)
		}
	}

	// 开启实名验证时，手机号密钥必须存在且为 32 字节。
	if c.VerificationMode != "off" {
		if len(c.PhoneEncKey) != 32 {
			return fmt.Errorf("VERIFICATION_MODE=%s 时 PHONE_ENC_KEY 必填且必须为 32 字节", c.VerificationMode)
		}
		if len(c.PhoneHmacKey) != 32 {
			return fmt.Errorf("VERIFICATION_MODE=%s 时 PHONE_HMAC_KEY 必填且必须为 32 字节", c.VerificationMode)
		}
	}

	// 生产环境的额外约束。
	if c.Env == "production" {
		// 仅在启用实名验证（VERIFICATION_MODE≠off）时才需要真实短信服务商；
		// off 模式不会发送短信，允许使用默认 console。
		if c.VerificationMode != "off" && c.SMSProvider == "console" {
			return fmt.Errorf("生产环境启用实名验证时不允许使用 SMS_PROVIDER=console（会把验证码写入日志）")
		}
		if len(c.AllowedOrigins) == 0 {
			return fmt.Errorf("生产环境必须配置 ALLOWED_ORIGINS")
		}
		if c.WordlistPath == "" {
			return fmt.Errorf("生产环境必须配置 WORDLIST_PATH")
		}
	}
	return nil
}

// IsProduction 报告当前是否为生产环境。
func (c *Config) IsProduction() bool {
	return c.Env == "production"
}

// validateAddr 校验 host:port 形式，要求端口为合法数字。
func validateAddr(addr string) error {
	if strings.TrimSpace(addr) == "" {
		return fmt.Errorf("地址为空")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	_ = host
	if port == "" {
		return fmt.Errorf("端口为空")
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("端口 %q 不是数字", port)
	}
	return nil
}

// getEnv 读取环境变量，为空时返回默认值。
func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// getDuration 解析时长环境变量，为空时返回默认值。
func getDuration(key string, def time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是合法的时长: %w", key, err)
	}
	return d, nil
}

// getInt 解析整型环境变量，为空时返回默认值。
func getInt(key string, def int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 不是合法的整数: %w", key, err)
	}
	return n, nil
}

// splitList 把逗号分隔的字符串切成去空白的切片。
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// getBase64Key 解析 base64 编码的密钥；为空时返回 nil（是否必填由 validate 决定）。
// 解码失败返回错误，便于启动时快速失败。
func getBase64Key(key string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil, nil
	}
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		// 兼容不带填充的 base64url/base64 编码。
		if b2, err2 := base64.RawURLEncoding.DecodeString(raw); err2 == nil {
			return b2, nil
		}
		return nil, fmt.Errorf("%s 不是合法的 base64: %w", key, err)
	}
	return b, nil
}

// contains 判断字符串切片是否包含目标值。
func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
