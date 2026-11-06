package verify

import (
	"log/slog"
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// maxBodyBytes 是 REST 请求体大小上限（64KB）。
const maxBodyBytes = 64 << 10

// Handler 暴露实名验证相关的 HTTP 接口。
type Handler struct {
	svc      *Service
	resolver *httpx.ClientIPResolver
	logger   *slog.Logger
}

// NewHandler 创建验证 HTTP 处理器。
func NewHandler(svc *Service, resolver *httpx.ClientIPResolver, logger *slog.Logger) *Handler {
	return &Handler{svc: svc, resolver: resolver, logger: logger}
}

// sendRequest 是 POST /api/verify/sms/send 的请求体。
type sendRequest struct {
	Phone string `json:"phone"`
}

// confirmRequest 是 POST /api/verify/sms/confirm 的请求体。
type confirmRequest struct {
	Phone string `json:"phone"`
	Code  string `json:"code"`
}

// confirmResponse 是确认成功后的响应。
type confirmResponse struct {
	Verified bool `json:"verified"`
}

// Send 处理 POST /api/verify/sms/send（需经 RequireIdentity）。
func (h *Handler) Send(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req sendRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.SendCode(r.Context(), req.Phone, h.resolver.ClientIP(r)); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Confirm 处理 POST /api/verify/sms/confirm（需经 RequireIdentity）。
func (h *Handler) Confirm(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req confirmRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err := h.svc.ConfirmCode(r.Context(), req.Phone, req.Code, ident, h.resolver.ClientIP(r)); err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, confirmResponse{Verified: true})
}
