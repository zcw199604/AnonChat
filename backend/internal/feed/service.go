// Package feed 负责公告、广播、匿名动态聚合与在线统计（how.md 3.3）。
package feed

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// OnlineProvider 提供在线统计快照（由 api 装配层注入 hub / 匹配器 / 房间计数）。
type OnlineProvider interface {
	Online() (online, matching, inRooms int)
	// RoomOnline 只做开销较大的房间在线聚合（会被缓存 10 秒）。
	RoomOnline() int
}

// Broadcaster 用于广播与作品的举报快照过滤。
type Filter interface {
	Filter(ctx context.Context, text string) (string, bool)
}

// Service 提供公告、广播与动态聚合。
type Service struct {
	pool    *pgxpool.Pool
	q       *store.Queries
	logger  *slog.Logger
	filter  Filter
	online  OnlineProvider
	postlim moderation.RateLimiter
	now     func() time.Time

	onlineMu   sync.Mutex
	onlineAt   time.Time
	onlineSnap OnlineStats
}

// OnlineStats 是 GET /api/stats/online 的响应。
type OnlineStats struct {
	Online   int `json:"online"`
	Matching int `json:"matching"`
	InRooms  int `json:"inRooms"`
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

// WithFilter 注入敏感词过滤器（广播提交前先过一遍）。
func WithFilter(f Filter) Option {
	return func(s *Service) {
		if f != nil {
			s.filter = f
		}
	}
}

// WithOnlineProvider 注入在线统计来源。
func WithOnlineProvider(p OnlineProvider) Option {
	return func(s *Service) {
		if p != nil {
			s.online = p
		}
	}
}

// WithBroadcastLimiter 注入广播提交限流器。
func WithBroadcastLimiter(l moderation.RateLimiter) Option {
	return func(s *Service) {
		if l != nil {
			s.postlim = l
		}
	}
}

// New 创建 feed 服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		pool:    pool,
		q:       store.New(pool),
		logger:  logger,
		postlim: moderation.AllowAll(),
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ---- 公告 ----

// Announcement 是对外的公告表示。
type Announcement struct {
	ID        string `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"createdAt"`
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// ActiveAnnouncements 返回当前有效公告（最多 5 条）。
func (s *Service) ActiveAnnouncements(ctx context.Context) ([]Announcement, error) {
	rows, err := s.q.ListActiveAnnouncements(ctx, 5)
	if err != nil {
		return nil, err
	}
	out := make([]Announcement, 0, len(rows))
	for _, r := range rows {
		item := Announcement{ID: r.ID.String(), Content: r.Content, CreatedAt: formatTS(r.CreatedAt)}
		if r.EndsAt.Valid {
			item.ExpiresAt = formatTS(r.EndsAt)
		}
		out = append(out, item)
	}
	return out, nil
}

// AdminAnnouncements 返回管理后台的公告列表。
func (s *Service) AdminAnnouncements(ctx context.Context, limit int32) ([]Announcement, error) {
	rows, err := s.q.ListAdminAnnouncements(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Announcement, 0, len(rows))
	for _, r := range rows {
		item := Announcement{ID: r.ID.String(), Content: r.Content, CreatedAt: formatTS(r.CreatedAt)}
		if r.EndsAt.Valid {
			item.ExpiresAt = formatTS(r.EndsAt)
		}
		out = append(out, item)
	}
	return out, nil
}

// CreateAnnouncement 创建公告（内容 1–120 字）。
func (s *Service) CreateAnnouncement(ctx context.Context, content string, endsAt *time.Time, adminID pgtype.UUID) (Announcement, error) {
	if n := len([]rune(content)); n < 1 || n > 120 {
		return Announcement{}, httpx.NewError(httpx.CodeInvalidRequest, "公告内容需 1–120 个字符")
	}
	row, err := s.q.CreateAnnouncement(ctx, store.CreateAnnouncementParams{
		Content:   content,
		EndsAt:    tsPtr(endsAt),
		CreatedBy: adminID,
	})
	if err != nil {
		return Announcement{}, err
	}
	return Announcement{ID: row.ID.String(), Content: row.Content, CreatedAt: formatTS(row.CreatedAt)}, nil
}

// DeleteAnnouncement 软删除公告。
func (s *Service) DeleteAnnouncement(ctx context.Context, id pgtype.UUID) error {
	affected, err := s.q.SoftDeleteAnnouncement(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeNotFound, "公告不存在")
	}
	return nil
}

// ---- 广播 ----

// Broadcast 是对外的广播表示（不含任何身份信息）。
type Broadcast struct {
	ID           string  `json:"id"`
	Content      string  `json:"content"`
	Status       string  `json:"status"`
	RejectReason *string `json:"rejectReason,omitempty"`
	ShowUntil    *string `json:"showUntil,omitempty"`
	CreatedAt    string  `json:"createdAt"`
}

// SubmitBroadcast 提交广播：先过违禁词，再扣 1 张广播卡（事务内），状态 pending。
func (s *Service) SubmitBroadcast(ctx context.Context, identID pgtype.UUID, content string) (Broadcast, error) {
	if !s.postlim.Allow(ctx, "broadcast_post:"+identID.String()) {
		return Broadcast{}, httpx.NewError(httpx.CodeRateLimited, "今天提交广播的次数已用完")
	}
	if n := len([]rune(content)); n < 1 || n > 60 {
		return Broadcast{}, httpx.NewError(httpx.CodeInvalidRequest, "广播内容需 1–60 个字符")
	}
	// 违禁词：blocked 时整体拒绝，否则保存打码后的文本（审查意见 R1）。
	if s.filter != nil {
		masked, blocked := s.filter.Filter(ctx, content)
		if blocked {
			return Broadcast{}, httpx.NewError(httpx.CodeContentBlocked, "内容包含违规信息")
		}
		content = masked
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Broadcast{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	affected, err := q.ConsumeBroadcastCard(ctx, identID)
	if err != nil {
		return Broadcast{}, err
	}
	if affected == 0 {
		return Broadcast{}, httpx.NewError(httpx.CodeNoBroadcastCard, "没有可用的广播卡")
	}
	row, err := q.CreateBroadcast(ctx, store.CreateBroadcastParams{IdentityID: identID, Content: content})
	if err != nil {
		return Broadcast{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Broadcast{}, err
	}
	return Broadcast{
		ID:        row.ID.String(),
		Content:   row.Content,
		Status:    row.Status,
		CreatedAt: formatTS(row.CreatedAt),
	}, nil
}

// CurrentBroadcasts 返回展示中的广播（approved 且 show_until > now，最多 3 条）。
func (s *Service) CurrentBroadcasts(ctx context.Context) ([]Broadcast, error) {
	rows, err := s.q.ListCurrentBroadcasts(ctx, 3)
	if err != nil {
		return nil, err
	}
	out := make([]Broadcast, 0, len(rows))
	for _, r := range rows {
		item := Broadcast{ID: r.ID.String(), Content: r.Content, Status: "approved", CreatedAt: formatTS(r.CreatedAt)}
		if r.ShowUntil.Valid {
			v := formatTS(r.ShowUntil)
			item.ShowUntil = &v
		}
		out = append(out, item)
	}
	return out, nil
}

// MyBroadcasts 返回自己提交的广播及审核状态。
func (s *Service) MyBroadcasts(ctx context.Context, identID pgtype.UUID) ([]Broadcast, error) {
	rows, err := s.q.ListMyBroadcasts(ctx, store.ListMyBroadcastsParams{IdentityID: identID, Limit: 20})
	if err != nil {
		return nil, err
	}
	out := make([]Broadcast, 0, len(rows))
	for _, r := range rows {
		item := Broadcast{
			ID:           r.ID.String(),
			Content:      r.Content,
			Status:       r.Status,
			RejectReason: r.RejectReason,
			CreatedAt:    formatTS(r.CreatedAt),
		}
		if r.ShowUntil.Valid {
			v := formatTS(r.ShowUntil)
			item.ShowUntil = &v
		}
		out = append(out, item)
	}
	return out, nil
}

// AdminBroadcast 是审核队列条目（含提交者的匿名编号，便于定位）。
type AdminBroadcast struct {
	Broadcast
	PublicCode string `json:"publicCode"`
}

// AdminBroadcasts 返回广播审核队列。
func (s *Service) AdminBroadcasts(ctx context.Context, status *string, limit int32) ([]AdminBroadcast, error) {
	rows, err := s.q.ListAdminBroadcasts(ctx, store.ListAdminBroadcastsParams{Status: status, Lim: limit})
	if err != nil {
		return nil, err
	}
	out := make([]AdminBroadcast, 0, len(rows))
	for _, r := range rows {
		item := AdminBroadcast{
			Broadcast: Broadcast{
				ID:           r.ID.String(),
				Content:      r.Content,
				Status:       r.Status,
				RejectReason: r.RejectReason,
				CreatedAt:    formatTS(r.CreatedAt),
			},
			PublicCode: r.PublicCode,
		}
		if r.ShowUntil.Valid {
			v := formatTS(r.ShowUntil)
			item.ShowUntil = &v
		}
		out = append(out, item)
	}
	return out, nil
}

// ApproveBroadcast 通过审核：设置 show_until = now + 24h。
func (s *Service) ApproveBroadcast(ctx context.Context, id, adminID pgtype.UUID) error {
	affected, err := s.q.ApproveBroadcast(ctx, store.ApproveBroadcastParams{ID: id, ReviewedBy: adminID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "广播不在待审核状态")
	}
	return nil
}

// RejectBroadcast 驳回广播并退还广播卡（事务内）。
func (s *Service) RejectBroadcast(ctx context.Context, id, adminID pgtype.UUID, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	bc, err := q.GetBroadcast(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotFound, "广播不存在")
		}
		return err
	}
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	affected, err := q.RejectBroadcast(ctx, store.RejectBroadcastParams{
		ID:           id,
		ReviewedBy:   adminID,
		RejectReason: reasonPtr,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "广播不在待审核状态")
	}
	if err := q.RefundBroadcastCard(ctx, bc.IdentityID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RemoveBroadcast 下架广播（不退还广播卡）。
func (s *Service) RemoveBroadcast(ctx context.Context, id, adminID pgtype.UUID) error {
	affected, err := s.q.RemoveBroadcast(ctx, store.RemoveBroadcastParams{ID: id, ReviewedBy: adminID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "广播状态不允许下架")
	}
	return nil
}

// ReportBroadcast 举报广播：写入 reports（target_type=broadcast）。
func (s *Service) ReportBroadcast(ctx context.Context, reporter pgtype.UUID, broadcastID pgtype.UUID, reason, note string) (pgtype.UUID, error) {
	bc, err := s.q.GetBroadcast(ctx, broadcastID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeNotFound, "广播不存在")
		}
		return pgtype.UUID{}, err
	}
	if bc.IdentityID == reporter {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "不能举报自己")
	}
	snapshot, _ := json.Marshal(map[string]any{"type": "broadcast", "id": bc.ID.String(), "content": bc.Content})
	target := bc.ID.String()
	return s.q.InsertTypedReport(ctx, store.InsertTypedReportParams{
		ReporterIdentityID: reporter,
		ReportedIdentityID: bc.IdentityID,
		ConversationID:     pgtype.UUID{},
		Reason:             reason,
		Note:               notePtr(note),
		Snapshot:           snapshot,
		TargetType:         "broadcast",
		TargetID:           &target,
	})
}

// ---- 匿名动态 ----

// FeedItem 是匿名动态条目。
type FeedItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
	At   string `json:"at"`
}

// Timeline 聚合公告、展示中的广播与已通过的作品，按时间倒序，最多 20 条。
func (s *Service) Timeline(ctx context.Context) ([]FeedItem, error) {
	items := make([]FeedItem, 0, 30)
	anns, err := s.q.ListFeedAnnouncements(ctx, 10)
	if err != nil {
		return nil, err
	}
	for _, a := range anns {
		items = append(items, FeedItem{Type: "announcement", ID: a.ID.String(), Text: a.Content, At: formatTS(a.CreatedAt)})
	}
	bcs, err := s.q.ListFeedBroadcasts(ctx, 10)
	if err != nil {
		return nil, err
	}
	for _, b := range bcs {
		items = append(items, FeedItem{Type: "broadcast", ID: b.ID.String(), Text: b.Content, At: formatTS(b.At)})
	}
	works, err := s.q.ListFeedWorks(ctx, 10)
	if err != nil {
		return nil, err
	}
	for _, w := range works {
		items = append(items, FeedItem{Type: "work", ID: w.ID.String(), Text: "《" + w.Title + "》", At: formatTS(w.At)})
	}
	sortByAtDesc(items)
	if len(items) > 20 {
		items = items[:20]
	}
	return items, nil
}

// Stats 返回在线统计。
// 房间在线聚合会遍历全部房间，按 how.md 缓存 10 秒；在线人数与排队人数是廉价的
// 内存计数，实时读取，避免刚连上时读到过期值。
func (s *Service) Stats(ctx context.Context) OnlineStats {
	_ = ctx
	if s.online == nil {
		return OnlineStats{}
	}
	s.onlineMu.Lock()
	defer s.onlineMu.Unlock()
	if s.now().Sub(s.onlineAt) >= 10*time.Second {
		s.onlineSnap.InRooms = s.online.RoomOnline()
		s.onlineAt = s.now()
	}
	online, matching, _ := s.online.Online()
	return OnlineStats{Online: online, Matching: matching, InRooms: s.onlineSnap.InRooms}
}

// ---- 工具 ----

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

func notePtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// sortByAtDesc 按时间字符串（RFC3339）倒序排序。
func sortByAtDesc(items []FeedItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].At > items[j-1].At; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
