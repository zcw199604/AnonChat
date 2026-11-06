package profile

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// Patch 是 PATCH /api/identity/profile 的入参，用出现标记支持部分更新
// （区分「未提供」与「显式置空」）。
type Patch struct {
	Gender         *string
	Age            *int
	Province       *string
	ProvincePublic *bool
	Tags           *[]string
	CleanMode      *bool
	MatchGender    *string
	SameProvince   *bool

	// ageNull 表示 age 被显式置空（JSON null），与「未提供」区分。
	ageNull bool
}

// UnmarshalJSON 解析部分更新载荷，记录每个字段是否出现。
func (p *Patch) UnmarshalJSON(b []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		switch k {
		case "gender":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			p.Gender = &s
		case "age":
			if string(v) == "null" {
				var zero int
				p.Age = &zero
				p.ageNull = true
				continue
			}
			var n int
			if err := json.Unmarshal(v, &n); err != nil {
				return err
			}
			p.Age = &n
		case "province":
			if string(v) == "null" {
				empty := ""
				p.Province = &empty
				continue
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return err
			}
			p.Province = &s
		case "provincePublic":
			var bv bool
			if err := json.Unmarshal(v, &bv); err != nil {
				return err
			}
			p.ProvincePublic = &bv
		case "tags":
			var tags []string
			if err := json.Unmarshal(v, &tags); err != nil {
				return err
			}
			p.Tags = &tags
		case "cleanMode":
			var bv bool
			if err := json.Unmarshal(v, &bv); err != nil {
				return err
			}
			p.CleanMode = &bv
		case "matchPrefs":
			var mp struct {
				Gender       *string `json:"gender"`
				SameProvince *bool   `json:"sameProvince"`
			}
			if err := json.Unmarshal(v, &mp); err != nil {
				return err
			}
			if mp.Gender != nil {
				p.MatchGender = mp.Gender
			}
			if mp.SameProvince != nil {
				p.SameProvince = mp.SameProvince
			}
		default:
			return errors.New("未知字段: " + k)
		}
	}
	return nil
}

// Filter 是内容过滤器接口（实现对 moderation.Moderator）。
type Filter interface {
	Filter(ctx context.Context, text string) (string, bool)
}

// Service 提供资料相关的写操作。
type Service struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	logger  *slog.Logger
	filter  Filter
	shuffle moderation.RateLimiter
	setNick moderation.RateLimiter
	now     func() time.Time
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithFilter 注入敏感词过滤器（自定义昵称需过违禁词）。
func WithFilter(f Filter) Option {
	return func(s *Service) {
		if f != nil {
			s.filter = f
		}
	}
}

// WithNicknameLimiters 注入随机/自定义昵称限流器。
func WithNicknameLimiters(shuffle, set moderation.RateLimiter) Option {
	return func(s *Service) {
		if shuffle != nil {
			s.shuffle = shuffle
		}
		if set != nil {
			s.setNick = set
		}
	}
}

// New 创建资料服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		pool:    pool,
		q:       store.New(pool),
		logger:  logger,
		shuffle: moderation.AllowAll(),
		setNick: moderation.AllowAll(),
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Update 校验并部分更新资料与匹配偏好，返回最新身份。
func (s *Service) Update(ctx context.Context, ident *store.Identity, p Patch) (store.Identity, error) {
	gender := ident.Gender
	age := ident.Age
	province := ident.Province
	provincePublic := ident.ProvincePublic
	tags := ident.Tags
	cleanMode := ident.CleanMode
	matchGender := ident.MatchGender
	sameProvince := ident.MatchSameProvince

	if p.Gender != nil {
		if msg := ValidateGender(*p.Gender); msg != "" {
			return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
		}
		gender = *p.Gender
	}
	if p.Age != nil {
		if p.ageNull {
			age = nil
		} else {
			if msg := ValidateAge(p.Age); msg != "" {
				return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
			}
			v := int16(*p.Age)
			age = &v
		}
	}
	if p.Province != nil {
		if msg := ValidateProvince(*p.Province); msg != "" {
			return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
		}
		if *p.Province == "" {
			province = nil
			provincePublic = false
		} else {
			v := *p.Province
			province = &v
		}
	}
	if p.ProvincePublic != nil {
		if *p.ProvincePublic && (province == nil || *province == "") {
			return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, "先选择省份才能公开")
		}
		provincePublic = *p.ProvincePublic
	}
	if p.Tags != nil {
		if msg := ValidateTags(*p.Tags); msg != "" {
			return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
		}
		tags = *p.Tags
	}
	if p.CleanMode != nil {
		cleanMode = *p.CleanMode
	}

	// 匹配偏好是 VIP 权益：非 VIP 一律拒绝（服务端判定，不信任前端）。
	if p.MatchGender != nil || p.SameProvince != nil {
		if !isVIP(ident, s.now()) {
			return store.Identity{}, httpx.NewError(httpx.CodeVIPRequired, "匹配偏好是 VIP 特权，请先兑换激活码")
		}
	}
	if p.MatchGender != nil {
		if msg := ValidateMatchGender(*p.MatchGender); msg != "" {
			return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
		}
		matchGender = *p.MatchGender
	}
	if p.SameProvince != nil {
		sameProvince = *p.SameProvince
	}

	if tags == nil {
		tags = []string{}
	}
	updated, err := s.q.UpdateIdentityProfile(ctx, store.UpdateIdentityProfileParams{
		ID:                ident.ID,
		Gender:            gender,
		Age:               age,
		Province:          province,
		ProvincePublic:    provincePublic,
		Tags:              tags,
		CleanMode:         cleanMode,
		MatchGender:       matchGender,
		MatchSameProvince: sameProvince,
	})
	if err != nil {
		return store.Identity{}, err
	}
	return updated, nil
}

// ShuffleNickname 生成并保存一个随机昵称。
func (s *Service) ShuffleNickname(ctx context.Context, ident *store.Identity) (store.Identity, error) {
	if !s.shuffle.Allow(ctx, "nickname_shuffle:"+ident.ID.String()) {
		return store.Identity{}, httpx.NewError(httpx.CodeRateLimited, "换昵称太频繁了，请稍后再试")
	}
	return s.q.UpdateIdentityNickname(ctx, store.UpdateIdentityNicknameParams{
		ID:       ident.ID,
		Nickname: identity.GenerateNickname(),
	})
}

// SetNickname 设置自定义昵称：2–12 字、去掉首尾空白、禁止控制字符、过违禁词。
func (s *Service) SetNickname(ctx context.Context, ident *store.Identity, raw string) (store.Identity, error) {
	if !s.setNick.Allow(ctx, "nickname_set:"+ident.ID.String()) {
		return store.Identity{}, httpx.NewError(httpx.CodeRateLimited, "今天改昵称的次数已用完")
	}
	nickname, msg := NormalizeNickname(raw)
	if msg != "" {
		return store.Identity{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	// 违禁词：blocked 时拒绝，否则保存打码后的昵称（审查意见 R1）。
	if s.filter != nil {
		masked, blocked := s.filter.Filter(ctx, nickname)
		if blocked {
			return store.Identity{}, httpx.NewError(httpx.CodeContentBlocked, "昵称包含违规信息")
		}
		nickname = masked
	}
	return s.q.UpdateIdentityNickname(ctx, store.UpdateIdentityNicknameParams{
		ID:       ident.ID,
		Nickname: nickname,
	})
}

// isVIP 是本地最小判定（只看 vip_until > now），避免与 internal/vip 形成循环依赖。
func isVIP(ident *store.Identity, now time.Time) bool {
	return ident.VipUntil.Valid && ident.VipUntil.Time.After(now)
}
