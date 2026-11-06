package bot

import (
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// TestCheckBotLevelLimits 覆盖 bot 级限额：0（不限）、未超、刚好等于上限。
func TestCheckBotLevelLimits(t *testing.T) {
	cases := []struct {
		name   string
		limits Limits
		usage  Usage
		reason string
	}{
		{
			name:   "全部为 0 表示不限",
			limits: Limits{},
			usage:  Usage{UsedTokens: 1 << 40, UsedReplies: 1 << 20, WindowTokens: 1 << 40, WindowReplies: 1 << 20},
		},
		{
			name:   "总 token 未达上限",
			limits: Limits{TotalTokenLimit: 1000},
			usage:  Usage{UsedTokens: 999},
		},
		{
			name:   "总 token 刚好达到上限",
			limits: Limits{TotalTokenLimit: 1000},
			usage:  Usage{UsedTokens: 1000},
			reason: ReasonTotalTokens,
		},
		{
			name:   "总回复条数刚好达到上限",
			limits: Limits{TotalReplyLimit: 10},
			usage:  Usage{UsedReplies: 10},
			reason: ReasonTotalReplies,
		},
		{
			name:   "总回复条数已达上限",
			limits: Limits{TotalReplyLimit: 10},
			usage:  Usage{UsedReplies: 11},
			reason: ReasonTotalReplies,
		},
		{
			name:   "窗口 token 刚好达到上限",
			limits: Limits{WindowTokenLimit: 500},
			usage:  Usage{WindowTokens: 500},
			reason: ReasonWindowTokens,
		},
		{
			name:   "窗口回复条数刚好达到上限",
			limits: Limits{WindowReplyLimit: 5},
			usage:  Usage{WindowReplies: 5},
			reason: ReasonWindowReplies,
		},
		{
			name:   "窗口回复条数未达上限",
			limits: Limits{WindowReplyLimit: 5},
			usage:  Usage{WindowReplies: 4},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exceeded, reason := Check(c.limits, c.usage)
			if c.reason == "" {
				if exceeded {
					t.Fatalf("不应超限, reason=%q", reason)
				}
				return
			}
			if !exceeded || reason != c.reason {
				t.Fatalf("超限判定不符: exceeded=%v reason=%q, 期望 %q", exceeded, reason, c.reason)
			}
		})
	}
}

// TestCheckConversationLimits 覆盖对话级限额：回复条数与最小间隔。
func TestCheckConversationLimits(t *testing.T) {
	t.Run("对话回复条数不限", func(t *testing.T) {
		if exceeded, _ := Check(Limits{}, Usage{ConvReplies: 100}); exceeded {
			t.Fatal("0 表示不限")
		}
	})
	t.Run("对话回复条数刚好达到上限", func(t *testing.T) {
		exceeded, reason := Check(Limits{ConvReplyLimit: 3}, Usage{ConvReplies: 3})
		if !exceeded || reason != ReasonConvReplies {
			t.Fatalf("exceeded=%v reason=%q", exceeded, reason)
		}
	})
	t.Run("对话回复条数未达上限", func(t *testing.T) {
		if exceeded, _ := Check(Limits{ConvReplyLimit: 3}, Usage{ConvReplies: 2}); exceeded {
			t.Fatal("2 < 3 不应超限")
		}
	})
	t.Run("最小间隔未到", func(t *testing.T) {
		exceeded, reason := Check(
			Limits{ConvMinInterval: 10 * time.Second},
			Usage{HasLastReply: true, SinceLastReply: 3 * time.Second},
		)
		if !exceeded || reason != ReasonConvInterval {
			t.Fatalf("exceeded=%v reason=%q", exceeded, reason)
		}
	})
	t.Run("最小间隔已过", func(t *testing.T) {
		if exceeded, _ := Check(
			Limits{ConvMinInterval: 10 * time.Second},
			Usage{HasLastReply: true, SinceLastReply: 10 * time.Second},
		); exceeded {
			t.Fatal("刚好等于间隔不应超限")
		}
	})
	t.Run("没有上一条回复时不看间隔", func(t *testing.T) {
		if exceeded, _ := Check(Limits{ConvMinInterval: time.Hour}, Usage{}); exceeded {
			t.Fatal("没有历史回复不应触发间隔限制")
		}
	})
}

// TestLimitsFromBot 验证从 bot 设置构造限额。
func TestLimitsFromBot(t *testing.T) {
	bot := store.Bot{
		TotalTokenLimit:        100,
		TotalReplyLimit:        20,
		WindowTokenLimit:       50,
		WindowReplyLimit:       5,
		ConvReplyLimit:         3,
		ConvMinIntervalSeconds: 7,
	}
	l := LimitsFromBot(bot)
	if l.TotalTokenLimit != 100 || l.TotalReplyLimit != 20 || l.WindowTokenLimit != 50 ||
		l.WindowReplyLimit != 5 || l.ConvReplyLimit != 3 || l.ConvMinInterval != 7*time.Second {
		t.Fatalf("限额构造不符: %+v", l)
	}
}
