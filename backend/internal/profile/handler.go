package profile

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// maxBodyBytes 限制请求体大小（与 V1 约定一致）。
const maxBodyBytes = 64 << 10

// Handler 暴露资料相关的 HTTP 接口。
type Handler struct {
	svc              *Service
	logger           *slog.Logger
	verificationMode string
}

// NewHandler 创建资料 HTTP 处理器。
func NewHandler(svc *Service, logger *slog.Logger, verificationMode string) *Handler {
	return &Handler{svc: svc, logger: logger, verificationMode: verificationMode}
}

// Update 处理 PATCH /api/identity/profile。
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var patch Patch
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		httpx.WriteError(w, err)
		return
	}
	updated, err := h.svc.Update(r.Context(), ident, patch)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, identity.NewMeResponse(&updated, h.verificationMode, time.Now()))
}

type nicknameRequest struct {
	Nickname string `json:"nickname"`
}

// Shuffle 处理 POST /api/identity/nickname/shuffle。
func (h *Handler) Shuffle(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	updated, err := h.svc.ShuffleNickname(r.Context(), ident)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, identity.NewMeResponse(&updated, h.verificationMode, time.Now()))
}

// SetNickname 处理 PUT /api/identity/nickname。
func (h *Handler) SetNickname(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req nicknameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	updated, err := h.svc.SetNickname(r.Context(), ident, req.Nickname)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, identity.NewMeResponse(&updated, h.verificationMode, time.Now()))
}
