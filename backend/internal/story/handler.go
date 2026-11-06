package story

import (
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

const maxBodyBytes = 64 << 10

// Handler 暴露故事书与作品的 HTTP 接口。
type Handler struct {
	svc *Service
	adm *admin.Service
}

// NewHandler 创建故事书 HTTP 处理器。
func NewHandler(svc *Service, adm *admin.Service) *Handler {
	return &Handler{svc: svc, adm: adm}
}

type createStoryRequest struct {
	Title    string   `json:"title"`
	Opening  string   `json:"opening"`
	Tags     []string `json:"tags"`
	RoleA    string   `json:"roleA"`
	RoleB    string   `json:"roleB"`
	HostRole string   `json:"hostRole"`
}

// CreateStory 处理 POST /api/stories。
func (h *Handler) CreateStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createStoryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.Create(r.Context(), ident, CreateInput(req))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ListStories 处理 GET /api/stories?status=waiting&before=。
func (h *Handler) ListStories(w http.ResponseWriter, r *http.Request) {
	if s := r.URL.Query().Get("status"); s != "" && s != "waiting" {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "只支持 status=waiting"))
		return
	}
	q := r.URL.Query()
	var before *time.Time
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = &t
		}
	}
	limit := int32(20)
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 50 {
		limit = int32(v)
	}
	out, err := h.svc.ListWaiting(r.Context(), before, limit)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GetStory 处理 GET /api/stories/{id}。
func (h *Handler) GetStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "故事 ID 非法"))
		return
	}
	out, err := h.svc.Detail(r.Context(), id, ident.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// JoinStory 处理 POST /api/stories/{id}/join。
func (h *Handler) JoinStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "故事 ID 非法"))
		return
	}
	convID, err := h.svc.Join(r.Context(), id, ident.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"conversationId": convID})
}

// EndStory 处理 POST /api/stories/{id}/end。
func (h *Handler) EndStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "故事 ID 非法"))
		return
	}
	out, err := h.svc.End(r.Context(), id, ident.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// CancelStory 处理 DELETE /api/stories/{id}。
func (h *Handler) CancelStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "故事 ID 非法"))
		return
	}
	if err := h.svc.Cancel(r.Context(), id, ident.ID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type consentRequest struct {
	Agree bool `json:"agree"`
}

// ConsentStory 处理 POST /api/stories/{id}/consent。
func (h *Handler) ConsentStory(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "故事 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req consentRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	out, err := h.svc.Consent(r.Context(), id, ident.ID, req.Agree)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ---- 作品 ----

// ListWorks 处理 GET /api/works?before=。
func (h *Handler) ListWorks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var before *time.Time
	if v := q.Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = &t
		}
	}
	limit := int32(20)
	if v, err := strconv.Atoi(q.Get("limit")); err == nil && v > 0 && v <= 50 {
		limit = int32(v)
	}
	out, err := h.svc.ListWorks(r.Context(), before, limit)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GetWork 处理 GET /api/works/{id}。
func (h *Handler) GetWork(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	out, err := h.svc.GetWork(r.Context(), id, ident.ID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// LikeWork 处理 POST /api/works/{id}/like。
func (h *Handler) LikeWork(w http.ResponseWriter, r *http.Request) {
	h.setLike(w, r, true)
}

// UnlikeWork 处理 DELETE /api/works/{id}/like。
func (h *Handler) UnlikeWork(w http.ResponseWriter, r *http.Request) {
	h.setLike(w, r, false)
}

func (h *Handler) setLike(w http.ResponseWriter, r *http.Request, like bool) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	count, err := h.svc.SetLike(r.Context(), id, ident.ID, like)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"likeCount": count})
}

// DeleteWork 处理 DELETE /api/works/{id}。
func (h *Handler) DeleteWork(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	if err := h.svc.DeleteWork(r.Context(), id, ident.ID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// WithdrawWork 处理 POST /api/works/{id}/withdraw。
func (h *Handler) WithdrawWork(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	if err := h.svc.Withdraw(r.Context(), id, ident.ID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type reportRequest struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// ReportWork 处理 POST /api/works/{id}/report。
func (h *Handler) ReportWork(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if !validReason(req.Reason) {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "举报原因非法"))
		return
	}
	rid, err := h.svc.ReportWork(r.Context(), id, ident.ID, req.Reason, req.Note)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": rid.String()})
}

// ---- 管理后台 ----

// AdminListWorks 处理 GET /api/admin/works?status=。
func (h *Handler) AdminListWorks(w http.ResponseWriter, r *http.Request) {
	var status *string
	if s := r.URL.Query().Get("status"); s != "" {
		status = &s
	}
	out, err := h.svc.ListAdminWorks(r.Context(), status, 50)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"works": out})
}

// AdminApproveWork 处理 POST /api/admin/works/{id}/approve。
func (h *Handler) AdminApproveWork(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	if err := h.svc.ApproveWork(r.Context(), id, adminID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "approve_work", id.String())
	w.WriteHeader(http.StatusNoContent)
}

// AdminRejectWork 处理 POST /api/admin/works/{id}/reject。
func (h *Handler) AdminRejectWork(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reportRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.RejectWork(r.Context(), id, adminID, req.Reason); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "reject_work", id.String())
	w.WriteHeader(http.StatusNoContent)
}

// AdminRemoveWork 处理 POST /api/admin/works/{id}/remove。
func (h *Handler) AdminRemoveWork(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "作品 ID 非法"))
		return
	}
	if err := h.svc.AdminRemoveWork(r.Context(), id, adminID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "remove_work", id.String())
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) audit(r *http.Request, adminID pgtype.UUID, action, target string) {
	if h.adm == nil {
		return
	}
	h.adm.LogAudit(r.Context(), adminID, action, target, nil)
}

func internalErr() error { return httpx.NewError(httpx.CodeInternal, "服务器内部错误") }

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "ID 非法")
	}
	return u, nil
}

func validReason(r string) bool {
	switch r {
	case "spam", "harassment", "porn", "illegal", "other":
		return true
	default:
		return false
	}
}
