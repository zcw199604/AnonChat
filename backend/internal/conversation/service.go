package conversation

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 默认时间参数。
const (
	defaultBurnDelay = 10 * time.Second
	defaultBurnTick  = time.Second
)

// Service 提供会话与消息的领域操作。
type Service struct {
	pool          *pgxpool.Pool
	q             *store.Queries
	logger        *slog.Logger
	broker        realtime.Broker
	limiter       RateLimiter
	limitSettings *moderation.LimitSettings
	filter        ContentFilter
	media         MediaForSend
	blocker       BlockChecker
	now           func() time.Time

	burnDelay      time.Duration
	burnTick       time.Duration
	roomMsgLimiter RateLimiter

	// observers 是会话事件观察者（bot 引擎等），由 AddObserver 注册。
	obsMu     sync.RWMutex
	observers []Observer
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

// WithLimiter 注入限流器。
func WithLimiter(l RateLimiter) Option {
	return func(s *Service) {
		if l != nil {
			s.limiter = l
		}
	}
}

// WithLimitSettings 注入即时生效的组合限流配置。
func WithLimitSettings(settings *moderation.LimitSettings) Option {
	return func(s *Service) { s.limitSettings = settings }
}

// WithFilter 注入内容过滤器。
func WithFilter(f ContentFilter) Option {
	return func(s *Service) {
		if f != nil {
			s.filter = f
		}
	}
}

// WithBlocker 注入屏蔽检查器。
func WithBlocker(b BlockChecker) Option {
	return func(s *Service) {
		if b != nil {
			s.blocker = b
		}
	}
}

// WithBurnTiming 设置焚毁延迟与扫描间隔（测试用）。
func WithBurnTiming(delay, tick time.Duration) Option {
	return func(s *Service) {
		if delay > 0 {
			s.burnDelay = delay
		}
		if tick > 0 {
			s.burnTick = tick
		}
	}
}

// WithRoomMessageLimiter 设置房间内发言间隔限流器（同一身份在同一房间每秒最多 1 条）。
func WithRoomMessageLimiter(l RateLimiter) Option {
	return func(s *Service) {
		if l != nil {
			s.roomMsgLimiter = l
		}
	}
}

// New 创建会话服务。broker 为 nil 时不广播。
func New(pool *pgxpool.Pool, logger *slog.Logger, broker realtime.Broker, opts ...Option) *Service {
	s := &Service{
		pool:      pool,
		q:         store.New(pool),
		logger:    logger,
		broker:    broker,
		limiter:   moderation.AllowAll(),
		filter:    NoopFilter{},
		now:       time.Now,
		burnDelay: defaultBurnDelay,
		burnTick:  defaultBurnTick,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create 在给定事务中创建会话并加入成员（member_id 从 1 递增分配）。
func (s *Service) Create(ctx context.Context, tx pgx.Tx, kind store.ConversationKind, burnAfterRead bool, identityIDs ...pgtype.UUID) (store.Conversation, []store.ConversationMember, error) {
	q := s.q.WithTx(tx)
	conv, err := q.CreateConversation(ctx, store.CreateConversationParams{
		Kind:          kind,
		BurnAfterRead: burnAfterRead,
	})
	if err != nil {
		return store.Conversation{}, nil, err
	}
	members := make([]store.ConversationMember, 0, len(identityIDs))
	for _, id := range identityIDs {
		m, err := addMemberTx(ctx, q, conv.ID, id)
		if err != nil {
			return store.Conversation{}, nil, err
		}
		members = append(members, m)
	}
	return conv, members, nil
}

// AddMember 在给定事务中加入成员（幂等；重新加入复用原 member_id）。
func (s *Service) AddMember(ctx context.Context, tx pgx.Tx, conversationID, identityID pgtype.UUID) (store.ConversationMember, error) {
	return addMemberTx(ctx, s.q.WithTx(tx), conversationID, identityID)
}

// addMemberTx 在事务内分配 member_id 并插入成员。
func addMemberTx(ctx context.Context, q *store.Queries, conversationID, identityID pgtype.UUID) (store.ConversationMember, error) {
	// 锁会话行，串行分配 member_id。
	if _, err := q.LockConversation(ctx, conversationID); err != nil {
		return store.ConversationMember{}, err
	}
	m, err := q.GetMember(ctx, store.GetMemberParams{ConversationID: conversationID, IdentityID: identityID})
	if err == nil {
		if m.LeftAt.Valid {
			if rerr := q.RejoinMember(ctx, store.RejoinMemberParams{ConversationID: conversationID, IdentityID: identityID}); rerr != nil {
				return store.ConversationMember{}, rerr
			}
			m.LeftAt = pgtype.Timestamptz{}
		}
		return m, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.ConversationMember{}, err
	}
	next, err := q.NextMemberID(ctx, conversationID)
	if err != nil {
		return store.ConversationMember{}, err
	}
	return q.InsertMember(ctx, store.InsertMemberParams{
		ConversationID: conversationID,
		MemberID:       next,
		IdentityID:     identityID,
	})
}

// Leave 让成员离开会话。
func (s *Service) Leave(ctx context.Context, conversationID, identityID pgtype.UUID) error {
	return s.q.LeaveMember(ctx, store.LeaveMemberParams{
		ConversationID: conversationID,
		IdentityID:     identityID,
	})
}

// End 结束会话，并向在线成员推送相应通知（match→match.ended，其它→conv.ended）。
func (s *Service) End(ctx context.Context, conversationID pgtype.UUID, reason string) error {
	conv, err := s.q.GetConversation(ctx, conversationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	affected, err := s.q.EndConversation(ctx, store.EndConversationParams{
		ID:        conversationID,
		EndReason: &reason,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return nil // 已结束
	}
	// 真正结束（affected>0）才通知观察者，重复结束不再通知。
	s.notifyEnded(conversationID)
	if s.broker != nil {
		msgType := realtime.TypeConvEnded
		if conv.Kind == store.ConversationKindMatch {
			msgType = realtime.TypeMatchEnded
		}
		if env, e := realtime.NewEnvelope(msgType, "", convEndedPayload{
			ConversationID: conversationID.String(),
			Reason:         reason,
		}); e == nil {
			s.broker.Broadcast(conversationID, env, nil)
		}
	}
	return nil
}
