package story

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// WorkListItem 是书架列表元素。
type WorkListItem struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Opening   string   `json:"opening"`
	RoleA     string   `json:"roleA"`
	RoleB     string   `json:"roleB"`
	Tags      []string `json:"tags"`
	LikeCount int32    `json:"likeCount"`
	At        string   `json:"at"`
}

// Work 是作品详情。lines 只含角色与正文，不含任何身份字段。
type Work struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Opening   string     `json:"opening"`
	Tags      []string   `json:"tags"`
	RoleA     string     `json:"roleA"`
	RoleB     string     `json:"roleB"`
	Lines     []workLine `json:"lines"`
	Status    string     `json:"status"`
	LikeCount int32      `json:"likeCount"`
	Liked     bool       `json:"liked"`
	Mine      *string    `json:"mine"`
	CreatedAt string     `json:"createdAt"`
}

// ListWorks 返回已通过的作品（书架）。
func (s *Service) ListWorks(ctx context.Context, before *time.Time, limit int32) ([]WorkListItem, error) {
	rows, err := s.q.ListApprovedWorks(ctx, store.ListApprovedWorksParams{
		BeforeCreated: tsPtr(before),
		Lim:           limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WorkListItem, 0, len(rows))
	for _, r := range rows {
		tags := r.Tags
		if tags == nil {
			tags = []string{}
		}
		out = append(out, WorkListItem{
			ID:        r.ID.String(),
			Title:     r.Title,
			Opening:   r.Opening,
			RoleA:     r.RoleA,
			RoleB:     r.RoleB,
			Tags:      tags,
			LikeCount: r.LikeCount,
			At:        formatTS(r.At),
		})
	}
	return out, nil
}

// GetWork 返回作品详情：approved 对所有人可见；pending/rejected 只对参与者可见。
func (s *Service) GetWork(ctx context.Context, workID, viewer pgtype.UUID) (Work, error) {
	row, err := s.q.GetStoryWork(ctx, workID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Work{}, httpx.NewError(httpx.CodeNotFound, "作品不存在")
		}
		return Work{}, err
	}
	story, err := s.q.GetStory(ctx, row.StoryID)
	if err != nil {
		return Work{}, err
	}
	mine := mineOf(story, viewer)
	switch row.Status {
	case "approved":
	case "removed":
		if mine == nil || *mine != "host" {
			return Work{}, httpx.NewError(httpx.CodeNotFound, "作品不存在")
		}
	default:
		if mine == nil {
			return Work{}, httpx.NewError(httpx.CodeNotFound, "作品不存在")
		}
	}

	var lines []workLine
	if err := json.Unmarshal(row.Lines, &lines); err != nil {
		lines = []workLine{}
	}
	liked := false
	if v, err := s.q.GetWorkLiked(ctx, store.GetWorkLikedParams{WorkID: workID, IdentityID: viewer}); err == nil {
		liked = v
	}
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}
	return Work{
		ID:        row.ID.String(),
		Title:     row.Title,
		Opening:   row.Opening,
		Tags:      tags,
		RoleA:     row.RoleA,
		RoleB:     row.RoleB,
		Lines:     lines,
		Status:    row.Status,
		LikeCount: row.LikeCount,
		Liked:     liked,
		Mine:      mine,
		CreatedAt: formatTS(row.CreatedAt),
	}, nil
}

// SetLike 点赞 / 取消点赞（幂等），并维护 like_count。
func (s *Service) SetLike(ctx context.Context, workID, viewer pgtype.UUID, like bool) (int32, error) {
	row, err := s.q.GetStoryWork(ctx, workID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, httpx.NewError(httpx.CodeNotFound, "作品不存在")
		}
		return 0, err
	}
	if row.Status != "approved" {
		return 0, httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if like {
		if _, err := q.InsertWorkLike(ctx, store.InsertWorkLikeParams{WorkID: workID, IdentityID: viewer}); err != nil {
			return 0, err
		}
	} else {
		if _, err := q.DeleteWorkLike(ctx, store.DeleteWorkLikeParams{WorkID: workID, IdentityID: viewer}); err != nil {
			return 0, err
		}
	}
	count, err := q.CountWorkLikes(ctx, workID)
	if err != nil {
		return 0, err
	}
	if err := q.SetWorkLikeCount(ctx, store.SetWorkLikeCountParams{ID: workID, LikeCount: int32(count)}); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int32(count), nil
}

// DeleteWork 发起人删除作品（状态改为 removed）。
func (s *Service) DeleteWork(ctx context.Context, workID, actor pgtype.UUID) error {
	row, err := s.q.GetStoryWork(ctx, workID)
	if err != nil {
		return httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	story, err := s.q.GetStory(ctx, row.StoryID)
	if err != nil {
		return httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	if story.HostIdentityID != actor {
		return httpx.NewError(httpx.CodeNotMember, "只有发起人可以删除作品")
	}
	if _, err := s.q.RemoveWork(ctx, store.RemoveWorkParams{ID: workID, ReviewedBy: actor}); err != nil {
		return err
	}
	return nil
}

// Withdraw 参与者撤回同意：作品状态改为 removed。
func (s *Service) Withdraw(ctx context.Context, workID, actor pgtype.UUID) error {
	row, err := s.q.GetStoryWork(ctx, workID)
	if err != nil {
		return httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	story, err := s.q.GetStory(ctx, row.StoryID)
	if err != nil {
		return httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	if !isParticipant(story, actor) {
		return httpx.NewError(httpx.CodeNotMember, "你不是这场故事的参与者")
	}
	if _, err := s.q.RemoveWork(ctx, store.RemoveWorkParams{ID: workID, ReviewedBy: actor}); err != nil {
		return err
	}
	return nil
}

// ReportWork 举报作品：写入 reports（target_type=work）。
func (s *Service) ReportWork(ctx context.Context, workID, reporter pgtype.UUID, reason, note string) (pgtype.UUID, error) {
	row, err := s.q.GetStoryWork(ctx, workID)
	if err != nil {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	story, err := s.q.GetStory(ctx, row.StoryID)
	if err != nil {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeNotFound, "作品不存在")
	}
	snapshot, _ := json.Marshal(map[string]any{"type": "work", "id": row.ID.String(), "title": row.Title})
	target := row.ID.String()
	return s.q.InsertTypedReport(ctx, store.InsertTypedReportParams{
		ReporterIdentityID: reporter,
		ReportedIdentityID: story.HostIdentityID,
		ConversationID:     pgtype.UUID{},
		Reason:             reason,
		Note:               noteOrNil(note),
		Snapshot:           snapshot,
		TargetType:         "work",
		TargetID:           &target,
	})
}

// AdminWork 是管理后台的作品条目。
type AdminWork struct {
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Status       string  `json:"status"`
	LikeCount    int32   `json:"likeCount"`
	RejectReason *string `json:"rejectReason"`
	CreatedAt    string  `json:"createdAt"`
}

// ListAdminWorks 返回作品审核队列。
func (s *Service) ListAdminWorks(ctx context.Context, status *string, limit int32) ([]AdminWork, error) {
	rows, err := s.q.ListAdminWorks(ctx, store.ListAdminWorksParams{Status: status, Lim: limit})
	if err != nil {
		return nil, err
	}
	out := make([]AdminWork, 0, len(rows))
	for _, r := range rows {
		out = append(out, AdminWork{
			ID:           r.ID.String(),
			Title:        r.Title,
			Status:       r.Status,
			LikeCount:    r.LikeCount,
			RejectReason: r.RejectReason,
			CreatedAt:    formatTS(r.CreatedAt),
		})
	}
	return out, nil
}

// ApproveWork 通过审核。
func (s *Service) ApproveWork(ctx context.Context, id, adminID pgtype.UUID) error {
	affected, err := s.q.ApproveWork(ctx, store.ApproveWorkParams{ID: id, ReviewedBy: adminID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "作品不在待审核状态")
	}
	return nil
}

// RejectWork 驳回作品。
func (s *Service) RejectWork(ctx context.Context, id, adminID pgtype.UUID, reason string) error {
	var reasonPtr *string
	if reason != "" {
		reasonPtr = &reason
	}
	affected, err := s.q.RejectWork(ctx, store.RejectWorkParams{ID: id, ReviewedBy: adminID, RejectReason: reasonPtr})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "作品不在待审核状态")
	}
	return nil
}

// AdminRemoveWork 下架作品。
func (s *Service) AdminRemoveWork(ctx context.Context, id, adminID pgtype.UUID) error {
	affected, err := s.q.RemoveWork(ctx, store.RemoveWorkParams{ID: id, ReviewedBy: adminID})
	if err != nil {
		return err
	}
	if affected == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "作品状态不允许下架")
	}
	return nil
}

// mineOf 返回查看者在作品中的身份（host/guest/nil）。
func mineOf(story store.Story, viewer pgtype.UUID) *string {
	if story.HostIdentityID == viewer {
		v := "host"
		return &v
	}
	if story.GuestIdentityID.Valid && story.GuestIdentityID == viewer {
		v := "guest"
		return &v
	}
	return nil
}

func noteOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
