package feed

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

const maxBodyBytes = 64 << 10

// Handler 暴露公告、广播、动态与在线统计的 HTTP 接口。
type Handler struct {
	svc *Service
	adm *admin.Service
	now func() time.Time
}

// NewHandler 创建 feed HTTP 处理器。adm 用于写入管理员审计日志。
func NewHandler(svc *Service, adm *admin.Service) *Handler {
	return &Handler{svc: svc, adm: adm, now: time.Now}
}

// Announcements 处理 GET /api/announcements。
func (h *Handler) Announcements(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.ActiveAnnouncements(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

// CurrentBroadcasts 处理 GET /api/broadcasts/current。
func (h *Handler) CurrentBroadcasts(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.CurrentBroadcasts(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type broadcastRequest struct {
	Content string `json:"content"`
}

// SubmitBroadcast 处理 POST /api/broadcasts。
func (h *Handler) SubmitBroadcast(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req broadcastRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	bc, err := h.svc.SubmitBroadcast(r.Context(), ident.ID, req.Content)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": bc.ID})
}

// MyBroadcasts 处理 GET /api/broadcasts/mine。
func (h *Handler) MyBroadcasts(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	list, err := h.svc.MyBroadcasts(r.Context(), ident.ID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, list)
}

type reportRequest struct {
	Reason string `json:"reason"`
	Note   string `json:"note"`
}

// ReportBroadcast 处理 POST /api/broadcasts/{id}/report。
func (h *Handler) ReportBroadcast(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "广播 ID 非法"))
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
	rid, err := h.svc.ReportBroadcast(r.Context(), ident.ID, id, req.Reason, req.Note)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"id": rid.String()})
}

// Timeline 处理 GET /api/feed。
func (h *Handler) Timeline(w http.ResponseWriter, r *http.Request) {
	items, err := h.svc.Timeline(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, items)
}

// Stats 处理 GET /api/stats/online。
func (h *Handler) Stats(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, h.svc.Stats(r.Context()))
}

// ---- 管理后台 ----

type createAnnouncementRequest struct {
	Content   string     `json:"content"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// AdminListAnnouncements 处理 GET /api/admin/announcements。
func (h *Handler) AdminListAnnouncements(w http.ResponseWriter, r *http.Request) {
	list, err := h.svc.AdminAnnouncements(r.Context(), 50)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"announcements": list})
}

// AdminCreateAnnouncement 处理 POST /api/admin/announcements。
func (h *Handler) AdminCreateAnnouncement(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createAnnouncementRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	item, err := h.svc.CreateAnnouncement(r.Context(), req.Content, req.ExpiresAt, adminID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "create_announcement", item.ID, map[string]any{"content": item.Content})
	httpx.WriteJSON(w, http.StatusOK, item)
}

// AdminDeleteAnnouncement 处理 DELETE /api/admin/announcements/{id}。
func (h *Handler) AdminDeleteAnnouncement(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "公告 ID 非法"))
		return
	}
	if err := h.svc.DeleteAnnouncement(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "delete_announcement", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// AdminListBroadcasts 处理 GET /api/admin/broadcasts?status=。
func (h *Handler) AdminListBroadcasts(w http.ResponseWriter, r *http.Request) {
	var status *string
	if s := r.URL.Query().Get("status"); s != "" {
		status = &s
	}
	list, err := h.svc.AdminBroadcasts(r.Context(), status, 50)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"broadcasts": list})
}

// AdminApproveBroadcast 处理 POST /api/admin/broadcasts/{id}/approve。
func (h *Handler) AdminApproveBroadcast(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "广播 ID 非法"))
		return
	}
	if err := h.svc.ApproveBroadcast(r.Context(), id, adminID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "approve_broadcast", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

type reviewRequest struct {
	Reason string `json:"reason"`
}

// AdminRejectBroadcast 处理 POST /api/admin/broadcasts/{id}/reject。
func (h *Handler) AdminRejectBroadcast(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "广播 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req reviewRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.RejectBroadcast(r.Context(), id, adminID, req.Reason); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "reject_broadcast", id.String(), map[string]any{"reason": req.Reason})
	w.WriteHeader(http.StatusNoContent)
}

// AdminRemoveBroadcast 处理 POST /api/admin/broadcasts/{id}/remove。
func (h *Handler) AdminRemoveBroadcast(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "广播 ID 非法"))
		return
	}
	if err := h.svc.RemoveBroadcast(r.Context(), id, adminID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, adminID, "remove_broadcast", id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) audit(r *http.Request, adminID pgtype.UUID, action, target string, detail map[string]any) {
	if h.adm == nil {
		return
	}
	h.adm.LogAudit(r.Context(), adminID, action, target, detail)
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
