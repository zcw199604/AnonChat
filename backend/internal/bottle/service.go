// Package bottle 实现漂流瓶（C 形态）：投放、捞取、扔回、回复、我的瓶子与举报。
package bottle

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	maxContentRunes = 500
	maxReplyRunes   = 1000
	defaultHold     = 10 * time.Minute
)

// Service 提供漂流瓶操作。
type Service struct {
	q       *store.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	convSvc *conversation.Service
	hub     realtime.Realtime
	filter  conversation.ContentFilter

	postLimiter moderation.RateLimiter
	pickLimiter moderation.RateLimiter
	reportSvc   *moderation.ReportService

	now       func() time.Time
	bottleTTL time.Duration
	hold      time.Duration
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

// WithBottleTTL 设置瓶子有效期。
func WithBottleTTL(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.bottleTTL = d
		}
	}
}

// New 创建漂流瓶服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, convSvc *conversation.Service, hub realtime.Realtime,
	filter conversation.ContentFilter, postLimiter, pickLimiter moderation.RateLimiter,
	reportSvc *moderation.ReportService, opts ...Option) *Service {
	if postLimiter == nil {
		postLimiter = moderation.AllowAll()
	}
	if pickLimiter == nil {
		pickLimiter = moderation.AllowAll()
	}
	s := &Service{
		q:           store.New(pool),
		pool:        pool,
		logger:      logger,
		convSvc:     convSvc,
		hub:         hub,
		filter:      filter,
		postLimiter: postLimiter,
		pickLimiter: pickLimiter,
		reportSvc:   reportSvc,
		now:         time.Now,
		bottleTTL:   168 * time.Hour,
		hold:        defaultHold,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Post 投放一个漂流瓶。
func (s *Service) Post(ctx context.Context, identityID pgtype.UUID, content string) (pgtype.UUID, error) {
	body := trimSpace(content)
	if body == "" || utf8.RuneCountInString(body) > maxContentRunes {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "内容为空或超过 500 字")
	}
	if !s.postLimiter.Allow(ctx, "bottle_post:"+identityID.String()) {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeRateLimited, "今日投放次数已达上限")
	}
	filtered, blocked := s.filter.Filter(ctx, body)
	if blocked {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeContentBlocked, "内容包含违规信息")
	}
	b, err := s.q.InsertBottle(ctx, store.InsertBottleParams{
		IdentityID: identityID,
		Content:    filtered,
		ExpiresAt:  pgtype.Timestamptz{Time: s.now().Add(s.bottleTTL), Valid: true},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return b.ID, nil
}

// PickedBottle 是捞取结果。
type PickedBottle struct {
	ID        pgtype.UUID
	Content   string
	CreatedAt pgtype.Timestamptz
	// PosterIdentityID 是投瓶人（HoldForBot 用于构造提示词里的对方资料）。
	PosterIdentityID pgtype.UUID
}

// Pick 捞取一个瓶子；没有可捞的返回 empty_sea。
func (s *Service) Pick(ctx context.Context, identityID pgtype.UUID) (*PickedBottle, error) {
	if !s.pickLimiter.Allow(ctx, "bottle_pick:"+identityID.String()) {
		return nil, httpx.NewError(httpx.CodeRateLimited, "今日捞取次数已达上限")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	// 再次捞取时先把之前持有的瓶子放回海里。
	if err := q.ReleaseHeldBy(ctx, identityID); err != nil {
		return nil, err
	}

	now := pgtype.Timestamptz{Time: s.now(), Valid: true}
	rnd, err := genUUID()
	if err != nil {
		return nil, err
	}

	var picked *PickedBottle
	cand, err := q.PickCandidateFrom(ctx, store.PickCandidateFromParams{IdentityID: identityID, ID: rnd, ExpiresAt: now})
	switch {
	case err == nil:
		picked = &PickedBottle{ID: cand.ID, Content: cand.Content, CreatedAt: cand.CreatedAt}
	case errors.Is(err, pgx.ErrNoRows):
		cand2, err2 := q.PickCandidateAny(ctx, store.PickCandidateAnyParams{IdentityID: identityID, ExpiresAt: now})
		if err2 == nil {
			picked = &PickedBottle{ID: cand2.ID, Content: cand2.Content, CreatedAt: cand2.CreatedAt}
		} else if !errors.Is(err2, pgx.ErrNoRows) {
			return nil, err2
		}
	default:
		return nil, err
	}

	if picked == nil {
		// 提交“放回旧瓶子”的改动后返回 empty_sea。
		if cerr := tx.Commit(ctx); cerr != nil {
			return nil, cerr
		}
		return nil, httpx.NewError(httpx.CodeEmptySea, "海里暂时没有瓶子")
	}

	if err := q.MarkBottleHeld(ctx, store.MarkBottleHeldParams{
		HeldBy:    identityID,
		HeldUntil: pgtype.Timestamptz{Time: s.now().Add(s.hold), Valid: true},
		ID:        picked.ID,
	}); err != nil {
		return nil, err
	}
	if err := q.InsertBottlePick(ctx, store.InsertBottlePickParams{BottleID: picked.ID, PickerIdentityID: identityID}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return picked, nil
}

// HoldForBot 让 bot 捞一个“投出至少 minAge、还没有人回复过”的瓶子（how.md §9）。
// 在一个事务里完成：选瓶子（FOR UPDATE SKIP LOCKED）→ 标记持有 → 记录捞取。
// 没有可用的瓶子时返回 nil, nil。
func (s *Service) HoldForBot(ctx context.Context, botIdentityID pgtype.UUID, minAge time.Duration) (*PickedBottle, error) {
	if minAge < 0 {
		minAge = 0
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	now := s.now()
	row, err := q.PickBottleForBot(ctx, store.PickBottleForBotParams{
		Now:           pgtype.Timestamptz{Time: now, Valid: true},
		CreatedBefore: pgtype.Timestamptz{Time: now.Add(-minAge), Valid: true},
		BotIdentityID: botIdentityID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 没有可捞的瓶子：保持事务干净地提交。
			if cerr := tx.Commit(ctx); cerr != nil {
				return nil, cerr
			}
			return nil, nil
		}
		return nil, err
	}
	if err := q.MarkBottleHeld(ctx, store.MarkBottleHeldParams{
		HeldBy:    botIdentityID,
		HeldUntil: pgtype.Timestamptz{Time: now.Add(s.hold), Valid: true},
		ID:        row.ID,
	}); err != nil {
		return nil, err
	}
	if err := q.InsertBottlePick(ctx, store.InsertBottlePickParams{
		BottleID:         row.ID,
		PickerIdentityID: botIdentityID,
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &PickedBottle{
		ID:               row.ID,
		Content:          row.Content,
		CreatedAt:        row.CreatedAt,
		PosterIdentityID: row.IdentityID,
	}, nil
}

// ThrowBack 把当前持有的瓶子扔回海里。
func (s *Service) ThrowBack(ctx context.Context, identityID, bottleID pgtype.UUID) error {
	n, err := s.q.ThrowBack(ctx, store.ThrowBackParams{ID: bottleID, HeldBy: identityID})
	if err != nil {
		return err
	}
	if n == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "你不是该瓶子的当前持有者")
	}
	return nil
}

// Reply 回复瓶子，创建 kind=bottle 的会话；返回会话 ID。
func (s *Service) Reply(ctx context.Context, identityID, bottleID pgtype.UUID, content string) (pgtype.UUID, error) {
	body := trimSpace(content)
	if body == "" || utf8.RuneCountInString(body) > maxReplyRunes {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "内容为空或超过 1000 字")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	b, err := q.LockBottleByID(ctx, bottleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "瓶子不存在")
		}
		return pgtype.UUID{}, err
	}
	if b.Status != "held" || b.HeldBy != identityID {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "你不是该瓶子的当前持有者")
	}
	pick, err := q.GetBottlePick(ctx, store.GetBottlePickParams{BottleID: bottleID, PickerIdentityID: identityID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "你还没有捞取该瓶子")
		}
		return pgtype.UUID{}, err
	}
	if pick.ConversationID.Valid {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "你已经回复过该瓶子")
	}

	filtered, blocked := s.filter.Filter(ctx, body)
	if blocked {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeContentBlocked, "内容包含违规信息")
	}

	// 创建会话：投放者为成员 1，回复者为成员 2。
	conv, members, err := s.convSvc.Create(ctx, tx, store.ConversationKindBottle, false, b.IdentityID, identityID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if len(members) != 2 {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInternal, "服务器内部错误")
	}
	replier := members[1]

	// 以回复者身份插入第一条消息。
	clientMsgID, err := genUUID()
	if err != nil {
		return pgtype.UUID{}, err
	}
	if _, err := q.InsertMessage(ctx, store.InsertMessageParams{
		ConversationID:   conv.ID,
		SenderIdentityID: identityID,
		SenderMemberID:   replier.MemberID,
		ClientMsgID:      clientMsgID,
		Kind:             "text",
		Body:             filtered,
		MediaID:          pgtype.UUID{}, // 文本消息没有媒体
	}); err != nil {
		return pgtype.UUID{}, err
	}

	// 记录会话到 pick，并把瓶子放回海里（他人仍可捞取并各自回复）。
	affected, err := q.SetBottlePickConversation(ctx, store.SetBottlePickConversationParams{
		BottleID: bottleID, PickerIdentityID: identityID, ConversationID: conv.ID,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	if affected == 0 {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "你已经回复过该瓶子")
	}
	if _, err := q.ThrowBack(ctx, store.ThrowBackParams{ID: bottleID, HeldBy: identityID}); err != nil {
		return pgtype.UUID{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}

	// 订阅双方在线连接，并向投放者推送 bottle.reply。
	s.hub.SubscribeIdentity(b.IdentityID, conv.ID)
	s.hub.SubscribeIdentity(identityID, conv.ID)
	if env, e := realtime.NewEnvelope(realtime.TypeBottleReply, "", map[string]any{
		"bottleId":       bottleID.String(),
		"conversationId": conv.ID.String(),
	}); e == nil {
		s.hub.SendToIdentity(b.IdentityID, env)
	}
	return conv.ID, nil
}

// MineReply 是“我的瓶子”里的一个回复会话。
type MineReply struct {
	ConversationID pgtype.UUID
	Unread         int64
}

// MineBottle 是“我的瓶子”条目。
type MineBottle struct {
	ID             pgtype.UUID
	ContentPreview string
	CreatedAt      pgtype.Timestamptz
	Replies        []MineReply
}

// Mine 返回本人投放、未过期的瓶子及回复。
func (s *Service) Mine(ctx context.Context, identityID pgtype.UUID) ([]MineBottle, error) {
	rows, err := s.q.ListMyBottles(ctx, store.ListMyBottlesParams{
		IdentityID: identityID,
		ExpiresAt:  pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]MineBottle, 0, len(rows))
	for _, row := range rows {
		replies := make([]MineReply, 0)
		rrows, err := s.q.ListBottleReplies(ctx, store.ListBottleRepliesParams{BottleID: row.ID, SenderIdentityID: identityID})
		if err != nil {
			return nil, err
		}
		for _, rr := range rrows {
			replies = append(replies, MineReply{ConversationID: rr.ConversationID, Unread: rr.Unread})
		}
		out = append(out, MineBottle{
			ID:             row.ID,
			ContentPreview: preview(row.Content, 50),
			CreatedAt:      row.CreatedAt,
			Replies:        replies,
		})
	}
	return out, nil
}

// Report 举报当前持有的瓶子（生成快照）并把它放回海里。
func (s *Service) Report(ctx context.Context, identityID, bottleID pgtype.UUID, reason, note string) (pgtype.UUID, error) {
	if s.reportSvc == nil {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInternal, "服务器内部错误")
	}
	b, err := s.q.LockBottleByID(ctx, bottleID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "瓶子不存在")
		}
		return pgtype.UUID{}, err
	}
	if b.HeldBy != identityID {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "只有当前持有者可以举报")
	}
	snapshot := map[string]any{
		"bottle": map[string]any{
			"id":        b.ID.String(),
			"content":   b.Content,
			"createdAt": formatTS(b.CreatedAt),
		},
	}
	reportID, err := s.reportSvc.CreateReport(ctx, identityID, b.IdentityID, pgtype.UUID{}, reason, note, snapshot)
	if err != nil {
		return pgtype.UUID{}, err
	}
	// 举报后放回海里。
	if err := s.q.ReleaseBottleByHolder(ctx, store.ReleaseBottleByHolderParams{ID: bottleID, HeldBy: identityID}); err != nil {
		s.logger.Warn("举报后释放瓶子失败", "error", err)
	}
	return reportID, nil
}

// ---- 辅助 ----

func genUUID() (pgtype.UUID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: b, Valid: true}, nil
}

func trimSpace(s string) string { return strings.TrimSpace(s) }

func preview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
