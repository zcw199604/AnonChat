package vip

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

// Handler 暴露 VIP 兑换与管理后台激活码批次的 HTTP 接口。
type Handler struct {
	svc              *Service
	adm              *admin.Service
	verificationMode string
}

// NewHandler 创建 VIP HTTP 处理器。
func NewHandler(svc *Service, adm *admin.Service, verificationMode string) *Handler {
	return &Handler{svc: svc, adm: adm, verificationMode: verificationMode}
}

type redeemRequest struct {
	Code string `json:"code"`
}

// Redeem 处理 POST /api/vip/redeem。
func (h *Handler) Redeem(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req redeemRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	ip := clientIP(r)
	updated, err := h.svc.Redeem(r.Context(), ident, req.Code, ip, r.UserAgent())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, identity.NewMeResponse(&updated, h.verificationMode, time.Now()))
}

// ---- 管理后台：激活码批次 ----

type createBatchRequest struct {
	Label          string    `json:"label"`
	VipDays        int32     `json:"vipDays"`
	BroadcastCards int32     `json:"broadcastCards"`
	Count          int32     `json:"count"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

type batchJSON struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	VipDays        int32  `json:"vipDays"`
	BroadcastCards int32  `json:"broadcastCards"`
	CodeCount      int32  `json:"codeCount"`
	ExpiresAt      string `json:"expiresAt"`
	CreatedAt      string `json:"createdAt"`
	Total          int64  `json:"total"`
	Redeemed       int64  `json:"redeemed"`
	Revoked        int64  `json:"revoked"`
}

// CreateBatch 处理 POST /api/admin/activation-batches（明文只返回一次）。
func (h *Handler) CreateBatch(w http.ResponseWriter, r *http.Request) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createBatchRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	batch, codes, err := h.svc.CreateBatch(r.Context(), CreateBatchInput{
		Label:          req.Label,
		VipDays:        req.VipDays,
		BroadcastCards: req.BroadcastCards,
		Count:          req.Count,
		ExpiresAt:      req.ExpiresAt,
		CreatedBy:      adminID,
	})
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "create_activation_batch", batch.ID.String(), map[string]any{"count": len(codes)})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"batch": toBatchJSON(batch),
		"codes": codes,
	})
}

// ListBatches 处理 GET /api/admin/activation-batches。
func (h *Handler) ListBatches(w http.ResponseWriter, r *http.Request) {
	limit := int32(50)
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= 100 {
		limit = int32(v)
	}
	list, err := h.svc.ListBatches(r.Context(), limit)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]batchJSON, 0, len(list))
	for _, b := range list {
		out = append(out, toBatchJSON(b))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"batches": out})
}

// RevokeBatch 处理 POST /api/admin/activation-batches/{id}/revoke。
func (h *Handler) RevokeBatch(w http.ResponseWriter, r *http.Request) {
	_, ok := admin.AdminIDFromContext(r.Context())
	if !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
		return
	}
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "批次 ID 非法"))
		return
	}
	n, err := h.svc.RevokeBatch(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "revoke_activation_batch", id.String(), map[string]any{"revoked": n})
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"revoked": n})
}

func toBatchJSON(b Batch) batchJSON {
	return batchJSON{
		ID:             b.ID.String(),
		Label:          b.Label,
		VipDays:        b.VipDays,
		BroadcastCards: b.BroadcastCards,
		CodeCount:      b.CodeCount,
		ExpiresAt:      b.ExpiresAt.UTC().Format(time.RFC3339),
		CreatedAt:      b.CreatedAt.UTC().Format(time.RFC3339),
		Total:          b.Total,
		Redeemed:       b.Redeemed,
		Revoked:        b.Revoked,
	}
}

func (h *Handler) audit(r *http.Request, action, target string, detail map[string]any) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok || h.adm == nil {
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

// clientIP 取客户端 IP（Origin 校验与限流已在中间件完成，这里只做限流 key）。
func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return v
	}
	host := r.RemoteAddr
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
