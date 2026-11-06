package bot

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// maxBodyBytes 是 JSON 请求体的上限。
const maxBodyBytes = 64 << 10

// Handler 暴露角色卡、bot 与全局设置的后台 HTTP 接口（how.md §10、§15.3）。
type Handler struct {
	svc      *Service
	adm      *admin.Service
	engine   *Engine        // 可以为 nil：为空时不刷新引擎缓存
	settings *SettingsCache // 可以为 nil：为空时全局设置接口返回 internal
	// settingsTestLimiter 限制"测试连接"的频率（每个管理员每分钟 10 次）。
	settingsTestLimiter moderation.RateLimiter
	logger              *slog.Logger
}

// NewHandler 创建 bot 后台处理器。engine / settings 可以为 nil（测试或未初始化时）。
func NewHandler(svc *Service, adm *admin.Service, engine *Engine, settings *SettingsCache, testLimiter moderation.RateLimiter, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{
		svc:                 svc,
		adm:                 adm,
		engine:              engine,
		settings:            settings,
		settingsTestLimiter: testLimiter,
		logger:              logger,
	}
}

// ---- 角色卡 ----

// cardResponse 是角色卡的对外表示。
type cardResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Personality     string `json:"personality"`
	Scenario        string `json:"scenario"`
	FirstMessage    string `json:"firstMessage"`
	ExampleDialogue string `json:"exampleDialogue"`
	SystemPrompt    string `json:"systemPrompt"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type cardRequest struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Personality     string `json:"personality"`
	Scenario        string `json:"scenario"`
	FirstMessage    string `json:"firstMessage"`
	ExampleDialogue string `json:"exampleDialogue"`
	SystemPrompt    string `json:"systemPrompt"`
}

// toCardResponse 构造角色卡响应。
func toCardResponse(card store.CharacterCard) cardResponse {
	return cardResponse{
		ID:              card.ID.String(),
		Name:            card.Name,
		Description:     card.Description,
		Personality:     card.Personality,
		Scenario:        card.Scenario,
		FirstMessage:    card.FirstMessage,
		ExampleDialogue: card.ExampleDialogue,
		SystemPrompt:    card.SystemPrompt,
		CreatedAt:       formatTS(card.CreatedAt),
		UpdatedAt:       formatTS(card.UpdatedAt),
	}
}

// ListCardsHandler 处理 GET /api/admin/character-cards。
func (h *Handler) ListCardsHandler(w http.ResponseWriter, r *http.Request) {
	cards, err := h.svc.ListCards(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]cardResponse, 0, len(cards))
	for _, card := range cards {
		out = append(out, toCardResponse(card))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// CreateCardHandler 处理 POST /api/admin/character-cards。
func (h *Handler) CreateCardHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req cardRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	card, err := h.svc.CreateCard(r.Context(), CardInput(req))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.card.create", card.ID.String(), map[string]any{"name": card.Name})
	httpx.WriteJSON(w, http.StatusCreated, toCardResponse(card))
}

// UpdateCardHandler 处理 PUT /api/admin/character-cards/{id}。
func (h *Handler) UpdateCardHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "角色卡 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req cardRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	card, err := h.svc.UpdateCard(r.Context(), id, CardInput(req))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.card.update", card.ID.String(), map[string]any{"name": card.Name})
	h.invalidate()
	httpx.WriteJSON(w, http.StatusOK, toCardResponse(card))
}

// DeleteCardHandler 处理 DELETE /api/admin/character-cards/{id}。
func (h *Handler) DeleteCardHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "角色卡 ID 非法"))
		return
	}
	if err := h.svc.DeleteCard(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.card.delete", id.String(), nil)
	h.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// ---- Bot ----

// profileResponse 是 bot 对外资料。
type profileResponse struct {
	Nickname       string   `json:"nickname"`
	AvatarSeed     string   `json:"avatarSeed"`
	PublicCode     string   `json:"publicCode"`
	Gender         string   `json:"gender"`
	Age            *int16   `json:"age"`
	Province       *string  `json:"province"`
	ProvincePublic bool     `json:"provincePublic"`
	Tags           []string `json:"tags"`
	CleanMode      bool     `json:"cleanMode"`
}

// usageResponse 是 bot 的用量统计。
type usageResponse struct {
	UsedTokens         int64 `json:"usedTokens"`
	UsedReplies        int64 `json:"usedReplies"`
	WindowTokens       int64 `json:"windowTokens"`
	WindowPromptTokens int64 `json:"windowPromptTokens"`
	WindowCachedTokens int64 `json:"windowCachedTokens"`
	WindowReplies      int64 `json:"windowReplies"`
	ActiveMatches      int64 `json:"activeMatches"`
}

// botResponse 是 bot 的对外表示（设置 + 对外资料 + 用量）。
type botResponse struct {
	ID                    string          `json:"id"`
	Label                 string          `json:"label"`
	CardID                *string         `json:"cardId"`
	MediaFolderID         *string         `json:"mediaFolderId"`
	Enabled               bool            `json:"enabled"`
	Model                 string          `json:"model"`
	Temperature           float32         `json:"temperature"`
	MaxOutputTokens       int32           `json:"maxOutputTokens"`
	ServeMatch            bool            `json:"serveMatch"`
	ServeBottle           bool            `json:"serveBottle"`
	MaxActiveMatches      int32           `json:"maxActiveMatches"`
	TotalTokenLimit       int64           `json:"totalTokenLimit"`
	TotalReplyLimit       int64           `json:"totalReplyLimit"`
	WindowSeconds         int32           `json:"windowSeconds"`
	WindowTokenLimit      int64           `json:"windowTokenLimit"`
	WindowReplyLimit      int32           `json:"windowReplyLimit"`
	ConvReplyLimit        int32           `json:"convReplyLimit"`
	ConvMinIntervalSecond int32           `json:"convMinIntervalSeconds"`
	ConvMediaLimit        int32           `json:"convMediaLimit"`
	ReplyDelayMinMs       int32           `json:"replyDelayMinMs"`
	ReplyDelayMaxMs       int32           `json:"replyDelayMaxMs"`
	TypingMsPerChar       int32           `json:"typingMsPerChar"`
	BottleIntervalSeconds int32           `json:"bottleIntervalSeconds"`
	CardName              string          `json:"cardName"`
	FolderName            string          `json:"folderName"`
	Profile               profileResponse `json:"profile"`
	Usage                 usageResponse   `json:"usage"`
	CreatedAt             string          `json:"createdAt"`
	UpdatedAt             string          `json:"updatedAt"`
}

// toBotResponse 用后台列表行构造响应。
func toBotResponse(row store.ListBotsRow) botResponse {
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}
	resp := botResponse{
		ID:                    row.ID.String(),
		Label:                 row.Label,
		CardID:                uuidPtr(row.CardID),
		MediaFolderID:         uuidPtr(row.MediaFolderID),
		Enabled:               row.Enabled,
		Model:                 row.Model,
		Temperature:           row.Temperature,
		MaxOutputTokens:       row.MaxOutputTokens,
		ServeMatch:            row.ServeMatch,
		ServeBottle:           row.ServeBottle,
		MaxActiveMatches:      row.MaxActiveMatches,
		TotalTokenLimit:       row.TotalTokenLimit,
		TotalReplyLimit:       row.TotalReplyLimit,
		WindowSeconds:         row.WindowSeconds,
		WindowTokenLimit:      row.WindowTokenLimit,
		WindowReplyLimit:      row.WindowReplyLimit,
		ConvReplyLimit:        row.ConvReplyLimit,
		ConvMinIntervalSecond: row.ConvMinIntervalSeconds,
		ConvMediaLimit:        row.ConvMediaLimit,
		ReplyDelayMinMs:       row.ReplyDelayMinMs,
		ReplyDelayMaxMs:       row.ReplyDelayMaxMs,
		TypingMsPerChar:       row.TypingMsPerChar,
		BottleIntervalSeconds: row.BottleIntervalSeconds,
		CardName:              row.CardName,
		FolderName:            row.FolderName,
		Profile: profileResponse{
			Nickname:       row.Nickname,
			AvatarSeed:     row.AvatarSeed,
			PublicCode:     row.PublicCode,
			Gender:         row.Gender,
			Age:            row.Age,
			Province:       row.Province,
			ProvincePublic: row.ProvincePublic,
			Tags:           tags,
			CleanMode:      row.CleanMode,
		},
		Usage: usageResponse{
			UsedTokens:         row.UsedTokens,
			UsedReplies:        row.UsedReplies,
			WindowTokens:       row.WindowTokens,
			WindowPromptTokens: row.WindowPromptTokens,
			WindowCachedTokens: row.WindowCachedTokens,
			WindowReplies:      row.WindowReplies,
			ActiveMatches:      row.ActiveMatches,
		},
		CreatedAt: formatTS(row.CreatedAt),
		UpdatedAt: formatTS(row.UpdatedAt),
	}
	return resp
}

type createBotRequest struct {
	Label         string `json:"label"`
	Nickname      string `json:"nickname"`
	CardID        string `json:"cardId"`
	MediaFolderID string `json:"mediaFolderId"`
	Enabled       bool   `json:"enabled"`
}

type updateBotProfileRequest struct {
	Nickname       string   `json:"nickname"`
	Gender         string   `json:"gender"`
	Age            *int     `json:"age"`
	Province       string   `json:"province"`
	ProvincePublic bool     `json:"provincePublic"`
	Tags           []string `json:"tags"`
	CleanMode      bool     `json:"cleanMode"`
}

type updateBotRequest struct {
	Label         string `json:"label"`
	CardID        string `json:"cardId"`
	MediaFolderID string `json:"mediaFolderId"`
	Enabled       bool   `json:"enabled"`

	Model           string  `json:"model"`
	Temperature     float32 `json:"temperature"`
	MaxOutputTokens int32   `json:"maxOutputTokens"`
	ServeMatch      bool    `json:"serveMatch"`
	ServeBottle     bool    `json:"serveBottle"`

	MaxActiveMatches int32 `json:"maxActiveMatches"`

	TotalTokenLimit  int64 `json:"totalTokenLimit"`
	TotalReplyLimit  int64 `json:"totalReplyLimit"`
	WindowSeconds    int32 `json:"windowSeconds"`
	WindowTokenLimit int64 `json:"windowTokenLimit"`
	WindowReplyLimit int32 `json:"windowReplyLimit"`

	ConvReplyLimit         int32 `json:"convReplyLimit"`
	ConvMinIntervalSeconds int32 `json:"convMinIntervalSeconds"`
	ConvMediaLimit         int32 `json:"convMediaLimit"`
	ReplyDelayMinMs        int32 `json:"replyDelayMinMs"`
	ReplyDelayMaxMs        int32 `json:"replyDelayMaxMs"`
	TypingMsPerChar        int32 `json:"typingMsPerChar"`
	BottleIntervalSeconds  int32 `json:"bottleIntervalSeconds"`

	Profile updateBotProfileRequest `json:"profile"`
}

// ListBotsHandler 处理 GET /api/admin/bots。
func (h *Handler) ListBotsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListBots(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]botResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toBotResponse(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// CreateBotHandler 处理 POST /api/admin/bots：身份与 bots 行在同一事务里创建。
func (h *Handler) CreateBotHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createBotRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	cardID, err := optionalUUID(req.CardID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "cardId 非法"))
		return
	}
	folderID, err := optionalUUID(req.MediaFolderID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "mediaFolderId 非法"))
		return
	}
	created, err := h.svc.CreateBot(r.Context(), CreateBotInput{
		Label:         req.Label,
		Nickname:      req.Nickname,
		CardID:        cardID,
		MediaFolderID: folderID,
		Enabled:       req.Enabled,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.create", created.ID.String(), map[string]any{"label": created.Label})
	h.invalidate()
	h.writeBot(w, r, http.StatusCreated, created.ID)
}

// UpdateBotHandler 处理 PUT /api/admin/bots/{id}：更新全部设置与对外资料。
func (h *Handler) UpdateBotHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "bot ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req updateBotRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	cardID, err := optionalUUID(req.CardID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "cardId 非法"))
		return
	}
	folderID, err := optionalUUID(req.MediaFolderID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "mediaFolderId 非法"))
		return
	}
	updated, err := h.svc.UpdateBot(r.Context(), id, UpdateBotInput{
		Label:                  req.Label,
		CardID:                 cardID,
		MediaFolderID:          folderID,
		Enabled:                req.Enabled,
		Model:                  req.Model,
		Temperature:            req.Temperature,
		MaxOutputTokens:        req.MaxOutputTokens,
		ServeMatch:             req.ServeMatch,
		ServeBottle:            req.ServeBottle,
		MaxActiveMatches:       req.MaxActiveMatches,
		TotalTokenLimit:        req.TotalTokenLimit,
		TotalReplyLimit:        req.TotalReplyLimit,
		WindowSeconds:          req.WindowSeconds,
		WindowTokenLimit:       req.WindowTokenLimit,
		WindowReplyLimit:       req.WindowReplyLimit,
		ConvReplyLimit:         req.ConvReplyLimit,
		ConvMinIntervalSeconds: req.ConvMinIntervalSeconds,
		ConvMediaLimit:         req.ConvMediaLimit,
		ReplyDelayMinMs:        req.ReplyDelayMinMs,
		ReplyDelayMaxMs:        req.ReplyDelayMaxMs,
		TypingMsPerChar:        req.TypingMsPerChar,
		BottleIntervalSeconds:  req.BottleIntervalSeconds,
		Profile: ProfileInput{
			Nickname:       req.Profile.Nickname,
			Gender:         req.Profile.Gender,
			Age:            req.Profile.Age,
			Province:       req.Profile.Province,
			ProvincePublic: req.Profile.ProvincePublic,
			Tags:           req.Profile.Tags,
			CleanMode:      req.Profile.CleanMode,
		},
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.update", updated.ID.String(), map[string]any{"enabled": updated.Enabled})
	h.invalidate()
	h.writeBot(w, r, http.StatusOK, updated.ID)
}

// ResetUsageHandler 处理 POST /api/admin/bots/{id}/reset-usage。
func (h *Handler) ResetUsageHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "bot ID 非法"))
		return
	}
	reset, err := h.svc.ResetUsage(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.usage.reset", reset.ID.String(), nil)
	h.invalidate()
	h.writeBot(w, r, http.StatusOK, reset.ID)
}

// DeleteBotHandler 处理 DELETE /api/admin/bots/{id}。
func (h *Handler) DeleteBotHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "bot ID 非法"))
		return
	}
	if err := h.svc.DeleteBot(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.delete", id.String(), nil)
	h.invalidate()
	w.WriteHeader(http.StatusNoContent)
}

// ---- 内部工具 ----

// writeBot 用 ListBots 的字段集合输出单个 bot（保证与列表接口结构一致）。
func (h *Handler) writeBot(w http.ResponseWriter, r *http.Request, status int, id pgtype.UUID) {
	row, err := h.svc.BotDetail(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, status, toBotResponse(row))
}

// audit 写一条管理员操作审计日志。
func (h *Handler) audit(r *http.Request, action, target string, detail map[string]any) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok || h.adm == nil {
		return
	}
	h.adm.LogAudit(r.Context(), adminID, action, target, detail)
}

// invalidate 让引擎立即刷新 bot 缓存（engine 为 nil 时什么也不做）。
func (h *Handler) invalidate() {
	if h.engine != nil {
		h.engine.Invalidate()
	}
}

// internalErr 构造 internal 错误。
func internalErr() error { return httpx.NewError(httpx.CodeInternal, "服务器内部错误") }

// parseUUID 解析必填 UUID 字符串。
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, errors.New("UUID 为空")
	}
	return u, nil
}

// optionalUUID 解析可选 UUID 字符串（空串表示未设置）。
func optionalUUID(s string) (pgtype.UUID, error) {
	if s == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUID(s)
}

// uuidPtr 把有效 UUID 转成字符串指针（无效时返回 nil，JSON 里为 null）。
func uuidPtr(id pgtype.UUID) *string {
	if !id.Valid {
		return nil
	}
	v := id.String()
	return &v
}

// formatTS 把时间戳格式化为 RFC3339。
func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
