// Package api 负责装配 HTTP 路由、中间件以及各项依赖。
package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/bottle"
	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/privatechat"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/room"
	"github.com/anonchat/anonchat/backend/internal/verify"
)

// Deps 汇总装配路由所需的依赖，便于生产与测试分别构造。
type Deps struct {
	Cfg          *config.Config
	Pool         *pgxpool.Pool
	Logger       *slog.Logger
	Identity     *identity.Handler
	Verify       *verify.Handler
	Realtime     *realtime.Gateway
	Conversation *conversation.Service
	Report       *moderation.ReportService
	Ban          *moderation.BanService
	Resolver     *httpx.ClientIPResolver
	Match        *match.Service
	Room         *room.Service
	Bottle       *bottle.Service
	Private      *privatechat.Service
	Admin        *admin.Service
}

// Server 持有装配好的依赖。
type Server struct {
	deps Deps
}

// NewServer 用给定依赖创建 api.Server。
func NewServer(deps Deps) *Server {
	return &Server{deps: deps}
}

// NewDefaultDeps 依据配置构造生产环境默认的依赖集合。
func NewDefaultDeps(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) (Deps, error) {
	resolver, err := httpx.NewClientIPResolver(cfg.TrustedProxies)
	if err != nil {
		return Deps{}, err
	}
	limits := moderation.DefaultLimits().Scaled(cfg.RateLimitMultiplier)

	// 敏感词过滤器。
	filter, err := buildModerator(cfg, logger)
	if err != nil {
		return Deps{}, err
	}

	// 封禁服务（带 30 秒缓存 + 踢连接），后注入 WS 网关与身份处理器。
	banSvc := moderation.NewBanService(pool, nil)

	identitySvc := identity.NewService(pool, logger, limits.IdentityCreate.New())
	identityHandler := identity.NewHandler(identitySvc, resolver, logger, cfg.VerificationMode)
	identityHandler.SetIPBanChecker(banSvc)

	// WebSocket 网关：真实身份解析、BanService 封禁检查、身份服务写访问日志。
	gateway := realtime.NewGateway(realtime.Options{
		AllowedOrigins: cfg.AllowedOrigins,
	}, logger, identitySvc, banSvc, identitySvc, resolver)
	hub := gateway.Hub()
	// 让 BanService 能踢在线连接。
	banSvc.SetKicker(hub)

	// 会话与消息管道：broker 用网关 Hub，屏蔽检查基于 blocks 表，限流与内容过滤接入。
	convSvc := conversation.New(pool, logger, hub,
		conversation.WithBlocker(conversation.NewSQLBlockChecker(pool)),
		conversation.WithLimiter(limits.Message.New()),
		conversation.WithRoomMessageLimiter(moderation.NewTokenBucket(1, 1)),
		conversation.WithFilter(filter),
	)
	convSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)

	// 随机匹配。
	matchSvc := match.New(pool, logger, convSvc, hub, limits.MatchStart.New(), cfg.MatchRelaxAfter, cfg.MatchGrace)
	matchSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)
	gateway.AddObserver(matchSvc)
	gateway.SetResumableProvider(matchSvc.Resumable)

	// 聊天室。
	roomSvc := room.New(pool, logger, convSvc, hub, room.WithMessageRetention(cfg.MessageRetention))
	roomSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)
	gateway.AddConnObserver(roomSvc)

	reportSvc := moderation.NewReportService(pool, logger, limits.ReportDaily.New(), identitySvc)

	// 漂流瓶。
	bottleSvc := bottle.New(pool, logger, convSvc, hub, filter,
		limits.BottlePost.New(), limits.BottlePick.New(), reportSvc)

	// 私密链接。
	privateSvc := privatechat.New(pool, logger, convSvc, hub,
		limits.PrivateLinkCreate.New(), limits.PrivateLinkPassword.New())
	privateSvc.SetResolver(resolver)

	// 管理后台。
	adminSvc := admin.New(pool, logger, banSvc, hub,
		admin.WithLoginLimiters(limits.AdminLogin.New(), limits.AdminLogin.New()))
	adminSvc.SetResolver(resolver)

	// VERIFICATION_MODE=off 时不构造实名验证服务，也不注册 /api/verify/* 路由。
	var verifyHandler *verify.Handler
	if cfg.VerificationMode != "off" {
		sms, err := newSMSProvider(cfg, logger)
		if err != nil {
			return Deps{}, err
		}
		verifySvc, err := verify.NewService(pool, logger, sms, cfg.PhoneEncKey, cfg.PhoneHmacKey,
			verify.WithLimiters(limits.SMSPerPhone.New(), limits.SMSPerPhoneDaily.New(), limits.SMSPerIPDaily.New()),
		)
		if err != nil {
			return Deps{}, err
		}
		verifyHandler = verify.NewHandler(verifySvc, resolver, logger)
	}

	return Deps{
		Cfg:          cfg,
		Pool:         pool,
		Logger:       logger,
		Identity:     identityHandler,
		Verify:       verifyHandler,
		Realtime:     gateway,
		Conversation: convSvc,
		Report:       reportSvc,
		Ban:          banSvc,
		Resolver:     resolver,
		Match:        matchSvc,
		Room:         roomSvc,
		Bottle:       bottleSvc,
		Private:      privateSvc,
		Admin:        adminSvc,
	}, nil
}

// buildModerator 依据配置加载敏感词库（生产环境必须可加载）。
func buildModerator(cfg *config.Config, logger *slog.Logger) (*moderation.Moderator, error) {
	if cfg.WordlistPath == "" {
		if cfg.IsProduction() {
			return nil, fmt.Errorf("生产环境必须配置 WORDLIST_PATH")
		}
		logger.Warn("未配置 WORDLIST_PATH，使用空敏感词库")
		return moderation.NewModerator(nil), nil
	}
	entries, err := moderation.LoadWordlistFile(cfg.WordlistPath)
	if err != nil {
		return nil, err
	}
	return moderation.NewModerator(entries), nil
}

// newSMSProvider 依据配置选择短信服务商实现。
func newSMSProvider(cfg *config.Config, logger *slog.Logger) (verify.SMSProvider, error) {
	switch cfg.SMSProvider {
	case "console":
		return verify.NewConsoleProvider(logger), nil
	default:
		return nil, fmt.Errorf("SMS_PROVIDER=%q 尚未实现", cfg.SMSProvider)
	}
}

// Handler 返回装配好中间件和路由的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	// WebSocket 网关。
	if s.deps.Realtime != nil {
		mux.HandleFunc("GET /ws", s.deps.Realtime.ServeWS)
	}

	// 需要身份 + 封禁检查的包装。
	protected := func(h http.Handler) http.Handler {
		return s.deps.Identity.RequireIdentity(s.banMiddleware(h))
	}
	// 聊天类：再加实名校验。
	chatProtected := func(h http.Handler) http.Handler {
		return s.deps.Identity.RequireIdentity(s.banMiddleware(verify.RequireVerified(s.deps.Cfg.VerificationMode)(h)))
	}

	// 身份相关路由。
	mux.HandleFunc("POST /api/identity", s.deps.Identity.Create)
	mux.Handle("GET /api/identity/me", protected(http.HandlerFunc(s.deps.Identity.Me)))
	mux.Handle("POST /api/identity/reset", protected(http.HandlerFunc(s.deps.Identity.Reset)))

	// 实名验证路由（需要身份，但不要求已验证）。
	if s.deps.Verify != nil {
		mux.Handle("POST /api/verify/sms/send", protected(http.HandlerFunc(s.deps.Verify.Send)))
		mux.Handle("POST /api/verify/sms/confirm", protected(http.HandlerFunc(s.deps.Verify.Confirm)))
	}

	// 会话与消息。
	if s.deps.Conversation != nil {
		mux.Handle("GET /api/conversations", chatProtected(http.HandlerFunc(s.deps.Conversation.ListConversationsHandler)))
		mux.Handle("GET /api/conversations/{id}", chatProtected(http.HandlerFunc(s.deps.Conversation.ConversationHandler)))
		mux.Handle("GET /api/conversations/{id}/messages", chatProtected(http.HandlerFunc(s.deps.Conversation.ListMessagesHandler)))
		mux.Handle("POST /api/conversations/{id}/block", chatProtected(http.HandlerFunc(s.handleBlockConversation)))
		mux.Handle("POST /api/conversations/{id}/members/{memberId}/block", chatProtected(http.HandlerFunc(s.handleBlockMember)))
	}

	// 房间。
	if s.deps.Room != nil {
		mux.Handle("GET /api/rooms", chatProtected(http.HandlerFunc(s.deps.Room.ListRoomsHandler)))
		mux.Handle("GET /api/rooms/{id}/messages", chatProtected(http.HandlerFunc(s.deps.Room.RoomMessagesHandler)))
	}

	// 漂流瓶。
	if s.deps.Bottle != nil {
		mux.Handle("POST /api/bottles", chatProtected(http.HandlerFunc(s.deps.Bottle.PostHandler)))
		mux.Handle("POST /api/bottles/pick", chatProtected(http.HandlerFunc(s.deps.Bottle.PickHandler)))
		mux.Handle("POST /api/bottles/{id}/throw-back", chatProtected(http.HandlerFunc(s.deps.Bottle.ThrowBackHandler)))
		mux.Handle("POST /api/bottles/{id}/reply", chatProtected(http.HandlerFunc(s.deps.Bottle.ReplyHandler)))
		mux.Handle("GET /api/bottles/mine", chatProtected(http.HandlerFunc(s.deps.Bottle.MineHandler)))
		mux.Handle("POST /api/bottles/{id}/report", chatProtected(http.HandlerFunc(s.deps.Bottle.ReportHandler)))
	}
	// 私密链接。
	if s.deps.Private != nil {
		mux.Handle("POST /api/private-links", chatProtected(http.HandlerFunc(s.deps.Private.CreateHandler)))
		mux.Handle("GET /api/private-links/{token}", chatProtected(http.HandlerFunc(s.deps.Private.LookupHandler)))
		mux.Handle("POST /api/private-links/{token}/join", chatProtected(http.HandlerFunc(s.deps.Private.JoinHandler)))
		mux.Handle("POST /api/private-links/{token}/destroy", chatProtected(http.HandlerFunc(s.deps.Private.DestroyHandler)))
	}

	// 举报。
	if s.deps.Report != nil {
		mux.Handle("POST /api/reports", protected(http.HandlerFunc(s.handleReport)))
	}

	// 管理后台（独立 Cookie + 可选 IP 白名单）。
	if s.deps.Admin != nil {
		mux.Handle("/api/admin/", s.adminHandler())
	}

	// 中间件顺序：RequestID → Recover → Logging → SecurityHeaders → OriginCheck。
	return httpx.Chain(mux,
		httpx.RequestID(),
		httpx.Recover(s.deps.Logger),
		httpx.Logging(s.deps.Logger),
		httpx.SecurityHeaders(),
		httpx.OriginCheck(s.deps.Cfg.AllowedOrigins),
	)
}

// adminHandler 装配管理后台路由（IP 白名单 + 独立 Cookie 鉴权）。
func (s *Server) adminHandler() http.Handler {
	a := s.deps.Admin
	am := http.NewServeMux()
	am.HandleFunc("POST /api/admin/login", a.LoginHandler)
	am.HandleFunc("POST /api/admin/logout", a.LogoutHandler)
	am.Handle("GET /api/admin/reports", a.RequireAdmin(http.HandlerFunc(a.ListReportsHandler)))
	am.Handle("GET /api/admin/reports/{id}", a.RequireAdmin(http.HandlerFunc(a.GetReportHandler)))
	am.Handle("POST /api/admin/reports/{id}/resolve", a.RequireAdmin(http.HandlerFunc(a.ResolveReportHandler)))
	am.Handle("GET /api/admin/bans", a.RequireAdmin(http.HandlerFunc(a.ListBansHandler)))
	am.Handle("POST /api/admin/bans", a.RequireAdmin(http.HandlerFunc(a.CreateBanHandler)))
	am.Handle("DELETE /api/admin/bans/{id}", a.RequireAdmin(http.HandlerFunc(a.RevokeBanHandler)))
	am.Handle("GET /api/admin/stats", a.RequireAdmin(http.HandlerFunc(a.StatsHandler)))
	am.Handle("GET /api/admin/audit-logs", a.RequireAdmin(http.HandlerFunc(a.AuditLogsHandler)))

	var prefixes []netip.Prefix
	for _, c := range s.deps.Cfg.AdminIPAllowlist {
		if p, err := netip.ParsePrefix(c); err == nil {
			prefixes = append(prefixes, p)
		}
	}
	return admin.IPAllowlist(prefixes, s.deps.Resolver)(am)
}

// ShutdownWS 关闭全部 WebSocket 连接（关闭码 1001），在 HTTP 服务停止接收新连接后调用。
func (s *Server) ShutdownWS() {
	if s.deps.Realtime != nil {
		s.deps.Realtime.CloseAll()
	}
}

// StartBackground 启动后台任务（阅后即焚扫描、匹配 goroutine）与启动清理，随 ctx 取消停止。
func (s *Server) StartBackground(ctx context.Context) {
	if s.deps.Conversation != nil {
		go s.deps.Conversation.RunBurner(ctx)
	}
	if s.deps.Match != nil {
		if err := s.deps.Match.CleanupOnStartup(ctx); err != nil {
			s.deps.Logger.Warn("启动清理匹配会话失败", "error", err)
		}
		go s.deps.Match.Run(ctx)
	}
	if s.deps.Private != nil {
		go s.deps.Private.StartExpiry(ctx)
	}
}

// handleHealthz 是存活检查，只要进程在运行就返回 200。
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReadyz 检查数据库连接，失败返回 503。
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.deps.Pool.Ping(ctx); err != nil {
		httpx.WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
