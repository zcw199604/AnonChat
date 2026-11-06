//go:build integration

// Package testutil 提供集成测试用的公共设施：连接测试库、执行迁移、清空数据、构建完整 HTTP 服务。
// 仅在 -tags integration 时编译。
package testutil

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/api"
	"github.com/anonchat/anonchat/backend/internal/bot"
	"github.com/anonchat/anonchat/backend/internal/bottle"
	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/feed"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/privatechat"
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/room"
	"github.com/anonchat/anonchat/backend/internal/story"
	"github.com/anonchat/anonchat/backend/internal/verify"
	"github.com/anonchat/anonchat/backend/internal/vip"
	"github.com/anonchat/anonchat/backend/migrations"
)

// Config 汇总测试服务的可覆盖配置。
type Config struct {
	VerificationMode string
	EncKey           []byte
	HmacKey          []byte
	TrustedProxies   []string
	AllowedOrigins   []string

	IdentityLimiter      moderation.RateLimiter
	VerifyCooldown       moderation.RateLimiter
	VerifyPhoneDay       moderation.RateLimiter
	VerifyIPDay          moderation.RateLimiter
	MessageLimiter       moderation.RateLimiter
	ReportLimiter        moderation.RateLimiter
	MatchLimiter         moderation.RateLimiter
	MatchRelaxAfter      time.Duration
	MatchGrace           time.Duration
	BottlePostLimiter    moderation.RateLimiter
	BottlePickLimiter    moderation.RateLimiter
	PrivateCreateLimiter moderation.RateLimiter
	PasswordLimiter      moderation.RateLimiter
	AdminLoginLimiter    moderation.RateLimiter
	AdminIPAllowlist     []string

	Filter conversation.ContentFilter

	// StoryMinDuration 是故事有效场次的最短时长（测试可调短）。
	StoryMinDuration time.Duration
	// BroadcastLimiter / StoryLimiter / VIPLimiters 允许测试收紧限流。
	BroadcastLimiter moderation.RateLimiter
	StoryLimiter     moderation.RateLimiter
	VIPPerIDLimiter  moderation.RateLimiter
	VIPPerIPLimiter  moderation.RateLimiter
	NicknameShuffle  moderation.RateLimiter
	NicknameSet      moderation.RateLimiter

	SMS   verify.SMSProvider
	Clock func() time.Time

	// MediaRoot 是媒体文件根目录，默认 t.TempDir()。
	MediaRoot string

	// BotLLM 非 nil 时启用 bot 引擎（相当于 BOT_ENABLED=true）。
	BotLLM bot.LLM
	// BotFallbackAfter 是排队多久后由 bot 兜底，默认 200ms。
	BotFallbackAfter time.Duration
	// BotTick 是匹配器定时扫描间隔，默认 20ms。
	BotTick time.Duration
	// BotSettingsTestLimiter 限制"测试连接"接口的频率（默认 AllowAll）。
	BotSettingsTestLimiter moderation.RateLimiter
	// BotRefresh 是 bot 列表缓存刷新间隔，默认 20ms。
	BotRefresh time.Duration
	// BotBottleLoop 是漂流瓶循环间隔，默认 20ms。
	BotBottleLoop time.Duration
	// BottleMinAge 是瓶子投出多久后 bot 才会回复，默认 0。
	BottleMinAge time.Duration
	// BotSettingsDisabled 为 true 时，初始化写入的全局设置为「关闭」（用于热切换测试）。
	BotSettingsDisabled bool
	// NoSettingsEncKey 为 true 时不配置 SETTINGS_ENC_KEY（后台不能保存 API Key）。
	NoSettingsEncKey bool
}

// Env 是集成测试环境的句柄，暴露底层 api.Deps 以便直接调用各服务。
type Env struct {
	Server *httptest.Server
	Deps   api.Deps
}

// MustPool 连接测试库并确保迁移已执行；调用方负责在测试结束后 Close。
func MustPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL 未设置，无法运行集成测试")
	}
	runMigrations(t, url)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("Ping 测试库失败: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

var migrateMu sync.Mutex

// runMigrations 在测试库上执行迁移（幂等）。
func runMigrations(t *testing.T, url string) {
	t.Helper()
	migrateMu.Lock()
	defer migrateMu.Unlock()

	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	defer db.Close()
	if _, err := migrations.Up(context.Background(), db); err != nil {
		t.Fatalf("执行迁移失败: %v", err)
	}
}

// Truncate 清空身份/验证相关表，确保测试之间互不影响。
func Truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	const q = `TRUNCATE identities, verified_accounts, sms_codes, access_logs, conversations, conversation_members, messages, blocks, bottles, bottle_picks, private_links, reports, bans, rooms, admin_users, admin_sessions, admin_audit_logs, announcements, broadcasts, activation_batches, activation_codes, stories, story_works, story_work_likes, character_cards, media_folders, media_items, bots, bot_usage, bot_settings RESTART IDENTITY CASCADE`
	if _, err := pool.Exec(context.Background(), q); err != nil {
		t.Fatalf("清空表失败: %v", err)
	}
}

// MustServer 构建一个完整的 HTTP 测试服务。
func MustServer(t *testing.T, pool *pgxpool.Pool, c Config) *httptest.Server {
	return MustEnv(t, pool, c).Server
}

// MustEnv 构建完整测试环境（含 api.Deps）。
func MustEnv(t *testing.T, pool *pgxpool.Pool, c Config) *Env {
	t.Helper()

	cfg := buildConfig(c)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := c.Clock
	if clock == nil {
		clock = time.Now
	}

	resolver, err := httpx.NewClientIPResolver(cfg.TrustedProxies)
	if err != nil {
		t.Fatalf("构造 ClientIPResolver 失败: %v", err)
	}

	idLimiter := c.IdentityLimiter
	if idLimiter == nil {
		idLimiter = moderation.AllowAll()
	}
	idSvc := identity.NewService(pool, logger, idLimiter, identity.WithClock(clock))
	idHandler := identity.NewHandler(idSvc, resolver, logger, cfg.VerificationMode)

	banSvc := moderation.NewBanService(pool, nil, moderation.WithBanClock(clock))
	idHandler.SetIPBanChecker(banSvc)

	sms := c.SMS
	if sms == nil {
		sms = verify.NewConsoleProvider(logger)
	}
	var verHandler *verify.Handler
	if cfg.VerificationMode != "off" {
		verSvc, err := verify.NewService(pool, logger, sms, cfg.PhoneEncKey, cfg.PhoneHmacKey,
			verify.WithClock(clock),
			verify.WithLimiters(c.VerifyCooldown, c.VerifyPhoneDay, c.VerifyIPDay),
		)
		if err != nil {
			t.Fatalf("构造 verify.Service 失败: %v", err)
		}
		verHandler = verify.NewHandler(verSvc, resolver, logger)
	}

	gateway := realtime.NewGateway(realtime.Options{AllowedOrigins: cfg.AllowedOrigins},
		logger, idSvc, banSvc, idSvc, resolver)
	banSvc.SetKicker(gateway.Hub())

	filter := c.Filter
	if filter == nil {
		filter = moderation.NewModerator(nil)
	}
	msgLimiter := c.MessageLimiter
	if msgLimiter == nil {
		msgLimiter = moderation.AllowAll()
	}
	convSvc := conversation.New(pool, logger, gateway.Hub(),
		conversation.WithBlocker(conversation.NewSQLBlockChecker(pool)),
		conversation.WithClock(clock),
		conversation.WithFilter(filter),
		conversation.WithLimiter(msgLimiter),
		conversation.WithRoomMessageLimiter(moderation.NewTokenBucket(1, 1)),
	)
	convSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)

	matchLimiter := c.MatchLimiter
	if matchLimiter == nil {
		matchLimiter = moderation.AllowAll()
	}
	relaxAfter := c.MatchRelaxAfter
	if relaxAfter <= 0 {
		relaxAfter = 15 * time.Second
	}
	grace := c.MatchGrace
	if grace <= 0 {
		grace = 30 * time.Second
	}
	botTick := c.BotTick
	if botTick <= 0 {
		botTick = 20 * time.Millisecond
	}
	matchSvc := match.New(pool, logger, convSvc, gateway.Hub(), matchLimiter, relaxAfter, grace,
		match.WithClock(clock), match.WithTick(botTick))
	matchSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)
	gateway.AddObserver(matchSvc)
	gateway.SetResumableProvider(matchSvc.Resumable)

	roomSvc := room.New(pool, logger, convSvc, gateway.Hub(), room.WithClock(clock))
	roomSvc.RegisterWS(gateway.Router(), cfg.VerificationMode)
	gateway.AddConnObserver(roomSvc)

	mctx, mcancel := context.WithCancel(context.Background())
	go matchSvc.Run(mctx)
	t.Cleanup(mcancel)

	reportLimiter := c.ReportLimiter
	if reportLimiter == nil {
		reportLimiter = moderation.AllowAll()
	}
	reportSvc := moderation.NewReportService(pool, logger, reportLimiter, idSvc, moderation.WithReportClock(clock))

	bottleSvc := bottle.New(pool, logger, convSvc, gateway.Hub(), filter,
		limiterOr(c.BottlePostLimiter), limiterOr(c.BottlePickLimiter), reportSvc, bottle.WithClock(clock))
	privateSvc := privatechat.New(pool, logger, convSvc, gateway.Hub(),
		limiterOr(c.PrivateCreateLimiter), limiterOr(c.PasswordLimiter),
		privatechat.WithClock(clock), privatechat.WithScanInterval(200*time.Millisecond))
	privateSvc.SetResolver(resolver)

	adminSvc := admin.New(pool, logger, banSvc, gateway.Hub(),
		admin.WithClock(clock),
		admin.WithLoginLimiters(limiterOr(c.AdminLoginLimiter), limiterOr(c.AdminLoginLimiter)))

	profileSvc := profile.New(pool, logger,
		profile.WithClock(clock),
		profile.WithFilter(filter),
		profile.WithNicknameLimiters(limiterOr(c.NicknameShuffle), limiterOr(c.NicknameSet)),
	)
	profileHandler := profile.NewHandler(profileSvc, logger, cfg.VerificationMode)

	vipSvc := vip.New(pool, logger,
		vip.WithClock(clock),
		vip.WithLimiters(limiterOr(c.VIPPerIDLimiter), limiterOr(c.VIPPerIPLimiter)),
		vip.WithAccessLogger(idSvc),
	)
	vipHandler := vip.NewHandler(vipSvc, adminSvc, cfg.VerificationMode)

	feedSvc := feed.New(pool, logger,
		feed.WithClock(clock),
		feed.WithFilter(filter),
		feed.WithBroadcastLimiter(limiterOr(c.BroadcastLimiter)),
	)
	feedHandler := feed.NewHandler(feedSvc, adminSvc)

	storySvc := story.New(pool, logger, convSvc, gateway.Hub(),
		story.WithClock(clock),
		story.WithFilter(filter),
		story.WithLimiter(limiterOr(c.StoryLimiter)),
		story.WithMinDuration(c.StoryMinDuration),
	)
	storyHandler := story.NewHandler(storySvc, adminSvc)

	// AI 机器人：引擎始终创建并运行（how.md §15.2）；BotLLM 非 nil 时注入假模型并默认启用设置。
	// 生效配置写在 bot_settings 里：开关/模型/兜底时间/瓶龄/时区都从数据库读，
	// 因此测试可以在运行时用 PUT /api/admin/bot-settings 热切换。
	botSvc := bot.New(pool, logger, bot.WithConversation(convSvc))
	bottleLoop := c.BotBottleLoop
	if bottleLoop <= 0 {
		bottleLoop = 20 * time.Millisecond
	}
	refresh := c.BotRefresh
	if refresh <= 0 {
		refresh = 20 * time.Millisecond
	}
	fallbackAfter := c.BotFallbackAfter
	if fallbackAfter <= 0 {
		fallbackAfter = 200 * time.Millisecond
	}
	botSettings, err := bot.NewSettingsCache(context.Background(), pool, logger, cfg.SettingsEncKey, bot.EnvSettings{
		Enabled:            c.BotLLM != nil && !c.BotSettingsDisabled,
		BaseURL:            "http://llm.test/v1",
		Model:              "test-model",
		Timeout:            cfg.LLMTimeout,
		SendCacheKey:       true,
		MatchFallbackAfter: fallbackAfter,
		BottleMinAge:       c.BottleMinAge,
		Timezone:           "Asia/Shanghai",
	}, c.BotLLM != nil)
	if err != nil {
		t.Fatalf("初始化 bot 设置失败: %v", err)
	}
	botEngine := bot.NewEngine(pool, logger, convSvc, c.BotLLM, botSettings,
		bot.WithClock(clock),
		bot.WithRefreshInterval(refresh),
		bot.WithBottleLoopInterval(bottleLoop),
		bot.WithBottles(bottleSvc),
	)
	convSvc.AddObserver(botEngine)
	matchSvc.SetBotFallback(botEngine)
	botHandler := bot.NewHandler(botSvc, adminSvc, botEngine, botSettings,
		limiterOr(c.BotSettingsTestLimiter), logger)
	go botEngine.Run(mctx)

	mediaRoot := c.MediaRoot
	if mediaRoot == "" {
		mediaRoot = t.TempDir()
	}
	cfg.MediaRoot = mediaRoot
	mediaStorage, err := media.NewStorage(mediaRoot)
	if err != nil {
		t.Fatalf("构造媒体存储失败: %v", err)
	}
	mediaHandler := media.NewHandler(media.New(pool, logger, mediaStorage, cfg.MediaMaxUploadMB), adminSvc)

	deps := api.Deps{Cfg: cfg, Pool: pool, Logger: logger, Identity: idHandler, Verify: verHandler,
		Realtime: gateway, Conversation: convSvc, Report: reportSvc, Ban: banSvc, Resolver: resolver,
		Match: matchSvc, Room: roomSvc, Bottle: bottleSvc, Private: privateSvc, Admin: adminSvc,
		Profile: profileHandler, VIP: vipHandler, Feed: feedHandler, Story: storyHandler, StoryService: storySvc,
		Media: mediaHandler, BotAdmin: botHandler, BotEngine: botEngine}
	srv := httptest.NewServer(api.NewServer(deps).Handler())
	t.Cleanup(srv.Close)
	return &Env{Server: srv, Deps: deps}
}

// buildConfig 依据测试配置构造 config.Config。
func buildConfig(c Config) *config.Config {
	origins := c.AllowedOrigins
	if len(origins) == 0 {
		origins = []string{"http://test.local"}
	}
	mode := c.VerificationMode
	if mode == "" {
		mode = "off"
	}
	storyMin := c.StoryMinDuration
	if storyMin <= 0 {
		storyMin = 5 * time.Minute
	}
	return &config.Config{
		Env:              "test",
		HTTPAddr:         ":8080",
		DatabaseURL:      os.Getenv("TEST_DATABASE_URL"),
		AllowedOrigins:   origins,
		TrustedProxies:   c.TrustedProxies,
		VerificationMode: mode,
		SMSProvider:      "console",
		PhoneEncKey:      c.EncKey,
		PhoneHmacKey:     c.HmacKey,
		AdminIPAllowlist: c.AdminIPAllowlist,
		StoryMinDuration: storyMin,
		// 媒体库：根目录由 MustEnv 注入（默认 t.TempDir()）。
		MediaRoot:        c.MediaRoot,
		MediaMaxUploadMB: 100,
		BotTimezone:      "Asia/Shanghai",
		LLMTimeout:       10 * time.Second,
		BotBottleMinAge:  bottleMinAgeOrZero(c.BottleMinAge),
		SettingsEncKey:   settingsEncKey(c.NoSettingsEncKey),
	}
}

// DefaultOrigin 是测试请求默认携带的 Origin。
const DefaultOrigin = "http://test.local"

// Response 是一次 HTTP 调用的结果。
type Response struct {
	Status    int
	Body      []byte
	Token     string // 从 Set-Cookie 提取的 __Host-anon 令牌（若有）
	SetCookie *http.Cookie
}

// JSON 把响应体解析到 v。
func (r Response) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.Body, v); err != nil {
		t.Fatalf("解析响应 JSON 失败: %v, body=%s", err, r.Body)
	}
}

// Do 发送一次 JSON 请求。cookieToken 非空时带上身份 Cookie；origin 为空时使用 DefaultOrigin。
func Do(t *testing.T, srv *httptest.Server, method, path string, body any, cookieToken, origin string) Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("序列化请求体失败: %v", err)
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	} else {
		req.Header.Set("Origin", DefaultOrigin)
	}
	if cookieToken != "" {
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: cookieToken})
	}

	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求失败: %v", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	out := Response{Status: resp.StatusCode, Body: respBody}
	for _, ck := range resp.Cookies() {
		if ck.Name == identity.CookieName {
			out.Token = ck.Value
			out.SetCookie = ck
		}
	}
	return out
}

// CapturingSMS 是记录验证码的短信提供方，供测试读取。
type CapturingSMS struct {
	mu      sync.Mutex
	byPhone map[string][]string
}

// NewCapturingSMS 创建记录型短信提供方。
func NewCapturingSMS() *CapturingSMS {
	return &CapturingSMS{byPhone: make(map[string][]string)}
}

// Send 记录验证码。
func (c *CapturingSMS) Send(_ context.Context, phone, code string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byPhone[phone] = append(c.byPhone[phone], code)
	return nil
}

// Last 返回该号码最近一次的验证码。
func (c *CapturingSMS) Last(phone string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	codes := c.byPhone[phone]
	if len(codes) == 0 {
		return "", false
	}
	return codes[len(codes)-1], true
}

// SeedRooms 插入 6 个官方房间（与迁移 00003 一致），供 Truncate 后重建种子。
func SeedRooms(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	rooms := []struct {
		slug, name, descr string
		ord               int32
	}{
		{"lobby", "闲聊大厅", "随便聊聊，认识新朋友", 1},
		{"late-night", "深夜树洞", "夜深了，把心事说出来", 2},
		{"gaming", "游戏", "开黑、组队、聊游戏", 3},
		{"study", "学习打卡", "一起学习，互相监督", 4},
		{"emotion", "情感", "关于喜欢、关系和烦恼", 5},
		{"work", "职场", "工作、面试与吐槽", 6},
	}
	for _, r := range rooms {
		var convID pgtype.UUID
		if err := pool.QueryRow(context.Background(),
			"INSERT INTO conversations (kind) VALUES ('room') RETURNING id").Scan(&convID); err != nil {
			t.Fatalf("插入房间会话失败: %v", err)
		}
		if _, err := pool.Exec(context.Background(),
			"INSERT INTO rooms (conversation_id, slug, name, description, sort_order) VALUES ($1,$2,$3,$4,$5)",
			convID, r.slug, r.name, r.descr, r.ord); err != nil {
			t.Fatalf("插入房间失败: %v", err)
		}
	}
}

// settingsEncKey 返回后台设置的加密密钥（测试默认给 32 字节；NoSettingsEncKey 时返回 nil）。
func settingsEncKey(disabled bool) []byte {
	if disabled {
		return nil
	}
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(200 + i)
	}
	return k
}

// bottleMinAgeOrZero 返回瓶子最小年龄（测试默认 0，让 bot 立刻能捞到）。
func bottleMinAgeOrZero(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}

// limiterOr 返回给定限流器，nil 时回退 AllowAll。
func limiterOr(l moderation.RateLimiter) moderation.RateLimiter {
	if l == nil {
		return moderation.AllowAll()
	}
	return l
}
