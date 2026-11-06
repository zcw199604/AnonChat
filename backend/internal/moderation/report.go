package moderation

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 举报相关常量。
const (
	maxReportMessages = 20
	maxReportNoteRune = 500
	contextBefore     = 10
	contextAfter      = 10
	eventReport       = "report"
)

// 允许的举报原因。
var allowedReportReasons = map[string]struct{}{
	"spam": {}, "harassment": {}, "porn": {}, "illegal": {}, "other": {},
}

// AccessLogger 写访问审计日志（由 identity.Service 实现）。
type AccessLogger interface {
	LogAccess(ctx context.Context, identityID pgtype.UUID, event, ip, userAgent string)
}

// ReportRequest 是举报请求。
type ReportRequest struct {
	ConversationID pgtype.UUID
	MemberID       int32
	MessageIDs     []int64
	Reason         string
	Note           string
}

// snapshotItem 是证据快照中的一条消息。
type snapshotItem struct {
	ID        int64  `json:"id"`
	MemberID  int32  `json:"memberId"`
	Nickname  string `json:"nickname"`
	Body      string `json:"body"`
	CreatedAt string `json:"createdAt"`
	BurnedAt  string `json:"burnedAt,omitempty"`
	Reported  bool   `json:"reported"`
}

// ReportService 负责举报与证据快照。
type ReportService struct {
	q         *store.Queries
	logger    *slog.Logger
	limiter   RateLimiter
	accessLog AccessLogger
	now       func() time.Time
}

// ReportOption 配置 ReportService。
type ReportOption func(*ReportService)

// WithReportClock 注入时钟。
func WithReportClock(fn func() time.Time) ReportOption {
	return func(r *ReportService) {
		if fn != nil {
			r.now = fn
		}
	}
}

// NewReportService 创建举报服务。
func NewReportService(pool *pgxpool.Pool, logger *slog.Logger, limiter RateLimiter, accessLog AccessLogger, opts ...ReportOption) *ReportService {
	if limiter == nil {
		limiter = AllowAll()
	}
	r := &ReportService{
		q:         store.New(pool),
		logger:    logger,
		limiter:   limiter,
		accessLog: accessLog,
		now:       time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Report 校验并创建举报，返回举报 ID。
func (r *ReportService) Report(ctx context.Context, reporterID pgtype.UUID, req ReportRequest, ip string) (pgtype.UUID, error) {
	if _, ok := allowedReportReasons[req.Reason]; !ok {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "举报原因非法")
	}
	if len(req.MessageIDs) < 1 || len(req.MessageIDs) > maxReportMessages {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "举报消息条数须为 1-20")
	}
	if utf8.RuneCountInString(req.Note) > maxReportNoteRune {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "备注不能超过 500 字")
	}

	// 举报者必须是该会话成员（含已离开）。
	if _, err := r.q.GetMember(ctx, store.GetMemberParams{ConversationID: req.ConversationID, IdentityID: reporterID}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeNotMember, "你不在该会话中")
		}
		return pgtype.UUID{}, err
	}

	// 被举报成员必须存在。
	target, err := r.q.GetMemberByMemberID(ctx, store.GetMemberByMemberIDParams{
		ConversationID: req.ConversationID,
		MemberID:       req.MemberID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "被举报成员不存在")
		}
		return pgtype.UUID{}, err
	}

	// 频率限制（每身份每天上限）。
	if !r.limiter.Allow(ctx, "report:"+reporterID.String()) {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeRateLimited, "举报过于频繁，请稍后再试")
	}

	// 校验每条被举报消息属于该会话且发送者为该 memberId。
	for _, id := range req.MessageIDs {
		msg, err := r.q.GetMessageForReport(ctx, store.GetMessageForReportParams{ID: id, ConversationID: req.ConversationID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "举报消息不属于该会话")
			}
			return pgtype.UUID{}, err
		}
		if msg.SenderMemberID != req.MemberID {
			return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "被举报消息的发送者与 memberId 不符")
		}
	}

	snapshot, err := r.buildSnapshot(ctx, req.ConversationID, req.MessageIDs)
	if err != nil {
		return pgtype.UUID{}, err
	}
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return pgtype.UUID{}, err
	}

	var note *string
	if req.Note != "" {
		note = &req.Note
	}
	reportID, err := r.q.InsertReport(ctx, store.InsertReportParams{
		ReporterIdentityID: reporterID,
		ReportedIdentityID: target.IdentityID,
		ConversationID:     req.ConversationID,
		Reason:             req.Reason,
		Note:               note,
		Snapshot:           snapshotJSON,
	})
	if err != nil {
		return pgtype.UUID{}, err
	}

	if r.accessLog != nil {
		r.accessLog.LogAccess(ctx, reporterID, eventReport, ip, "")
	}
	return reportID, nil
}

// buildSnapshot 为每条被举报消息附加前后各 10 条上下文，按 id 去重升序。
func (r *ReportService) buildSnapshot(ctx context.Context, convID pgtype.UUID, messageIDs []int64) ([]snapshotItem, error) {
	reported := make(map[int64]struct{}, len(messageIDs))
	for _, id := range messageIDs {
		reported[id] = struct{}{}
	}
	items := make(map[int64]snapshotItem)

	for _, id := range messageIDs {
		before, err := r.q.ListReportContextBefore(ctx, store.ListReportContextBeforeParams{ConversationID: convID, ID: id})
		if err != nil {
			return nil, err
		}
		for _, row := range before {
			items[row.ID] = toSnapshotItem(row.ID, row.SenderMemberID, row.Nickname, row.Body, row.CreatedAt, row.BurnedAt, reported)
		}
		after, err := r.q.ListReportContextAfter(ctx, store.ListReportContextAfterParams{ConversationID: convID, ID: id})
		if err != nil {
			return nil, err
		}
		for _, row := range after {
			items[row.ID] = toSnapshotItem(row.ID, row.SenderMemberID, row.Nickname, row.Body, row.CreatedAt, row.BurnedAt, reported)
		}
	}

	out := make([]snapshotItem, 0, len(items))
	for _, it := range items {
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// toSnapshotItem 构造快照条目。
func toSnapshotItem(id int64, memberID int32, nickname, body string, createdAt, burnedAt pgtype.Timestamptz, reported map[int64]struct{}) snapshotItem {
	_, isReported := reported[id]
	it := snapshotItem{
		ID:        id,
		MemberID:  memberID,
		Nickname:  nickname,
		Body:      body,
		CreatedAt: formatSnapshotTS(createdAt),
		Reported:  isReported,
	}
	if burnedAt.Valid {
		it.BurnedAt = formatSnapshotTS(burnedAt)
	}
	return it
}

// formatSnapshotTS 格式化时间戳。
func formatSnapshotTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

// CreateReport 创建一条不依赖会话的举报（如漂流瓶），复用原因白名单与每日限额（与会话举报共享配额）。
func (r *ReportService) CreateReport(ctx context.Context, reporterID, reportedID, conversationID pgtype.UUID, reason, note string, snapshot any) (pgtype.UUID, error) {
	if _, ok := allowedReportReasons[reason]; !ok {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "举报原因非法")
	}
	if !r.limiter.Allow(ctx, "report:"+reporterID.String()) {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeRateLimited, "举报过于频繁，请稍后再试")
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return pgtype.UUID{}, err
	}
	var notePtr *string
	if note != "" {
		notePtr = &note
	}
	return r.q.InsertReport(ctx, store.InsertReportParams{
		ReporterIdentityID: reporterID,
		ReportedIdentityID: reportedID,
		ConversationID:     conversationID,
		Reason:             reason,
		Note:               notePtr,
		Snapshot:           b,
	})
}
