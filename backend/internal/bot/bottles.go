package bot

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/bottle"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 漂流瓶循环的默认参数。
const (
	// defaultBottleLoop 是回瓶循环的扫描间隔。
	defaultBottleLoop = 30 * time.Second
)

// BottleService 是漂流瓶循环需要的操作（由 bottle.Service 实现）。
type BottleService interface {
	// HoldForBot 让 bot 捞一个没人回复过的旧瓶子；没有可用的返回 nil, nil。
	HoldForBot(ctx context.Context, botIdentityID pgtype.UUID, minAge time.Duration) (*bottle.PickedBottle, error)
	// Reply 回复瓶子并返回新建会话的 ID。
	Reply(ctx context.Context, identityID, bottleID pgtype.UUID, content string) (pgtype.UUID, error)
	// ThrowBack 把瓶子放回海里。
	ThrowBack(ctx context.Context, identityID, bottleID pgtype.UUID) error
}

// WithBottles 注入漂流瓶服务；未注入时不启动回瓶循环。
// 最短瓶龄与回瓶间隔来自生效配置（how.md §15.2）。
func WithBottles(svc BottleService) EngineOption {
	return func(e *Engine) {
		if svc != nil {
			e.bottles = svc
		}
	}
}

// WithBottleLoopInterval 设置回瓶循环的扫描间隔（测试用）。
func WithBottleLoopInterval(d time.Duration) EngineOption {
	return func(e *Engine) {
		if d > 0 {
			e.bottleLoop = d
		}
	}
}

// runBottleLoop 每 bottleLoop 执行一次回瓶尝试，直到 ctx 取消。
func (e *Engine) runBottleLoop(ctx context.Context) {
	ticker := time.NewTicker(e.bottleLoop)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			e.bottleTick(ctx)
		}
	}
}

// bottleTick 对每个可回瓶的 bot 尝试捞瓶子并回复（how.md §9）。
// 全局关闭或配置不可用时整轮跳过。
func (e *Engine) bottleTick(ctx context.Context) {
	if !e.ready() {
		return
	}
	for _, entry := range e.botEntries() {
		if ctx.Err() != nil {
			return
		}
		bot := entry.bot
		// 只处理启用、开启漂流瓶、且绑定了角色卡的 bot。
		if !bot.Enabled || !bot.ServeBottle || !bot.CardID.Valid {
			continue
		}
		if e.now().Before(e.nextBottleAt(bot.ID)) {
			continue
		}
		e.bottleForBot(ctx, entry)
	}
}

// bottleForBot 为一个 bot 执行一次"捞瓶子并回复"。
func (e *Engine) bottleForBot(ctx context.Context, entry *botEntry) {
	bot := entry.bot
	interval := time.Duration(bot.BottleIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	// 触发 bot 级限额后不再消耗：本次不捞瓶子。
	if exceeded, reason := Check(LimitsFromBot(bot), e.botUsage(ctx, bot)); exceeded {
		e.logger.Info("bot 触发限额，跳过漂流瓶", "bot_id", bot.ID.String(), "reason", reason)
		e.setNextBottleAt(bot.ID, e.now().Add(e.pacer.bottleNext(interval)))
		return
	}

	picked, err := e.bottles.HoldForBot(ctx, bot.IdentityID, e.Settings().BottleMinAge)
	if err != nil {
		e.logger.Warn("bot 捞取漂流瓶失败", "bot_id", bot.ID.String(), "error", err)
		e.setNextBottleAt(bot.ID, e.now().Add(e.pacer.bottleNext(interval)))
		return
	}
	if picked == nil {
		// 没有可捞的瓶子：以 interval/4 的频率再看一次。
		e.setNextBottleAt(bot.ID, e.now().Add(interval/4))
		return
	}

	text, calls, ok := e.bottleReply(ctx, entry, picked)
	if !ok {
		e.recordCalls(ctx, bot, pgtype.UUID{}, calls, false)
		e.throwBack(ctx, bot, picked.ID)
		e.setNextBottleAt(bot.ID, e.now().Add(e.pacer.bottleNext(interval)))
		return
	}
	convID, err := e.bottles.Reply(ctx, bot.IdentityID, picked.ID, text)
	if err != nil {
		e.recordCalls(ctx, bot, pgtype.UUID{}, calls, false)
		e.logger.Warn("bot 回复漂流瓶失败", "bot_id", bot.ID.String(), "error", err)
		e.throwBack(ctx, bot, picked.ID)
		e.setNextBottleAt(bot.ID, e.now().Add(e.pacer.bottleNext(interval)))
		return
	}
	// 用量记录带上新会话的 ID。
	e.recordCalls(ctx, bot, convID, calls, true)
	e.touchAfterReply(ctx, bot, picked.PosterIdentityID, nil)
	e.setNextBottleAt(bot.ID, e.now().Add(e.pacer.bottleNext(interval)))
}

// bottleReply 用模型生成一条回复（只取文本气泡，用换行拼起来）。
func (e *Engine) bottleReply(ctx context.Context, entry *botEntry, picked *bottle.PickedBottle) (string, []modelCall, bool) {
	card, err := e.card(ctx, entry.bot)
	if err != nil || card == nil {
		return "", nil, false
	}
	in := PromptInput{
		CoreMode:    entry.bot.CoreMode,
		Card:        CardFromStore(*card),
		MediaTags:   entry.tags,
		Kind:        store.ConversationKindBottle,
		BottleText:  picked.Content,
		Now:         e.now(),
		Loc:         e.Settings().Location,
		Instruction: InstructionBottle,
		PersonaMode: e.Settings().PersonaMode,
	}
	if in.CoreMode == "" {
		in.CoreMode = "single"
	}
	// 投瓶人的公开资料（与真人配对时使用的规则一致）。
	if picked.PosterIdentityID.Valid {
		in.Memory, in.Relation, err = e.loadPeerMemory(ctx, entry.bot.ID, picked.PosterIdentityID)
		if err != nil {
			e.logger.Warn("读取漂流瓶记忆失败", "bot_id", entry.bot.ID.String())
			return "", nil, false
		}
		if poster, perr := e.q.GetIdentityByID(ctx, picked.PosterIdentityID); perr == nil {
			in.Peer = PromptPeer{Gender: poster.Gender, Age: poster.Age, Tags: poster.Tags}
			if poster.ProvincePublic && poster.Province != nil {
				in.Peer.Province = *poster.Province
			}
		}
	}

	bubbles, calls, err := e.chatStyled(ctx, entry.bot, in)
	if err != nil {
		e.logger.Warn("bot 生成漂流瓶回复失败", "bot_id", entry.bot.ID.String())
		return "", calls, false
	}
	// 只取文本气泡。
	lines := make([]string, 0, maxBubbles)
	for _, b := range bubbles {
		if b.Kind == "text" && strings.TrimSpace(b.Text) != "" {
			lines = append(lines, b.Text)
		}
	}
	if len(lines) == 0 {
		return "", calls, false
	}
	return strings.Join(lines, "\n"), calls, true
}

// throwBack 把瓶子放回海里（失败只记录日志）。
func (e *Engine) throwBack(ctx context.Context, bot store.Bot, bottleID pgtype.UUID) {
	if err := e.bottles.ThrowBack(ctx, bot.IdentityID, bottleID); err != nil {
		e.logger.Warn("bot 放回漂流瓶失败", "bot_id", bot.ID.String(), "bottle_id", bottleID.String(), "error", err)
	}
}

// botEntries 返回当前缓存里的 bot 快照。
func (e *Engine) botEntries() []*botEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*botEntry, 0, len(e.bots))
	for _, entry := range e.bots {
		out = append(out, entry)
	}
	return out
}

// botUsage 读取某个 bot 的 bot 级用量（窗口 + 累计）。
func (e *Engine) botUsage(ctx context.Context, bot store.Bot) Usage {
	usage := Usage{UsedTokens: bot.UsedTokens, UsedReplies: bot.UsedReplies}
	cutoff := pgtype.Timestamptz{
		Time:  e.now().Add(-time.Duration(bot.WindowSeconds) * time.Second),
		Valid: true,
	}
	if win, err := e.q.SumBotUsageSince(ctx, store.SumBotUsageSinceParams{BotID: bot.ID, CreatedAt: cutoff}); err == nil {
		usage.WindowTokens = win.Tokens
		usage.WindowReplies = win.Replies
	} else {
		e.logger.Warn("读取 bot 窗口用量失败", "bot_id", bot.ID.String(), "error", err)
	}
	return usage
}

// nextBottleAt 返回该 bot 下一次可以回瓶的时间。
func (e *Engine) nextBottleAt(botID pgtype.UUID) time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.nextBottle[botID]
}

// setNextBottleAt 记录该 bot 下一次可以回瓶的时间。
func (e *Engine) setNextBottleAt(botID pgtype.UUID, at time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextBottle[botID] = at
}
