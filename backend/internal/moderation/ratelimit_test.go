package moderation

import (
	"context"
	"testing"
	"time"
)

func TestFixedWindow_Limit(t *testing.T) {
	fw := NewFixedWindow(3, time.Minute)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if !fw.Allow(ctx, "ip1") {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if fw.Allow(ctx, "ip1") {
		t.Fatal("第 4 次应被拒绝")
	}
	// 不同 key 互不影响。
	if !fw.Allow(ctx, "ip2") {
		t.Fatal("不同 key 应独立计数")
	}
}

func TestFixedWindow_WindowReset(t *testing.T) {
	fw := NewFixedWindow(1, time.Minute)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fw.now = func() time.Time { return base }
	ctx := context.Background()

	if !fw.Allow(ctx, "k") {
		t.Fatal("窗口内首次应放行")
	}
	if fw.Allow(ctx, "k") {
		t.Fatal("同窗口内第二次应拒绝")
	}
	// 时间越过一个窗口后应重新放行。
	fw.now = func() time.Time { return base.Add(time.Minute) }
	if !fw.Allow(ctx, "k") {
		t.Fatal("新窗口应重新放行")
	}
}

func TestFixedWindow_ZeroLimitDeniesAll(t *testing.T) {
	fw := NewFixedWindow(0, time.Minute)
	if fw.Allow(context.Background(), "k") {
		t.Fatal("limit=0 时应全部拒绝")
	}
}

func TestFuncLimiter(t *testing.T) {
	var calls []string
	fl := NewFuncLimiter(func(_ context.Context, key string) bool {
		calls = append(calls, key)
		return key != "deny"
	})
	if !fl.Allow(context.Background(), "ok") {
		t.Error("应放行 ok")
	}
	if fl.Allow(context.Background(), "deny") {
		t.Error("应拒绝 deny")
	}
	if len(calls) != 2 {
		t.Errorf("回调调用次数 = %d, 期望 2", len(calls))
	}
}

func TestAllowAll(t *testing.T) {
	if !AllowAll().Allow(context.Background(), "anything") {
		t.Error("AllowAll 应始终放行")
	}
}

func TestFixedWindow_CleanupIdleKeys(t *testing.T) {
	fw := NewFixedWindow(5, time.Minute)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fw.now = func() time.Time { return base }
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		fw.Allow(ctx, "k"+string(rune('0'+i)))
	}
	if len(fw.buckets) != 10 {
		t.Fatalf("桶数 = %d, 期望 10", len(fw.buckets))
	}
	// 越过窗口后触发清理，桶应被回收。
	fw.now = func() time.Time { return base.Add(2 * time.Minute) }
	fw.calls = cleanEvery - 1
	fw.Allow(ctx, "trigger")
	if len(fw.buckets) > 1 {
		t.Errorf("过期 key 应被回收, 剩余 %d（仅当前 trigger）", len(fw.buckets))
	}
}

func TestTokenBucket_BurstAndRefill(t *testing.T) {
	tb := NewTokenBucket(5, 3) // 5/s，突发 3
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tb.now = func() time.Time { return base }
	ctx := context.Background()

	// 突发 3 次放行，第 4 次拒绝。
	for i := 0; i < 3; i++ {
		if !tb.Allow(ctx, "k") {
			t.Fatalf("第 %d 次突发应放行", i+1)
		}
	}
	if tb.Allow(ctx, "k") {
		t.Fatal("超过突发容量应拒绝")
	}
	// 200ms 后补充 1 个令牌（5/s * 0.2s = 1）。
	base = base.Add(200 * time.Millisecond)
	if !tb.Allow(ctx, "k") {
		t.Fatal("补充令牌后应放行一次")
	}
	if tb.Allow(ctx, "k") {
		t.Fatal("令牌已用尽应拒绝")
	}
}

func TestTokenBucket_KeyIsolation(t *testing.T) {
	tb := NewTokenBucket(1, 1)
	tb.now = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	ctx := context.Background()
	if !tb.Allow(ctx, "a") {
		t.Fatal("a 首次应放行")
	}
	if tb.Allow(ctx, "a") {
		t.Fatal("a 第二次应拒绝")
	}
	if !tb.Allow(ctx, "b") {
		t.Fatal("不同 key 应独立计数")
	}
}

func TestTokenBucket_CleanupIdleKeys(t *testing.T) {
	tb := NewTokenBucket(1, 1)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	tb.now = func() time.Time { return base }
	tb.idleTTL = time.Minute
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		tb.Allow(ctx, "k"+string(rune('0'+i)))
	}
	if len(tb.buckets) != 5 {
		t.Fatalf("桶数 = %d, 期望 5", len(tb.buckets))
	}
	tb.now = func() time.Time { return base.Add(2 * time.Minute) }
	tb.calls = cleanEvery - 1
	tb.Allow(ctx, "trigger")
	if len(tb.buckets) > 1 {
		t.Errorf("空闲 key 应被回收, 剩余 %d", len(tb.buckets))
	}
}

func TestDefaultLimits(t *testing.T) {
	l := DefaultLimits()
	if l.IdentityCreate.Limit != 5 || l.IdentityCreate.Window != 24*time.Hour {
		t.Errorf("IdentityCreate = %+v", l.IdentityCreate)
	}
	if l.Message.Rate != 5 || l.Message.Burst != 10 {
		t.Errorf("Message = %+v", l.Message)
	}
	if l.ReportDaily.Limit != 20 {
		t.Errorf("ReportDaily = %+v", l.ReportDaily)
	}
}
