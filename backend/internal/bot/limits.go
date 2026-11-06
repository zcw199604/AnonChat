package bot

import (
	"time"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// 限额原因（供日志与测试判断）。
const (
	// ReasonTotalTokens 表示累计 token 达到上限。
	ReasonTotalTokens = "total_tokens"
	// ReasonTotalReplies 表示累计回复条数达到上限。
	ReasonTotalReplies = "total_replies"
	// ReasonWindowTokens 表示滑动窗口内 token 达到上限。
	ReasonWindowTokens = "window_tokens"
	// ReasonWindowReplies 表示滑动窗口内回复条数达到上限。
	ReasonWindowReplies = "window_replies"
	// ReasonConvReplies 表示本对话回复条数达到上限。
	ReasonConvReplies = "conv_replies"
	// ReasonConvInterval 表示距离上次回复不足最小间隔。
	ReasonConvInterval = "conv_interval"
)

// Limits 是某个 bot 的限额（0 表示不限）。
type Limits struct {
	// 总上限。
	TotalTokenLimit int64
	TotalReplyLimit int64
	// 滑动窗口上限。
	WindowTokenLimit int64
	WindowReplyLimit int64
	// 单对话上限：回复条数与两次回复的最小间隔。
	ConvReplyLimit  int32
	ConvMinInterval time.Duration
}

// LimitsFromBot 从 bot 设置构造限额。
func LimitsFromBot(bot store.Bot) Limits {
	return Limits{
		TotalTokenLimit:  bot.TotalTokenLimit,
		TotalReplyLimit:  bot.TotalReplyLimit,
		WindowTokenLimit: bot.WindowTokenLimit,
		WindowReplyLimit: int64(bot.WindowReplyLimit),
		ConvReplyLimit:   bot.ConvReplyLimit,
		ConvMinInterval:  time.Duration(bot.ConvMinIntervalSeconds) * time.Second,
	}
}

// Usage 是判定限额时的用量快照。
type Usage struct {
	// 累计用量（bots.used_tokens / used_replies）。
	UsedTokens  int64
	UsedReplies int64
	// 滑动窗口内的用量。
	WindowTokens  int64
	WindowReplies int64
	// 本对话的用量。
	ConvReplies int64
	// HasLastReply 为 true 时 SinceLastReply 表示距上次回复的时长。
	HasLastReply   bool
	SinceLastReply time.Duration
}

// Check 判断是否触发限额，返回原因（未触发时 reason 为空）。
func Check(l Limits, u Usage) (bool, string) {
	if l.TotalTokenLimit > 0 && u.UsedTokens >= l.TotalTokenLimit {
		return true, ReasonTotalTokens
	}
	if l.TotalReplyLimit > 0 && u.UsedReplies >= l.TotalReplyLimit {
		return true, ReasonTotalReplies
	}
	if l.WindowTokenLimit > 0 && u.WindowTokens >= l.WindowTokenLimit {
		return true, ReasonWindowTokens
	}
	if l.WindowReplyLimit > 0 && u.WindowReplies >= int64(l.WindowReplyLimit) {
		return true, ReasonWindowReplies
	}
	if l.ConvReplyLimit > 0 && u.ConvReplies >= int64(l.ConvReplyLimit) {
		return true, ReasonConvReplies
	}
	if l.ConvMinInterval > 0 && u.HasLastReply && u.SinceLastReply < l.ConvMinInterval {
		return true, ReasonConvInterval
	}
	return false, ""
}
