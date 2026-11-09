// Package moderation 提供内容安全与频率限制的基础设施。
package moderation

import (
	"context"
	"math"
	"sync"
	"time"
)

// RateLimiter 表示一个频率限制器。语义按 how.md：
// Allow 返回 true 表示本次请求被放行并消耗一次配额；返回 false 表示已超限。
// 具体限制值与窗口由构造限制器时配置，key 用于区分不同对象（如 IP、手机号、身份）。
type RateLimiter interface {
	Allow(ctx context.Context, key string) bool
}

// cleanEvery 是触发空闲 key 清理的调用间隔。
const cleanEvery = 1024

// ---- 固定窗口 ----

// bucket 是某个 key 在当前固定窗口内的计数桶。
type bucket struct {
	windowStart time.Time
	count       int
}

// FixedWindow 是进程内的固定窗口限流器。
type FixedWindow struct {
	limit  int
	window time.Duration
	now    func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
	calls   int
}

// NewFixedWindow 创建一个固定窗口限流器：每个窗口 window 内最多放行 limit 次。
func NewFixedWindow(limit int, window time.Duration) *FixedWindow {
	return &FixedWindow{
		limit:   limit,
		window:  window,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
}

// Allow 实现 RateLimiter。
func (f *FixedWindow) Allow(_ context.Context, key string) bool {
	if f.limit <= 0 {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	now := f.now()
	f.calls++
	if f.calls%cleanEvery == 0 {
		f.purgeLocked(now)
	}

	b, ok := f.buckets[key]
	if !ok || now.Sub(b.windowStart) >= f.window {
		f.buckets[key] = &bucket{windowStart: now, count: 1}
		return true
	}
	if b.count >= f.limit {
		return false
	}
	b.count++
	return true
}

// purgeLocked 回收已过期（空闲超过一个窗口）的 key，避免内存无限增长。
func (f *FixedWindow) purgeLocked(now time.Time) {
	for k, b := range f.buckets {
		if now.Sub(b.windowStart) >= f.window {
			delete(f.buckets, k)
		}
	}
}

// ---- 令牌桶 ----

type tbBucket struct {
	tokens float64
	last   time.Time
}

// TokenBucket 是进程内的令牌桶限流器：每秒补充 rate 个令牌，桶容量 burst。
type TokenBucket struct {
	rate    float64
	burst   float64
	idleTTL time.Duration
	now     func() time.Time

	mu      sync.Mutex
	buckets map[string]*tbBucket
	calls   int
}

// NewTokenBucket 创建令牌桶：rate 为每秒补充速率，burst 为突发容量。
func NewTokenBucket(rate float64, burst int) *TokenBucket {
	return &TokenBucket{
		rate:    rate,
		burst:   float64(burst),
		idleTTL: 10 * time.Minute,
		now:     time.Now,
		buckets: make(map[string]*tbBucket),
	}
}

// Allow 实现 RateLimiter。
func (t *TokenBucket) Allow(_ context.Context, key string) bool {
	if t.rate <= 0 || t.burst <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	now := t.now()
	t.calls++
	if t.calls%cleanEvery == 0 {
		t.purgeLocked(now)
	}

	b := t.buckets[key]
	if b == nil {
		b = &tbBucket{tokens: t.burst, last: now}
		t.buckets[key] = b
	} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(t.burst, b.tokens+elapsed*t.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// purgeLocked 回收空闲超过 idleTTL 的 key。
func (t *TokenBucket) purgeLocked(now time.Time) {
	for k, b := range t.buckets {
		if now.Sub(b.last) >= t.idleTTL {
			delete(t.buckets, k)
		}
	}
}

// ---- 测试桩 ----

// FuncLimiter 是一个基于回调函数的限流器，主要用于测试注入。
type FuncLimiter struct {
	fn func(ctx context.Context, key string) bool
}

// NewFuncLimiter 用给定判定函数构造 FuncLimiter。
func NewFuncLimiter(fn func(ctx context.Context, key string) bool) *FuncLimiter {
	if fn == nil {
		fn = func(context.Context, string) bool { return true }
	}
	return &FuncLimiter{fn: fn}
}

// Allow 实现 RateLimiter。
func (l *FuncLimiter) Allow(ctx context.Context, key string) bool { return l.fn(ctx, key) }

type allowAll struct{}

// Allow 实现 RateLimiter，总是返回 true。
func (allowAll) Allow(context.Context, string) bool { return true }

// AllowAll 返回一个始终放行的限流器。
func AllowAll() RateLimiter { return allowAll{} }

// ---- 集中配置 ----

// WindowLimit 描述固定窗口限制。
type WindowLimit struct {
	Limit  int
	Window time.Duration
}

// New 构造对应的固定窗口限流器。
func (w WindowLimit) New() *FixedWindow { return NewFixedWindow(w.Limit, w.Window) }

// BucketLimit 描述令牌桶限制。
type BucketLimit struct {
	Rate  float64
	Burst int
}

// New 构造对应的令牌桶限流器。
func (b BucketLimit) New() *TokenBucket { return NewTokenBucket(b.Rate, b.Burst) }

// Limits 是 how.md「频率限制默认值」表的集中定义，含后续任务才会用到的限流点。
// 各调用点从此处引用，避免散落魔法数字。
type Limits struct {
	// 每个 IP 创建身份。
	IdentityCreate WindowLimit
	// 每个身份发消息（令牌桶）。
	Message BucketLimit
	// 房间内每条消息最小间隔。
	RoomMessageInterval time.Duration
	// 开始匹配。
	MatchStart WindowLimit
	// 投放 / 捞取漂流瓶。
	BottlePost WindowLimit
	BottlePick WindowLimit
	// 创建私密链接。
	PrivateLinkCreate WindowLimit
	// 私密链接密码错误。
	PrivateLinkPassword WindowLimit
	// 短信验证码：每手机号冷却、每手机号每天、每 IP 每天。
	SMSPerPhone      WindowLimit
	SMSPerPhoneDaily WindowLimit
	SMSPerIPDaily    WindowLimit
	// 管理员登录失败。
	AdminLogin WindowLimit
	// 举报（每身份每天）。
	ReportDaily WindowLimit
	// 随机昵称（每身份每分钟）。
	NicknameShuffle WindowLimit
	// 自定义昵称（每身份每天）。
	NicknameSet WindowLimit
	// 兑换激活码：每身份、每 IP。
	VIPRedeemPerID WindowLimit
	VIPRedeemPerIP WindowLimit
	// 提交广播（每身份每天）。
	BroadcastPost WindowLimit
	// 发起故事（每身份每天）。
	StoryCreate WindowLimit
	// 后台"测试 bot 模型连接"（每管理员每分钟，how.md §15.3）。
	BotSettingsTest WindowLimit
}

// DefaultLimits 返回 how.md 表中的默认限流值。
func DefaultLimits() Limits {
	return Limits{
		IdentityCreate:      WindowLimit{Limit: 5, Window: 24 * time.Hour},
		Message:             BucketLimit{Rate: 5, Burst: 10},
		RoomMessageInterval: time.Second,
		MatchStart:          WindowLimit{Limit: 15, Window: time.Minute},
		BottlePost:          WindowLimit{Limit: 10, Window: 24 * time.Hour},
		BottlePick:          WindowLimit{Limit: 50, Window: 24 * time.Hour},
		PrivateLinkCreate:   WindowLimit{Limit: 20, Window: 24 * time.Hour},
		PrivateLinkPassword: WindowLimit{Limit: 5, Window: 10 * time.Minute},
		SMSPerPhone:         WindowLimit{Limit: 1, Window: time.Minute},
		SMSPerPhoneDaily:    WindowLimit{Limit: 5, Window: 24 * time.Hour},
		SMSPerIPDaily:       WindowLimit{Limit: 20, Window: 24 * time.Hour},
		AdminLogin:          WindowLimit{Limit: 5, Window: 15 * time.Minute},
		ReportDaily:         WindowLimit{Limit: 20, Window: 24 * time.Hour},
		NicknameShuffle:     WindowLimit{Limit: 10, Window: time.Minute},
		NicknameSet:         WindowLimit{Limit: 10, Window: 24 * time.Hour},
		VIPRedeemPerID:      WindowLimit{Limit: 5, Window: 10 * time.Minute},
		VIPRedeemPerIP:      WindowLimit{Limit: 20, Window: 10 * time.Minute},
		BroadcastPost:       WindowLimit{Limit: 5, Window: 24 * time.Hour},
		StoryCreate:         WindowLimit{Limit: 10, Window: 24 * time.Hour},
		BotSettingsTest:     WindowLimit{Limit: 10, Window: time.Minute},
	}
}

// Scaled 返回把各限流上限乘以系数 m 的副本（用于 E2E 放宽限流；m<=1 时原样返回）。
// 房间发言间隔（时长）不随上限缩放。
func (l Limits) Scaled(m int) Limits {
	if m <= 1 {
		return l
	}
	sw := func(w WindowLimit) WindowLimit {
		return WindowLimit{Limit: w.Limit * m, Window: w.Window}
	}
	l.IdentityCreate = sw(l.IdentityCreate)
	l.Message = BucketLimit{Rate: l.Message.Rate * float64(m), Burst: l.Message.Burst * m}
	l.MatchStart = sw(l.MatchStart)
	l.BottlePost = sw(l.BottlePost)
	l.BottlePick = sw(l.BottlePick)
	l.PrivateLinkCreate = sw(l.PrivateLinkCreate)
	l.PrivateLinkPassword = sw(l.PrivateLinkPassword)
	l.SMSPerPhone = sw(l.SMSPerPhone)
	l.SMSPerPhoneDaily = sw(l.SMSPerPhoneDaily)
	l.SMSPerIPDaily = sw(l.SMSPerIPDaily)
	l.AdminLogin = sw(l.AdminLogin)
	l.ReportDaily = sw(l.ReportDaily)
	l.NicknameShuffle = sw(l.NicknameShuffle)
	l.NicknameSet = sw(l.NicknameSet)
	l.VIPRedeemPerID = sw(l.VIPRedeemPerID)
	l.VIPRedeemPerIP = sw(l.VIPRedeemPerIP)
	l.BroadcastPost = sw(l.BroadcastPost)
	l.StoryCreate = sw(l.StoryCreate)
	l.BotSettingsTest = sw(l.BotSettingsTest)
	return l
}
