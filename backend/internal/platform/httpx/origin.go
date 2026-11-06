package httpx

import "net/http"

// OriginCheck 对修改类请求（POST/PUT/PATCH/DELETE）校验 Origin 头是否在白名单内。
// 不在白名单（含缺失 Origin）时返回 403 forbidden_origin，用于防 CSRF。
// GET/HEAD/OPTIONS 不校验，便于静态资源与预检请求。
func OriginCheck(allowed []string) Middleware {
	set := make(map[string]struct{}, len(allowed))
	for _, o := range allowed {
		set[o] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
				origin := r.Header.Get("Origin")
				if _, ok := set[origin]; !ok {
					WriteError(w, NewError(CodeForbiddenOrigin, "来源不被允许"))
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
