package bot

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // 内嵌时区数据库：容器里没有 tzdata 时也能解析 BOT_TIMEZONE
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 默认参数。
const (
	// defaultRefreshEvery 是 bot 列表缓存的刷新间隔。
	defaultRefreshEvery = 30 * time.Second
	// defaultTurnTimeout 是一次回复流程（读库 + 调用模型 + 发送）的超时。
	defaultTurnTimeout = 2 * time.Minute
	// queryTimeout 是引擎内部单次查询的超时。
	queryTimeout = 10 * time.Second
	// defaultReplyDelayMin / Max 是缓存里还没有 bot 设置时使用的兜底节奏。
	defaultReplyDelayMin = 3000 * time.Millisecond
	defaultReplyDelayMax = 12000 * time.Millisecond
	// historyQueryLimit 与提示词的历史上限保持一致。
	historyQueryLimit = historyMaxMessages
)

// fallbackLocation 是时区解析失败时的回退（UTC+8 固定时区）。
var fallbackLocation = time.FixedZone("UTC+8", 8*3600)

// LoadLocation 解析 BOT_TIMEZONE；失败时回退到 UTC+8（how.md §6）。
func LoadLocation(name string) *time.Location {
	if name == "" {
		return fallbackLocation
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		slog.Warn("BOT_TIMEZONE 解析失败，回退到 UTC+8", "timezone", name, "error", err)
		return fallbackLocation
	}
	return loc
}

// botEntry 是内存缓存的 bot（设置 + 身份 + 角色卡 + 媒体标签词表）。
type botEntry struct {
	bot   store.Bot
	ident store.Identity
	card  *store.CharacterCard
	tags  []string
}

// convState 是每个会话的防抖状态。
type convState struct {
	timer          *time.Timer
	firstPendingAt time.Time
	running        bool
	dirty          bool
	bot            store.Bot
}

// Engine 是 bot 的 worker：实现 conversation.Observer，按节奏生成并发送回复。
type Engine struct {
	pool   *pgxpool.Pool
	q      *store.Queries
	logger *slog.Logger
	conv   *conversation.Service
	pacer  pacer
	// llmOverride 是测试注入的假模型客户端；非 nil 时始终优先于按配置构建的客户端。
	llmOverride LLM
	// settings 是"生效配置"的内存缓存（开关、模型接入、兜底时间、瓶龄、时区）。
	settings *SettingsCache
	// bottles 为 nil 时不启动漂流瓶循环。
	bottles    BottleService
	bottleLoop time.Duration

	now          func() time.Time
	sleep        func(ctx context.Context, d time.Duration) error
	refreshEvery time.Duration
	turnTimeout  time.Duration

	// llmMu/llmClient 缓存按配置构建的 OpenAI 兼容客户端。
	llmMu     sync.Mutex
	llmClient *OpenAIClient
	llmKey    llmClientKey

	mu    sync.Mutex
	bots  map[pgtype.UUID]*botEntry
	convs map[pgtype.UUID]*convState
	// nextBottle 记录每个 bot 下一次可以回瓶的时间。
	nextBottle map[pgtype.UUID]time.Time
	ctx        context.Context
	wg         sync.WaitGroup
}

// 编译期断言：Engine 实现了会话观察者与匹配兜底的 provider 接口（how.md §3.3、§7.1、§8）。
var (
	_ conversation.Observer = (*Engine)(nil)
	_ match.BotProvider     = (*Engine)(nil)
)

// EngineOption 用于配置 Engine。
type EngineOption func(*Engine)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) EngineOption {
	return func(e *Engine) {
		if fn != nil {
			e.now = fn
		}
	}
}

// WithRand 注入随机源（测试用）。
func WithRand(r RandSource) EngineOption {
	return func(e *Engine) {
		if r != nil {
			e.pacer.rnd = r
		}
	}
}

// WithSleep 注入等待函数（测试用）。
func WithSleep(fn func(ctx context.Context, d time.Duration) error) EngineOption {
	return func(e *Engine) {
		if fn != nil {
			e.sleep = fn
		}
	}
}

// WithRefreshInterval 设置 bot 列表的刷新间隔。
func WithRefreshInterval(d time.Duration) EngineOption {
	return func(e *Engine) {
		if d > 0 {
			e.refreshEvery = d
		}
	}
}

// NewEngine 创建 bot 引擎。
// llm 是测试注入的假客户端（可为 nil：此时按后台配置构建 OpenAI 兼容客户端）；
// settings 是生效配置缓存（开关、模型接入、兜底时间、瓶龄、时区）。
func NewEngine(pool *pgxpool.Pool, logger *slog.Logger, conv *conversation.Service, llm LLM, settings *SettingsCache, opts ...EngineOption) *Engine {
	e := &Engine{
		pool:         pool,
		q:            store.New(pool),
		logger:       logger,
		conv:         conv,
		llmOverride:  llm,
		settings:     settings,
		pacer:        pacer{rnd: newAsyncRand(time.Now().UnixNano())},
		now:          time.Now,
		sleep:        sleepCtx,
		refreshEvery: defaultRefreshEvery,
		turnTimeout:  defaultTurnTimeout,
		bottleLoop:   defaultBottleLoop,
		bots:         make(map[pgtype.UUID]*botEntry),
		convs:        make(map[pgtype.UUID]*convState),
		nextBottle:   make(map[pgtype.UUID]time.Time),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Settings 返回生效配置的当前快照（只读内存）。
func (e *Engine) Settings() Settings {
	if e.settings == nil {
		return Settings{NotReadyReason: "未初始化设置"}
	}
	return e.settings.Get()
}

// ready 报告当前配置是否可用（引擎据此决定是否工作）。
func (e *Engine) ready() bool {
	return e.Settings().Ready
}

// FallbackAfter 实现 match.BotProvider：返回当前的排队兜底时间，0 表示不兜底。
// 只读内存缓存，匹配 goroutine 内不会访问数据库。
func (e *Engine) FallbackAfter() time.Duration {
	if e.settings == nil {
		return 0
	}
	s := e.settings.Get()
	if !s.Ready {
		return 0
	}
	return s.MatchFallbackAfter
}

// llmClientKey 是构建/复用 LLM 客户端时的配置指纹。
type llmClientKey struct {
	baseURL      string
	apiKey       string
	sendCacheKey bool
	timeout      time.Duration
}

// llm 返回本次调用使用的客户端与超时：注入的假客户端优先，否则按配置构建/复用。
func (e *Engine) llm() (LLM, time.Duration) {
	s := e.Settings()
	if e.llmOverride != nil {
		return e.llmOverride, s.Timeout
	}
	key := llmClientKey{baseURL: s.BaseURL, apiKey: s.APIKey, sendCacheKey: s.SendCacheKey, timeout: s.Timeout}
	e.llmMu.Lock()
	defer e.llmMu.Unlock()
	if e.llmClient == nil || e.llmKey != key {
		e.llmClient = NewOpenAIClient(ClientOptions{
			BaseURL:      key.baseURL,
			APIKey:       key.apiKey,
			SendCacheKey: key.sendCacheKey,
			Logger:       e.logger,
		})
		e.llmKey = key
	}
	return e.llmClient, key.timeout
}

// Run 启动引擎：先刷新 bot 缓存，然后按间隔刷新，直到 ctx 取消。
// 退出时停止所有计时器。
func (e *Engine) Run(ctx context.Context) {
	e.mu.Lock()
	e.ctx = ctx
	e.mu.Unlock()

	e.refresh()

	// 生效配置每 30 秒自动刷新（后台保存会立即刷新）。
	if e.settings != nil {
		go e.settings.Run(ctx)
	}

	// 漂流瓶循环（注入了漂流瓶服务时才启动）。
	if e.bottles != nil {
		go e.runBottleLoop(ctx)
	}

	ticker := time.NewTicker(e.refreshEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.stopAll()
			return
		case <-ticker.C:
			e.refresh()
		}
	}
}

// Invalidate 立即重新加载生效配置与 bot 缓存（后台写操作后调用，无需重启）。
func (e *Engine) Invalidate() {
	if e == nil {
		return
	}
	if e.settings != nil {
		ctx, cancel := context.WithTimeout(e.baseCtx(), queryTimeout)
		if err := e.settings.Refresh(ctx); err != nil {
			e.logger.Warn("刷新 bot 设置失败", "error", err)
		}
		cancel()
	}
	e.refresh()
}

// refresh 从数据库重新加载已启用的 bot 列表。
func (e *Engine) refresh() {
	ctx, cancel := context.WithTimeout(e.baseCtx(), queryTimeout)
	defer cancel()

	bots, err := e.q.ListEnabledBots(ctx)
	if err != nil {
		e.logger.Warn("加载 bot 列表失败", "error", err)
		return
	}
	idents, err := e.q.ListBotIdentities(ctx)
	if err != nil {
		e.logger.Warn("加载 bot 身份失败", "error", err)
		return
	}
	identByID := make(map[pgtype.UUID]store.Identity, len(idents))
	for _, ident := range idents {
		identByID[ident.ID] = ident
	}

	next := make(map[pgtype.UUID]*botEntry, len(bots))
	for _, b := range bots {
		ident, ok := identByID[b.IdentityID]
		if !ok {
			continue
		}
		entry := &botEntry{bot: b, ident: ident}
		if b.CardID.Valid {
			if card, cerr := e.q.GetCharacterCard(ctx, b.CardID); cerr == nil {
				entry.card = &card
			}
		}
		if b.MediaFolderID.Valid {
			if tags, terr := e.q.ListFolderTags(ctx, b.MediaFolderID); terr == nil {
				entry.tags = tags
			}
		}
		next[b.IdentityID] = entry
	}

	e.mu.Lock()
	e.bots = next
	e.mu.Unlock()
}

// botByIdentity 返回该身份对应的 bot（不是 bot 时返回 nil）。
func (e *Engine) botByIdentity(identityID pgtype.UUID) *botEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bots[identityID]
}

// baseCtx 返回引擎的基础 ctx（未启动时为 Background）。
func (e *Engine) baseCtx() context.Context {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx != nil {
		return e.ctx
	}
	return context.Background()
}

// stopAll 停止全部计时器并清空会话状态。
func (e *Engine) stopAll() {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, st := range e.convs {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(e.convs, id)
	}
}

// ---- conversation.Observer ----

// OnMessage 处理新消息：只关心 match/bottle 会话里由用户发出的消息。
// 立即返回，查询与等待都在 goroutine 中完成。
func (e *Engine) OnMessage(ev conversation.MessageEvent) {
	if ev.ConversationKind != store.ConversationKindMatch && ev.ConversationKind != store.ConversationKindBottle {
		return
	}
	// 全局关闭或配置不可用：不再调度新的回复。
	if !e.ready() {
		return
	}
	// bot 自己发的消息不触发回复。
	if e.botByIdentity(ev.SenderIdentityID) != nil {
		return
	}
	convID := ev.ConversationID
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		ctx, cancel := context.WithTimeout(e.baseCtx(), queryTimeout)
		defer cancel()
		bot, err := e.q.GetConversationBot(ctx, convID)
		if err != nil {
			return // 会话里没有未离开的 bot 成员
		}
		e.schedule(convID, bot)
	}()
}

// OnConversationEnded 停止计时器并删除会话状态；立即返回。
func (e *Engine) OnConversationEnded(conversationID pgtype.UUID) {
	e.forgetConv(conversationID)
}

// BotMatched 由匹配兜底的 provider 调用：读消息延迟后，若会话里还没有任何消息就执行开场。
func (e *Engine) BotMatched(conversationID, botIdentityID pgtype.UUID) {
	if !e.ready() {
		return
	}
	e.wg.Add(1)
	go func() {
		defer e.wg.Done()
		minDelay, maxDelay := defaultReplyDelayMin, defaultReplyDelayMax
		if entry := e.botByIdentity(botIdentityID); entry != nil {
			minDelay = msDuration(entry.bot.ReplyDelayMinMs)
			maxDelay = msDuration(entry.bot.ReplyDelayMaxMs)
		}
		if err := e.sleep(e.baseCtx(), e.pacer.readDelay(minDelay, maxDelay)); err != nil {
			return
		}
		e.startTurn(conversationID, botIdentityID, true)
	}()
}

// PickBot 实现 match.BotProvider（how.md §8）：按 §8 的条件筛选可用的 bot。
// 并发匹配时可能让某个 bot 的进行中会话数超过 max_active_matches 1 个，可以接受。
func (e *Engine) PickBot(ctx context.Context, user *store.Identity, ic match.IdentityContext) (*match.BotCandidate, error) {
	if user == nil || !e.ready() {
		return nil, nil
	}
	type candidate struct {
		entry   *botEntry
		profile match.PrefProfile
	}
	available := make([]candidate, 0, len(e.botEntries()))
	preferred := make([]candidate, 0, len(e.botEntries()))

	for _, entry := range e.botEntries() {
		bot := entry.bot
		// 基础条件：启用、承接匹配、绑定了角色卡。
		if !bot.Enabled || !bot.ServeMatch || !bot.CardID.Valid {
			continue
		}
		// 双向无屏蔽（ic.Blocked 是用户视角的双向集合）。
		if _, blocked := ic.Blocked[bot.IdentityID]; blocked {
			continue
		}
		prof := profileOf(entry.ident)
		// 与真人配对使用同一套兼容规则。
		if !match.Compatible(ic.Profile, prof) {
			continue
		}
		// bot 级限额（总量 / 滑动窗口）。
		if exceeded, _ := Check(LimitsFromBot(bot), e.botUsage(ctx, bot)); exceeded {
			continue
		}
		// 进行中的匹配会话数不超过上限。
		active, err := e.q.CountActiveMatchConversationsForIdentity(ctx, bot.IdentityID)
		if err != nil {
			continue
		}
		if active >= int64(bot.MaxActiveMatches) {
			continue
		}
		cand := candidate{entry: entry, profile: prof}
		available = append(available, cand)
		if _, recent := ic.Recent[bot.IdentityID]; !recent {
			preferred = append(preferred, cand)
		}
	}
	// 优先选不在「最近聊过」里的 bot；都在的话退而求其次。
	pool := preferred
	if len(pool) == 0 {
		pool = available
	}
	if len(pool) == 0 {
		return nil, nil
	}
	pick := pool[e.pacer.randIntn(len(pool))]
	ident := pick.entry.ident
	return &match.BotCandidate{Identity: &ident, Profile: pick.profile}, nil
}

// profileOf 把 bot 的身份资料转成匹配用资料快照（bot 自身没有筛选偏好，VIP=false）。
func profileOf(ident store.Identity) match.PrefProfile {
	return match.PrefProfile{
		Gender:         ident.Gender,
		Age:            ident.Age,
		Province:       ident.Province,
		ProvincePublic: ident.ProvincePublic,
		Tags:           ident.Tags,
		CleanMode:      ident.CleanMode,
		MatchGender:    "any",
		MatchSameProv:  false,
		VIP:            false,
	}
}

// ---- 防抖调度 ----

// schedule 安排（或重置）某个会话的回复计时器。
func (e *Engine) schedule(convID pgtype.UUID, bot store.Bot) {
	now := e.now()
	minDelay := msDuration(bot.ReplyDelayMinMs)
	maxDelay := msDuration(bot.ReplyDelayMaxMs)

	e.mu.Lock()
	st := e.convs[convID]
	if st == nil {
		st = &convState{}
		e.convs[convID] = st
	}
	st.bot = bot
	if st.firstPendingAt.IsZero() {
		st.firstPendingAt = now
	}
	if st.running {
		// 生成回复期间又来了新消息：本轮结束后重新调度。
		st.dirty = true
		e.mu.Unlock()
		return
	}
	wait := e.pacer.readDelay(minDelay, maxDelay)
	// 从第一条待处理消息算起，最长等待 2 × reply_delay_max_ms。
	if deadline := st.firstPendingAt.Add(2 * maxDelay); now.Add(wait).After(deadline) {
		wait = deadline.Sub(now)
	}
	if wait < 0 {
		wait = 0
	}
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(wait, func() {
		e.startTurn(convID, bot.IdentityID, false)
	})
	e.mu.Unlock()
}

// rescheduleAt 把计时器改到指定时刻（用于对话最小间隔）。
func (e *Engine) rescheduleAt(convID pgtype.UUID, at time.Time, botIdentityID pgtype.UUID) {
	wait := at.Sub(e.now())
	if wait < 0 {
		wait = 0
	}
	e.mu.Lock()
	st := e.convs[convID]
	if st == nil {
		st = &convState{}
		e.convs[convID] = st
	}
	if st.timer != nil {
		st.timer.Stop()
	}
	st.timer = time.AfterFunc(wait, func() {
		e.startTurn(convID, botIdentityID, false)
	})
	e.mu.Unlock()
}

// startTurn 切换为"处理中"，随后执行一轮回复。
func (e *Engine) startTurn(convID, botIdentityID pgtype.UUID, opening bool) {
	e.mu.Lock()
	st := e.convs[convID]
	if st == nil {
		st = &convState{}
		e.convs[convID] = st
	}
	st.running = true
	st.firstPendingAt = time.Time{}
	if st.timer != nil {
		st.timer.Stop()
		st.timer = nil
	}
	st.bot.IdentityID = botIdentityID
	e.mu.Unlock()

	e.wg.Add(1)
	defer e.wg.Done()
	e.runTurn(convID, opening)
	e.finishTurn(convID)
}

// finishTurn 结束"处理中"状态；期间有新消息时重新调度。
func (e *Engine) finishTurn(convID pgtype.UUID) {
	e.mu.Lock()
	st := e.convs[convID]
	if st == nil {
		e.mu.Unlock()
		return
	}
	st.running = false
	dirty := st.dirty
	st.dirty = false
	bot := st.bot
	if dirty {
		st.firstPendingAt = e.now()
	}
	e.mu.Unlock()

	if dirty {
		e.schedule(convID, bot)
	}
}

// setConvBot 记住该会话最新的 bot 设置（dirty 重排时用它的节奏参数）。
func (e *Engine) setConvBot(convID pgtype.UUID, bot store.Bot) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st := e.convs[convID]; st != nil {
		st.bot = bot
	}
}

// forgetConv 删除会话状态并停止计时器。
func (e *Engine) forgetConv(convID pgtype.UUID) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if st := e.convs[convID]; st != nil {
		if st.timer != nil {
			st.timer.Stop()
		}
		delete(e.convs, convID)
	}
}

// ---- 一轮回复 ----

// runTurn 执行一轮：限额 → 组装提示词 → 调用模型 → 逐条发送 → 记录用量。
func (e *Engine) runTurn(convID pgtype.UUID, opening bool) {
	ctx, cancel := context.WithTimeout(e.baseCtx(), e.turnTimeout)
	defer cancel()

	conv, err := e.q.GetConversation(ctx, convID)
	if err != nil || conv.EndedAt.Valid {
		e.forgetConv(convID)
		return
	}
	bot, err := e.q.GetConversationBot(ctx, convID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			e.forgetConv(convID)
		}
		return
	}
	// bot 被停用时与“触发限额”同样处理：匹配会话以 peer_left 结束，漂流瓶会话保持沉默。
	// 开场同样受此限制（这里位于开场分支之前）。
	if !bot.Enabled {
		e.logger.Info("bot 已停用，停止回复", "bot_id", bot.ID.String())
		e.stopForBot(ctx, convID, conv)
		return
	}
	if s := e.Settings(); !s.Ready {
		e.logger.Info("bot 全局配置不可用，停止回复", "reason", s.NotReadyReason)
		e.stopForBot(ctx, convID, conv)
		return
	}
	// 开场只在会话里还没有任何消息时执行。
	if opening {
		if n, cerr := e.q.CountConversationMessages(ctx, convID); cerr != nil || n > 0 {
			return
		}
	}
	member, err := e.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: bot.IdentityID})
	if err != nil || member.LeftAt.Valid {
		return
	}
	ident, err := e.q.GetIdentityByID(ctx, bot.IdentityID)
	if err != nil {
		return
	}
	turn := &turnContext{bot: bot, ident: ident, conv: conv, member: member}
	e.setConvBot(convID, bot)

	if !opening {
		if done := e.checkLimits(ctx, turn, convID); done {
			return
		}
	}

	card, err := e.card(ctx, bot)
	if err != nil {
		return
	}

	// 开场：角色卡有开场白时直接发送，不调用模型（token 记 0）。
	if opening && card != nil && strings.TrimSpace(card.FirstMessage) != "" {
		replied := e.sendBubble(ctx, turn, Bubble{Kind: "text", Text: strings.TrimSpace(card.FirstMessage)})
		e.recordUsage(ctx, bot, convID, ChatResult{}, replied)
		return
	}

	in, err := e.buildPromptInput(ctx, turn, card, opening)
	if err != nil {
		e.logger.Warn("组装提示词失败", "bot_id", bot.ID.String(), "error", err)
		return
	}
	res, err := e.chat(ctx, bot, in)
	if err != nil {
		// 调用失败：本轮不回复（是否记录用量由调用方结果决定）。
		e.logger.Warn("bot 调用模型失败", "bot_id", bot.ID.String(), "error", err)
		return
	}
	warnEmptyOutput(e.logger, bot, res)
	replied := false
	for _, bubble := range ParseReply(res.Content) {
		if e.sendBubble(ctx, turn, bubble) {
			replied = true
		}
	}
	e.recordUsage(ctx, bot, convID, res, replied)
}

// checkLimits 判断限额；返回 true 表示本轮不再回复。
func (e *Engine) checkLimits(ctx context.Context, turn *turnContext, convID pgtype.UUID) bool {
	usage, limits := e.loadUsage(ctx, turn.bot, convID)
	// 对话最小间隔：未到时间就改为在 lastReplyAt + interval 触发。
	if limits.ConvMinInterval > 0 && usage.HasLastReply && usage.SinceLastReply < limits.ConvMinInterval {
		at := e.now().Add(limits.ConvMinInterval - usage.SinceLastReply)
		e.rescheduleAt(convID, at, turn.bot.IdentityID)
		return true
	}
	if exceeded, reason := Check(limits, usage); exceeded {
		e.logger.Info("bot 触发限额", "bot_id", turn.bot.ID.String(), "reason", reason)
		e.stopForBot(ctx, convID, turn.conv)
		return true
	}
	return false
}

// stopForBot 在 bot 无法继续回复（停用或触发限额）时收尾：
// 匹配会话以 peer_left 结束，漂流瓶会话保持沉默；同时清理会话状态。
func (e *Engine) stopForBot(ctx context.Context, convID pgtype.UUID, conv store.Conversation) {
	if conv.Kind == store.ConversationKindMatch {
		if err := e.conv.End(ctx, convID, "peer_left"); err != nil {
			e.logger.Warn("结束匹配会话失败", "conversation_id", convID.String(), "error", err)
		}
	}
	e.forgetConv(convID)
}

// loadUsage 读取限额判定所需的用量快照。
func (e *Engine) loadUsage(ctx context.Context, bot store.Bot, convID pgtype.UUID) (Usage, Limits) {
	usage := e.botUsage(ctx, bot)
	if cu, err := e.q.GetConversationBotUsage(ctx, convID); err == nil {
		usage.ConvReplies = cu.Replies
		if cu.LastReplyAt.Valid {
			usage.HasLastReply = true
			usage.SinceLastReply = e.now().Sub(cu.LastReplyAt.Time)
		}
	} else {
		e.logger.Warn("读取会话用量失败", "conversation_id", convID.String(), "error", err)
	}
	return usage, LimitsFromBot(bot)
}

// turnContext 是一轮回复里用到的数据。
type turnContext struct {
	bot    store.Bot
	ident  store.Identity
	conv   store.Conversation
	member store.ConversationMember
}

// card 读取 bot 绑定的角色卡（未绑定返回 nil）。
func (e *Engine) card(ctx context.Context, bot store.Bot) (*store.CharacterCard, error) {
	if !bot.CardID.Valid {
		return nil, nil
	}
	card, err := e.q.GetCharacterCard(ctx, bot.CardID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &card, nil
}

// buildPromptInput 组装提示词输入（bot 级内容 + 对话级内容 + 历史）。
func (e *Engine) buildPromptInput(ctx context.Context, turn *turnContext, card *store.CharacterCard, opening bool) (PromptInput, error) {
	in := PromptInput{
		MediaTags: e.mediaTags(ctx, turn.bot),
		Kind:      turn.conv.Kind,
		Now:       e.now(),
		Loc:       e.Settings().Location,
		// 人设注入方式来自全局设置（部分网关会丢弃 system 消息）。
		PersonaMode: e.Settings().PersonaMode,
	}
	if card != nil {
		in.Card = CardFromStore(*card)
	}
	if opening {
		in.Instruction = InstructionOpening
	} else {
		in.Instruction = InstructionReply
	}

	// 对方的公开资料（只带公开的字段）。
	others, err := e.q.ListOtherMemberIdentities(ctx, store.ListOtherMemberIdentitiesParams{
		ConversationID: turn.conv.ID,
		IdentityID:     turn.bot.IdentityID,
	})
	if err == nil && len(others) > 0 {
		if peer, perr := e.q.GetIdentityByID(ctx, others[0]); perr == nil {
			in.Peer = PromptPeer{Gender: peer.Gender, Age: peer.Age, Tags: peer.Tags}
			if peer.ProvincePublic && peer.Province != nil {
				in.Peer.Province = *peer.Province
			}
		}
	}
	// 漂流瓶会话带上瓶子原文。
	if turn.conv.Kind == store.ConversationKindBottle {
		if content, cerr := e.q.GetBottleContentByConversation(ctx, turn.conv.ID); cerr == nil {
			in.BottleText = content
		}
	}

	history, err := e.history(ctx, turn)
	if err != nil {
		return PromptInput{}, err
	}
	in.History = history
	return in, nil
}

// mediaTags 读取 bot 媒体文件夹里去重排序后的标签（缓存优先）。
func (e *Engine) mediaTags(ctx context.Context, bot store.Bot) []string {
	if !bot.MediaFolderID.Valid {
		return nil
	}
	if entry := e.botByIdentity(bot.IdentityID); entry != nil && entry.bot.MediaFolderID == bot.MediaFolderID {
		return entry.tags
	}
	tags, err := e.q.ListFolderTags(ctx, bot.MediaFolderID)
	if err != nil {
		return nil
	}
	return tags
}

// history 读取最近的历史消息并转成提示词格式。
func (e *Engine) history(ctx context.Context, turn *turnContext) ([]PromptHistory, error) {
	rows, err := e.q.ListMessagesDesc(ctx, store.ListMessagesDescParams{
		ConversationID:      turn.conv.ID,
		RequesterIdentityID: turn.bot.IdentityID,
		Lim:                 historyQueryLimit,
	})
	if err != nil {
		return nil, err
	}
	// DESC 结果反转为时间升序。
	out := make([]PromptHistory, 0, len(rows))
	mediaTags := make(map[pgtype.UUID][]string)
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		h := PromptHistory{
			FromBot: row.SenderMemberID == turn.member.MemberID,
			Kind:    row.Kind,
			Text:    row.Body,
		}
		if row.MediaID.Valid {
			tags, ok := mediaTags[row.MediaID]
			if !ok {
				if item, ierr := e.q.GetMediaItem(ctx, row.MediaID); ierr == nil {
					tags = item.Tags
				}
				mediaTags[row.MediaID] = tags
			}
			h.MediaTags = tags
		}
		out = append(out, h)
	}
	return out, nil
}

// chat 调用模型；模型为空时用默认模型。
func (e *Engine) chat(ctx context.Context, bot store.Bot, in PromptInput) (ChatResult, error) {
	model := strings.TrimSpace(bot.Model)
	if model == "" {
		model = e.Settings().Model
	}
	client, timeout := e.llm()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return client.Chat(cctx, ChatRequest{
		Model:       model,
		Messages:    BuildMessages(in),
		MaxTokens:   int(bot.MaxOutputTokens),
		Temperature: float64(bot.Temperature),
		CacheKey:    CacheKey(bot.ID),
	})
}

// sendBubble 发送一条气泡：广播 typing → 等待打字时长 → 再次确认会话未结束 → 发送。
func (e *Engine) sendBubble(ctx context.Context, turn *turnContext, bubble Bubble) bool {
	kind := bubble.Kind
	text := strings.TrimSpace(bubble.Text)
	var mediaID pgtype.UUID

	switch kind {
	case "image", "video":
		item, ok := e.pickMedia(ctx, turn, bubble.Tags)
		if !ok {
			return false // 没有可用媒体：静默跳过这一条
		}
		mediaID = item.ID
		kind = item.Kind
	default:
		if text == "" {
			return false
		}
	}

	e.conv.BroadcastTyping(ctx, turn.conv.ID, turn.bot.IdentityID, turn.member.MemberID)
	if delay := typingDelay(utf8.RuneCountInString(text), int(turn.bot.TypingMsPerChar)); delay > 0 {
		if err := e.sleep(ctx, delay); err != nil {
			return false
		}
	}
	// 发送前再确认一次：全局配置仍可用、会话没有结束（关闭后不再发送）。
	if !e.ready() {
		return false
	}
	conv, err := e.q.GetConversation(ctx, turn.conv.ID)
	if err != nil || conv.EndedAt.Valid {
		return false
	}
	clientMsgID, err := genUUID()
	if err != nil {
		return false
	}
	_, _, err = e.conv.SendAs(ctx, conversation.SendInput{
		Identity:       &turn.ident,
		ConversationID: turn.conv.ID,
		ClientMsgID:    clientMsgID,
		Kind:           kind,
		Body:           text,
		MediaID:        mediaID,
		SkipRateLimit:  true,
	})
	if err != nil {
		var se *conversation.SendError
		if errors.As(err, &se) {
			e.logger.Info("bot 发送被拒绝", "bot_id", turn.bot.ID.String(), "code", se.Code)
		} else {
			e.logger.Warn("bot 发送失败", "bot_id", turn.bot.ID.String(), "error", err)
		}
		return false
	}
	return true
}

// pickMedia 为媒体气泡挑一个媒体：遵守 conv_media_limit，并排除本对话已发过的媒体。
func (e *Engine) pickMedia(ctx context.Context, turn *turnContext, tags []string) (store.MediaItem, bool) {
	if !turn.bot.MediaFolderID.Valid || len(tags) == 0 {
		return store.MediaItem{}, false
	}
	sent, err := e.q.ListConversationSentMediaIDs(ctx, turn.conv.ID)
	if err != nil {
		return store.MediaItem{}, false
	}
	if turn.bot.ConvMediaLimit >= 0 && len(sent) >= int(turn.bot.ConvMediaLimit) {
		return store.MediaItem{}, false
	}
	item, err := e.q.PickMediaForBot(ctx, store.PickMediaForBotParams{
		FolderID:       turn.bot.MediaFolderID,
		Tags:           tags,
		ConversationID: turn.conv.ID,
	})
	if err != nil {
		return store.MediaItem{}, false
	}
	return item, true
}

// recordUsage 记录一次调用的用量，并在同一事务里累加累计用量。
func (e *Engine) recordUsage(ctx context.Context, bot store.Bot, convID pgtype.UUID, res ChatResult, replied bool) {
	replies := int32(0)
	if replied {
		replies = 1
	}
	tokens := int64(res.PromptTokens + res.CompletionTokens)

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		e.logger.Warn("记录 bot 用量失败", "bot_id", bot.ID.String(), "error", err)
		return
	}
	defer tx.Rollback(ctx)
	q := e.q.WithTx(tx)
	if _, err := q.InsertBotUsage(ctx, store.InsertBotUsageParams{
		BotID:            bot.ID,
		ConversationID:   convID,
		PromptTokens:     int32(res.PromptTokens),
		CompletionTokens: int32(res.CompletionTokens),
		CachedTokens:     int32(res.CachedTokens),
		Replies:          replies,
	}); err != nil {
		e.logger.Warn("写入 bot 用量失败", "bot_id", bot.ID.String(), "error", err)
		return
	}
	if tokens > 0 || replies > 0 {
		if err := q.AddBotUsed(ctx, store.AddBotUsedParams{
			ID:          bot.ID,
			UsedTokens:  tokens,
			UsedReplies: int64(replies),
		}); err != nil {
			e.logger.Warn("累加 bot 用量失败", "bot_id", bot.ID.String(), "error", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		e.logger.Warn("提交 bot 用量失败", "bot_id", bot.ID.String(), "error", err)
	}
}

// msDuration 把毫秒数转成时长（负数按 0 处理）。
func msDuration(ms int32) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

// genUUID 生成一个随机 UUID（消息的 clientMsgID）。
func genUUID() (pgtype.UUID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: b, Valid: true}, nil
}

// warnEmptyOutput 在模型正文为空时记录原因：思考型模型可能把输出额度全部用在推理上
// （finish_reason=length），此时应在后台调大该 bot 的"输出 token 上限"。不记录任何正文。
func warnEmptyOutput(logger *slog.Logger, bot store.Bot, res ChatResult) {
	if strings.TrimSpace(res.Content) != "" {
		return
	}
	if res.FinishReason == "length" {
		logger.Warn("bot 模型输出为空：输出 token 上限被用完（思考型模型请调大输出上限）",
			"bot_id", bot.ID.String(), "max_output_tokens", bot.MaxOutputTokens,
			"completion_tokens", res.CompletionTokens)
		return
	}
	logger.Info("bot 模型输出为空", "bot_id", bot.ID.String(), "finish_reason", res.FinishReason)
}
