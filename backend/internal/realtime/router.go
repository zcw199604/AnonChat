package realtime

import (
	"context"
	"sync"
)

// HandlerFunc 是消息处理函数。后续业务模块通过 Router 注册自己的处理函数。
type HandlerFunc func(ctx context.Context, c *Conn, env Envelope)

// Router 按消息类型分发。未知类型由调用方返回 invalid_request。
type Router struct {
	mu       sync.RWMutex
	handlers map[MessageType]HandlerFunc
}

// NewRouter 创建路由器。
func NewRouter() *Router {
	return &Router{handlers: make(map[MessageType]HandlerFunc)}
}

// Handle 注册某消息类型的处理函数。
func (r *Router) Handle(t MessageType, fn HandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[t] = fn
}

// lookup 查找处理函数。
func (r *Router) lookup(t MessageType) (HandlerFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.handlers[t]
	return fn, ok
}
