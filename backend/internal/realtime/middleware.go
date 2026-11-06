package realtime

import "context"

// Middleware 包装 HandlerFunc，用于在 Router 层提供可复用的处理中间件
// （例如实名校验），后续所有聊天类消息都应挂上。
type Middleware func(next HandlerFunc) HandlerFunc

// RequireVerified 返回实名校验中间件：仅当 mode == "required" 且当前身份未绑定实名账号时，
// 返回 verification_required 并终止处理；off / optional 模式直接放行。
func RequireVerified(mode string) Middleware {
	if mode != "required" {
		return func(next HandlerFunc) HandlerFunc { return next }
	}
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, c *Conn, env Envelope) {
			if !c.identity.AccountID.Valid {
				c.sendError(env.ID, CodeVerificationRequired, "需要先完成实名验证")
				return
			}
			next(ctx, c, env)
		}
	}
}
