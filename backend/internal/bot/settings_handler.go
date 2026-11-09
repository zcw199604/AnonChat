package bot

import (
	"context"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// 测试连接接口的参数。
const (
	// testConnectionMaxTokens 是测试连接请求的最大输出 token（只做一次极短的请求）。
	testConnectionMaxTokens = 8
	// testConnectionTimeoutCap 是测试连接的超时上限。
	testConnectionTimeoutCap = 15 * time.Second
	// testConnectionErrorMax 是返回给前端的错误文案长度上限。
	testConnectionErrorMax = 300
)

// settingsResponse 是全局设置的对外表示。
// 注意：响应里永远不包含 API Key 的明文或密文，只给是否已设置与掩码提示。
type settingsResponse struct {
	ReasonModel         string `json:"reasonModel"`
	MemoryModel         string `json:"memoryModel"`
	MemoryRetentionDays int32  `json:"memoryRetentionDays"`
	MemoryExtractEvery  int32  `json:"memoryExtractEvery"`
	OOCGuard            bool   `json:"oocGuard"`
	Enabled             bool   `json:"enabled"`
	BaseURL             string `json:"baseUrl"`
	Model               string `json:"model"`
	TimeoutSeconds      int32  `json:"timeoutSeconds"`
	SendCacheKey        bool   `json:"sendCacheKey"`
	FallbackSeconds     int32  `json:"matchFallbackSeconds"`
	BottleMinAgeSecs    int32  `json:"bottleMinAgeSeconds"`
	Timezone            string `json:"timezone"`
	PersonaMode         string `json:"personaMode"`
	// APIKeySet / APIKeyHint / APIKeySource 只暴露"是否设置""末 4 位""来源"。
	APIKeySet  bool   `json:"apiKeySet"`
	APIKeyHint string `json:"apiKeyHint"`
	APIKeySrc  string `json:"apiKeySource"`
	// APIKeyStale 表示已保存的 key 绑定在别的 Base URL 上（当前不生效）。
	APIKeyStale bool   `json:"apiKeyStale"`
	SecretOK    bool   `json:"secretStorageAvailable"`
	Ready       bool   `json:"ready"`
	NotReadyMsg string `json:"notReadyReason"`
	UpdatedAt   string `json:"updatedAt"`
}

// toSettingsResponse 构造响应（绝不包含 key）。
func toSettingsResponse(s Settings, encAvailable bool) settingsResponse {
	return settingsResponse{
		ReasonModel:         s.ReasonModel,
		MemoryModel:         s.MemoryModel,
		MemoryRetentionDays: int32(s.MemoryRetention / (24 * time.Hour)),
		MemoryExtractEvery:  int32(s.MemoryExtractEvery),
		OOCGuard:            s.OOCGuard,
		Enabled:             s.Enabled,
		BaseURL:             s.BaseURL,
		Model:               s.Model,
		TimeoutSeconds:      int32(s.Timeout / time.Second),
		SendCacheKey:        s.SendCacheKey,
		FallbackSeconds:     int32(s.MatchFallbackAfter / time.Second),
		BottleMinAgeSecs:    int32(s.BottleMinAge / time.Second),
		Timezone:            s.Timezone,
		PersonaMode:         s.PersonaMode,
		APIKeySet:           s.APIKeySet,
		APIKeyHint:          s.APIKeyHint,
		APIKeySrc:           s.APIKeySource,
		APIKeyStale:         s.APIKeyStale,
		SecretOK:            encAvailable,
		Ready:               s.Ready,
		NotReadyMsg:         s.NotReadyReason,
		UpdatedAt: formatTS(pgtype.Timestamptz{Time: s.UpdatedAt,
			Valid: !s.UpdatedAt.IsZero()}),
	}
}

// settingsRequest 是保存全局设置的请求体。
// apiKey 为空表示保持不变；clearApiKey=true 表示清除数据库中的 key。
type settingsRequest struct {
	// 新字段省略时保留当前值，兼容尚未升级的后台客户端；显式传零仍校验。
	ReasonModel         *string `json:"reasonModel"`
	MemoryModel         *string `json:"memoryModel"`
	MemoryRetentionDays *int32  `json:"memoryRetentionDays"`
	MemoryExtractEvery  *int32  `json:"memoryExtractEvery"`
	OOCGuard            *bool   `json:"oocGuard"`
	Enabled             bool    `json:"enabled"`
	BaseURL             string  `json:"baseUrl"`
	Model               string  `json:"model"`
	APIKey              string  `json:"apiKey"`
	ClearAPIKey         bool    `json:"clearApiKey"`
	TimeoutSeconds      int32   `json:"timeoutSeconds"`
	SendCacheKey        bool    `json:"sendCacheKey"`
	FallbackSeconds     int32   `json:"matchFallbackSeconds"`
	BottleMinAgeSeconds int32   `json:"bottleMinAgeSeconds"`
	Timezone            string  `json:"timezone"`
	// PersonaMode 兼容旧客户端传值，实际统一使用 user。
	PersonaMode string `json:"personaMode"`
}

// GetSettingsHandler 处理 GET /api/admin/bot-settings。
func (h *Handler) GetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "bot 设置不可用"))
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toSettingsResponse(h.settings.Get(), h.settings.EncryptionAvailable()))
}

// UpdateSettingsHandler 处理 PUT /api/admin/bot-settings：保存后立即生效（调用 Invalidate）。
func (h *Handler) UpdateSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "bot 设置不可用"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req settingsRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	adminID, _ := admin.AdminIDFromContext(r.Context())
	current := h.settings.Get()
	reason, memory := current.ReasonModel, current.MemoryModel
	days, every, guard := int32(current.MemoryRetention/(24*time.Hour)), int32(current.MemoryExtractEvery), current.OOCGuard
	if req.ReasonModel != nil {
		reason = *req.ReasonModel
	}
	if req.MemoryModel != nil {
		memory = *req.MemoryModel
	}
	if req.MemoryRetentionDays != nil {
		days = *req.MemoryRetentionDays
	}
	if req.MemoryExtractEvery != nil {
		every = *req.MemoryExtractEvery
	}
	if req.OOCGuard != nil {
		guard = *req.OOCGuard
	}
	updated, err := h.settings.Update(r.Context(), UpdateInput{
		ReasonModel:          reason,
		MemoryModel:          memory,
		MemoryRetentionDays:  days,
		MemoryExtractEvery:   every,
		OOCGuard:             guard,
		Enabled:              req.Enabled,
		BaseURL:              req.BaseURL,
		Model:                req.Model,
		TimeoutSeconds:       req.TimeoutSeconds,
		SendCacheKey:         req.SendCacheKey,
		MatchFallbackSeconds: req.FallbackSeconds,
		BottleMinAgeSeconds:  req.BottleMinAgeSeconds,
		Timezone:             req.Timezone,
		PersonaMode:          req.PersonaMode,
		APIKey:               req.APIKey,
		ClearAPIKey:          req.ClearAPIKey,
	}, adminID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	// 审计内容不含 key：只记录开关与"是否修改了 key"。
	h.audit(r, "bot.settings.update", "bot-settings", map[string]any{
		"enabled":       updated.Enabled,
		"apiKeyChanged": strings.TrimSpace(req.APIKey) != "",
		"apiKeyCleared": req.ClearAPIKey,
	})
	h.invalidate()
	httpx.WriteJSON(w, http.StatusOK, toSettingsResponse(updated, h.settings.EncryptionAvailable()))
}

// settingsTestRequest 是测试连接的请求体（可选覆盖项，不保存）。
type settingsTestRequest struct {
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model"`
	APIKey  string `json:"apiKey"`
}

// settingsTestResponse 是测试连接的结果。
type settingsTestResponse struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Model     string `json:"model"`
	Error     string `json:"error"`
}

// TestSettingsHandler 处理 POST /api/admin/bot-settings/test：
// 用（可能被临时覆盖的）已保存设置发一次极短请求，只用于验证连通性。
func (h *Handler) TestSettingsHandler(w http.ResponseWriter, r *http.Request) {
	if h.settings == nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "bot 设置不可用"))
		return
	}
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	// 每个管理员每分钟最多 10 次。
	if h.settingsTestLimiter != nil && !h.settingsTestLimiter.Allow(r.Context(), "bot_settings_test:"+adminID.String()) {
		httpx.WriteError(w, httpx.NewError(httpx.CodeRateLimited, "测试连接太频繁了，请稍后再试"))
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req settingsTestRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}

	saved := h.settings.Get()
	savedBase := NormalizeBaseURL(saved.BaseURL)
	baseURL := savedBase
	if v := strings.TrimSpace(req.BaseURL); v != "" {
		baseURL = NormalizeBaseURL(v)
	}
	model := strings.TrimSpace(saved.Model)
	if v := strings.TrimSpace(req.Model); v != "" {
		model = v
	}
	requestKey := strings.TrimSpace(req.APIKey)
	// 安全约束：Base URL 与已保存的不同时，绝不使用已保存的 key（否则可以把 key 引到任意地址）。
	// 此时必须显式带上新 key，否则直接返回失败且不发出任何网络请求（how.md §15.3）。
	if baseURL != savedBase && requestKey == "" {
		httpx.WriteJSON(w, http.StatusOK, settingsTestResponse{
			OK:    false,
			Model: model,
			Error: "更换 Base URL 测试时需要填写 API Key",
		})
		return
	}
	apiKey := requestKey
	if apiKey == "" {
		// 地址与已保存一致：可以用已保存的 key（它已在加载时按绑定关系校验过）。
		apiKey = saved.APIKey
	}
	if baseURL == "" || model == "" || apiKey == "" {
		httpx.WriteJSON(w, http.StatusOK, settingsTestResponse{
			OK:    false,
			Model: model,
			Error: "缺少 Base URL、模型或 API Key",
		})
		return
	}
	if !isHTTPURL(baseURL) {
		httpx.WriteJSON(w, http.StatusOK, settingsTestResponse{
			OK:    false,
			Model: model,
			Error: "Base URL 必须是 http 或 https 的绝对地址",
		})
		return
	}

	timeout := saved.Timeout
	if timeout <= 0 || timeout > testConnectionTimeoutCap {
		timeout = testConnectionTimeoutCap
	}
	client := NewOpenAIClient(ClientOptions{
		BaseURL:      baseURL,
		APIKey:       apiKey,
		SendCacheKey: saved.SendCacheKey,
		Logger:       h.logger,
	})
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	started := time.Now()
	_, err := client.Chat(ctx, ChatRequest{
		Model: model,
		Messages: []ChatMessage{{Role: "user",
			Content: "ping"}},
		MaxTokens:   testConnectionMaxTokens,
		Temperature: 0,
	})
	latency := time.Since(started).Milliseconds()
	if err != nil {
		httpx.WriteJSON(w, http.StatusOK, settingsTestResponse{
			OK:        false,
			LatencyMs: latency,
			Model:     model,
			Error:     sanitizeTestError(err.Error(), apiKey),
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, settingsTestResponse{OK: true, LatencyMs: latency, Model: model})
}

// sanitizeTestError 截断错误文案并确保不含 API Key。
func sanitizeTestError(msg, apiKey string) string {
	if key := strings.TrimSpace(apiKey); key != "" {
		msg = strings.ReplaceAll(msg, key, "***")
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "调用失败"
	}
	if utf8.RuneCountInString(msg) > testConnectionErrorMax {
		msg = string([]rune(msg)[:testConnectionErrorMax]) + "…"
	}
	return msg
}

// isHTTPURL 判断是否是 http/https 绝对地址。
func isHTTPURL(raw string) bool {
	in := UpdateInput{MemoryRetentionDays: 30, MemoryExtractEvery: 20, BaseURL: raw, TimeoutSeconds: minTimeoutSeconds, MatchFallbackSeconds: minFallbackSecs, Timezone: "UTC"}
	return in.Validate() == nil
}
