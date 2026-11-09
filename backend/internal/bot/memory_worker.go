package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// MemoryWorker 在一个 goroutine 中串行抽取记忆，通知只用于提前唤醒。
type MemoryWorker struct {
	engine *Engine
	wake   chan struct{}
}

// NewMemoryWorker 创建随引擎运行的记忆任务。
func NewMemoryWorker(e *Engine) *MemoryWorker {
	return &MemoryWorker{engine: e, wake: make(chan struct{}, 1)}
}

// Notify 非阻塞地请求提前抽取，重复通知合并为一轮。
func (w *MemoryWorker) Notify() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Run 每 60 秒抽取一轮，退出由引擎的 ctx 控制。
func (w *MemoryWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		if err := w.RunOnce(ctx); err != nil && ctx.Err() == nil {
			// 数据库错误和模型错误可能带内容，日志只报告状态。
			w.engine.logger.Warn("记忆抽取本轮未完成")
		}
	}
}

// RunOnce 处理一轮最多 20 个候选，未就绪时不读库或调用模型。
func (w *MemoryWorker) RunOnce(ctx context.Context) error {
	e := w.engine
	s := e.Settings()
	if !s.Ready {
		return nil
	}
	candidates, err := e.q.ListMemoryCandidates(ctx, store.ListMemoryCandidatesParams{
		AsOf: memoryTime(e.now()), ExtractEvery: int32(s.MemoryExtractEvery),
	})
	if err != nil {
		return errors.New("读取记忆候选失败")
	}
	var failures []error
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !e.ready() {
			break
		}
		cctx, cancel := context.WithTimeout(ctx, e.turnTimeout)
		err := w.extract(cctx, candidate)
		cancel()
		if err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (w *MemoryWorker) extract(ctx context.Context, candidate store.ListMemoryCandidatesRow) error {
	e := w.engine
	bot, err := e.q.GetBot(ctx, candidate.BotID)
	if err != nil {
		return errors.New("读取记忆 bot 失败")
	}
	if !bot.Enabled || !bot.MemoryEnabled {
		return nil
	}
	// 读取失败不能当作零用量，否则会绕过窗口限额。
	win, err := e.q.SumBotUsageSince(ctx, store.SumBotUsageSinceParams{
		BotID: bot.ID, CreatedAt: memoryTime(e.now().Add(-time.Duration(bot.WindowSeconds) * time.Second)),
	})
	if err != nil {
		return errors.New("读取记忆用量失败")
	}
	usage := Usage{UsedTokens: bot.UsedTokens, UsedReplies: bot.UsedReplies, WindowTokens: win.Tokens, WindowReplies: win.Replies}
	if exceeded, _ := Check(LimitsFromBot(bot), usage); exceeded {
		// 让出候选名额，保持提取位置和失败次数，避免超限会话长期挡住其他 bot。
		_, err := e.q.UpsertMemoryCursor(ctx, store.UpsertMemoryCursorParams{
			ConversationID: candidate.ConversationID, BotID: candidate.BotID,
			ExtractedUntil: candidate.ExtractedUntil, FailCount: candidate.FailCount,
			NextTryAt: memoryTime(e.now().Add(10 * time.Minute)),
		})
		if err != nil {
			return errors.New("推迟超限记忆抽取失败")
		}
		return nil
	}
	messages, err := e.q.ListMemoryMessages(ctx, store.ListMemoryMessagesParams{
		ConversationID: candidate.ConversationID, ExtractedUntil: candidate.ExtractedUntil,
	})
	if err != nil {
		return errors.New("读取记忆消息失败")
	}
	if len(messages) == 0 {
		return nil
	}
	current, err := e.q.GetPeerMemory(ctx, store.GetPeerMemoryParams{BotID: bot.ID, PeerIdentityID: candidate.PeerIdentityID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return errors.New("读取抽取画像失败")
	}
	name := ""
	if card, err := e.card(ctx, bot); err != nil {
		return errors.New("读取记忆角色卡失败")
	} else if card != nil {
		name = card.Name
	}
	profile := string(current.Profile)
	if profile == "" {
		profile = "{}"
	}
	var text strings.Builder
	fmt.Fprintf(&text, "当前画像：%s\n当前好感度：%d\n角色名：%s\n新对话记录：\n", profile, current.Affinity, name)
	for _, message := range messages {
		who := "对方"
		if message.SenderIdentityID == bot.IdentityID {
			who = "你（" + name + "）"
		}
		content := message.Body
		if message.Kind == "image" {
			content = "[图片:" + strings.Join(message.Tags, ",") + "]"
		} else if message.Kind == "video" {
			content = "[视频:" + strings.Join(message.Tags, ",") + "]"
		}
		fmt.Fprintf(&text, "%s：%s\n", who, content)
	}
	req := e.modelRequest(bot, "memory")
	req.Messages = []ChatMessage{{Role: "system", Content: memoryExtractPrompt}, {Role: "user", Content: text.String()}}
	client, timeout := e.llm()
	cctx, cancel := context.WithTimeout(ctx, timeout)
	res, callErr := client.Chat(cctx, req)
	cancel()
	last := messages[len(messages)-1].CreatedAt
	if err := ctx.Err(); err != nil {
		// 调用已消耗的 token 不能随外层取消丢失；失败退避与用量仍原子保存。
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), queryTimeout)
		defer cleanupCancel()
		return errors.Join(err, w.failed(cleanupCtx, candidate, last, res))
	}
	if callErr != nil {
		return w.failed(ctx, candidate, last, res)
	}
	extraction, err := ParseMemoryOutput(res.Content)
	if err != nil {
		return w.failed(ctx, candidate, last, res)
	}
	return w.save(ctx, candidate, last, res, extraction)
}

func (w *MemoryWorker) save(ctx context.Context, candidate store.ListMemoryCandidatesRow, last pgtype.Timestamptz, res ChatResult, extraction MemoryExtraction) error {
	e := w.engine
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return errors.New("开始记忆事务失败")
	}
	defer tx.Rollback(ctx)
	q := e.q.WithTx(tx)
	if err = q.EnsurePeerMemory(ctx, store.EnsurePeerMemoryParams{BotID: candidate.BotID, PeerIdentityID: candidate.PeerIdentityID, LastInteractionAt: last}); err != nil {
		return errors.New("创建记忆行失败")
	}
	// 模型调用期间可能有回复更新关系，落库前锁定并合并最新画像。
	current, err := q.LockPeerMemory(ctx, store.LockPeerMemoryParams{BotID: candidate.BotID, PeerIdentityID: candidate.PeerIdentityID})
	if err != nil {
		return errors.New("锁定记忆行失败")
	}
	var profile MemoryProfile
	if err := json.Unmarshal(current.Profile, &profile); err != nil {
		return errors.New("读取画像结构失败")
	}
	data, err := json.Marshal(mergeProfile(profile, extraction.Profile, e.now()))
	if err != nil {
		return errors.New("编码画像失败")
	}
	interactedAt := last
	if current.LastInteractionAt.Time.After(interactedAt.Time) {
		interactedAt = current.LastInteractionAt
	}
	_, err = q.UpsertPeerMemory(ctx, store.UpsertPeerMemoryParams{
		BotID: candidate.BotID, PeerIdentityID: candidate.PeerIdentityID,
		Profile: data, Affinity: int16(max(-100, min(100, int(current.Affinity)+max(-10, min(10, extraction.AffinityDelta))))),
		MoodLabel: current.MoodLabel, MoodValue: current.MoodValue, LastInteractionAt: interactedAt,
	})
	if err != nil {
		return errors.New("保存画像失败")
	}
	for _, event := range extraction.Events {
		tags := event.Tags
		if tags == nil {
			tags = []string{}
		}
		if _, err := q.InsertPeerEvent(ctx, store.InsertPeerEventParams{
			BotID: candidate.BotID, PeerIdentityID: candidate.PeerIdentityID, ConversationID: candidate.ConversationID,
			Gist: event.Gist, Tags: tags,
		}); err != nil {
			return errors.New("保存记忆事件失败")
		}
	}
	if _, err := q.TrimPeerEvents(ctx, store.TrimPeerEventsParams{BotID: candidate.BotID, PeerIdentityID: candidate.PeerIdentityID}); err != nil {
		return errors.New("裁剪记忆事件失败")
	}
	// 游标只推进到本批消息末尾，不能使用并发更新后的互动时间。
	_, err = q.UpsertMemoryCursor(ctx, store.UpsertMemoryCursorParams{
		ConversationID: candidate.ConversationID, BotID: candidate.BotID,
		ExtractedUntil: last,
	})
	if err != nil {
		return errors.New("保存记忆游标失败")
	}
	if err := memoryUsage(ctx, q, candidate, res); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("提交记忆事务失败")
	}
	return nil
}

// failed 把失败退避与失败调用的用量放在同一个事务中。
func (w *MemoryWorker) failed(ctx context.Context, candidate store.ListMemoryCandidatesRow, last pgtype.Timestamptz, res ChatResult) error {
	e := w.engine
	count := candidate.FailCount + 1
	until := candidate.ExtractedUntil
	next := memoryTime(e.now().Add(time.Duration(count) * 5 * time.Minute))
	skipped := count >= 3
	if skipped {
		until, count, next = last, 0, pgtype.Timestamptz{}
	}
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return errors.New("开始记忆退避事务失败")
	}
	defer tx.Rollback(ctx)
	q := e.q.WithTx(tx)
	if _, err := q.UpsertMemoryCursor(ctx, store.UpsertMemoryCursorParams{
		ConversationID: candidate.ConversationID, BotID: candidate.BotID,
		ExtractedUntil: until, FailCount: count, NextTryAt: next,
	}); err != nil {
		return errors.New("保存记忆退避失败")
	}
	if res.PromptTokens+res.CompletionTokens > 0 {
		if err := memoryUsage(ctx, q, candidate, res); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return errors.New("提交记忆退避失败")
	}
	if skipped {
		e.logger.Warn("记忆抽取连续失败，跳过本批消息", "bot_id", candidate.BotID.String(), "conversation_id", candidate.ConversationID.String())
	}
	return nil
}

func memoryUsage(ctx context.Context, q *store.Queries, candidate store.ListMemoryCandidatesRow, res ChatResult) error {
	if _, err := q.InsertBotUsage(ctx, store.InsertBotUsageParams{
		BotID: candidate.BotID, ConversationID: candidate.ConversationID, Kind: "memory", Replies: 0,
		PromptTokens: int32(res.PromptTokens), CompletionTokens: int32(res.CompletionTokens), CachedTokens: int32(res.CachedTokens),
	}); err != nil {
		return errors.New("记录记忆用量失败")
	}
	if err := q.AddBotUsed(ctx, store.AddBotUsedParams{ID: candidate.BotID, UsedTokens: int64(res.PromptTokens + res.CompletionTokens)}); err != nil {
		return errors.New("累加记忆用量失败")
	}
	return nil
}

func memoryTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// loadPeerMemory 读取回复所需的画像和最近八条事件，关闭记忆时不读画像。
func (e *Engine) loadPeerMemory(ctx context.Context, botID, peerID pgtype.UUID) (*PromptMemory, PromptRelation, error) {
	bot, err := e.q.GetBot(ctx, botID)
	if err != nil {
		return nil, PromptRelation{}, err
	}
	if !bot.MemoryEnabled {
		return nil, PromptRelation{}, nil
	}
	current, err := e.q.GetPeerMemory(ctx, store.GetPeerMemoryParams{BotID: botID, PeerIdentityID: peerID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, PromptRelation{Stage: affinityStage(0)}, nil
	}
	if err != nil {
		return nil, PromptRelation{}, err
	}
	memory := &PromptMemory{}
	if err := json.Unmarshal(current.Profile, &memory.Profile); err != nil {
		return nil, PromptRelation{}, errors.New("读取画像结构失败")
	}
	events, err := e.q.ListPeerEvents(ctx, store.ListPeerEventsParams{BotID: botID, PeerIdentityID: peerID, Limit: 8})
	if err != nil {
		return nil, PromptRelation{}, err
	}
	for _, event := range events {
		memory.Events = append(memory.Events, PromptMemoryEvent{Gist: event.Gist, CreatedAt: event.CreatedAt.Time})
	}
	return memory, PromptRelation{Stage: affinityStage(int(current.Affinity)), MoodLabel: current.MoodLabel, MoodValue: int(current.MoodValue)}, nil
}

// touchPeerMemory 更新成功回复后的互动状态，智核单轮好感变动最多五点。
func (e *Engine) touchPeerMemory(ctx context.Context, botID, peerID pgtype.UUID, delta int, moodLabel string, moodValue int) error {
	bot, err := e.q.GetBot(ctx, botID)
	if err != nil || !bot.MemoryEnabled {
		return err
	}
	if moodLabel == "" {
		moodValue = 5
	}
	return e.q.TouchPeerMemory(ctx, store.TouchPeerMemoryParams{
		BotID: botID, PeerIdentityID: peerID, Delta: int16(max(-5, min(5, delta))),
		MoodLabel: truncateRunes(moodLabel, 10), MoodValue: int16(max(0, min(10, moodValue))), InteractedAt: memoryTime(e.now()),
	})
}
