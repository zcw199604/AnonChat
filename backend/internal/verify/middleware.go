package verify

import (
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// RequireVerified 返回一个中间件：仅当 mode == "required" 时，对未绑定实名账号的身份
// 返回 403 verification_required；off / optional 模式下直接放行。
// 必须放在 RequireIdentity 之后使用（依赖 context 中的身份）。
func RequireVerified(mode string) httpx.Middleware {
	if mode != "required" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ident, ok := identity.FromContext(r.Context())
			if !ok || !ident.AccountID.Valid {
				httpx.WriteError(w, httpx.NewError(httpx.CodeVerificationRequired, "需要先完成实名验证"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
