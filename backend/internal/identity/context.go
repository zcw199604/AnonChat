package identity

import (
	"context"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// ctxKey 是本包私有的 context 键类型。
type ctxKey struct{}

// identityKey 用于在 context 中存放当前身份。
var identityKey ctxKey

// WithIdentity 把身份放入 context（导出以便中间件组合与测试）。
func WithIdentity(ctx context.Context, id *store.Identity) context.Context {
	return context.WithValue(ctx, identityKey, id)
}

// FromContext 从 context 取出当前身份；RequireIdentity 中间件成功后会写入。
func FromContext(ctx context.Context) (*store.Identity, bool) {
	v, ok := ctx.Value(identityKey).(*store.Identity)
	return v, ok && v != nil
}
