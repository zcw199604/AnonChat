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
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// match.found 的 peer 负载。
// 1v1 场景透传对方的真实昵称与头像种子（how.md 3.4 已按产品决定调整）：
// 匹配成功后双方直接以真实昵称称呼，前端不再使用「匿名·XXXX」代号。
type peerInfo struct {
	MemberID   int32    `json:"memberId"`
	Nickname   string   `json:"nickname"`
	AvatarSeed string   `json:"avatarSeed"`
	Gender     *string  `json:"gender"`
	Age        *int16   `json:"age"`
	Province   *string  `json:"province"`
	Tags       []string `json:"tags"`
	CleanMode  bool     `json:"cleanMode"`
}

// matchFoundPayload 是 match.found 的负载。
type matchFoundPayload struct {
	ConversationID string   `json:"conversationId"`
	Peer           peerInfo `json:"peer"`
}

// matchFilters 是服务端最终生效的匹配条件（match.waiting 的 filters）。
type matchFilters struct {
	CleanMode    bool   `json:"cleanMode"`
	Gender       string `json:"gender"`
	SameProvince bool   `json:"sameProvince"`
}

// matchWaitingPayload 是 match.waiting 的负载。
type matchWaitingPayload struct {
	Filters matchFilters `json:"filters"`
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
	now     func() time.Time
	// botProvider 是排队兜底的 bot 提供者（BOT_ENABLED 时由装配注入，可为 nil）。
	botProvider BotProvider

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
			s.now = fn
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
		now:         time.Now,
		graceTimers: make(map[pgtype.UUID]*time.Timer),
	}
	// 用闭包延后取值，确保 WithClock 注入后生效。
	prefetcher := &sqlPrefetcher{q: s.q, now: func() time.Time { return s.now() }}
	s.matcher = NewMemoryMatcher(prefetcher, s.onPair, relaxAfter, time.Second)
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetBotFallback 注册排队兜底的 provider（how.md §8、§15.2）。
// 兜底时间由 p.FallbackAfter() 动态提供（后台可热切换），必须在 Run 之前调用。
func (s *Service) SetBotFallback(p BotProvider) {
	if p == nil {
		return
	}
	s.botProvider = p
	s.matcher.setBotFallback(p, s.onBotPair, s.logger)
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
	// 先回 waiting（带服务端最终生效的条件），再入队（若立即配对，found 会在 waiting 之后到达）。
	c.SendEvent(realtime.TypeMatchWaiting, env.ID, matchWaitingPayload{Filters: s.effectiveFilters(ctx, ident)})
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
	if !s.limiter.Allow(ctx, "match_start:"+ident.ID.String()) {
		c.SendError(env.ID, realtime.CodeRateLimited, "匹配请求过于频繁")
		return
	}

	if conv, ok := s.activeMatchConv(ctx, ident.ID); ok {
		_ = s.convSvc.End(ctx, conv, "peer_skipped")
	}
	// 先回 waiting（带服务端最终生效的条件），再入队（若立即配对，found 会在 waiting 之后到达）。
	c.SendEvent(realtime.TypeMatchWaiting, env.ID, matchWaitingPayload{Filters: s.effectiveFilters(ctx, ident)})
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

// onPair 在独立 goroutine 中执行：创建会话、订阅双方连接、推送 match.found。
func (s *Service) onPair(ae, be *queueEntry) error {
	a, b := ae.ident, be.ident
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

	// members[0] 是 a，members[1] 是 b。peer 携带对方的真实昵称与头像种子。
	s.sendFound(a.ID, conv.ID, members[1].MemberID, be.ic.Profile, b.Nickname, b.AvatarSeed)
	s.sendFound(b.ID, conv.ID, members[0].MemberID, ae.ic.Profile, a.Nickname, a.AvatarSeed)
	return nil
}

// onBotPair 在匹配 goroutine 之外执行：与 onPair 相同的建会话、订阅与推送流程，
// 只是对方是 bot——peer 用 bot 身份的公开资料，构造方式与真人配对完全一致（how.md §8）。
func (s *Service) onBotPair(entry *queueEntry, cand *BotCandidate) error {
	if cand == nil || cand.Identity == nil {
		return errInvalidPair
	}
	user := entry.ident
	botIdent := cand.Identity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		s.logger.Error("匹配兜底：开启事务失败", "error", err)
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	conv, members, err := s.convSvc.Create(ctx, tx, store.ConversationKindMatch, false, user.ID, botIdent.ID)
	if err != nil {
		s.logger.Error("匹配兜底：创建会话失败", "error", err)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		s.logger.Error("匹配兜底：提交事务失败", "error", err)
		return err
	}
	if len(members) != 2 {
		s.logger.Error("匹配兜底：成员数异常", "count", len(members))
		return errInvalidPair
	}

	s.hub.SubscribeIdentity(user.ID, conv.ID)
	s.hub.SubscribeIdentity(botIdent.ID, conv.ID)

	// 用户收到的 match.found：peer 字段由 bot 身份的公开资料按同一规则构造，
	// 昵称与头像种子同样取自 bot 身份。
	s.sendFound(user.ID, conv.ID, members[1].MemberID, cand.Profile, botIdent.Nickname, botIdent.AvatarSeed)
	if s.botProvider != nil {
		s.botProvider.BotMatched(conv.ID, botIdent.ID)
	}
	return nil
}

// sendFound 向某身份的全部连接推送 match.found：公开资料 + 对方的真实昵称与头像种子。
func (s *Service) sendFound(identityID, convID pgtype.UUID, peerMemberID int32, prof PrefProfile, peerNickname, peerAvatarSeed string) {
	pub := profile.NewPublicProfile(prof.Gender, prof.Age, prof.Province, prof.ProvincePublic, prof.Tags, prof.CleanMode)
	env, err := realtime.NewEnvelope(realtime.TypeMatchFound, "", matchFoundPayload{
		ConversationID: convID.String(),
		Peer: peerInfo{
			MemberID:   peerMemberID,
			Nickname:   peerNickname,
			AvatarSeed: peerAvatarSeed,
			Gender:     pub.Gender,
			Age:        pub.Age,
			Province:   pub.Province,
			Tags:       pub.Tags,
			CleanMode:  pub.CleanMode,
		},
	})
	if err != nil {
		return
	}
	s.hub.SendToIdentity(identityID, env)
}

// effectiveFilters 计算服务端最终生效的匹配条件（偏好暂不参与匹配，仅清流模式生效）。
func (s *Service) effectiveFilters(ctx context.Context, ident *store.Identity) matchFilters {
	out := matchFilters{Gender: "any", SameProvince: false}
	row, err := s.q.GetMatchProfile(ctx, ident.ID)
	if err != nil {
		return out
	}
	out.CleanMode = row.CleanMode
	return out
}

// QueueLen 返回当前排队人数（用于在线统计）。
func (s *Service) QueueLen() int { return s.matcher.QueueLen() }

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
	q   *store.Queries
	now func() time.Time
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
	row, err := c.q.GetMatchProfile(ctx, identityID)
	if err != nil {
		return IdentityContext{}, err
	}
	prof := PrefProfile{
		Gender:         row.Gender,
		Age:            row.Age,
		Province:       row.Province,
		ProvincePublic: row.ProvincePublic,
		Tags:           row.Tags,
		CleanMode:      row.CleanMode,
		MatchGender:    row.MatchGender,
		MatchSameProv:  row.MatchSameProvince,
		VIP:            row.VipUntil.Valid && row.VipUntil.Time.After(c.now()),
	}
	return IdentityContext{Blocked: uuidSet(blockedRows), Recent: uuidSet(recentRows), Profile: prof}, nil
}

func uuidSet(ids []pgtype.UUID) map[pgtype.UUID]struct{} {
	set := make(map[pgtype.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

// ExternalHooks 将 Bridge 的记录写入同一事务，并控制提交后的内存发布。
type ExternalHooks struct {
	// OnCreated 在创建会话的事务中执行。
	OnCreated func(context.Context, pgx.Tx, pgtype.UUID) error
	// Activate 必须在 link 锁内确认状态并调用 publish；返回 false 时不得调用 publish。
	Activate func(pgtype.UUID, func()) bool
}

// SetExternalMatcher 必须在 Run 前调用；online 与 Plan 均只读内存。
func (s *Service) SetExternalMatcher(x ExternalMatcher, online func() int) {
	s.matcher.external, s.matcher.online = x, online
}

// ExternalFound 在匹配 goroutine 之外完成认领、建会话和激活。
// 完成时交还在途计数；失败回队还必须通过意图版本校验。
func (s *Service) ExternalFound(ctx context.Context, userID pgtype.UUID, attempt uint64, peer ExternalPeer, hooks ExternalHooks) (pgtype.UUID, error) {
	var claim externalClaim
	s.matcher.do(command{kind: cmdClaimAttempt, id: userID, attempt: attempt, claim: &claim})
	if claim.entry == nil {
		return pgtype.UUID{}, ErrNotQueued
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	activated := false
	defer func() {
		kind := cmdRequeueExternal
		if activated {
			kind = cmdExternalDone
		}
		s.matcher.do(command{kind: kind, entry: claim.entry, intentSeq: claim.intentSeq})
	}()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	conv, members, err := s.convSvc.Create(ctx, tx, store.ConversationKindMatch, false, userID, peer.IdentityID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if len(members) != 2 {
		return pgtype.UUID{}, errInvalidPair
	}
	if hooks.OnCreated != nil {
		if err := hooks.OnCreated(ctx, tx, conv.ID); err != nil {
			return pgtype.UUID{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}
	// publish 只进行 hub 内存操作。Bridge 在 link 锁内调用它，保证 found 与结束事件的顺序。
	publish := func() {
		s.hub.SubscribeIdentity(userID, conv.ID)
		s.hub.SubscribeIdentity(peer.IdentityID, conv.ID)
		s.sendFound(userID, conv.ID, members[1].MemberID, peer.Profile, peer.Nickname, peer.AvatarSeed)
	}
	if hooks.Activate == nil || !hooks.Activate(conv.ID, publish) {
		endCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := s.convSvc.End(endCtx, conv.ID, "peer_left"); err != nil {
			s.logger.Error("外站匹配：拒绝激活后结束会话失败", "conversation_id", conv.ID.String(), "error", err)
			return pgtype.UUID{}, errors.Join(ErrActivationRejected, err)
		}
		return pgtype.UUID{}, ErrActivationRejected
	}
	activated = true
	return conv.ID, nil
}
