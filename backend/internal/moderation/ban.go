package moderation

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// 封禁相关常量。
const (
	banCacheTTL   = 30 * time.Second
	kickReasonBan = "banned"
	// banCleanEvery / banCacheSweep 控制缓存过期条目清理频率，避免只增不减。
	banCleanEvery = 1024
	banCacheSweep = time.Minute
)

// 封禁主体类型。
const (
	SubjectIdentity = "identity"
	SubjectIP       = "ip"
	SubjectAccount  = "account"
)

// BanSubject 是一条封禁主体（类型 + 值）。
type BanSubject struct {
	Type  string
	Value string
}

// Kicker 负责踢掉在线连接（由 realtime.Hub 实现）。
type Kicker interface {
	KickIdentity(identityID pgtype.UUID, reason string)
	KickIP(ip string, reason string)
	KickAccount(accountID pgtype.UUID, reason string)
}

type banCacheEntry struct {
	banned bool
	until  time.Time
}

// BanService 负责封禁的创建、撤销与检查，带 30 秒缓存。
type BanService struct {
	autoLocks [64]sync.Mutex
	q         *store.Queries
	kicker    Kicker
	now       func() time.Time

	mu        sync.Mutex
	cache     map[string]banCacheEntry
	queries   int
	lastSweep time.Time
}

// BanOption 配置 BanService。
type BanOption func(*BanService)

// WithBanClock 注入时钟（测试用）。
func WithBanClock(fn func() time.Time) BanOption {
	return func(b *BanService) {
		if fn != nil {
			b.now = fn
		}
	}
}

// NewBanService 创建封禁服务。kicker 为 nil 时不踢连接。
func NewBanService(pool *pgxpool.Pool, kicker Kicker, opts ...BanOption) *BanService {
	b := &BanService{
		q:      store.New(pool),
		kicker: kicker,
		now:    time.Now,
		cache:  make(map[string]banCacheEntry),
	}
	for _, o := range opts {
		o(b)
	}
	b.lastSweep = b.now()
	return b
}

// Create 创建封禁（可多个主体）。duration<=0 表示永久。返回创建的封禁记录。
func (b *BanService) Create(ctx context.Context, subjects []BanSubject, duration time.Duration, reason string, createdBy pgtype.UUID) ([]store.Ban, error) {
	now := b.now()
	var expires pgtype.Timestamptz
	if duration > 0 {
		expires = pgtype.Timestamptz{Time: now.Add(duration), Valid: true}
	}
	out := make([]store.Ban, 0, len(subjects))
	for _, s := range subjects {
		if err := validateSubject(s); err != nil {
			return nil, err
		}
		ban, err := b.q.InsertBan(ctx, store.InsertBanParams{
			SubjectType:  s.Type,
			SubjectValue: s.Value,
			Reason:       reason,
			ExpiresAt:    expires,
			CreatedBy:    createdBy,
		})
		if err != nil {
			return nil, err
		}
		b.invalidate(s.Type, s.Value)
		b.kick(s)
		out = append(out, ban)
	}
	return out, nil
}

// Revoke 撤销一个封禁并失效缓存。
func (b *BanService) Revoke(ctx context.Context, id pgtype.UUID) error {
	ban, err := b.q.GetBan(ctx, id)
	if err != nil {
		return err
	}
	if _, err := b.q.RevokeBan(ctx, id); err != nil {
		return err
	}
	b.invalidate(ban.SubjectType, ban.SubjectValue)
	return nil
}

// IsBanned 判断身份/账号/IP 是否被封禁。ident 可为 nil（仅查 IP）。
func (b *BanService) IsBanned(ctx context.Context, ident *store.Identity, ip string) bool {
	if ident != nil {
		if b.isSubjectBanned(ctx, SubjectIdentity, ident.ID.String()) {
			return true
		}
		if ident.AccountID.Valid && b.isSubjectBanned(ctx, SubjectAccount, ident.AccountID.String()) {
			return true
		}
	}
	if ip != "" && b.isSubjectBanned(ctx, SubjectIP, ip) {
		return true
	}
	return false
}

// IsIPBanned 判断某 IP 是否被封禁（供创建身份前检查）。
func (b *BanService) IsIPBanned(ctx context.Context, ip string) bool {
	return b.isSubjectBanned(ctx, SubjectIP, ip)
}

// isSubjectBanned 带缓存地查询某主体是否处于有效封禁中。
func (b *BanService) isSubjectBanned(ctx context.Context, subjectType, value string) bool {
	if value == "" {
		return false
	}
	now := b.now()
	key := subjectType + "|" + value

	b.mu.Lock()
	b.queries++
	if b.queries%banCleanEvery == 0 || now.Sub(b.lastSweep) >= banCacheSweep {
		b.purgeLocked(now)
	}
	if e, ok := b.cache[key]; ok && now.Before(e.until) {
		b.mu.Unlock()
		return e.banned
	}
	b.mu.Unlock()

	ban, err := b.q.GetActiveBan(ctx, store.GetActiveBanParams{
		SubjectType:  subjectType,
		SubjectValue: value,
		ExpiresAt:    pgtype.Timestamptz{Time: now, Valid: true},
	})
	banned := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		// 查询失败时不缓存，按未封禁处理（避免误伤）。
		return false
	}

	until := now.Add(banCacheTTL)
	if banned && ban.ExpiresAt.Valid && ban.ExpiresAt.Time.Before(until) {
		until = ban.ExpiresAt.Time
	}
	b.mu.Lock()
	b.cache[key] = banCacheEntry{banned: banned, until: until}
	b.mu.Unlock()
	return banned
}

// purgeLocked 清理已过期（until 已过）的缓存条目，保证 map 大小有上界。
func (b *BanService) purgeLocked(now time.Time) {
	b.lastSweep = now
	for k, e := range b.cache {
		if !now.Before(e.until) {
			delete(b.cache, k)
		}
	}
}

// invalidate 失效某主体的缓存。
func (b *BanService) invalidate(subjectType, value string) {
	b.mu.Lock()
	delete(b.cache, subjectType+"|"+value)
	b.mu.Unlock()
}

// kick 踢掉当前主体对应的在线连接。
func (b *BanService) kick(s BanSubject) {
	if b.kicker == nil {
		return
	}
	switch s.Type {
	case SubjectIdentity:
		if id, err := parseUUID(s.Value); err == nil {
			b.kicker.KickIdentity(id, kickReasonBan)
		}
	case SubjectAccount:
		if id, err := parseUUID(s.Value); err == nil {
			b.kicker.KickAccount(id, kickReasonBan)
		}
	case SubjectIP:
		b.kicker.KickIP(s.Value, kickReasonBan)
	}
}

// validateSubject 校验主体类型与值。
func validateSubject(s BanSubject) error {
	switch s.Type {
	case SubjectIdentity, SubjectIP, SubjectAccount:
	default:
		return fmt.Errorf("非法的封禁主体类型: %q", s.Type)
	}
	if s.Value == "" {
		return fmt.Errorf("封禁主体值不能为空")
	}
	return nil
}

// parseUUID 解析 UUID 字符串。
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, fmt.Errorf("UUID 为空")
	}
	return u, nil
}

// SetKicker 注入踢连接器（在网关装配完成后调用）。
func (b *BanService) SetKicker(k Kicker) { b.kicker = k }

// EnsureAutoBan 串行检查有效封禁，已有封禁不会被延长。分片锁避免主体锁表无界增长。
func (b *BanService) EnsureAutoBan(ctx context.Context, subject BanSubject, banSeconds int, reason string) error {
	if banSeconds == 0 {
		return nil
	}
	if banSeconds < -1 {
		return fmt.Errorf("非法封禁时长")
	}
	if err := validateSubject(subject); err != nil {
		return err
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(subject.Type + "|" + subject.Value))
	mu := &b.autoLocks[h.Sum32()%uint32(len(b.autoLocks))]
	mu.Lock()
	defer mu.Unlock()
	_, err := b.q.GetActiveBan(ctx, store.GetActiveBanParams{SubjectType: subject.Type, SubjectValue: subject.Value, ExpiresAt: pgtype.Timestamptz{Time: b.now(), Valid: true}})
	if err == nil {
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.Error("查询自动封禁失败", "error", err)
		return err
	}
	var duration time.Duration
	if banSeconds > 0 {
		duration = time.Duration(banSeconds) * time.Second
	}
	_, err = b.Create(ctx, []BanSubject{subject}, duration, reason, pgtype.UUID{})
	if err != nil {
		slog.Error("写入自动封禁失败", "error", err)
	}
	return err
}
