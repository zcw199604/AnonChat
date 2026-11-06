package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 角色卡字段长度上限（防御性校验，避免超长提示词）。
const (
	maxCardNameRunes  = 40
	maxCardFieldRunes = 4000
	maxBotLabelRunes  = 40
)

// 本包的对外错误。
var (
	// ErrBotNotFound 表示 bot 不存在。
	ErrBotNotFound = httpx.NewError(httpx.CodeNotFound, "bot 不存在")
	// ErrCardNotFound 表示角色卡不存在。
	ErrCardNotFound = httpx.NewError(httpx.CodeNotFound, "角色卡不存在")
)

// ConversationEnder 结束会话并通知对端（由 conversation.Service 实现）。
type ConversationEnder interface {
	End(ctx context.Context, conversationID pgtype.UUID, reason string) error
}

// Service 提供角色卡与 bot 的增删改查以及用量统计（供后台使用）。
type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
	conv   ConversationEnder
}

// Option 用于配置 Service。
type Option func(*Service)

// WithConversation 注入会话服务（删除 bot 时用来结束进行中的匹配会话）。
func WithConversation(c ConversationEnder) Option {
	return func(s *Service) {
		if c != nil {
			s.conv = c
		}
	}
}

// New 创建 bot 服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{pool: pool, q: store.New(pool), logger: logger}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ---- 角色卡 ----

// CardInput 是新建/更新角色卡的字段。
type CardInput struct {
	Name            string
	Description     string
	Personality     string
	Scenario        string
	FirstMessage    string
	ExampleDialogue string
	SystemPrompt    string
}

// validate 校验角色卡（名称 1–40 字，其余字段有长度上限）。
func (in CardInput) validate() error {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > maxCardNameRunes {
		return httpx.NewError(httpx.CodeInvalidRequest, "角色名长度需为 1–40 字")
	}
	fields := []struct {
		label string
		value string
	}{
		{"身份与外貌", in.Description},
		{"性格与说话风格", in.Personality},
		{"场景设定", in.Scenario},
		{"开场白", in.FirstMessage},
		{"示例对话", in.ExampleDialogue},
		{"额外系统指令", in.SystemPrompt},
	}
	for _, f := range fields {
		if utf8.RuneCountInString(strings.TrimSpace(f.value)) > maxCardFieldRunes {
			return httpx.NewError(httpx.CodeInvalidRequest, f.label+" 内容过长")
		}
	}
	return nil
}

// ListCards 返回全部角色卡。
func (s *Service) ListCards(ctx context.Context) ([]store.CharacterCard, error) {
	return s.q.ListCharacterCards(ctx)
}

// CreateCard 新建角色卡。
func (s *Service) CreateCard(ctx context.Context, in CardInput) (store.CharacterCard, error) {
	if err := in.validate(); err != nil {
		return store.CharacterCard{}, err
	}
	return s.q.InsertCharacterCard(ctx, store.InsertCharacterCardParams{
		Name:            strings.TrimSpace(in.Name),
		Description:     strings.TrimSpace(in.Description),
		Personality:     strings.TrimSpace(in.Personality),
		Scenario:        strings.TrimSpace(in.Scenario),
		FirstMessage:    strings.TrimSpace(in.FirstMessage),
		ExampleDialogue: strings.TrimSpace(in.ExampleDialogue),
		SystemPrompt:    strings.TrimSpace(in.SystemPrompt),
	})
}

// UpdateCard 更新角色卡。
func (s *Service) UpdateCard(ctx context.Context, id pgtype.UUID, in CardInput) (store.CharacterCard, error) {
	if err := in.validate(); err != nil {
		return store.CharacterCard{}, err
	}
	card, err := s.q.UpdateCharacterCard(ctx, store.UpdateCharacterCardParams{
		ID:              id,
		Name:            strings.TrimSpace(in.Name),
		Description:     strings.TrimSpace(in.Description),
		Personality:     strings.TrimSpace(in.Personality),
		Scenario:        strings.TrimSpace(in.Scenario),
		FirstMessage:    strings.TrimSpace(in.FirstMessage),
		ExampleDialogue: strings.TrimSpace(in.ExampleDialogue),
		SystemPrompt:    strings.TrimSpace(in.SystemPrompt),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.CharacterCard{}, ErrCardNotFound
		}
		return store.CharacterCard{}, err
	}
	return card, nil
}

// DeleteCard 删除角色卡（引用它的 bot 的 card_id 由外键置空）。
func (s *Service) DeleteCard(ctx context.Context, id pgtype.UUID) error {
	affected, err := s.q.DeleteCharacterCard(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrCardNotFound
	}
	return nil
}

// ---- Bot ----

// CreateBotInput 是新建 bot 的入参（其余设置取库默认值）。
type CreateBotInput struct {
	Label         string
	Nickname      string
	CardID        pgtype.UUID
	MediaFolderID pgtype.UUID
	Enabled       bool
}

// CreateBot 在同一个事务里创建匿名身份与 bots 行（how.md §10）。
func (s *Service) CreateBot(ctx context.Context, in CreateBotInput) (store.Bot, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" || utf8.RuneCountInString(label) > maxBotLabelRunes {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, "备注名长度需为 1–40 字")
	}
	nickname, msg := normalizeBotNickname(in.Nickname)
	if msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	token, err := identity.GenerateToken()
	if err != nil {
		return store.Bot{}, err
	}
	publicCode, err := identity.NewPublicCode()
	if err != nil {
		return store.Bot{}, err
	}
	if nickname == "" {
		nickname = identity.GenerateNickname()
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Bot{}, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	ident, err := q.CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash:  identity.HashToken(token),
		Nickname:   nickname,
		AvatarSeed: identity.GenerateAvatarSeed(),
		PublicCode: publicCode,
	})
	if err != nil {
		return store.Bot{}, err
	}
	created, err := q.InsertBot(ctx, store.InsertBotParams{
		IdentityID:    ident.ID,
		Label:         label,
		CardID:        in.CardID,
		MediaFolderID: in.MediaFolderID,
		Enabled:       in.Enabled,
	})
	if err != nil {
		return store.Bot{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Bot{}, err
	}
	return created, nil
}

// ListBots 返回后台列表（设置 + 对外资料 + 用量）。
func (s *Service) ListBots(ctx context.Context) ([]store.ListBotsRow, error) {
	return s.q.ListBots(ctx)
}

// BotDetail 返回单个 bot 的后台视图（字段与 ListBots 一致，含用量与外资料）。
func (s *Service) BotDetail(ctx context.Context, id pgtype.UUID) (store.ListBotsRow, error) {
	rows, err := s.q.ListBots(ctx)
	if err != nil {
		return store.ListBotsRow{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return row, nil
		}
	}
	return store.ListBotsRow{}, ErrBotNotFound
}

// UpdateBotInput 是更新 bot 的全部设置与对外资料。
type UpdateBotInput struct {
	Label         string
	CardID        pgtype.UUID
	MediaFolderID pgtype.UUID
	Enabled       bool

	Model           string
	Temperature     float32
	MaxOutputTokens int32
	ServeMatch      bool
	ServeBottle     bool

	MaxActiveMatches int32

	TotalTokenLimit  int64
	TotalReplyLimit  int64
	WindowSeconds    int32
	WindowTokenLimit int64
	WindowReplyLimit int32

	ConvReplyLimit         int32
	ConvMinIntervalSeconds int32
	ConvMediaLimit         int32
	ReplyDelayMinMs        int32
	ReplyDelayMaxMs        int32
	TypingMsPerChar        int32
	BottleIntervalSeconds  int32

	// 对外资料（与真人的资料字段完全一致）。
	Profile ProfileInput
}

// ProfileInput 是 bot 的对外资料。
type ProfileInput struct {
	Nickname       string
	Gender         string
	Age            *int
	Province       string
	ProvincePublic bool
	Tags           []string
	CleanMode      bool
}

// UpdateBot 更新 bot 设置与它对外的身份资料（同一事务）。
func (s *Service) UpdateBot(ctx context.Context, id pgtype.UUID, in UpdateBotInput) (store.Bot, error) {
	label := strings.TrimSpace(in.Label)
	if label == "" || utf8.RuneCountInString(label) > maxBotLabelRunes {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, "备注名长度需为 1–40 字")
	}
	nickname, msg := normalizeBotNickname(in.Profile.Nickname)
	if msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if msg := profile.ValidateGender(in.Profile.Gender); msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if msg := profile.ValidateAge(in.Profile.Age); msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if msg := profile.ValidateProvince(in.Profile.Province); msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if msg := profile.ValidateTags(in.Profile.Tags); msg != "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if in.Profile.ProvincePublic && strings.TrimSpace(in.Profile.Province) == "" {
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, "先选择省份才能公开")
	}

	existing, err := s.q.GetBot(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Bot{}, ErrBotNotFound
		}
		return store.Bot{}, err
	}

	var age *int16
	if in.Profile.Age != nil {
		v := int16(*in.Profile.Age)
		age = &v
	}
	var province *string
	if p := strings.TrimSpace(in.Profile.Province); p != "" {
		province = &p
	}
	tags := in.Profile.Tags
	if tags == nil {
		tags = []string{}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Bot{}, err
	}
	defer tx.Rollback(ctx)
	q := s.q.WithTx(tx)

	updated, err := q.UpdateBot(ctx, store.UpdateBotParams{
		ID:                     id,
		Label:                  label,
		CardID:                 in.CardID,
		MediaFolderID:          in.MediaFolderID,
		Enabled:                in.Enabled,
		Model:                  strings.TrimSpace(in.Model),
		Temperature:            in.Temperature,
		MaxOutputTokens:        in.MaxOutputTokens,
		ServeMatch:             in.ServeMatch,
		ServeBottle:            in.ServeBottle,
		MaxActiveMatches:       in.MaxActiveMatches,
		TotalTokenLimit:        in.TotalTokenLimit,
		TotalReplyLimit:        in.TotalReplyLimit,
		WindowSeconds:          in.WindowSeconds,
		WindowTokenLimit:       in.WindowTokenLimit,
		WindowReplyLimit:       in.WindowReplyLimit,
		ConvReplyLimit:         in.ConvReplyLimit,
		ConvMinIntervalSeconds: in.ConvMinIntervalSeconds,
		ConvMediaLimit:         in.ConvMediaLimit,
		ReplyDelayMinMs:        in.ReplyDelayMinMs,
		ReplyDelayMaxMs:        in.ReplyDelayMaxMs,
		TypingMsPerChar:        in.TypingMsPerChar,
		BottleIntervalSeconds:  in.BottleIntervalSeconds,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Bot{}, ErrBotNotFound
		}
		return store.Bot{}, httpx.NewError(httpx.CodeInvalidRequest, "bot 设置超出允许范围")
	}

	// 对外资料写进 identities 的现有列（与真人共用同一套字段）。
	if _, err := q.UpdateIdentityProfile(ctx, store.UpdateIdentityProfileParams{
		ID:                existing.IdentityID,
		Gender:            in.Profile.Gender,
		Age:               age,
		Province:          province,
		ProvincePublic:    in.Profile.ProvincePublic,
		Tags:              tags,
		CleanMode:         in.Profile.CleanMode,
		MatchGender:       "any",
		MatchSameProvince: false,
	}); err != nil {
		return store.Bot{}, err
	}
	if nickname != "" {
		if _, err := q.UpdateIdentityNickname(ctx, store.UpdateIdentityNicknameParams{
			ID:       existing.IdentityID,
			Nickname: nickname,
		}); err != nil {
			return store.Bot{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Bot{}, err
	}
	return updated, nil
}

// ResetUsage 清零累计用量。
func (s *Service) ResetUsage(ctx context.Context, id pgtype.UUID) (store.Bot, error) {
	bot, err := s.q.ResetBotUsage(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Bot{}, ErrBotNotFound
		}
		return store.Bot{}, err
	}
	return bot, nil
}

// DeleteBot 先结束该 bot 所有进行中的匹配会话（peer_left），再删除 bots 行并吊销身份。
func (s *Service) DeleteBot(ctx context.Context, id pgtype.UUID) error {
	bot, err := s.q.GetBot(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBotNotFound
		}
		return err
	}
	// 走 conversation.Service.End，才能给对端推送 match.ended。
	if s.conv != nil {
		convs, cerr := s.q.ListOpenMatchConversationsForIdentity(ctx, bot.IdentityID)
		if cerr != nil {
			return cerr
		}
		for _, convID := range convs {
			if eerr := s.conv.End(ctx, convID, "peer_left"); eerr != nil {
				s.logger.Warn("结束 bot 匹配会话失败", "conversation_id", convID.String(), "error", eerr)
			}
		}
	}
	affected, err := s.q.DeleteBot(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrBotNotFound
	}
	// bot 的 identity 不会被身份清理任务删除，删除 bot 时显式吊销。
	if err := s.q.RevokeIdentity(ctx, bot.IdentityID); err != nil {
		return err
	}
	return nil
}

// normalizeBotNickname 校验并返回去掉首尾空白的昵称（空串表示使用随机昵称）。
func normalizeBotNickname(raw string) (string, string) {
	if strings.TrimSpace(raw) == "" {
		return "", ""
	}
	return profile.NormalizeNickname(raw)
}
