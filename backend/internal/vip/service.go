// Package vip 负责 VIP 权益与激活码：生成、规范化、哈希、兑换（事务 + 行锁，并发安全）、
// 管理后台批次管理（how.md 3.3）。
package vip

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 激活码格式：16 位 Crockford Base32（80 bit），展示为 XXXX-XXXX-XXXX-XXXX。
const (
	codeLength = 16
	groupSize  = 4
)

// crockford 是 Crockford Base32 字符集（去掉 I、L、O、U）。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// 兑换失败统一错误码，不区分不存在 / 已用 / 已撤销 / 已过期（防止探测）。
var errInvalidCode = httpx.NewError(httpx.CodeInvalidCode, "激活码无效或已使用")

// AccessLogger 记录兑换等敏感操作（生产实现为 identity.Service.LogAccess）。
type AccessLogger interface {
	LogAccess(ctx context.Context, identityID pgtype.UUID, event, ip, userAgent string)
}

// Service 提供激活码与 VIP 权益操作。
type Service struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
	perID  moderation.RateLimiter
	perIP  moderation.RateLimiter
	access AccessLogger
	now    func() time.Time
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithLimiters 注入兑换限流器（每身份、每 IP）。
func WithLimiters(perID, perIP moderation.RateLimiter) Option {
	return func(s *Service) {
		if perID != nil {
			s.perID = perID
		}
		if perIP != nil {
			s.perIP = perIP
		}
	}
}

// WithAccessLogger 注入访问审计日志写入器。
func WithAccessLogger(l AccessLogger) Option {
	return func(s *Service) {
		if l != nil {
			s.access = l
		}
	}
}

// New 创建 VIP 服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		pool:   pool,
		q:      store.New(pool),
		logger: logger,
		perID:  moderation.AllowAll(),
		perIP:  moderation.AllowAll(),
		now:    time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// IsVIP 判断身份在给定时刻是否 VIP（只看 vip_until > now）。
func IsVIP(ident *store.Identity, now time.Time) bool {
	return ident.VipUntil.Valid && ident.VipUntil.Time.After(now)
}

// NormalizeCode 规范化激活码：去空格与连字符、转大写、O→0、I/L→1。
func NormalizeCode(raw string) string {
	s := strings.ToUpper(strings.TrimSpace(raw))
	replacer := strings.NewReplacer("-", "", " ", "", "\t", "", "O", "0", "I", "1", "L", "1")
	return replacer.Replace(s)
}

// HashCode 返回规范化后激活码的 sha256（库里只存哈希）。
func HashCode(normalized string) []byte {
	sum := sha256.Sum256([]byte(normalized))
	return sum[:]
}

// GenerateCode 生成一个 16 位 Crockford Base32 激活码，返回格式化的明文（XXXX-XXXX-XXXX-XXXX）。
func GenerateCode() (string, error) {
	b := make([]byte, codeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, 0, codeLength+groupSize-1)
	for i, v := range b {
		if i > 0 && i%groupSize == 0 {
			out = append(out, '-')
		}
		out = append(out, crockford[int(v)%len(crockford)])
	}
	return string(out), nil
}

// RedeemResult 是兑换结果。
type RedeemResult struct {
	Identity store.Identity
}

// Redeem 兑换激活码：事务内 SELECT … FOR UPDATE，保证并发兑换同一个码只成功一次。
func (s *Service) Redeem(ctx context.Context, ident *store.Identity, rawCode, clientIP, userAgent string) (store.Identity, error) {
	if !s.perID.Allow(ctx, "vip_redeem:"+ident.ID.String()) || !s.perIP.Allow(ctx, "vip_redeem_ip:"+clientIP) {
		return store.Identity{}, httpx.NewError(httpx.CodeRateLimited, "尝试过于频繁，请稍后再试")
	}
	normalized := NormalizeCode(rawCode)
	if len(normalized) != codeLength {
		return store.Identity{}, errInvalidCode
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.Identity{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	code, err := q.LockActivationCode(ctx, HashCode(normalized))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Identity{}, errInvalidCode
		}
		return store.Identity{}, err
	}
	if code.RedeemedAt.Valid || code.RevokedAt.Valid {
		return store.Identity{}, errInvalidCode
	}
	batch, err := q.GetActivationBatch(ctx, code.BatchID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Identity{}, errInvalidCode
		}
		return store.Identity{}, err
	}
	if !batch.ExpiresAt.Time.After(s.now()) {
		return store.Identity{}, errInvalidCode
	}

	affected, err := q.RedeemActivationCode(ctx, store.RedeemActivationCodeParams{
		CodeHash:   HashCode(normalized),
		RedeemedBy: ident.ID,
	})
	if err != nil {
		return store.Identity{}, err
	}
	if affected == 0 {
		return store.Identity{}, errInvalidCode
	}

	updated, err := q.ExtendIdentityVip(ctx, store.ExtendIdentityVipParams{
		ID:      ident.ID,
		VipDays: batch.VipDays,
		Cards:   batch.BroadcastCards,
	})
	if err != nil {
		return store.Identity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.Identity{}, err
	}

	if s.access != nil {
		s.access.LogAccess(ctx, ident.ID, "vip_redeem", clientIP, userAgent)
	}
	return updated, nil
}

// Batch 是管理后台的激活码批次。
type Batch struct {
	ID             pgtype.UUID
	Label          string
	VipDays        int32
	BroadcastCards int32
	CodeCount      int32
	ExpiresAt      time.Time
	CreatedAt      time.Time
	Total          int64
	Redeemed       int64
	Revoked        int64
}

// CreateBatchInput 是创建批次的入参。
type CreateBatchInput struct {
	Label          string
	VipDays        int32
	BroadcastCards int32
	Count          int32
	ExpiresAt      time.Time
	CreatedBy      pgtype.UUID
}

// CreateBatch 创建批次并生成 count 个激活码，返回明文（只出现这一次）。
func (s *Service) CreateBatch(ctx context.Context, in CreateBatchInput) (Batch, []string, error) {
	if in.Label == "" || len([]rune(in.Label)) > 60 {
		return Batch{}, nil, httpx.NewError(httpx.CodeInvalidRequest, "批次名称需 1–60 个字符")
	}
	if in.Count < 1 || in.Count > 1000 {
		return Batch{}, nil, httpx.NewError(httpx.CodeInvalidRequest, "数量需在 1–1000 之间")
	}
	if in.VipDays < 0 || in.VipDays > 366 || in.BroadcastCards < 0 || in.BroadcastCards > 100 {
		return Batch{}, nil, httpx.NewError(httpx.CodeInvalidRequest, "VIP 天数或广播卡数量超出范围")
	}
	if in.VipDays == 0 && in.BroadcastCards == 0 {
		return Batch{}, nil, httpx.NewError(httpx.CodeInvalidRequest, "VIP 天数与广播卡不能同时为 0")
	}
	if !in.ExpiresAt.After(s.now()) {
		return Batch{}, nil, httpx.NewError(httpx.CodeInvalidRequest, "兑换截止时间必须在将来")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Batch{}, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	row, err := q.CreateActivationBatch(ctx, store.CreateActivationBatchParams{
		Label:          in.Label,
		VipDays:        in.VipDays,
		BroadcastCards: in.BroadcastCards,
		CodeCount:      in.Count,
		ExpiresAt:      pgtype.Timestamptz{Time: in.ExpiresAt, Valid: true},
		CreatedBy:      in.CreatedBy,
	})
	if err != nil {
		return Batch{}, nil, err
	}
	codes := make([]string, 0, in.Count)
	for i := int32(0); i < in.Count; i++ {
		code, err := GenerateCode()
		if err != nil {
			return Batch{}, nil, err
		}
		if err := q.InsertActivationCode(ctx, store.InsertActivationCodeParams{
			CodeHash: HashCode(NormalizeCode(code)),
			BatchID:  row.ID,
		}); err != nil {
			return Batch{}, nil, err
		}
		codes = append(codes, code)
	}
	if err := tx.Commit(ctx); err != nil {
		return Batch{}, nil, err
	}
	return Batch{
		ID:             row.ID,
		Label:          row.Label,
		VipDays:        row.VipDays,
		BroadcastCards: row.BroadcastCards,
		CodeCount:      row.CodeCount,
		ExpiresAt:      row.ExpiresAt.Time,
		CreatedAt:      row.CreatedAt.Time,
		Total:          int64(in.Count),
	}, codes, nil
}

// ListBatches 列出批次（含已兑换/已撤销统计）。
func (s *Service) ListBatches(ctx context.Context, limit int32) ([]Batch, error) {
	rows, err := s.q.ListActivationBatches(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Batch, 0, len(rows))
	for _, r := range rows {
		out = append(out, Batch{
			ID:             r.ID,
			Label:          r.Label,
			VipDays:        r.VipDays,
			BroadcastCards: r.BroadcastCards,
			CodeCount:      r.CodeCount,
			ExpiresAt:      r.ExpiresAt.Time,
			CreatedAt:      r.CreatedAt.Time,
			Total:          r.Total,
			Redeemed:       r.Redeemed,
			Revoked:        r.Revoked,
		})
	}
	return out, nil
}

// RevokeBatch 撤销批次内所有未兑换的码。
func (s *Service) RevokeBatch(ctx context.Context, batchID pgtype.UUID) (int64, error) {
	if _, err := s.q.GetActivationBatch(ctx, batchID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, httpx.NewError(httpx.CodeNotFound, "批次不存在")
		}
		return 0, err
	}
	return s.q.RevokeBatchCodes(ctx, batchID)
}

// FormatCodesCSV 把明文激活码拼成 CSV（每行一个码），供管理后台下载。
func FormatCodesCSV(codes []string) string {
	var b strings.Builder
	b.WriteString("code\n")
	for _, c := range codes {
		b.WriteString(c)
		b.WriteString("\n")
	}
	return b.String()
}
