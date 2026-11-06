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
	"github.com/anonchat/anonchat/backend/internal/feed"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/privatechat"
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/room"
	"github.com/anonchat/anonchat/backend/internal/story"
	"github.com/anonchat/anonchat/backend/internal/verify"
	"github.com/anonchat/anonchat/backend/internal/vip"
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
	Profile      *profile.Handler
	VIP          *vip.Handler
	Feed         *feed.Handler
	Story        *story.Handler
	StoryService *story.Service
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

	// 资料与匹配偏好。
	profileSvc := profile.New(pool, logger,
		profile.WithFilter(filter),
		profile.WithNicknameLimiters(limits.NicknameShuffle.New(), limits.NicknameSet.New()),
	)
	profileHandler := profile.NewHandler(profileSvc, logger, cfg.VerificationMode)

	// VIP 与激活码。
	vipSvc := vip.New(pool, logger,
		vip.WithLimiters(limits.VIPRedeemPerID.New(), limits.VIPRedeemPerIP.New()),
		vip.WithAccessLogger(identitySvc),
	)
	vipHandler := vip.NewHandler(vipSvc, adminSvc, cfg.VerificationMode)

	// 公告、广播、匿名动态与在线统计。
	feedSvc := feed.New(pool, logger,
		feed.WithFilter(filter),
		feed.WithBroadcastLimiter(limits.BroadcastPost.New()),
		feed.WithOnlineProvider(onlineProvider{hub: hub, match: matchSvc, room: roomSvc}),
	)
	feedHandler := feed.NewHandler(feedSvc, adminSvc)

	// 故事书。
	storySvc := story.New(pool, logger, convSvc, hub,
		story.WithFilter(filter),
		story.WithLimiter(limits.StoryCreate.New()),
		story.WithMinDuration(cfg.StoryMinDuration),
	)
	storyHandler := story.NewHandler(storySvc, adminSvc)

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
		Profile:      profileHandler,
		VIP:          vipHandler,
		Feed:         feedHandler,
		Story:        storyHandler,
		StoryService: storySvc,
	}, nil
}

// onlineProvider 汇总在线人数、排队人数与房间在线数（供 feed 的统计接口使用）。
type onlineProvider struct {
	hub   realtime.ConnStats
	match *match.Service
	room  *room.Service
}

// Online 实现 feed.OnlineProvider。
func (p onlineProvider) Online() (online, matching, inRooms int) {
	if p.hub != nil {
		online = p.hub.OnlineCount()
	}
	if p.match != nil {
		matching = p.match.QueueLen()
	}
	return online, matching, p.RoomOnline()
}

// RoomOnline 实现 feed.OnlineProvider：遍历房间统计在线人数（开销较大，由 feed 缓存）。
func (p onlineProvider) RoomOnline() int {
	if p.room == nil {
		return 0
	}
	return p.room.TotalOnline(context.Background())
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

	// 资料与匹配偏好（需要身份）。
	if s.deps.Profile != nil {
		mux.Handle("PATCH /api/identity/profile", protected(http.HandlerFunc(s.deps.Profile.Update)))
		mux.Handle("POST /api/identity/nickname/shuffle", protected(http.HandlerFunc(s.deps.Profile.Shuffle)))
		mux.Handle("PUT /api/identity/nickname", protected(http.HandlerFunc(s.deps.Profile.SetNickname)))
	}

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

	// VIP 与激活码。
	if s.deps.VIP != nil {
		mux.Handle("POST /api/vip/redeem", protected(http.HandlerFunc(s.deps.VIP.Redeem)))
	}

	// 统计、公告、广播与匿名动态。
	if s.deps.Feed != nil {
		mux.Handle("GET /api/stats/online", protected(http.HandlerFunc(s.deps.Feed.Stats)))
		mux.Handle("GET /api/announcements", protected(http.HandlerFunc(s.deps.Feed.Announcements)))
		mux.Handle("GET /api/broadcasts/current", protected(http.HandlerFunc(s.deps.Feed.CurrentBroadcasts)))
		// 发布公开内容属于 UGC，与 V1 的漂流瓶一致挂 chatProtected：required 模式下要求已实名（审查意见 R3）。
	mux.Handle("POST /api/broadcasts", chatProtected(http.HandlerFunc(s.deps.Feed.SubmitBroadcast)))
		mux.Handle("GET /api/broadcasts/mine", protected(http.HandlerFunc(s.deps.Feed.MyBroadcasts)))
		mux.Handle("POST /api/broadcasts/{id}/report", protected(http.HandlerFunc(s.deps.Feed.ReportBroadcast)))
		mux.Handle("GET /api/feed", protected(http.HandlerFunc(s.deps.Feed.Timeline)))
	}

	// 故事书与作品。
	if s.deps.Story != nil {
		mux.Handle("GET /api/stories", protected(http.HandlerFunc(s.deps.Story.ListStories)))
		// 发起故事属于公开 UGC，同样挂 chatProtected（审查意见 R3）。
	mux.Handle("POST /api/stories", chatProtected(http.HandlerFunc(s.deps.Story.CreateStory)))
		mux.Handle("GET /api/stories/{id}", protected(http.HandlerFunc(s.deps.Story.GetStory)))
		mux.Handle("POST /api/stories/{id}/join", chatProtected(http.HandlerFunc(s.deps.Story.JoinStory)))
		mux.Handle("POST /api/stories/{id}/end", chatProtected(http.HandlerFunc(s.deps.Story.EndStory)))
		mux.Handle("DELETE /api/stories/{id}", protected(http.HandlerFunc(s.deps.Story.CancelStory)))
		mux.Handle("POST /api/stories/{id}/consent", protected(http.HandlerFunc(s.deps.Story.ConsentStory)))
		mux.Handle("GET /api/works", protected(http.HandlerFunc(s.deps.Story.ListWorks)))
		mux.Handle("GET /api/works/{id}", protected(http.HandlerFunc(s.deps.Story.GetWork)))
		mux.Handle("POST /api/works/{id}/like", protected(http.HandlerFunc(s.deps.Story.LikeWork)))
		mux.Handle("DELETE /api/works/{id}/like", protected(http.HandlerFunc(s.deps.Story.UnlikeWork)))
		mux.Handle("DELETE /api/works/{id}", protected(http.HandlerFunc(s.deps.Story.DeleteWork)))
		mux.Handle("POST /api/works/{id}/withdraw", protected(http.HandlerFunc(s.deps.Story.WithdrawWork)))
		mux.Handle("POST /api/works/{id}/report", protected(http.HandlerFunc(s.deps.Story.ReportWork)))
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
	am.Handle("GET /api/admin/identities/by-code/{code}", a.RequireAdmin(http.HandlerFunc(a.LookupIdentityByCodeHandler)))

	// 公告、广播审核、激活码批次、作品审核。
	if s.deps.Feed != nil {
		am.Handle("GET /api/admin/announcements", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminListAnnouncements)))
		am.Handle("POST /api/admin/announcements", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminCreateAnnouncement)))
		am.Handle("DELETE /api/admin/announcements/{id}", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminDeleteAnnouncement)))
		am.Handle("GET /api/admin/broadcasts", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminListBroadcasts)))
		am.Handle("POST /api/admin/broadcasts/{id}/approve", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminApproveBroadcast)))
		am.Handle("POST /api/admin/broadcasts/{id}/reject", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminRejectBroadcast)))
		am.Handle("POST /api/admin/broadcasts/{id}/remove", a.RequireAdmin(http.HandlerFunc(s.deps.Feed.AdminRemoveBroadcast)))
	}
	if s.deps.VIP != nil {
		am.Handle("GET /api/admin/activation-batches", a.RequireAdmin(http.HandlerFunc(s.deps.VIP.ListBatches)))
		am.Handle("POST /api/admin/activation-batches", a.RequireAdmin(http.HandlerFunc(s.deps.VIP.CreateBatch)))
		am.Handle("POST /api/admin/activation-batches/{id}/revoke", a.RequireAdmin(http.HandlerFunc(s.deps.VIP.RevokeBatch)))
	}
	if s.deps.Story != nil {
		am.Handle("GET /api/admin/works", a.RequireAdmin(http.HandlerFunc(s.deps.Story.AdminListWorks)))
		am.Handle("POST /api/admin/works/{id}/approve", a.RequireAdmin(http.HandlerFunc(s.deps.Story.AdminApproveWork)))
		am.Handle("POST /api/admin/works/{id}/reject", a.RequireAdmin(http.HandlerFunc(s.deps.Story.AdminRejectWork)))
		am.Handle("POST /api/admin/works/{id}/remove", a.RequireAdmin(http.HandlerFunc(s.deps.Story.AdminRemoveWork)))
	}

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
	if s.deps.StoryService != nil {
		go runStoryExpiry(ctx, s.deps.StoryService, s.deps.Logger)
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

// runStoryExpiry 每分钟把等待超时的故事置为 expired（how.md 3.1 留存规则）。
func runStoryExpiry(ctx context.Context, svc *story.Service, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := svc.ExpireWaiting(ctx); err != nil {
				logger.Warn("故事过期清理失败", "error", err)
			} else if n > 0 {
				logger.Info("故事过期清理", "rows", n)
			}
		}
	}
}
