// Package verify 负责手机号实名验证：验证码发送、校验、手机号加密存储与实名中间件。
package verify

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	// codeTTL 是验证码有效期。
	codeTTL = 5 * time.Minute
	// maxAttempts 是验证码最多可尝试次数。
	maxAttempts = 5
	// eventVerify 是验证成功写入 access_logs 的事件名。
	eventVerify = "verify"
	// KeyLen 是手机号加密与 HMAC 密钥的字节长度。
	KeyLen = 32
)

// Service 提供实名验证相关的领域操作。
type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
	sms    SMSProvider

	encKey  []byte
	hmacKey []byte

	now         func() time.Time
	codeTTL     time.Duration
	maxAttempts int

	phoneCooldown moderation.RateLimiter // 每手机号 60 秒 1 次
	phoneDaily    moderation.RateLimiter // 每手机号每天 5 次
	ipDaily       moderation.RateLimiter // 每 IP 每天 20 次
}

// Option 用于配置 Service（主要供测试注入）。
type Option func(*Service)

// WithClock 注入时钟。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithLimiters 覆盖默认限流器（测试可注入 AllowAll）。
func WithLimiters(cooldown, phoneDaily, ipDaily moderation.RateLimiter) Option {
	return func(s *Service) {
		if cooldown != nil {
			s.phoneCooldown = cooldown
		}
		if phoneDaily != nil {
			s.phoneDaily = phoneDaily
		}
		if ipDaily != nil {
			s.ipDaily = ipDaily
		}
	}
}

// NewService 创建实名验证服务。加密与 HMAC 密钥必须均为 32 字节，否则返回错误，
// 避免以空密钥（等同无加密）运行。
func NewService(pool *pgxpool.Pool, logger *slog.Logger, sms SMSProvider, encKey, hmacKey []byte, opts ...Option) (*Service, error) {
	if len(encKey) != KeyLen {
		return nil, fmt.Errorf("PHONE_ENC_KEY 必须为 %d 字节，实际 %d", KeyLen, len(encKey))
	}
	if len(hmacKey) != KeyLen {
		return nil, fmt.Errorf("PHONE_HMAC_KEY 必须为 %d 字节，实际 %d", KeyLen, len(hmacKey))
	}
	s := &Service{
		pool:          pool,
		q:             store.New(pool),
		logger:        logger,
		sms:           sms,
		encKey:        encKey,
		hmacKey:       hmacKey,
		now:           time.Now,
		codeTTL:       codeTTL,
		maxAttempts:   maxAttempts,
		phoneCooldown: moderation.NewFixedWindow(1, time.Minute),
		phoneDaily:    moderation.NewFixedWindow(5, 24*time.Hour),
		ipDaily:       moderation.NewFixedWindow(20, 24*time.Hour),
	}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// SendCode 规范化号码、检查频率、生成并保存验证码，然后通过 SMSProvider 发送。
func (s *Service) SendCode(ctx context.Context, phoneRaw, ip string) error {
	norm, err := NormalizePhone(phoneRaw)
	if err != nil {
		return invalidRequest("手机号格式不正确")
	}
	phoneHmac := HMACSHA256(s.hmacKey, []byte(norm))
	key := hex.EncodeToString(phoneHmac)

	if !s.phoneCooldown.Allow(ctx, "sms_cool:"+key) ||
		!s.phoneDaily.Allow(ctx, "sms_day:"+key) ||
		!s.ipDaily.Allow(ctx, "sms_ip:"+ip) {
		return httpx.NewError(httpx.CodeRateLimited, "验证码发送过于频繁，请稍后再试")
	}

	code, err := GenerateCode()
	if err != nil {
		return err
	}
	expires := s.now().Add(s.codeTTL)
	if _, err := s.q.InsertSmsCode(ctx, store.InsertSmsCodeParams{
		PhoneHmac: phoneHmac,
		CodeHash:  HashCode(s.hmacKey, phoneHmac, code),
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return err
	}

	if err := s.sms.Send(ctx, norm, code); err != nil {
		// 日志中不写入手机号明文。
		s.logger.Error("发送短信失败", "error", err)
		return httpx.NewError(httpx.CodeInternal, "发送验证码失败")
	}
	return nil
}

// ConfirmCode 校验验证码，成功后把实名账号绑定到当前身份。
// 为避免并发绕过尝试次数上限，先用一条原子 SQL 占用一次尝试机会；
// 未占用到行说明已过期/已消费/次数用尽。消费、upsert 实名账号、绑定身份在同一事务内完成，
// 且消费使用 where consumed_at is null 并检查影响行数，确保同一验证码只能成功一次。
func (s *Service) ConfirmCode(ctx context.Context, phoneRaw, code string, ident *store.Identity, ip string) error {
	norm, err := NormalizePhone(phoneRaw)
	if err != nil {
		return invalidRequest("手机号格式不正确")
	}
	phoneHmac := HMACSHA256(s.hmacKey, []byte(norm))

	row, err := s.q.GetLatestSmsCode(ctx, phoneHmac)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidRequest("验证码错误或已过期")
		}
		return err
	}

	// 原子占用一次尝试机会（同时校验未过期、未消费、次数未达上限）。
	attempt, err := s.q.ConsumeSmsAttempt(ctx, store.ConsumeSmsAttemptParams{
		ID:       row.ID,
		Attempts: int32(s.maxAttempts),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalidRequest("验证码错误或已过期")
		}
		return err
	}

	if !SecureEqual(HashCode(s.hmacKey, phoneHmac, code), attempt.CodeHash) {
		return invalidRequest("验证码错误")
	}

	// 消费 + upsert 实名账号 + 绑定身份，放在同一事务中。
	enc, err := Encrypt(s.encKey, []byte(norm))
	if err != nil {
		return err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	affected, err := q.ConsumeSmsCode(ctx, attempt.ID)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 已被并发的另一次请求消费，或状态已变化。
		return invalidRequest("验证码错误或已过期")
	}

	acct, err := q.UpsertVerifiedAccount(ctx, store.UpsertVerifiedAccountParams{
		PhoneHmac:   phoneHmac,
		PhoneEnc:    enc,
		PhoneMasked: MaskPhone(norm),
	})
	if err != nil {
		return err
	}
	if err := q.SetIdentityAccount(ctx, store.SetIdentityAccountParams{
		ID:        ident.ID,
		AccountID: acct.ID,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	s.writeAccessLog(ctx, ident.ID, eventVerify, ip)
	return nil
}

// writeAccessLog 写入验证审计日志。
func (s *Service) writeAccessLog(ctx context.Context, identityID pgtype.UUID, event, ip string) {
	var addr *netip.Addr
	if ip != "" {
		if parsed, err := netip.ParseAddr(ip); err == nil {
			addr = &parsed
		}
	}
	arg := store.InsertAccessLogParams{IdentityID: identityID, Event: event, Ip: addr}
	if err := s.q.InsertAccessLog(ctx, arg); err != nil {
		s.logger.Warn("写入 access_logs 失败", "event", event, "error", err)
	}
}

// invalidRequest 构造 invalid_request 错误。
func invalidRequest(msg string) error {
	return httpx.NewError(httpx.CodeInvalidRequest, msg)
}
