package match

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// fakeBotProvider 是可控的 BotProvider：可以阻塞、返回 nil、返回错误或返回候选。
type fakeBotProvider struct {
	mu      sync.Mutex
	calls   int
	cand    *BotCandidate
	err     error
	after   time.Duration // FallbackAfter 的返回值
	gate    chan struct{} // 非 nil 时 PickBot 会阻塞直到该 chan 被关闭
	matched chan pgtype.UUID
}

func newFakeBotProvider(cand *BotCandidate) *fakeBotProvider {
	return &fakeBotProvider{cand: cand, after: time.Second, matched: make(chan pgtype.UUID, 4)}
}

// FallbackAfter 实现 BotProvider：返回配置的兜底时间。
func (f *fakeBotProvider) FallbackAfter() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.after
}

// setAfter 修改兜底时间（模拟后台热切换）。
func (f *fakeBotProvider) setAfter(d time.Duration) {
	f.mu.Lock()
	f.after = d
	f.mu.Unlock()
}

func (f *fakeBotProvider) PickBot(_ context.Context, _ *store.Identity, _ IdentityContext) (*BotCandidate, error) {
	f.mu.Lock()
	f.calls++
	gate := f.gate
	cand, err := f.cand, f.err
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	if err != nil {
		return nil, err
	}
	return cand, nil
}

func (f *fakeBotProvider) BotMatched(conversationID, _ pgtype.UUID) {
	f.matched <- conversationID
}

func (f *fakeBotProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// setGate 设置阻塞闸门（nil 表示不阻塞）。
func (f *fakeBotProvider) setGate(gate chan struct{}) {
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
}

// fallbackSetup 启动一个开启兜底的匹配器，分别返回真人配对与 bot 配对的事件通道。
func fallbackSetup(t *testing.T, pf Prefetcher, provider BotProvider, tick time.Duration) (*memoryMatcher, chan pair, chan botPair, *fakeClock) {
	t.Helper()
	realOut := make(chan pair, 16)
	botOut := make(chan botPair, 16)
	clock := &fakeClock{}
	clock.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())

	m := NewMemoryMatcher(pf, func(a, b *queueEntry) error {
		realOut <- pair{a.ident.ID, b.ident.ID}
		return nil
	}, time.Hour, tick)
	m.now = clock.Now
	m.setBotFallback(provider, func(e *queueEntry, cand *BotCandidate) error {
		botOut <- botPair{user: e.ident.ID, bot: cand.Identity.ID}
		return nil
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(cancel)
	return m, realOut, botOut, clock
}

// botPair 是一次 bot 兜底配对。
type botPair struct {
	user pgtype.UUID
	bot  pgtype.UUID
}

// botCandidate 构造一个用于兜底的 bot 候选。
func botCandidate(n byte) *BotCandidate {
	return &BotCandidate{
		Identity: &store.Identity{ID: uuidN(n), Nickname: "bot", AvatarSeed: "seed"},
		Profile:  PrefProfile{Gender: "female", MatchGender: "any"},
	}
}

// waitUntil 轮询等待条件成立（带超时，避免固定长 sleep）。
func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// expectNoBotPair 断言一段时间内没有 bot 兜底配对。
func expectNoBotPair(t *testing.T, out chan botPair, wait time.Duration) {
	t.Helper()
	select {
	case p := <-out:
		t.Fatalf("不应发生 bot 兜底配对, 却得到 user=%v bot=%v", p.user, p.bot)
	case <-time.After(wait):
	}
}

// TestFallbackClaimsExpiredEntry 覆盖「到期的条目被 bot 认领」。
func TestFallbackClaimsExpiredEntry(t *testing.T) {
	provider := newFakeBotProvider(botCandidate(100))
	m, realOut, botOut, clock := fallbackSetup(t, newPrefetcher(), provider, 5*time.Millisecond)

	enq(t, m, 1)
	// 还没到兜底时间：不应尝试。
	time.Sleep(20 * time.Millisecond)
	if got := provider.callCount(); got != 0 {
		t.Fatalf("未到期不应调用 PickBot, 实际 %d 次", got)
	}

	// 时间推进到兜底点，由定时扫描触发兜底。
	clock.Advance(1100 * time.Millisecond)
	select {
	case p := <-botOut:
		if p.user != uuidN(1) || p.bot != uuidN(100) {
			t.Fatalf("兜底配对不符: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("到期的条目应被 bot 认领")
	}
	expectNoPair(t, realOut, 20*time.Millisecond)
}

// TestFallbackYieldsToRealUserWhilePending 覆盖「pending 期间来了真人时，真人配对优先，认领失败」。
func TestFallbackYieldsToRealUserWhilePending(t *testing.T) {
	gate := make(chan struct{})
	provider := newFakeBotProvider(botCandidate(100))
	provider.setGate(gate)
	m, realOut, botOut, clock := fallbackSetup(t, newPrefetcher(), provider, 5*time.Millisecond)

	enq(t, m, 1)
	// 触发兜底：PickBot 会阻塞在闸门上，条目处于 pending。
	clock.Advance(1100 * time.Millisecond)
	waitUntil(t, "兜底查询开始", func() bool { return provider.callCount() == 1 })

	// pending 期间另一个真人入队：应立刻与条目 1 配对（真人优先）。
	enq(t, m, 2)
	select {
	case p := <-realOut:
		if !p.matches(uuidN(1), uuidN(2)) {
			t.Fatalf("真人配对不符: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending 期间应允许真人配对")
	}

	// 放行 PickBot：认领应失败（条目已被真人配走），不会产生 bot 配对。
	close(gate)
	expectNoBotPair(t, botOut, 100*time.Millisecond)
}

// TestFallbackAbortsWhenCancelled 覆盖「pending 期间用户取消，认领失败」。
func TestFallbackAbortsWhenCancelled(t *testing.T) {
	gate := make(chan struct{})
	provider := newFakeBotProvider(botCandidate(100))
	provider.setGate(gate)
	m, _, botOut, clock := fallbackSetup(t, newPrefetcher(), provider, 5*time.Millisecond)

	enq(t, m, 1)
	clock.Advance(1100 * time.Millisecond)
	waitUntil(t, "兜底查询开始", func() bool { return provider.callCount() == 1 })

	m.Cancel(uuidN(1))
	close(gate)
	expectNoBotPair(t, botOut, 100*time.Millisecond)
}

// TestFallbackRetriesOnlyAfterInterval 覆盖「provider 返回 nil 后，过了 after 才再次尝试」。
func TestFallbackRetriesOnlyAfterInterval(t *testing.T) {
	// 第一次返回 nil，之后返回候选。
	provider := newFakeBotProvider(nil)
	m, _, botOut, clock := fallbackSetup(t, newPrefetcher(), provider, 5*time.Millisecond)

	enq(t, m, 1)
	clock.Advance(1100 * time.Millisecond)
	waitUntil(t, "第一次兜底尝试", func() bool { return provider.callCount() == 1 })

	// 还没到下一次尝试时间：不能再调用。
	time.Sleep(30 * time.Millisecond)
	if got := provider.callCount(); got != 1 {
		t.Fatalf("未到重试时间不应再次调用 PickBot, 实际 %d 次", got)
	}

	// 给 provider 一个候选，推进到下一次尝试时间。
	provider.mu.Lock()
	provider.cand = botCandidate(100)
	provider.mu.Unlock()
	clock.Advance(1100 * time.Millisecond)

	select {
	case p := <-botOut:
		if p.bot != uuidN(100) {
			t.Fatalf("兜底配对不符: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("过了 after 之后应再次尝试并配对成功")
	}
}

// TestFallbackRequeuesOnPairError 覆盖「建会话失败时放回队首」：
// 失败后条目仍在队列里，并且要过 after 才会再次尝试兜底。
func TestFallbackRequeuesOnPairError(t *testing.T) {
	provider := newFakeBotProvider(botCandidate(100))
	clock := &fakeClock{}
	clock.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())

	var fails atomic.Int32
	m := NewMemoryMatcher(newPrefetcher(), func(a, b *queueEntry) error { return nil }, time.Hour, 5*time.Millisecond)
	m.now = clock.Now
	m.setBotFallback(provider, func(e *queueEntry, cand *BotCandidate) error {
		fails.Add(1)
		return errInvalidPair // 模拟建会话失败
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(cancel)

	enq(t, m, 1)
	clock.Advance(1100 * time.Millisecond)
	waitUntil(t, "兜底建会话失败", func() bool { return fails.Load() == 1 })

	// 失败后不应立即重试（nextFallbackAt 被推后到 now+after）。
	time.Sleep(30 * time.Millisecond)
	if got := provider.callCount(); got != 1 {
		t.Fatalf("失败后不应立即重试, PickBot 调用 %d 次", got)
	}

	// 过了 after 之后条目仍在队列里，应再次尝试兜底（说明它被放回了队首）。
	clock.Advance(1100 * time.Millisecond)
	waitUntil(t, "放回队首后再次尝试", func() bool { return provider.callCount() == 2 })
}

// TestFallbackAfterIsDynamic 覆盖「兜底时间可热切换」：after=0 时不兜底，改成 1s 后按新值生效。
func TestFallbackAfterIsDynamic(t *testing.T) {
	provider := newFakeBotProvider(botCandidate(100))
	provider.setAfter(0) // 等价于全局关闭：不兜底
	m, _, botOut, clock := fallbackSetup(t, newPrefetcher(), provider, 5*time.Millisecond)

	enq(t, m, 1)
	clock.Advance(5 * time.Second)
	time.Sleep(30 * time.Millisecond)
	if got := provider.callCount(); got != 0 {
		t.Fatalf("after=0 时不应兜底, PickBot 调用 %d 次", got)
	}

	// 后台把兜底时间改成 1s：条目早已过 enqueuedAt+1s，应立即被兜底。
	provider.setAfter(time.Second)
	select {
	case p := <-botOut:
		if p.user != uuidN(1) || p.bot != uuidN(100) {
			t.Fatalf("兜底配对不符: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("打开兜底后应被 bot 认领")
	}
}

// TestCompatibleExported 验证导出的 Compatible 与内部判定一致。
func TestCompatibleExported(t *testing.T) {
	a := PrefProfile{Gender: "female", MatchGender: "any"}
	b := PrefProfile{Gender: "male", MatchGender: "any"}
	if !Compatible(a, b) {
		t.Fatal("Compatible 应与 compatible 一致（可配对）")
	}
	c := PrefProfile{CleanMode: true, MatchGender: "any"}
	if Compatible(c, a) {
		t.Fatal("清流与非清流不应配对")
	}
}
