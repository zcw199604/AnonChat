package identity

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// CookieMaxAge 是身份 Cookie 的有效期（365 天，单位秒）。
const CookieMaxAge = 365 * 24 * 60 * 60

// Handler 暴露身份相关的 HTTP 接口。
type Handler struct {
	svc              *Service
	resolver         *httpx.ClientIPResolver
	logger           *slog.Logger
	verificationMode string
	ipBans           IPBanChecker
}

// IPBanChecker 判断 IP 是否被封禁（供创建身份前检查）。接口实现为 moderation.BanService。
type IPBanChecker interface {
	IsIPBanned(ctx context.Context, ip string) bool
}

// SetIPBanChecker 注入 IP 封禁检查器。
func (h *Handler) SetIPBanChecker(c IPBanChecker) { h.ipBans = c }

// NewHandler 创建身份 HTTP 处理器。
func NewHandler(svc *Service, resolver *httpx.ClientIPResolver, logger *slog.Logger, verificationMode string) *Handler {
	return &Handler{svc: svc, resolver: resolver, logger: logger, verificationMode: verificationMode}
}

// identityResponse 是 POST /api/identity 与 /reset 的响应（不含身份 ID）。
type identityResponse struct {
	Nickname   string `json:"nickname"`
	AvatarSeed string `json:"avatarSeed"`
	Verified   bool   `json:"verified"`
}

// meResponse 是 GET /api/identity/me 的响应。
type meResponse struct {
	Nickname         string `json:"nickname"`
	AvatarSeed       string `json:"avatarSeed"`
	Verified         bool   `json:"verified"`
	VerificationMode string `json:"verificationMode"`
}

// Create 处理 POST /api/identity：有有效 Cookie 时返回现有身份，否则新建。
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		if ident, err := h.svc.Resolve(r.Context(), c.Value); err == nil {
			h.setCookie(w, c.Value)
			httpx.WriteJSON(w, http.StatusOK, toIdentityResponse(ident))
			return
		}
	}

	ip := h.resolver.ClientIP(r)
	if h.ipBans != nil && h.ipBans.IsIPBanned(r.Context(), ip) {
		httpx.WriteError(w, httpx.NewError(httpx.CodeBanned, "你已被封禁"))
		return
	}
	sess, err := h.svc.Create(r.Context(), ip, r.UserAgent())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.setCookie(w, sess.Token)
	httpx.WriteJSON(w, http.StatusOK, toIdentityResponse(&sess.Identity))
}

// Me 处理 GET /api/identity/me（需经 RequireIdentity）。
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	ident, ok := FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, ErrUnauthorized)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		Nickname:         ident.Nickname,
		AvatarSeed:       ident.AvatarSeed,
		Verified:         ident.AccountID.Valid,
		VerificationMode: h.verificationMode,
	})
}

// Reset 处理 POST /api/identity/reset（需经 RequireIdentity）。
func (h *Handler) Reset(w http.ResponseWriter, r *http.Request) {
	ident, ok := FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, ErrUnauthorized)
		return
	}
	ip := h.resolver.ClientIP(r)
	sess, err := h.svc.Reset(r.Context(), ident, ip, r.UserAgent())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.setCookie(w, sess.Token)
	httpx.WriteJSON(w, http.StatusOK, toIdentityResponse(&sess.Identity))
}

// RequireIdentity 校验 Cookie 中的令牌，成功则把身份写入 context 并更新 last_seen_at。
func (h *Handler) RequireIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(CookieName)
		if err != nil {
			httpx.WriteError(w, ErrUnauthorized)
			return
		}
		ident, err := h.svc.Resolve(r.Context(), c.Value)
		if err != nil {
			httpx.WriteError(w, err)
			return
		}
		h.svc.Touch(r.Context(), ident)
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), ident)))
	})
}

// setCookie 写入身份 Cookie（__Host- 前缀要求 Secure、Path=/、无 Domain）。
func (h *Handler) setCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   CookieMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

// toIdentityResponse 把身份记录转为对外响应（不含身份 ID）。
func toIdentityResponse(ident *store.Identity) identityResponse {
	return identityResponse{
		Nickname:   ident.Nickname,
		AvatarSeed: ident.AvatarSeed,
		Verified:   ident.AccountID.Valid,
	}
}
