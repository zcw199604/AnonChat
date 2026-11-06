// Package story 实现故事书（公测）：发起、认领、开演、结束判定、收录同意、作品生成
// 与作品的阅读/点赞/删除/撤回（how.md 3.3、3.5）。
package story

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 默认参数：有效场次要求时长 ≥ 5 分钟且双方各 ≥ 5 条消息（how.md 3.5）。
const (
	DefaultMinDuration = 5 * time.Minute
	MinMessages        = 5
	// WaitExpiry 是等待态的过期时间。
	WaitExpiry = 24 * time.Hour
)

// storyKind 是故事会话的类型。
const storyKind = store.ConversationKind("story")

// Filter 是内容过滤器接口（复用 moderation.Moderator）。
type Filter interface {
	Filter(ctx context.Context, text string) (string, bool)
}

// Service 提供故事书的领域操作。
type Service struct {
	pool        *pgxpool.Pool
	q           *store.Queries
	logger      *slog.Logger
	convSvc     *conversation.Service
	broker      realtime.Broker
	subscriber  realtime.Subscriber
	filter      Filter
	limiter     moderation.RateLimiter
	minDuration time.Duration
	now         func() time.Time
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

// WithMinDuration 设置有效场次的最短时长（测试可用 STORY_MIN_DURATION 调短）。
func WithMinDuration(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.minDuration = d
		}
	}
}

// WithFilter 注入敏感词过滤器。
func WithFilter(f Filter) Option {
	return func(s *Service) {
		if f != nil {
			s.filter = f
		}
	}
}

// WithLimiter 注入发起故事的限流器。
func WithLimiter(l moderation.RateLimiter) Option {
	return func(s *Service) {
		if l != nil {
			s.limiter = l
		}
	}
}

// New 创建故事书服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, convSvc *conversation.Service, hub realtime.Realtime, opts ...Option) *Service {
	s := &Service{
		pool:        pool,
		q:           store.New(pool),
		logger:      logger,
		convSvc:     convSvc,
		broker:      hub,
		subscriber:  hub,
		limiter:     moderation.AllowAll(),
		minDuration: DefaultMinDuration,
		now:         time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreateInput 是发起故事的入参。
type CreateInput struct {
	Title    string
	Opening  string
	Tags     []string
	RoleA    string
	RoleB    string
	HostRole string
}

// Story 是对外的故事表示。
type Story struct {
	ID             string   `json:"id"`
	Title          string   `json:"title"`
	Opening        string   `json:"opening"`
	Tags           []string `json:"tags"`
	RoleA          string   `json:"roleA"`
	RoleB          string   `json:"roleB"`
	HostRole       string   `json:"hostRole"`
	Status         string   `json:"status"`
	Valid          *bool    `json:"valid"`
	HostConsent    bool     `json:"hostConsent"`
	GuestConsent   bool     `json:"guestConsent"`
	ConversationID *string  `json:"conversationId"`
	MyRole         *string  `json:"myRole"`
	ExpiresAt      string   `json:"expiresAt"`
	CreatedAt      string   `json:"createdAt"`
}

// Works 生成用的行结构。
type workLine struct {
	Role string `json:"role"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

// Create 发起故事：标题、开场与角色名过违禁词（blocked 时拒绝，否则保存打码后的文本），
// 标签必须属于预设列表且去重，同一身份同时最多 1 个 waiting/active 故事。
func (s *Service) Create(ctx context.Context, host *store.Identity, in CreateInput) (Story, error) {
	if !s.limiter.Allow(ctx, "story_create:"+host.ID.String()) {
		return Story{}, httpx.NewError(httpx.CodeRateLimited, "发起太频繁了，请稍后再试")
	}
	// 标签先去重，再按 profile 常量校验取值与个数（审查意见 R2）。
	in.Tags = dedupeTags(in.Tags)
	if msg := validateCreate(in); msg != "" {
		return Story{}, httpx.NewError(httpx.CodeInvalidRequest, msg)
	}
	if s.filter != nil {
		for _, f := range []struct {
			field string
			text  *string
		}{
			{"标题", &in.Title},
			{"开场", &in.Opening},
			{"角色名", &in.RoleA},
			{"角色名", &in.RoleB},
		} {
			masked, blocked := s.filter.Filter(ctx, *f.text)
			if blocked {
				return Story{}, httpx.NewError(httpx.CodeContentBlocked, f.field+"包含违规信息")
			}
			*f.text = masked
		}
	}
	active, err := s.q.CountActiveStoriesByIdentity(ctx, host.ID)
	if err != nil {
		return Story{}, err
	}
	if active > 0 {
		return Story{}, httpx.NewError(httpx.CodeQuotaExceeded, "同时只能有 1 个进行中的故事")
	}
	tags := in.Tags
	if tags == nil {
		tags = []string{}
	}
	row, err := s.q.CreateStory(ctx, store.CreateStoryParams{
		HostIdentityID: host.ID,
		Title:          in.Title,
		Opening:        in.Opening,
		Tags:           tags,
		RoleA:          in.RoleA,
		RoleB:          in.RoleB,
		HostRole:       in.HostRole,
		ExpiresAt:      pgtype.Timestamptz{Time: s.now().Add(WaitExpiry), Valid: true},
	})
	if err != nil {
		return Story{}, err
	}
	return s.toStory(row, host.ID), nil
}

func validateCreate(in CreateInput) string {
	t := len([]rune(in.Title))
	if t < 2 || t > 30 {
		return "标题需 2–30 个字符"
	}
	o := len([]rune(in.Opening))
	if o < 10 || o > 500 {
		return "开场需 10–500 个字符"
	}
	if r := len([]rune(in.RoleA)); r < 1 || r > 12 {
		return "角色名需 1–12 个字符"
	}
	if r := len([]rune(in.RoleB)); r < 1 || r > 12 {
		return "角色名需 1–12 个字符"
	}
	if in.RoleA == in.RoleB {
		return "两个角色名不能相同"
	}
	if in.HostRole != "a" && in.HostRole != "b" {
		return "发起人角色非法"
	}
	// 标签个数、取值与重复由 profile 包已有的校验函数处理（审查意见 R2）。
	if msg := profile.ValidateTags(in.Tags); msg != "" {
		return msg
	}
	return ""
}

// dedupeTags 按首次出现顺序去重（保留 nil 语义）。
func dedupeTags(tags []string) []string {
	if tags == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(tags))
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if _, dup := seen[t]; dup {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// WaitingItem 是等待开演列表的元素（不含发起人信息）。
type WaitingItem struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Opening   string   `json:"opening"`
	Tags      []string `json:"tags"`
	RoleA     string   `json:"roleA"`
	RoleB     string   `json:"roleB"`
	OpenRole  string   `json:"openRole"`
	ExpiresAt string   `json:"expiresAt"`
	CreatedAt string   `json:"createdAt"`
}

// ListWaiting 返回等待开演的故事（分页参数 before 为创建时间）。
func (s *Service) ListWaiting(ctx context.Context, before *time.Time, limit int32) ([]WaitingItem, error) {
	rows, err := s.q.ListWaitingStories(ctx, store.ListWaitingStoriesParams{
		BeforeCreated: tsPtr(before),
		Lim:           limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WaitingItem, 0, len(rows))
	for _, r := range rows {
		openRole := r.RoleB
		if r.HostRole == "b" {
			openRole = r.RoleA
		}
		tags := r.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, WaitingItem{
			ID:        r.ID.String(),
			Title:     r.Title,
			Opening:   r.Opening,
			Tags:      tags,
			RoleA:     r.RoleA,
			RoleB:     r.RoleB,
			OpenRole:  openRole,
			ExpiresAt: formatTS(r.ExpiresAt),
			CreatedAt: formatTS(r.CreatedAt),
		})
	}
	return out, nil
}

// Detail 返回故事详情；conversationId 只对参与者可见。
func (s *Service) Detail(ctx context.Context, id, viewer pgtype.UUID) (Story, error) {
	row, err := s.q.GetStory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Story{}, httpx.NewError(httpx.CodeNotFound, "故事不存在")
		}
		return Story{}, err
	}
	out := s.toStory(row, viewer)
	if out.MyRole == nil {
		out.ConversationID = nil
		out.HostConsent = false
		out.GuestConsent = false
		out.Valid = nil
	}
	return out, nil
}

// Join 认领剩下的角色：status 从 waiting 变为 active，并创建 kind=story 的会话。
func (s *Service) Join(ctx context.Context, id, guest pgtype.UUID) (string, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	row, err := q.LockStory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", httpx.NewError(httpx.CodeNotFound, "故事不存在")
		}
		return "", err
	}
	if row.HostIdentityID == guest {
		return "", httpx.NewError(httpx.CodeInvalidRequest, "不能认领自己的故事")
	}
	// 存在任一方向的拉黑关系时拒绝认领（审查意见 R4）。不透露拉黑事实，统一返回 story_taken。
	blocked, err := q.ExistsBlockBetween(ctx, store.ExistsBlockBetweenParams{
		BlockerIdentityID: row.HostIdentityID,
		BlockedIdentityID: guest,
	})
	if err != nil {
		return "", err
	}
	if blocked {
		return "", httpx.NewError(httpx.CodeStoryTaken, "这个故事已经被认领了")
	}
	if row.Status != "waiting" || !row.ExpiresAt.Time.After(s.now()) {
		return "", httpx.NewError(httpx.CodeStoryTaken, "这个故事已经被认领了")
	}
	affected, err := q.JoinStory(ctx, store.JoinStoryParams{ID: id, GuestIdentityID: pgUUID(guest)})
	if err != nil {
		return "", err
	}
	if affected == 0 {
		return "", httpx.NewError(httpx.CodeStoryTaken, "这个故事已经被认领了")
	}
	conv, members, err := s.convSvc.Create(ctx, tx, storyKind, false, row.HostIdentityID, guest)
	if err != nil {
		return "", err
	}
	if err := q.SetStoryConversation(ctx, store.SetStoryConversationParams{ID: id, ConversationID: pgUUID(conv.ID)}); err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if len(members) == 2 {
		if s.subscriber != nil {
			s.subscriber.SubscribeIdentity(row.HostIdentityID, conv.ID)
			s.subscriber.SubscribeIdentity(guest, conv.ID)
		}
		if s.broker != nil {
			if env, e := realtime.NewEnvelope(realtime.TypeStoryStarted, "", map[string]string{
				"storyId":        id.String(),
				"conversationId": conv.ID.String(),
			}); e == nil {
				s.broker.SendToIdentity(row.HostIdentityID, env)
			}
		}
	}
	return conv.ID.String(), nil
}

// End 结束故事：服务端判定 valid（时长 ≥ STORY_MIN_DURATION 且双方各 ≥ 5 条非系统消息）。
func (s *Service) End(ctx context.Context, id, actor pgtype.UUID) (Story, error) {
	row, err := s.q.GetStory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Story{}, httpx.NewError(httpx.CodeNotFound, "故事不存在")
		}
		return Story{}, err
	}
	if !isParticipant(row, actor) {
		return Story{}, httpx.NewError(httpx.CodeNotMember, "你不是这场故事的参与者")
	}
	if row.Status != "active" {
		return Story{}, httpx.NewError(httpx.CodeInvalidRequest, "故事不在进行中")
	}
	valid, err := s.judgeValid(ctx, row)
	if err != nil {
		return Story{}, err
	}
	affected, err := s.q.EndStory(ctx, store.EndStoryParams{ID: id, Valid: &valid})
	if err != nil {
		return Story{}, err
	}
	if affected == 0 {
		return Story{}, httpx.NewError(httpx.CodeInvalidRequest, "故事不在进行中")
	}
	if row.ConversationID.Valid {
		_ = s.convSvc.End(ctx, row.ConversationID, "ended")
	}
	if s.broker != nil {
		if env, e := realtime.NewEnvelope(realtime.TypeStoryEnded, "", map[string]any{
			"storyId": id.String(),
			"valid":   valid,
		}); e == nil {
			s.broker.SendToIdentity(row.HostIdentityID, env)
			if row.GuestIdentityID.Valid {
				s.broker.SendToIdentity(row.GuestIdentityID, env)
			}
		}
	}
	updated, err := s.q.GetStory(ctx, id)
	if err != nil {
		return Story{}, err
	}
	return s.toStory(updated, actor), nil
}

// judgeValid 判定一场故事是否有效。
func (s *Service) judgeValid(ctx context.Context, row store.Story) (bool, error) {
	if !row.ActiveAt.Valid {
		return false, nil
	}
	var member1, member2 int64
	if row.ConversationID.Valid {
		counts, err := s.q.CountStoryMessages(ctx, row.ConversationID)
		if err != nil {
			// conversation_id 为空（例如会话已被清理）时按 0 条处理。
			return false, nil
		}
		member1, member2 = counts.Member1, counts.Member2
	}
	return validSession(row.ActiveAt.Time, s.now(), s.minDuration, member1, member2), nil
}

// validSession 是有效场次的纯函数判定（how.md 3.5）：
// 时长 ≥ minDuration 且双方各有 ≥ MinMessages 条非系统消息。
func validSession(activeAt, endedAt time.Time, minDuration time.Duration, member1, member2 int64) bool {
	if endedAt.Sub(activeAt) < minDuration {
		return false
	}
	return member1 >= int64(MinMessages) && member2 >= int64(MinMessages)
}

// Cancel 发起人在等待状态下取消。
func (s *Service) Cancel(ctx context.Context, id, host pgtype.UUID) error {
	row, err := s.q.GetStory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotFound, "故事不存在")
		}
		return err
	}
	if row.HostIdentityID != host {
		return httpx.NewError(httpx.CodeInvalidRequest, "只有发起人可以取消")
	}
	affected, err := s.q.CancelStory(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "故事不在等待状态")
	}
	return nil
}

// Consent 设置收录同意；双方都同意时生成作品（status=pending）。
func (s *Service) Consent(ctx context.Context, id, actor pgtype.UUID, agree bool) (Story, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Story{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	row, err := q.LockStory(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Story{}, httpx.NewError(httpx.CodeNotFound, "故事不存在")
		}
		return Story{}, err
	}
	if !isParticipant(row, actor) {
		return Story{}, httpx.NewError(httpx.CodeNotMember, "你不是这场故事的参与者")
	}
	if row.Valid == nil || !*row.Valid {
		return Story{}, httpx.NewError(httpx.CodeStoryNotValid, "这场故事未达到有效条件")
	}
	if row.Status != "ended" {
		return Story{}, httpx.NewError(httpx.CodeInvalidRequest, "故事还未结束")
	}
	updated, err := q.SetStoryConsent(ctx, store.SetStoryConsentParams{
		ID:     id,
		IsHost: row.HostIdentityID == actor,
		Agree:  agree,
	})
	if err != nil {
		return Story{}, err
	}

	var workID *string
	if updated.HostConsent && updated.GuestConsent {
		if existing, gerr := q.GetStoryWorkByStory(ctx, id); gerr == nil && existing.ID.Valid {
			v := existing.ID.String()
			workID = &v
		} else {
			lines, err := s.collectLines(ctx, q, updated)
			if err != nil {
				return Story{}, err
			}
			work, err := q.CreateStoryWork(ctx, store.CreateStoryWorkParams{
				StoryID: id,
				Title:   updated.Title,
				Opening: updated.Opening,
				Tags:    updated.Tags,
				RoleA:   updated.RoleA,
				RoleB:   updated.RoleB,
				Lines:   lines,
			})
			if err != nil {
				return Story{}, err
			}
			v := work.ID.String()
			workID = &v
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Story{}, err
	}

	if s.broker != nil {
		payload := map[string]any{
			"storyId":      id.String(),
			"hostConsent":  updated.HostConsent,
			"guestConsent": updated.GuestConsent,
		}
		if workID != nil {
			payload["workId"] = *workID
		}
		if env, e := realtime.NewEnvelope(realtime.TypeStoryConsent, "", payload); e == nil {
			s.broker.SendToIdentity(updated.HostIdentityID, env)
			if updated.GuestIdentityID.Valid {
				s.broker.SendToIdentity(updated.GuestIdentityID, env)
			}
		}
	}
	return s.toStory(updated, actor), nil
}

// collectLines 把会话消息转成作品正文（只含角色与正文，不含任何身份字段）。
func (s *Service) collectLines(ctx context.Context, q *store.Queries, row store.Story) ([]byte, error) {
	if !row.ConversationID.Valid {
		return []byte("[]"), nil
	}
	msgs, err := q.ListStoryMessages(ctx, row.ConversationID)
	if err != nil {
		return nil, err
	}
	hostRole, guestRole := row.RoleA, row.RoleB
	if row.HostRole == "b" {
		hostRole, guestRole = row.RoleB, row.RoleA
	}
	lines := make([]workLine, 0, len(msgs))
	for _, m := range msgs {
		role := hostRole
		if m.SenderMemberID == 2 {
			role = guestRole
		}
		lines = append(lines, workLine{Role: role, Text: m.Body, TS: m.CreatedAt.Time.Unix()})
	}
	return json.Marshal(lines)
}

// ExpireWaiting 把等待超过 24h 的故事置为 expired（留存任务调用）。
func (s *Service) ExpireWaiting(ctx context.Context) (int64, error) {
	return s.q.ExpireWaitingStories(ctx)
}

// DeleteOldEnded 删除结束超过 cutoff 且未生成作品的故事。
func (s *Service) DeleteOldEnded(ctx context.Context, cutoff time.Time) (int64, error) {
	return s.q.DeleteOldEndedStories(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
}

// toStory 把库记录转成对外表示（myRole 依据查看者计算）。
func (s *Service) toStory(row store.Story, viewer pgtype.UUID) Story {
	var myRole *string
	if row.HostIdentityID == viewer {
		r := "host"
		myRole = &r
	} else if row.GuestIdentityID.Valid && row.GuestIdentityID == viewer {
		r := "guest"
		myRole = &r
	}
	var convID *string
	if row.ConversationID.Valid && myRole != nil {
		v := row.ConversationID.String()
		convID = &v
	}
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}
	var valid *bool
	if row.Valid != nil {
		v := *row.Valid
		valid = &v
	}
	return Story{
		ID:             row.ID.String(),
		Title:          row.Title,
		Opening:        row.Opening,
		Tags:           tags,
		RoleA:          row.RoleA,
		RoleB:          row.RoleB,
		HostRole:       row.HostRole,
		Status:         row.Status,
		Valid:          valid,
		HostConsent:    row.HostConsent,
		GuestConsent:   row.GuestConsent,
		ConversationID: convID,
		MyRole:         myRole,
		ExpiresAt:      formatTS(row.ExpiresAt),
		CreatedAt:      formatTS(row.CreatedAt),
	}
}

func isParticipant(row store.Story, id pgtype.UUID) bool {
	if row.HostIdentityID == id {
		return true
	}
	return row.GuestIdentityID.Valid && row.GuestIdentityID == id
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func tsPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func pgUUID(u pgtype.UUID) pgtype.UUID { return u }
