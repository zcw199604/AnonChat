package match

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// match.found 的 peer 负载。
type peerInfo struct {
	MemberID   int32  `json:"memberId"`
	Nickname   string `json:"nickname"`
	AvatarSeed string `json:"avatarSeed"`
}

// matchFoundPayload 是 match.found 的负载。
type matchFoundPayload struct {
	ConversationID string   `json:"conversationId"`
	Peer           peerInfo `json:"peer"`
}

// Service 组合匹配器、WS 处理、断线宽限与会话创建。
type Service struct {
	q       *store.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	convSvc *conversation.Service
	hub     realtime.Realtime
	limiter moderation.RateLimiter
	matcher *memoryMatcher
	grace   time.Duration

	mu          sync.Mutex
	graceTimers map[pgtype.UUID]*time.Timer
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.matcher.now = fn
		}
	}
}

// WithTick 设置定时全量配对扫描间隔（测试用）。
func WithTick(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.matcher.tick = d
		}
	}
}

// New 创建匹配服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, convSvc *conversation.Service, hub realtime.Realtime, limiter moderation.RateLimiter, relaxAfter, grace time.Duration, opts ...Option) *Service {
	if limiter == nil {
		limiter = moderation.AllowAll()
	}
	s := &Service{
		q:           store.New(pool),
		pool:        pool,
		logger:      logger,
		convSvc:     convSvc,
		hub:         hub,
		limiter:     limiter,
		grace:       grace,
		graceTimers: make(map[pgtype.UUID]*time.Timer),
	}
	prefetcher := &sqlPrefetcher{q: s.q}
	s.matcher = NewMemoryMatcher(prefetcher, s.onPair, relaxAfter, time.Second)
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run 启动匹配 goroutine，直到 ctx 取消。
func (s *Service) Run(ctx context.Context) { s.matcher.Run(ctx) }

// RegisterWS 注册 match.* 处理函数（均挂 RequireVerified）。
func (s *Service) RegisterWS(router *realtime.Router, verificationMode string) {
	v := realtime.RequireVerified(verificationMode)
	router.Handle(realtime.TypeMatchStart, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleStart(ctx, c, env)
	}))
	router.Handle(realtime.TypeMatchCancel, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleCancel(ctx, c, env)
	}))
	router.Handle(realtime.TypeMatchNext, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleNext(ctx, c, env)
	}))
	router.Handle(realtime.TypeMatchLeave, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleLeave(ctx, c, env)
	}))
}

func (s *Service) handleStart(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	ident := c.Identity()
	if !s.limiter.Allow(ctx, "match_start:"+ident.ID.String()) {
		c.SendError(env.ID, realtime.CodeRateLimited, "匹配请求过于频繁")
		return
	}
	// 已有进行中的匹配会话 → 等同于 match.next。
	if conv, ok := s.activeMatchConv(ctx, ident.ID); ok {
		_ = s.convSvc.End(ctx, conv, "peer_skipped")
	}
	// 先回 waiting，再入队（若立即配对，found 会在 waiting 之后到达）。
	c.SendEvent(realtime.TypeMatchWaiting, env.ID, nil)
	if err := s.matcher.Enqueue(ctx, ident); err != nil {
		s.logger.Error("匹配：入队失败", "error", err)
		c.SendError(env.ID, realtime.CodeInternal, "匹配暂时不可用")
	}
}

func (s *Service) handleCancel(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	s.matcher.Cancel(c.Identity().ID)
	c.SendEvent(realtime.TypeAck, env.ID, nil)
}

func (s *Service) handleNext(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	ident := c.Identity()
	if conv, ok := s.activeMatchConv(ctx, ident.ID); ok {
		_ = s.convSvc.End(ctx, conv, "peer_skipped")
	}
	// 先回 waiting，再入队（若立即配对，found 会在 waiting 之后到达）。
	c.SendEvent(realtime.TypeMatchWaiting, env.ID, nil)
	if err := s.matcher.Enqueue(ctx, ident); err != nil {
		s.logger.Error("匹配：入队失败", "error", err)
		c.SendError(env.ID, realtime.CodeInternal, "匹配暂时不可用")
	}
}

func (s *Service) handleLeave(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	ident := c.Identity()
	if conv, ok := s.activeMatchConv(ctx, ident.ID); ok {
		_ = s.convSvc.End(ctx, conv, "peer_left")
	}
	s.matcher.Leave(ident.ID)
	c.SendEvent(realtime.TypeAck, env.ID, nil)
}

// onPair 在匹配 goroutine 内被调用：创建会话、订阅双方连接、推送 match.found。
func (s *Service) onPair(a, b *store.Identity) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("匹配：开启事务失败", "error", err)
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	conv, members, err := s.convSvc.Create(ctx, tx, store.ConversationKindMatch, false, a.ID, b.ID)
	if err != nil {
		s.logger.Error("匹配：创建会话失败", "error", err)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("匹配：提交事务失败", "error", err)
		return err
	}
	if len(members) != 2 {
		s.logger.Error("匹配：成员数异常", "count", len(members))
		return errInvalidPair
	}

	s.hub.SubscribeIdentity(a.ID, conv.ID)
	s.hub.SubscribeIdentity(b.ID, conv.ID)

	// members[0] 是 a，members[1] 是 b。
	s.sendFound(a.ID, conv.ID, members[1].MemberID, b)
	s.sendFound(b.ID, conv.ID, members[0].MemberID, a)
	return nil
}

// sendFound 向某身份的全部连接推送 match.found。
func (s *Service) sendFound(identityID, convID pgtype.UUID, peerMemberID int32, peer *store.Identity) {
	env, err := realtime.NewEnvelope(realtime.TypeMatchFound, "", matchFoundPayload{
		ConversationID: convID.String(),
		Peer: peerInfo{
			MemberID:   peerMemberID,
			Nickname:   peer.Nickname,
			AvatarSeed: peer.AvatarSeed,
		},
	})
	if err != nil {
		return
	}
	s.hub.SendToIdentity(identityID, env)
}

// activeMatchConv 返回该身份进行中的匹配会话（若存在）。
func (s *Service) activeMatchConv(ctx context.Context, identityID pgtype.UUID) (pgtype.UUID, bool) {
	conv, err := s.q.GetActiveMatchConversation(ctx, identityID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return pgtype.UUID{}, false
		}
		s.logger.Error("匹配：查询进行中会话失败", "error", err)
		return pgtype.UUID{}, false
	}
	return conv.ID, true
}

// OnIdentityOnline 实现 realtime.IdentityObserver：取消断线宽限计时。
func (s *Service) OnIdentityOnline(identityID pgtype.UUID) {
	s.mu.Lock()
	if t, ok := s.graceTimers[identityID]; ok {
		t.Stop()
		delete(s.graceTimers, identityID)
	}
	s.mu.Unlock()
}

// OnIdentityOffline 实现 realtime.IdentityObserver：排队立即出队；有进行中会话则启动宽限计时。
func (s *Service) OnIdentityOffline(identityID pgtype.UUID) {
	s.matcher.Cancel(identityID)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conv, ok := s.activeMatchConv(ctx, identityID)
	if !ok {
		return
	}
	s.mu.Lock()
	if t, exists := s.graceTimers[identityID]; exists {
		t.Stop()
	}
	s.graceTimers[identityID] = time.AfterFunc(s.grace, func() {
		s.endGrace(identityID, conv)
	})
	s.mu.Unlock()
}

// endGrace 在宽限期结束后，若该身份仍离线则结束会话（对方收到 peer_left）。
func (s *Service) endGrace(identityID, convID pgtype.UUID) {
	s.mu.Lock()
	delete(s.graceTimers, identityID)
	s.mu.Unlock()

	if s.hub.ConnCount(identityID) > 0 {
		return // 宽限期内已重连
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if cur, ok := s.activeMatchConv(ctx, identityID); ok && cur == convID {
		_ = s.convSvc.End(ctx, convID, "peer_left")
	}
}

// Resumable 返回该身份可恢复的会话 ID（进行中的匹配会话）。
func (s *Service) Resumable(identityID pgtype.UUID) []pgtype.UUID {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if conv, ok := s.activeMatchConv(ctx, identityID); ok {
		return []pgtype.UUID{conv}
	}
	return nil
}

// CleanupOnStartup 在服务启动时把所有未结束的匹配会话以 reason=expired 结束。
func (s *Service) CleanupOnStartup(ctx context.Context) error {
	ids, err := s.q.EndAllOpenMatchConversations(ctx)
	if err != nil {
		return err
	}
	if len(ids) > 0 {
		s.logger.Info("匹配：启动时清理未结束的匹配会话", "count", len(ids))
	}
	return nil
}

var errInvalidPair = errors.New("配对成员数异常")

// sqlPrefetcher 在入队时一次性查询屏蔽相关身份集合与最近 3 个对象。
type sqlPrefetcher struct {
	q *store.Queries
}

func (c *sqlPrefetcher) Prefetch(ctx context.Context, identityID pgtype.UUID) (IdentityContext, error) {
	blockedRows, err := c.q.ListBlocksRelated(ctx, identityID)
	if err != nil {
		return IdentityContext{}, err
	}
	recentRows, err := c.q.ListRecentMatchOpponents(ctx, store.ListRecentMatchOpponentsParams{
		IdentityID: identityID,
		Limit:      3,
	})
	if err != nil {
		return IdentityContext{}, err
	}
	return IdentityContext{Blocked: uuidSet(blockedRows), Recent: uuidSet(recentRows)}, nil
}

func uuidSet(ids []pgtype.UUID) map[pgtype.UUID]struct{} {
	set := make(map[pgtype.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
