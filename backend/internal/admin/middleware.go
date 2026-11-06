package admin

import (
	"context"
	"net/http"
	"net/netip"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

type ctxKey struct{}

var adminKey ctxKey

func withAdmin(ctx context.Context, id pgtype.UUID) context.Context {
	return context.WithValue(ctx, adminKey, id)
}

// AdminIDFromContext 取出当前管理员 ID。
func AdminIDFromContext(ctx context.Context) (pgtype.UUID, bool) {
	v, ok := ctx.Value(adminKey).(pgtype.UUID)
	return v, ok
}

// RequireAdmin 校验管理后台 Cookie（身份 Cookie 对其无效），失败返回 401。
func (s *Service) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(SessionCookie)
		if err != nil {
			httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "未登录"))
			return
		}
		id, ok := s.SessionAdmin(r.Context(), c.Value)
		if !ok {
			httpx.WriteError(w, httpx.NewError(httpx.CodeUnauthorized, "会话无效或已过期"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withAdmin(r.Context(), id)))
	})
}

// IPAllowlist 返回管理接口 IP 白名单中间件：配置后不在名单内一律 404（隐藏后台存在）。
func IPAllowlist(allowed []netip.Prefix, resolver *httpx.ClientIPResolver) httpx.Middleware {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := ""
			if resolver != nil {
				ip = resolver.ClientIP(r)
			}
			addr, err := netip.ParseAddr(ip)
			if err == nil {
				for _, p := range allowed {
					if p.Contains(addr) {
						next.ServeHTTP(w, r)
						return
					}
				}
			}
			w.WriteHeader(http.StatusNotFound)
		})
	}
}
