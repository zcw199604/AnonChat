// Package match 实现随机匹配（A 形态）：排队、配对规则、断线宽限与 WS 处理。
package match

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// PrefProfile 是匹配用的资料快照（how.md 3.4：入队时一次性查出，匹配时只在内存判断）。
type PrefProfile struct {
	Gender         string
	Age            *int16
	Province       *string
	ProvincePublic bool
	Tags           []string
	CleanMode      bool
	MatchGender    string
	MatchSameProv  bool
	VIP            bool
}

// IdentityContext 是某身份的配对约束快照（入队时一次性查出，匹配时仅做内存判断）。
type IdentityContext struct {
	Blocked map[pgtype.UUID]struct{}
	Recent  map[pgtype.UUID]struct{}
	Profile PrefProfile
}

// Prefetcher 在入队时（匹配 goroutine 之外、带超时）一次性拉取身份约束。
type Prefetcher interface {
	Prefetch(ctx context.Context, identityID pgtype.UUID) (IdentityContext, error)
}

// Matcher 是匹配队列接口（V1 内存实现，V2 可换 Redis）。
type Matcher interface {
	// Enqueue 让身份进入排队（同一身份重复排队以最后一次为准）。
	Enqueue(ctx context.Context, ident *store.Identity) error
	// Cancel 将身份移出队列（不影响当前会话）。
	Cancel(identityID pgtype.UUID)
	// Leave 将身份移出队列（用于 match.leave）。
	Leave(identityID pgtype.UUID)
	// QueueLen 返回当前排队人数（用于在线统计）。
	QueueLen() int
}

type cmdKind int

const (
	cmdEnqueue cmdKind = iota
	cmdCancel
	cmdLeave
	cmdRequeueFront
)

type command struct {
	kind    cmdKind
	ident   *store.Identity
	ic      IdentityContext
	id      pgtype.UUID
	entries []*queueEntry
	done    chan struct{}
}

type queueEntry struct {
	ident      *store.Identity
	enqueuedAt time.Time
	ic         IdentityContext
}

// memoryMatcher 是单 goroutine 驱动的内存匹配器：队列操作经命令通道串行化，配对判断只在内存进行。
type memoryMatcher struct {
	prefetcher Prefetcher
	onPair     func(a, b *queueEntry) error
	relaxAfter time.Duration
	tick       time.Duration
	now        func() time.Time
	cmds       chan command
	queue      []*queueEntry

	// mu/queueLen 只用于对外提供线程安全的排队人数读数（仅供统计）。
	mu       sync.Mutex
	queueLen int
}

// NewMemoryMatcher 创建内存匹配器。tick 为定时全量扫描间隔（默认 1s）。
func NewMemoryMatcher(prefetcher Prefetcher, onPair func(a, b *queueEntry) error, relaxAfter, tick time.Duration) *memoryMatcher {
	if relaxAfter <= 0 {
		relaxAfter = 15 * time.Second
	}
	if tick <= 0 {
		tick = time.Second
	}
	return &memoryMatcher{
		prefetcher: prefetcher,
		onPair:     onPair,
		relaxAfter: relaxAfter,
		tick:       tick,
		now:        time.Now,
		cmds:       make(chan command),
	}
}

// Run 启动匹配 goroutine，直到 ctx 取消。定时器周期性做全量配对扫描（放宽“最近聊过”）。
func (m *memoryMatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(m.tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-m.cmds:
			m.process(c)
			close(c.done)
		case <-ticker.C:
			m.tryMatchAll()
		}
	}
}

// Enqueue 实现 Matcher：先在 goroutine 之外预取身份约束，再提交入队命令。
func (m *memoryMatcher) Enqueue(ctx context.Context, ident *store.Identity) error {
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	ic, err := m.prefetcher.Prefetch(pctx, ident.ID)
	if err != nil {
		return err
	}
	m.do(command{kind: cmdEnqueue, ident: ident, ic: ic})
	return nil
}

// Cancel 实现 Matcher。
func (m *memoryMatcher) Cancel(identityID pgtype.UUID) {
	m.do(command{kind: cmdCancel, id: identityID})
}

// Leave 实现 Matcher。
func (m *memoryMatcher) Leave(identityID pgtype.UUID) { m.do(command{kind: cmdLeave, id: identityID}) }

// QueueLen 实现 Matcher：读取最近一次处理的队列长度（原子读，仅用于统计）。
func (m *memoryMatcher) QueueLen() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queueLen
}

// setQueueLen 更新对外的排队人数镜像。
func (m *memoryMatcher) setQueueLen(n int) {
	m.mu.Lock()
	m.queueLen = n
	m.mu.Unlock()
}

// do 同步提交一条命令并等待处理完成。
func (m *memoryMatcher) do(c command) {
	c.done = make(chan struct{})
	m.cmds <- c
	<-c.done
}

// process 在匹配 goroutine 内处理命令。
func (m *memoryMatcher) process(c command) {
	switch c.kind {
	case cmdEnqueue:
		m.remove(c.ident.ID)
		entry := &queueEntry{ident: c.ident, enqueuedAt: m.now(), ic: c.ic}
		m.queue = append(m.queue, entry)
		m.setQueueLen(len(m.queue))
		// 入队只与现有队列逐个比较（O(N)）。
		m.tryPairFor(entry)
		m.setQueueLen(len(m.queue))
	case cmdCancel, cmdLeave:
		m.remove(c.id)
		m.setQueueLen(len(m.queue))
	case cmdRequeueFront:
		m.queue = append(c.entries, m.queue...)
		m.setQueueLen(len(m.queue))
		m.tryMatchAll()
	}
}

// remove 从队列中移除某身份。
func (m *memoryMatcher) remove(id pgtype.UUID) {
	out := m.queue[:0]
	for _, e := range m.queue {
		if e.ident.ID != id {
			out = append(out, e)
		}
	}
	m.queue = out
}

// removeEntry 从队列中移除指定条目。
func (m *memoryMatcher) removeEntry(target *queueEntry) {
	out := m.queue[:0]
	for _, e := range m.queue {
		if e != target {
			out = append(out, e)
		}
	}
	m.queue = out
}

// contains 判断条目是否仍在队列中。
func (m *memoryMatcher) contains(target *queueEntry) bool {
	for _, e := range m.queue {
		if e == target {
			return true
		}
	}
	return false
}

// tryPairFor 为指定条目挑选最合适的候选并配对（单次扫描 O(N)）。
// 候选人选择：标签交集最大者优先；交集相同时选排队最久的。
func (m *memoryMatcher) tryPairFor(a *queueEntry) bool {
	if !m.contains(a) {
		return false
	}
	var best *queueEntry
	bestScore := -1
	for _, b := range m.queue {
		if b == a {
			continue
		}
		if !m.canPair(a, b) {
			continue
		}
		score := tagScore(a.ic.Profile.Tags, b.ic.Profile.Tags)
		if score > bestScore || (score == bestScore && best != nil && b.enqueuedAt.Before(best.enqueuedAt)) {
			best = b
			bestScore = score
		}
	}
	if best == nil {
		return false
	}
	m.removeEntry(a)
	m.removeEntry(best)
	m.executePair(a, best)
	return true
}

// tryMatchAll 全量扫描（定时器触发），反复配对直到无法再配对。
func (m *memoryMatcher) tryMatchAll() {
	for {
		paired := false
		snapshot := make([]*queueEntry, len(m.queue))
		copy(snapshot, m.queue)
		for _, a := range snapshot {
			if !m.contains(a) {
				continue
			}
			if m.tryPairFor(a) {
				paired = true
				break
			}
		}
		m.setQueueLen(len(m.queue))
		if !paired {
			return
		}
	}
}

// executePair 在独立 goroutine 中建会话并推送；失败则把两人放回队首。
func (m *memoryMatcher) executePair(a, b *queueEntry) {
	go func() {
		if err := m.onPair(a, b); err != nil {
			select {
			case m.cmds <- command{kind: cmdRequeueFront, entries: []*queueEntry{a, b}}:
			default:
				// 命令通道忙时也要保证能放回：阻塞发送（Run 一定会接收）。
				m.cmds <- command{kind: cmdRequeueFront, entries: []*queueEntry{a, b}}
			}
		}
	}()
}

// canPair 纯内存判断两人是否可配对。
func (m *memoryMatcher) canPair(a, b *queueEntry) bool {
	if _, ok := a.ic.Blocked[b.ident.ID]; ok {
		return false
	}
	if _, ok := b.ic.Blocked[a.ident.ID]; ok {
		return false
	}
	now := m.now()
	relaxed := now.Sub(a.enqueuedAt) >= m.relaxAfter || now.Sub(b.enqueuedAt) >= m.relaxAfter
	if !relaxed {
		_, ab := a.ic.Recent[b.ident.ID]
		_, ba := b.ic.Recent[a.ident.ID]
		if ab || ba {
			return false
		}
	}
	// 硬条件（清流、性别、同省）不随时间放宽。
	return compatible(a.ic.Profile, b.ic.Profile)
}

// compatible 判断两份资料快照是否双向兼容（how.md 3.4）。
// 非 VIP 的 match_gender / match_same_province 一律视为 any/false（服务端判定）。
func compatible(a, b PrefProfile) bool {
	if a.CleanMode != b.CleanMode {
		return false
	}
	if !genderMatches(a, b) || !genderMatches(b, a) {
		return false
	}
	if !provinceMatches(a, b) || !provinceMatches(b, a) {
		return false
	}
	return true
}

// genderMatches 判断 a 的性别筛选是否接受 b（gender=secret 的人不满足任何性别筛选）。
func genderMatches(a, b PrefProfile) bool {
	if !a.VIP || a.MatchGender == "" || a.MatchGender == "any" {
		return true
	}
	return b.Gender == a.MatchGender && (b.Gender == "male" || b.Gender == "female")
}

// provinceMatches 判断 a 的同省要求是否被 b 满足。
func provinceMatches(a, b PrefProfile) bool {
	if !a.VIP || !a.MatchSameProv {
		return true
	}
	return a.Province != nil && b.Province != nil && *a.Province == *b.Province
}

// tagScore 返回两人标签的交集大小。
func tagScore(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(a))
	for _, t := range a {
		set[t] = struct{}{}
	}
	score := 0
	for _, t := range b {
		if _, ok := set[t]; ok {
			score++
		}
	}
	return score
}
