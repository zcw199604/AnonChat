// Package identity 负责匿名身份的签发、校验、换身份以及身份中间件。
package identity

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// ErrUnauthorized 表示身份令牌缺失、无效或已吊销。
var ErrUnauthorized = httpx.NewError(httpx.CodeUnauthorized, "身份无效或已失效")

// 访问日志事件类型。
const (
	eventIdentityCreated = "identity_created"
	eventIdentityReset   = "identity_reset"
)

// 每 IP 创建身份（含换身份）的限流 key 前缀，Create 与 Reset 共用。
const createLimitKey = "identity_create:"

// lastSeen 清理阈值：每 1024 次 Touch 或每 1 分钟触发一次全量清理。
const (
	lastSeenSweepEveryCalls = 1024
	lastSeenSweepPeriod     = time.Minute
)

// Service 提供身份相关的领域操作。
type Service struct {
	pool     *pgxpool.Pool
	q        *store.Queries
	logger   *slog.Logger
	limiter  moderation.RateLimiter // 缺失有效 IP 时的内存限流
	settings *moderation.LimitSettings
	now      func() time.Time

	// lastSeen 节流：同一身份 touchInterval 内最多更新一次 last_seen_at。
	// 定期清理过期条目，避免 map 随历史身份总数无限增长。
	mu            sync.Mutex
	lastSeen      map[pgtype.UUID]time.Time
	touchInterval time.Duration
	touchCalls    int
	lastSweep     time.Time
}

// Option 用于配置 Service。
type Option func(*Service)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithLimitSettings 使用动态规则与数据库成功创建计数。
func WithLimitSettings(settings *moderation.LimitSettings) Option {
	return func(s *Service) { s.settings = settings }
}

// NewService 创建身份服务。limiter 用于限制每 IP 创建身份的频率。
func NewService(pool *pgxpool.Pool, logger *slog.Logger, limiter moderation.RateLimiter, opts ...Option) *Service {
	if limiter == nil {
		limiter = moderation.AllowAll()
	}
	s := &Service{
		pool:          pool,
		q:             store.New(pool),
		logger:        logger,
		limiter:       limiter,
		now:           time.Now,
		lastSeen:      make(map[pgtype.UUID]time.Time),
		touchInterval: time.Minute,
		lastSweep:     time.Now(),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Session 是一次身份创建的结果：明文令牌 + 身份记录。
type Session struct {
	Token    string
	Identity store.Identity
}

// Create 新建一个匿名身份，写入 access_logs，并返回明文令牌。
func (s *Service) Create(ctx context.Context, ip, userAgent string) (*Session, error) {
	return s.issue(ctx, nil, ip, userAgent)
}

// issue 计数成功创建的身份数（包括已撤销身份），而非尝试次数。
// IP 事务锁让 Create 与 Reset 在多个并发请求间共享滚动窗口。
func (s *Service) issue(ctx context.Context, current *store.Identity, ip, userAgent string) (*Session, error) {
	addr := parseIP(ip)
	if addr != nil {
		ip = addr.String()
	}
	if s.settings == nil || addr == nil {
		limiter := s.limiter
		if s.settings != nil {
			limiter = s.settings.Limiter(moderation.RuleIdentityCreate)
		}
		if !limiter.Allow(ctx, createLimitKey+ip) {
			return nil, httpx.NewError(httpx.CodeRateLimited, "创建身份过于频繁，请稍后再试")
		}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)
	if s.settings != nil && addr != nil {
		if err := q.LockIdentityCreateIP(ctx, ip); err != nil {
			return nil, err
		}
		rule := s.settings.Current().Rules[moderation.RuleIdentityCreate]
		if rule.Enabled {
			count, err := q.CountCreatedIdentitiesByIP(ctx, store.CountCreatedIdentitiesByIPParams{Ip: *addr, WindowSeconds: int32(rule.WindowSeconds)})
			if err != nil {
				return nil, err
			}
			if count >= int64(rule.Limit)*int64(s.settings.Multiplier()) {
				if err := tx.Rollback(ctx); err != nil {
					return nil, err
				}
				s.settings.EnsureRuleBan(ctx, rule, createLimitKey+ip)
				return nil, httpx.NewError(httpx.CodeRateLimited, "创建身份过于频繁，请稍后再试")
			}
		}
	}
	event := eventIdentityCreated
	if current != nil {
		locked, err := q.GetIdentityForUpdate(ctx, current.ID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		if err != nil {
			return nil, err
		}
		if locked.RevokedAt.Valid {
			return nil, ErrUnauthorized
		}
		current = &locked
		n, err := q.RevokeIdentity(ctx, current.ID)
		if err != nil {
			return nil, err
		}
		if n != 1 {
			return nil, ErrUnauthorized
		}
		event = eventIdentityReset
	}
	token, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	ident, err := s.createIdentityWithRetry(ctx, q, token, ip)
	if err != nil {
		return nil, err
	}
	if current != nil {
		if err := q.SetIdentityBenefits(ctx, store.SetIdentityBenefitsParams{ID: ident.ID, VipUntil: current.VipUntil, Cards: current.BroadcastCards}); err != nil {
			return nil, err
		}
		ident.VipUntil = current.VipUntil
		ident.BroadcastCards = current.BroadcastCards
	}
	if err := q.InsertAccessLog(ctx, s.accessLogArg(ident.ID, event, ip, userAgent)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &Session{Token: token, Identity: ident}, nil
}

// publicCodeRetries 是匿名编号唯一冲突时的最大重试次数（how.md 3.1）。
const publicCodeRetries = 3

// createIdentityWithRetry 使用 ON CONFLICT DO NOTHING 重试唯一冲突，避免使事务中止。
func (s *Service) createIdentityWithRetry(ctx context.Context, q *store.Queries, token, ip string) (store.Identity, error) {
	var lastErr error
	for attempt := 0; attempt < publicCodeRetries; attempt++ {
		code, err := NewPublicCode()
		if err != nil {
			return store.Identity{}, err
		}
		ident, err := q.CreateIdentity(ctx, store.CreateIdentityParams{
			TokenHash:  HashToken(token),
			Nickname:   GenerateNickname(),
			AvatarSeed: GenerateAvatarSeed(),
			CreatedIp:  parseIP(ip),
			PublicCode: code,
		})
		if err == nil {
			return ident, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return store.Identity{}, err
		}
		lastErr = err
	}
	return store.Identity{}, lastErr
}

// Resolve 用明文令牌查询身份；令牌无效或已吊销返回 ErrUnauthorized。
func (s *Service) Resolve(ctx context.Context, token string) (*store.Identity, error) {
	if token == "" {
		return nil, ErrUnauthorized
	}
	ident, err := s.q.GetIdentityByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUnauthorized
		}
		return nil, err
	}
	if ident.RevokedAt.Valid {
		return nil, ErrUnauthorized
	}
	return &ident, nil
}

// Reset 吊销当前身份并签发新身份。与 Create 共用同一创建限流 key；
// 吊销、创建、写 access_log 在同一事务内，任一步失败整体回滚（旧身份保持有效）。
func (s *Service) Reset(ctx context.Context, current *store.Identity, ip, userAgent string) (*Session, error) {
	return s.issue(ctx, current, ip, userAgent)
}

// Touch 节流地更新 last_seen_at：同一身份 touchInterval 内最多写库一次，并顺带清理过期条目。
func (s *Service) Touch(ctx context.Context, ident *store.Identity) {
	now := s.now()
	s.mu.Lock()
	last, ok := s.lastSeen[ident.ID]
	needUpdate := !ok || now.Sub(last) >= s.touchInterval
	if needUpdate {
		s.lastSeen[ident.ID] = now
	}
	s.maybeSweepLocked(now)
	s.mu.Unlock()

	if needUpdate {
		if err := s.q.TouchIdentityLastSeen(ctx, ident.ID); err != nil {
			s.logger.Warn("更新 last_seen_at 失败", "error", err)
		}
	}
}

// maybeSweepLocked 定期清理 lastSeen 中超过 touchInterval 未更新的条目。
// 每 lastSeenSweepEveryCalls 次调用或每 lastSeenSweepPeriod 触发一次。
func (s *Service) maybeSweepLocked(now time.Time) {
	s.touchCalls++
	if s.touchCalls < lastSeenSweepEveryCalls && now.Sub(s.lastSweep) < lastSeenSweepPeriod {
		return
	}
	s.touchCalls = 0
	s.lastSweep = now
	for id, t := range s.lastSeen {
		if now.Sub(t) >= s.touchInterval {
			delete(s.lastSeen, id)
		}
	}
}

// LogAccess 写入一条访问审计日志（供 realtime 等模块使用）。
func (s *Service) LogAccess(ctx context.Context, identityID pgtype.UUID, event, ip, userAgent string) {
	s.writeAccessLog(ctx, identityID, event, ip, userAgent)
}

// writeAccessLog 记录一次访问审计日志（失败不影响主流程）。
func (s *Service) writeAccessLog(ctx context.Context, identityID pgtype.UUID, event, ip, userAgent string) {
	if err := s.q.InsertAccessLog(ctx, s.accessLogArg(identityID, event, ip, userAgent)); err != nil {
		s.logger.Warn("写入 access_logs 失败", "event", event, "error", err)
	}
}

// accessLogArg 构造访问日志插入参数。
func (s *Service) accessLogArg(identityID pgtype.UUID, event, ip, userAgent string) store.InsertAccessLogParams {
	var ua *string
	if userAgent != "" {
		ua = &userAgent
	}
	return store.InsertAccessLogParams{
		IdentityID: identityID,
		Event:      event,
		Ip:         parseIP(ip),
		UserAgent:  ua,
	}
}

// parseIP 把字符串解析为 *netip.Addr，无法解析时返回 nil。
func parseIP(s string) *netip.Addr {
	if s == "" {
		return nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return nil
	}
	return &addr
}
