package match

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

type pair struct{ a, b pgtype.UUID }

// fakeClock 是并发安全的假时钟（匹配 goroutine 与测试共享）。
type fakeClock struct{ ns atomic.Int64 }

func (c *fakeClock) Now() time.Time          { return time.Unix(0, c.ns.Load()) }
func (c *fakeClock) Advance(d time.Duration) { c.ns.Add(int64(d)) }

type fakePrefetcher struct {
	blocked map[pgtype.UUID]map[pgtype.UUID]bool
	recent  map[pgtype.UUID]map[pgtype.UUID]bool
}

func newPrefetcher() *fakePrefetcher {
	return &fakePrefetcher{
		blocked: map[pgtype.UUID]map[pgtype.UUID]bool{},
		recent:  map[pgtype.UUID]map[pgtype.UUID]bool{},
	}
}

// addPair 双向登记（与 ListBlocksRelated 返回双向集合一致）。
func addPair(m map[pgtype.UUID]map[pgtype.UUID]bool, a, b pgtype.UUID) {
	if m[a] == nil {
		m[a] = map[pgtype.UUID]bool{}
	}
	if m[b] == nil {
		m[b] = map[pgtype.UUID]bool{}
	}
	m[a][b] = true
	m[b][a] = true
}

func toSet(m map[pgtype.UUID]bool) map[pgtype.UUID]struct{} {
	if m == nil {
		return nil
	}
	s := make(map[pgtype.UUID]struct{}, len(m))
	for k := range m {
		s[k] = struct{}{}
	}
	return s
}

func (p *fakePrefetcher) Prefetch(_ context.Context, id pgtype.UUID) (IdentityContext, error) {
	return IdentityContext{Blocked: toSet(p.blocked[id]), Recent: toSet(p.recent[id])}, nil
}

func uuidN(n byte) pgtype.UUID {
	var b [16]byte
	b[15] = n
	return pgtype.UUID{Bytes: b, Valid: true}
}
func ident(n byte) *store.Identity { return &store.Identity{ID: uuidN(n), Nickname: "u"} }

func setup(t *testing.T, pf Prefetcher, relaxAfter, tick time.Duration) (*memoryMatcher, chan pair, *fakeClock) {
	t.Helper()
	out := make(chan pair, 16)
	clock := &fakeClock{}
	clock.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	m := NewMemoryMatcher(pf, func(a, b *store.Identity) error {
		out <- pair{a.ID, b.ID}
		return nil
	}, relaxAfter, tick)
	m.now = clock.Now
	ctx, cancel := context.WithCancel(context.Background())
	go m.Run(ctx)
	t.Cleanup(cancel)
	return m, out, clock
}

func enq(t *testing.T, m *memoryMatcher, n byte) {
	t.Helper()
	if err := m.Enqueue(context.Background(), ident(n)); err != nil {
		t.Fatalf("入队失败: %v", err)
	}
}

func (p pair) matches(a, b pgtype.UUID) bool {
	return (p.a == a && p.b == b) || (p.a == b && p.b == a)
}

func expectPair(t *testing.T, out chan pair, wantA, wantB pgtype.UUID) {
	t.Helper()
	select {
	case p := <-out:
		if !p.matches(wantA, wantB) {
			t.Fatalf("配对 = %v,%v 期望 %v,%v（顺序无关）", p.a, p.b, wantA, wantB)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("期望配对未发生")
	}
}

func expectNoPair(t *testing.T, out chan pair, wait time.Duration) {
	t.Helper()
	select {
	case p := <-out:
		t.Fatalf("不应配对，却发生了 %v,%v", p.a, p.b)
	case <-time.After(wait):
	}
}

func readPair(t *testing.T, out chan pair) pair {
	t.Helper()
	select {
	case p := <-out:
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("期望配对未发生")
		return pair{}
	}
}

func TestFIFO(t *testing.T) {
	m, out, _ := setup(t, newPrefetcher(), time.Minute, time.Hour)
	enq(t, m, 1)
	enq(t, m, 2)
	enq(t, m, 3)
	enq(t, m, 4)
	// onPair 在独立 goroutine 执行，配对到达顺序不确定，按集合断言。
	p1 := readPair(t, out)
	p2 := readPair(t, out)
	got := []pair{p1, p2}
	want := []pair{{uuidN(1), uuidN(2)}, {uuidN(3), uuidN(4)}}
	for _, w := range want {
		found := false
		for _, g := range got {
			if g.matches(w.a, w.b) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("缺少配对 %v,%v, 实际 %v", w.a, w.b, got)
		}
	}
}

func TestBlockedNeverPaired(t *testing.T) {
	pf := newPrefetcher()
	addPair(pf.blocked, uuidN(1), uuidN(2))
	m, out, _ := setup(t, pf, time.Minute, time.Hour)
	enq(t, m, 1)
	enq(t, m, 2)
	enq(t, m, 3)
	expectPair(t, out, uuidN(1), uuidN(3))
	expectNoPair(t, out, 50*time.Millisecond)
}

func TestRecentExcludedThenRelaxedByTick(t *testing.T) {
	pf := newPrefetcher()
	addPair(pf.recent, uuidN(1), uuidN(2))
	// tick 很小，能触发定时全量扫描。
	m, out, clock := setup(t, pf, 10*time.Second, 10*time.Millisecond)
	enq(t, m, 1)
	enq(t, m, 2)
	expectNoPair(t, out, 50*time.Millisecond)

	// 超过放宽阈值，不再有新入队；靠定时扫描完成配对。
	clock.Advance(11 * time.Second)
	expectPair(t, out, uuidN(1), uuidN(2))
}

func TestCancelRemovesFromQueue(t *testing.T) {
	m, out, _ := setup(t, newPrefetcher(), time.Minute, time.Hour)
	enq(t, m, 1)
	m.Cancel(uuidN(1))
	enq(t, m, 2)
	enq(t, m, 3)
	expectPair(t, out, uuidN(2), uuidN(3))
}

func TestDuplicateEnqueueLastWins(t *testing.T) {
	m, out, _ := setup(t, newPrefetcher(), time.Minute, time.Hour)
	enq(t, m, 1)
	enq(t, m, 1)
	enq(t, m, 2)
	expectPair(t, out, uuidN(1), uuidN(2))
}

// BenchmarkEnqueuePairing1000 测量 1000 人队列下，单次入队（新人与所有人均被屏蔽）的配对判断耗时（不含 DB）。
func BenchmarkEnqueuePairing1000(b *testing.B) {
	base := make([]*queueEntry, 0, 1000)
	blocked := map[pgtype.UUID]struct{}{}
	for i := 0; i < 1000; i++ {
		id := uuidN(byte(i))
		base = append(base, &queueEntry{ident: ident(byte(i)), enqueuedAt: time.Now(), ic: IdentityContext{}})
		blocked[id] = struct{}{}
	}
	newEntry := &queueEntry{ident: &store.Identity{ID: uuidN(250)}, enqueuedAt: time.Now(), ic: IdentityContext{Blocked: blocked}}

	m := NewMemoryMatcher(newPrefetcher(), func(a, b *store.Identity) error { return nil }, time.Minute, time.Hour)
	m.now = time.Now
	queue := make([]*queueEntry, len(base))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(queue, base)
		m.queue = append(queue[:len(queue):len(queue)], newEntry)
		m.tryFrom(len(m.queue) - 1)
	}
}
