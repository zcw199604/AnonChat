package bottle

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

const maxBodyBytes = 64 << 10

type postRequest struct {
	Content string `json:"content"`
}

// PostHandler 处理 POST /api/bottles。
func (s *Service) PostHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req postRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	id, err := s.Post(r.Context(), ident.ID, req.Content)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

// PickHandler 处理 POST /api/bottles/pick。
func (s *Service) PickHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	picked, err := s.Pick(r.Context(), ident.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id":        picked.ID.String(),
		"content":   picked.Content,
		"createdAt": formatTS(picked.CreatedAt),
	})
}

// ThrowBackHandler 处理 POST /api/bottles/{id}/throw-back。
func (s *Service) ThrowBackHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	bottleID, err := parseUUIDParam(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "瓶子 ID 非法"))
		return
	}
	if err := s.ThrowBack(r.Context(), ident.ID, bottleID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type replyRequest struct {
	Content string `json:"content"`
}

// ReplyHandler 处理 POST /api/bottles/{id}/reply。
func (s *Service) ReplyHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	bottleID, err := parseUUIDParam(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "瓶子 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req replyRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	convID, err := s.Reply(r.Context(), ident.ID, bottleID, req.Content)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"conversationId": convID.String()})
}

// MineHandler 处理 GET /api/bottles/mine。
func (s *Service) MineHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	bottles, err := s.Mine(r.Context(), ident.ID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInternal, "服务器内部错误"))
		return
	}
	type replyItem struct {
		ConversationID string `json:"conversationId"`
		Unread         int64  `json:"unread"`
	}
	type item struct {
		ID             string      `json:"id"`
		ContentPreview string      `json:"contentPreview"`
		CreatedAt      string      `json:"createdAt"`
		Replies        []replyItem `json:"replies"`
	}
	out := make([]item, 0, len(bottles))
	for _, b := range bottles {
		replies := make([]replyItem, 0, len(b.Replies))
		for _, rr := range b.Replies {
			replies = append(replies, replyItem{ConversationID: rr.ConversationID.String(), Unread: rr.Unread})
		}
		out = append(out, item{
			ID:             b.ID.String(),
			ContentPreview: b.ContentPreview,
			CreatedAt:      formatTS(b.CreatedAt),
			Replies:        replies,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type reportRequest struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// ReportHandler 处理 POST /api/bottles/{id}/report。
func (s *Service) ReportHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	bottleID, err := parseUUIDParam(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "瓶子 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	id, err := s.Report(r.Context(), ident.ID, bottleID, req.Reason, req.Note)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

func parseUUIDParam(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "UUID 为空")
	}
	return u, nil
}
