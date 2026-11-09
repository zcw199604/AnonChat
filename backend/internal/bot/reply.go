package bot

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// modelCall 保存每次调用的独立用量，发送结束后按类型落库。
type modelCall struct {
	result ChatResult
	kind   string
}

// chatStyled 对心核结果最多纠正一次，重试不会重复进入智核。
func (e *Engine) chatStyled(ctx context.Context, bot store.Bot, in PromptInput) ([]Bubble, []modelCall, error) {
	if !e.modelAllowed(ctx, bot.ID) {
		return nil, nil, errors.New("心核调用已达到限额或不可用")
	}
	res, err := e.chat(ctx, bot, in)
	calls := []modelCall{{result: res, kind: "reply"}}
	if err != nil {
		return nil, calls, errors.New("心核调用失败")
	}
	warnEmptyOutput(e.logger, bot, res)
	guard := e.Settings().OOCGuard
	bubbles := parseReply(res.Content, guard)
	rules := CheckStyle(bubbles)
	if !guard || len(rules) == 0 {
		return bubbles, calls, nil
	}
	if !e.modelAllowedPending(ctx, bot.ID, int64(res.PromptTokens+res.CompletionTokens)) {
		return Sanitize(bubbles), calls, nil
	}
	in.FixHint = styleFixHint(rules)
	retry, err := e.chat(ctx, bot, in)
	calls = append(calls, modelCall{result: retry, kind: "retry"})
	if err != nil {
		return Sanitize(bubbles), calls, nil
	}
	bubbles = parseReply(retry.Content, true)
	if len(CheckStyle(bubbles)) > 0 {
		bubbles = Sanitize(bubbles)
	}
	return bubbles, calls, nil
}

func (e *Engine) recordCalls(ctx context.Context, bot store.Bot, convID pgtype.UUID, calls []modelCall, replied bool) {
	for _, call := range calls {
		e.recordUsage(ctx, bot, convID, call.result, replied, call.kind)
	}
}

// hasPendingInput 检查本轮开始后是否收到新的对方输入。
func (e *Engine) hasPendingInput(convID pgtype.UUID) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.convs[convID] != nil && e.convs[convID].dirty
}

func (e *Engine) touchAfterReply(ctx context.Context, bot store.Bot, peerID pgtype.UUID, reason *ReasonOutput) {
	if !peerID.Valid {
		return
	}
	delta, mood, value := 0, "", 5
	if reason != nil {
		delta, mood, value = reason.AffinityDelta, reason.Mood.Label, reason.Mood.Value
	}
	if err := e.touchPeerMemory(ctx, bot.ID, peerID, delta, mood, value); err != nil {
		e.logger.Warn("更新 bot 互动记忆失败", "bot_id", bot.ID.String())
	}
}

// modelAllowed 在每次附加模型调用前重新核对实际用量。
func (e *Engine) modelAllowed(ctx context.Context, botID pgtype.UUID) bool {
	return e.modelAllowedPending(ctx, botID, 0)
}

// pendingTokens 包括本轮已返回、等待与发送结果一起落库的心核用量。
func (e *Engine) modelAllowedPending(ctx context.Context, botID pgtype.UUID, pendingTokens int64) bool {
	if !e.ready() {
		return false
	}
	bot, err := e.q.GetBot(ctx, botID)
	if err != nil || !bot.Enabled {
		return false
	}
	window, err := e.q.SumBotUsageSince(ctx, store.SumBotUsageSinceParams{
		BotID: botID, CreatedAt: memoryTime(e.now().Add(-time.Duration(bot.WindowSeconds) * time.Second)),
	})
	if err != nil {
		return false
	}
	usage := Usage{UsedTokens: bot.UsedTokens + pendingTokens, UsedReplies: bot.UsedReplies,
		WindowTokens: window.Tokens + pendingTokens, WindowReplies: window.Replies}
	exceeded, _ := Check(LimitsFromBot(bot), usage)
	return !exceeded
}
