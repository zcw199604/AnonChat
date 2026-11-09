package realtime

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// Options 是网关可调参数，测试中注入较短值。
type Options struct {
	// AllowedOrigins 是握手允许的 Origin 白名单。
	AllowedOrigins []string
	// SendBuffer 是每连接发送缓冲容量，默认 64。
	SendBuffer int
	// MaxFrameBytes 是单帧上限，默认 16KB。
	MaxFrameBytes int64
	// IdleTimeout 是多久没收到任何帧就断开，默认 60s。
	IdleTimeout time.Duration
	// WriteTimeout 是单次写超时，默认 10s。
	WriteTimeout time.Duration
}

// withDefaults 填充默认值。
func (o Options) withDefaults() Options {
	if o.SendBuffer <= 0 {
		o.SendBuffer = 64
	}
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = 16 << 10
	}
	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 60 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 10 * time.Second
	}
	return o
}

// Gateway 是 WebSocket 网关。
type Gateway struct {
	opts      Options
	logger    *slog.Logger
	resolver  IdentityResolver
	bans      BanChecker
	accessLog AccessLogger
	clientIP  *httpx.ClientIPResolver
	router    *Router
	hub       *Hub
	allowed   map[string]struct{}

	observers     []IdentityObserver
	connObservers []ConnObserver
	resumable     func(identityID pgtype.UUID) []pgtype.UUID
}

// NewGateway 创建网关。bans 为 nil 时使用 AllowAllBans；clientIP 为 nil 时退回 RemoteAddr。
func NewGateway(opts Options, logger *slog.Logger, resolver IdentityResolver, bans BanChecker, accessLog AccessLogger, clientIP *httpx.ClientIPResolver) *Gateway {
	opts = opts.withDefaults()
	if bans == nil {
		bans = AllowAllBans{}
	}
	g := &Gateway{
		opts:      opts,
		logger:    logger,
		resolver:  resolver,
		bans:      bans,
		accessLog: accessLog,
		clientIP:  clientIP,
		router:    NewRouter(),
		hub:       NewHub(),
		allowed:   make(map[string]struct{}, len(opts.AllowedOrigins)),
	}
	for _, o := range opts.AllowedOrigins {
		g.allowed[o] = struct{}{}
	}
	// 内置处理：ping → pong。
	g.router.Handle(TypePing, func(_ context.Context, c *Conn, env Envelope) {
		c.sendEvent(TypePong, env.ID, nil)
	})
	return g
}

// Hub 返回底层 Hub（测试与运维用）。
func (g *Gateway) Hub() *Hub { return g.hub }

// AddObserver 注册身份上下线观察者，须在开始服务前调用。
func (g *Gateway) AddObserver(o IdentityObserver) { g.observers = append(g.observers, o) }

// AddConnObserver 注册连接开闭观察者，须在开始服务前调用。
func (g *Gateway) AddConnObserver(o ConnObserver) { g.connObservers = append(g.connObservers, o) }

// SetResumableProvider 设置 hello.resumable 的提供者（如可恢复的匹配会话）。
func (g *Gateway) SetResumableProvider(fn func(identityID pgtype.UUID) []pgtype.UUID) {
	g.resumable = fn
}

// Router 返回路由器，供后续业务模块注册处理函数。
func (g *Gateway) Router() *Router { return g.router }

// CloseAll 关闭全部连接（优雅关闭时使用，关闭码 1001）。
func (g *Gateway) CloseAll() {
	g.hub.CloseAll(websocket.StatusGoingAway, "server shutting down")
}

// clientAddr 解析客户端 IP。
func (g *Gateway) clientAddr(r *http.Request) string {
	if g.clientIP != nil {
		return g.clientIP.ClientIP(r)
	}
	return defaultClientIP(r)
}

// ServeWS 处理 GET /ws：校验 Origin → 解析身份 → 检查封禁 → 升级 → 运行连接。
// 握手失败时返回 HTTP 错误且不升级连接。
func (g *Gateway) ServeWS(w http.ResponseWriter, r *http.Request) {
	origin := r.Header.Get("Origin")
	if _, ok := g.allowed[origin]; !ok {
		httpx.WriteError(w, httpx.NewError(httpx.CodeForbiddenOrigin, "来源不被允许"))
		return
	}

	ck, err := r.Cookie(identity.CookieName)
	if err != nil {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	ident, err := g.resolver.Resolve(r.Context(), ck.Value)
	if err != nil {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}

	ip := g.clientAddr(r)
	if g.bans.IsBanned(r.Context(), ident, ip) {
		httpx.WriteError(w, httpx.NewError(httpx.CodeBanned, "你已被封禁"))
		return
	}

	// 自有 Origin 校验已完成，故关闭 coder/websocket 的内置校验（只保留一套）。
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		g.logger.Warn("WS 升级失败", "error", err)
		return
	}
	ws.SetReadLimit(g.opts.MaxFrameBytes)

	c := &Conn{
		g:           g,
		ws:          ws,
		identity:    ident,
		ip:          ip,
		userAgent:   r.UserAgent(),
		connectedAt: time.Now(),
		send:        make(chan outMessage, g.opts.SendBuffer),
		done:        make(chan struct{}),
		closed:      make(chan struct{}),
	}
	c.closeConn = func(code websocket.StatusCode, reason string) {
		_ = ws.Close(code, reason)
	}
	c.serve(r.Context())
}

// defaultClientIP 在未注入 ClientIPResolver 时使用 RemoteAddr 的主机部分。
func defaultClientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}
