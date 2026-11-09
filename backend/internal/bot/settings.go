package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/platform/secretbox"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 设置表与刷新相关的常量。
const (
	// settingsRefreshEvery 是生效配置的自动刷新间隔。
	settingsRefreshEvery = 30 * time.Second
	// 取值范围（与表约束保持一致）。
	minTimeoutSeconds = 5
	maxTimeoutSeconds = 300
	minFallbackSecs   = 3
	maxFallbackSecs   = 600
	maxBottleAgeSecs  = 604800
	// apiKeyHintPrefix 是后台显示用的掩码前缀（只保留末 4 位）。
	apiKeyHintPrefix = "••••"
	// notReadyURLChanged 是"Base URL 变了导致 key 失效"时的原因文案。
	notReadyURLChanged = "Base URL 已变更，请重新填写 API Key"
)

// API Key 来源。
const (
	// APIKeySourceDB 表示 key 来自数据库设置。
	APIKeySourceDB = "db"
	// APIKeySourceEnv 表示数据库没有设置，回退到环境变量 LLM_API_KEY。
	APIKeySourceEnv = "env"
	// APIKeySourceNone 表示没有任何可用的 key。
	APIKeySourceNone = "none"
)

// EnvSettings 是环境变量提供的首次初始化值与 API Key 回退。
type EnvSettings struct {
	Enabled            bool
	BaseURL            string
	APIKey             string
	Model              string
	Timeout            time.Duration
	SendCacheKey       bool
	MatchFallbackAfter time.Duration
	BottleMinAge       time.Duration
	Timezone           string
}

// Settings 是一份"生效配置"快照。明文 API Key 只存在于内存，绝不写日志、不进接口响应。
type Settings struct {
	ReasonModel        string
	MemoryModel        string
	MemoryRetention    time.Duration
	MemoryExtractEvery int
	OOCGuard           bool
	Enabled            bool
	BaseURL            string
	Model              string
	Timeout            time.Duration
	SendCacheKey       bool
	MatchFallbackAfter time.Duration
	BottleMinAge       time.Duration
	Timezone           string
	// PersonaMode 运行时固定为 user；兼容数据库中的旧 system 值。
	PersonaMode string
	// Location 是时区的 Location（解析失败时回退 UTC+8）。
	Location *time.Location

	// APIKey 是生效的明文 key（仅内存）。
	APIKey string
	// APIKeySet 表示是否有可用的 key；APIKeyHint 形如 ••••abcd。
	APIKeySet    bool
	APIKeyHint   string
	APIKeySource string
	// APIKeyStale 表示"存在 key，但它绑定的 Base URL 与当前 Base URL 不一致"：
	// 此时 key 不会被使用，需要运营重新填写（how.md §15.1）。
	APIKeyStale bool

	UpdatedAt time.Time
	// Ready 表示当前配置是否可用（开关打开且接入完整）。
	Ready          bool
	NotReadyReason string
}

// SettingsCache 缓存生效配置：启动加载、后台保存后立即刷新、每 30 秒自动刷新。
// 读取只走内存，匹配 goroutine 内不会访问数据库。
type SettingsCache struct {
	q      *store.Queries
	logger *slog.Logger
	// encKey 是 SETTINGS_ENC_KEY（32 字节）；为空表示后台不能保存 key。
	encKey []byte
	env    EnvSettings
	// hasInjectedLLM 表示测试注入了假模型客户端（此时不要求 base URL / model / key）。
	hasInjectedLLM bool

	mu      sync.RWMutex
	current Settings
}

// NewSettingsCache 创建缓存：先做首次初始化（表为空时写入环境变量），再加载一次。
func NewSettingsCache(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, encKey []byte, env EnvSettings, hasInjectedLLM bool) (*SettingsCache, error) {
	c := &SettingsCache{
		q:              store.New(pool),
		logger:         logger,
		encKey:         encKey,
		env:            env,
		hasInjectedLLM: hasInjectedLLM,
	}
	if err := c.ensureInitialized(ctx); err != nil {
		return nil, err
	}
	if err := c.Refresh(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// EncryptionAvailable 报告是否配置了 SETTINGS_ENC_KEY（后台能否保存 API Key）。
func (c *SettingsCache) EncryptionAvailable() bool { return len(c.encKey) == 32 }

// ensureInitialized 在表里没有这一行时，用环境变量的值写入（之后以数据库为准）。
func (c *SettingsCache) ensureInitialized(ctx context.Context) error {
	timeoutSecs := clampInt32(int32(c.env.Timeout.Seconds()), minTimeoutSeconds, maxTimeoutSeconds)
	fallbackSecs := clampInt32(int32(c.env.MatchFallbackAfter.Seconds()), minFallbackSecs, maxFallbackSecs)
	ageSecs := clampInt32(int32(c.env.BottleMinAge.Seconds()), 0, maxBottleAgeSecs)
	tz := c.env.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	if err := c.q.InsertBotSettings(ctx, store.InsertBotSettingsParams{
		Enabled:              c.env.Enabled,
		LlmBaseUrl:           strings.TrimSpace(c.env.BaseURL),
		LlmModel:             strings.TrimSpace(c.env.Model),
		LlmTimeoutSeconds:    timeoutSecs,
		LlmSendCacheKey:      c.env.SendCacheKey,
		MatchFallbackSeconds: fallbackSecs,
		BottleMinAgeSeconds:  ageSecs,
		Timezone:             tz,
	}); err != nil {
		return fmt.Errorf("初始化 bot 设置失败: %w", err)
	}
	return nil
}

// Refresh 从数据库重新加载生效配置。
func (c *SettingsCache) Refresh(ctx context.Context) error {
	row, err := c.q.GetBotSettings(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 表里没有行（初始化失败等）：退回环境变量的值，保证引擎能工作。
			c.store(c.fromEnv())
			return nil
		}
		return err
	}
	c.store(c.fromRow(row))
	return nil
}

// Run 每 30 秒自动刷新一次，直到 ctx 取消。
func (c *SettingsCache) Run(ctx context.Context) {
	ticker := time.NewTicker(settingsRefreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if err := c.Refresh(rctx); err != nil {
				c.logger.Warn("刷新 bot 设置失败", "error", err)
			}
			cancel()
		}
	}
}

// Get 返回当前生效配置的快照（只读内存）。
func (c *SettingsCache) Get() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// store 写入缓存快照。
func (c *SettingsCache) store(s Settings) {
	c.mu.Lock()
	c.current = s
	c.mu.Unlock()
}

// fromEnv 用环境变量构造快照（数据库不可用时的兜底）。
func (c *SettingsCache) fromEnv() Settings {
	tz := c.env.Timezone
	if tz == "" {
		tz = "Asia/Shanghai"
	}
	s := Settings{
		Enabled:            c.env.Enabled,
		BaseURL:            strings.TrimSpace(c.env.BaseURL),
		Model:              strings.TrimSpace(c.env.Model),
		Timeout:            clampDuration(c.env.Timeout, minTimeoutSeconds, maxTimeoutSeconds),
		SendCacheKey:       c.env.SendCacheKey,
		MatchFallbackAfter: clampDuration(c.env.MatchFallbackAfter, minFallbackSecs, maxFallbackSecs),
		BottleMinAge:       clampDuration(c.env.BottleMinAge, 0, maxBottleAgeSecs),
		Timezone:           tz,
		PersonaMode:        PersonaModeUser,
		MemoryRetention:    30 * 24 * time.Hour,
		MemoryExtractEvery: 20,
		OOCGuard:           true,
		Location:           LoadLocation(tz),
		APIKeySource:       APIKeySourceNone,
	}
	key, source, hint, stale := c.resolveAPIKey(nil, "", "", s.BaseURL)
	s.APIKey, s.APIKeySource, s.APIKeyHint, s.APIKeyStale = key, source, hint, stale
	s.APIKeySet = key != ""
	s.Ready, s.NotReadyReason = evaluateReady(s, c.hasInjectedLLM)
	return s
}

// fromRow 把数据库行转成快照（密钥在这里解密，结果只保留在内存）。
func (c *SettingsCache) fromRow(row store.BotSetting) Settings {
	s := Settings{
		Enabled:            row.Enabled,
		BaseURL:            strings.TrimSpace(row.LlmBaseUrl),
		Model:              strings.TrimSpace(row.LlmModel),
		Timeout:            time.Duration(row.LlmTimeoutSeconds) * time.Second,
		SendCacheKey:       row.LlmSendCacheKey,
		MatchFallbackAfter: time.Duration(row.MatchFallbackSeconds) * time.Second,
		BottleMinAge:       time.Duration(row.BottleMinAgeSeconds) * time.Second,
		Timezone:           row.Timezone,
		PersonaMode:        normalizePersonaMode(row.PersonaMode),
		Location:           LoadLocation(row.Timezone),
		UpdatedAt:          row.UpdatedAt.Time,
		ReasonModel:        strings.TrimSpace(row.ReasonModel),
		MemoryModel:        strings.TrimSpace(row.MemoryModel),
		MemoryRetention:    time.Duration(row.MemoryRetentionDays) * 24 * time.Hour,
		MemoryExtractEvery: int(row.MemoryExtractEvery),
		OOCGuard:           row.OocGuard,
	}
	key, source, hint, stale := c.resolveAPIKey(row.LlmApiKeyEnc, row.LlmApiKeyHint, row.LlmApiKeyBaseUrl, s.BaseURL)
	s.APIKey, s.APIKeySource, s.APIKeyHint, s.APIKeyStale = key, source, hint, stale
	s.APIKeySet = key != ""
	s.Ready, s.NotReadyReason = evaluateReady(s, c.hasInjectedLLM)
	return s
}

// NormalizeBaseURL 规范化 Base URL：去掉首尾空白与末尾的斜杠。
// key 的绑定地址统一用它比较，避免 https://a/v1 与 https://a/v1/ 被当成不同地址。
func NormalizeBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// resolveAPIKey 计算生效的 key：数据库（解密）> 环境变量 > 无。
// key 与 Base URL 绑定（how.md §15.1）：
//   - 数据库中的 key 只有在"当前 Base URL == 保存时的 Base URL"时才使用；
//   - 环境变量 LLM_API_KEY 只有在"当前 Base URL == 环境变量 LLM_BASE_URL"时才使用。
//
// 两者都不满足时视为没有 key（source=none），并返回 stale=true 供上层给出提示。
func (c *SettingsCache) resolveAPIKey(enc []byte, hint, boundBaseURL, baseURL string) (key, source, hintOut string, stale bool) {
	base := NormalizeBaseURL(baseURL)
	hintOut = hint

	if len(enc) > 0 {
		if !c.EncryptionAvailable() {
			c.logger.Warn("已保存的模型 API Key 无法解密：未配置 SETTINGS_ENC_KEY")
			stale = true
		} else if plain, err := secretbox.Decrypt(c.encKey, enc); err != nil {
			// 只记错误类型，绝不记录密钥内容。
			c.logger.Warn("解密模型 API Key 失败", "error", "decrypt_failed")
			stale = true
		} else if dbKey := string(plain); dbKey != "" {
			if base != "" && NormalizeBaseURL(boundBaseURL) == base {
				if hintOut == "" {
					hintOut = MaskAPIKey(dbKey)
				}
				return dbKey, APIKeySourceDB, hintOut, false
			}
			// key 绑定在别的地址上：不使用，避免被带到新地址。
			stale = true
		}
	}

	if envKey := strings.TrimSpace(c.env.APIKey); envKey != "" {
		if base != "" && NormalizeBaseURL(c.env.BaseURL) == base {
			return envKey, APIKeySourceEnv, MaskAPIKey(envKey), false
		}
		stale = true
		if hintOut == "" {
			hintOut = MaskAPIKey(envKey)
		}
	}
	return "", APIKeySourceNone, hintOut, stale
}

// MaskAPIKey 返回只保留末 4 位的掩码，例如 ••••abcd。
func MaskAPIKey(key string) string {
	k := strings.TrimSpace(key)
	if k == "" {
		return ""
	}
	if len(k) <= 4 {
		return apiKeyHintPrefix
	}
	return apiKeyHintPrefix + k[len(k)-4:]
}

// evaluateReady 判断配置是否可用（不可用时给出给运营看的原因）。
func evaluateReady(s Settings, injectedLLM bool) (bool, string) {
	if !s.Enabled {
		return false, "全局开关已关闭"
	}
	if injectedLLM {
		return true, ""
	}
	if s.BaseURL == "" {
		return false, "未配置模型 Base URL"
	}
	if s.Model == "" {
		return false, "未配置默认模型"
	}
	if !s.APIKeySet {
		if s.APIKeyStale {
			return false, notReadyURLChanged
		}
		return false, "未配置模型 API Key"
	}
	return true, ""
}

// UpdateInput 是后台保存的全局设置（apiKey 为空表示保持不变）。
type UpdateInput struct {
	ReasonModel          string
	MemoryModel          string
	MemoryRetentionDays  int32
	MemoryExtractEvery   int32
	OOCGuard             bool
	Enabled              bool
	BaseURL              string
	Model                string
	TimeoutSeconds       int32
	SendCacheKey         bool
	MatchFallbackSeconds int32
	BottleMinAgeSeconds  int32
	Timezone             string
	// PersonaMode 保留旧客户端兼容；接受 user/system，保存时统一为 user。
	PersonaMode string
	// APIKey 非空表示替换 key；ClearAPIKey 为 true 表示清除数据库中的 key。
	APIKey      string
	ClearAPIKey bool
}

// Validate 校验后台提交的设置（纯函数，便于单测）。
func (in UpdateInput) Validate() error {
	if in.MemoryRetentionDays < 1 || in.MemoryRetentionDays > 365 {
		return httpx.NewError(httpx.CodeInvalidRequest, "记忆保留期需在 1–365 天之间")
	}
	if in.MemoryExtractEvery < 5 || in.MemoryExtractEvery > 200 {
		return httpx.NewError(httpx.CodeInvalidRequest, "记忆抽取间隔需在 5–200 条之间")
	}
	base := strings.TrimSpace(in.BaseURL)
	if base != "" {
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return httpx.NewError(httpx.CodeInvalidRequest, "Base URL 必须是 http 或 https 的绝对地址")
		}
	}
	if in.TimeoutSeconds < minTimeoutSeconds || in.TimeoutSeconds > maxTimeoutSeconds {
		return httpx.NewError(httpx.CodeInvalidRequest, fmt.Sprintf("超时需在 %d–%d 秒之间", minTimeoutSeconds, maxTimeoutSeconds))
	}
	if in.MatchFallbackSeconds < minFallbackSecs || in.MatchFallbackSeconds > maxFallbackSecs {
		return httpx.NewError(httpx.CodeInvalidRequest, fmt.Sprintf("匹配兜底时间需在 %d–%d 秒之间", minFallbackSecs, maxFallbackSecs))
	}
	if in.BottleMinAgeSeconds < 0 || in.BottleMinAgeSeconds > maxBottleAgeSecs {
		return httpx.NewError(httpx.CodeInvalidRequest, fmt.Sprintf("漂流瓶最短瓶龄需在 0–%d 秒之间", maxBottleAgeSecs))
	}
	if strings.TrimSpace(in.Timezone) == "" {
		return httpx.NewError(httpx.CodeInvalidRequest, "时区不能为空")
	}
	if m := strings.TrimSpace(in.PersonaMode); m != "" && m != PersonaModeUser && m != PersonaModeSystem {
		return httpx.NewError(httpx.CodeInvalidRequest, "人设注入方式只能是 user 或 system")
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil {
		return httpx.NewError(httpx.CodeInvalidRequest, "时区无法识别，请填写 IANA 时区名（例如 Asia/Shanghai）")
	}
	return nil
}

// Update 保存设置并立即刷新缓存（后台保存后调用，无需重启）。
// 返回值里不含 API Key。
func (c *SettingsCache) Update(ctx context.Context, in UpdateInput, adminID pgtype.UUID) (Settings, error) {
	if err := in.Validate(); err != nil {
		return Settings{}, err
	}
	current, err := c.q.GetBotSettings(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, err
	}

	newBase := NormalizeBaseURL(in.BaseURL)
	oldBase := NormalizeBaseURL(current.LlmBaseUrl)
	sameBase := newBase == oldBase

	enc := current.LlmApiKeyEnc
	hint := current.LlmApiKeyHint
	bound := NormalizeBaseURL(current.LlmApiKeyBaseUrl)
	switch {
	case in.ClearAPIKey:
		// 明确清除：允许同时改地址（结果是没有任何 key）。
		enc, hint, bound = nil, "", ""
	case strings.TrimSpace(in.APIKey) != "":
		// 保存新 key：按本次的 Base URL 绑定。
		if !c.EncryptionAvailable() {
			return Settings{}, httpx.NewError(httpx.CodeInvalidRequest, "服务器未配置 SETTINGS_ENC_KEY，无法保存密钥")
		}
		sealed, serr := secretbox.Encrypt(c.encKey, []byte(strings.TrimSpace(in.APIKey)))
		if serr != nil {
			return Settings{}, serr
		}
		enc, hint, bound = sealed, MaskAPIKey(in.APIKey), newBase
	case sameBase:
		// 地址没变且没给新 key：保持原样。
	default:
		// 改了 Base URL 又没给新 key：如果旧地址上有生效的 key，就拒绝保存，
		// 避免把 key 带到新地址（how.md §15.1）。
		dbKeyBoundToOld := len(enc) > 0 && bound == oldBase && oldBase != ""
		if dbKeyBoundToOld || c.envKeyBoundTo(oldBase) != "" {
			return Settings{}, httpx.NewError(httpx.CodeInvalidRequest, "修改 Base URL 时需要重新填写 API Key")
		}
		// 没有生效的 key：顺手清掉不可用的旧密文与绑定。
		enc, hint, bound = nil, "", ""
	}

	personaMode := normalizePersonaMode(in.PersonaMode)

	row, err := c.q.UpdateBotSettings(ctx, store.UpdateBotSettingsParams{
		Enabled:              in.Enabled,
		LlmBaseUrl:           strings.TrimSpace(in.BaseURL),
		LlmApiKeyEnc:         enc,
		LlmApiKeyHint:        hint,
		LlmApiKeyBaseUrl:     bound,
		LlmModel:             strings.TrimSpace(in.Model),
		LlmTimeoutSeconds:    in.TimeoutSeconds,
		LlmSendCacheKey:      in.SendCacheKey,
		MatchFallbackSeconds: in.MatchFallbackSeconds,
		BottleMinAgeSeconds:  in.BottleMinAgeSeconds,
		Timezone:             strings.TrimSpace(in.Timezone),
		UpdatedBy:            adminID,
		PersonaMode:          personaMode,
		ReasonModel:          strings.TrimSpace(in.ReasonModel),
		MemoryModel:          strings.TrimSpace(in.MemoryModel),
		MemoryRetentionDays:  in.MemoryRetentionDays,
		MemoryExtractEvery:   in.MemoryExtractEvery,
		OocGuard:             in.OOCGuard,
	})
	if err != nil {
		return Settings{}, err
	}
	c.store(c.fromRow(row))
	return c.Get(), nil
}

// envKeyBoundTo 返回绑定在该 Base URL 上的环境变量 key（没有则空串）。
func (c *SettingsCache) envKeyBoundTo(baseURL string) string {
	envKey := strings.TrimSpace(c.env.APIKey)
	if envKey == "" {
		return ""
	}
	if NormalizeBaseURL(c.env.BaseURL) != NormalizeBaseURL(baseURL) {
		return ""
	}
	return envKey
}

// clampInt32 把值限制在 [min, max]。
func clampInt32(v, min, max int32) int32 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

// clampDuration 把时长限制在 [minSeconds, maxSeconds]（秒）。
func clampDuration(d time.Duration, minSeconds, maxSeconds int) time.Duration {
	secs := int(d.Seconds())
	if secs < minSeconds {
		secs = minSeconds
	}
	if secs > maxSeconds {
		secs = maxSeconds
	}
	return time.Duration(secs) * time.Second
}

// normalizePersonaMode 统一使用已验证的 user 注入；旧 system 值不再影响运行时。
func normalizePersonaMode(_ string) string {
	return PersonaModeUser
}
