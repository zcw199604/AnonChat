// Package match 实现随机匹配（A 形态）：排队、配对规则、断线宽限与 WS 处理。
package match

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// IdentityContext 是某身份的配对约束快照（入队时一次性查出，匹配时仅做内存判断）。
type IdentityContext struct {
	Blocked map[pgtype.UUID]struct{}
	Recent  map[pgtype.UUID]struct{}
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
	onPair     func(a, b *store.Identity) error
	relaxAfter time.Duration
	tick       time.Duration
	now        func() time.Time
	cmds       chan command
	queue      []*queueEntry
}

// NewMemoryMatcher 创建内存匹配器。tick 为定时全量扫描间隔（默认 1s）。
func NewMemoryMatcher(prefetcher Prefetcher, onPair func(a, b *store.Identity) error, relaxAfter, tick time.Duration) *memoryMatcher {
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
		m.queue = append(m.queue, &queueEntry{ident: c.ident, enqueuedAt: m.now(), ic: c.ic})
		// 入队只与现有队列逐个比较（O(N)）。
		m.tryFrom(len(m.queue) - 1)
	case cmdCancel, cmdLeave:
		m.remove(c.id)
	case cmdRequeueFront:
		m.queue = append(c.entries, m.queue...)
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

// tryFrom 只尝试让指定下标的条目与其余条目配对（O(N)）。
func (m *memoryMatcher) tryFrom(i int) {
	if i < 0 || i >= len(m.queue) {
		return
	}
	a := m.queue[i]
	for j := 0; j < len(m.queue); j++ {
		if j == i {
			continue
		}
		b := m.queue[j]
		if m.canPair(a, b) {
			m.removePair(i, j)
			m.executePair(a, b)
			return
		}
	}
}

// tryMatchAll 全量扫描（定时器触发），反复配对直到无法再配对。
func (m *memoryMatcher) tryMatchAll() {
	for {
		paired := false
		for i := 0; i < len(m.queue) && !paired; i++ {
			for j := i + 1; j < len(m.queue); j++ {
				if m.canPair(m.queue[i], m.queue[j]) {
					a, b := m.queue[i], m.queue[j]
					m.removePair(i, j)
					m.executePair(a, b)
					paired = true
					break
				}
			}
		}
		if !paired {
			return
		}
	}
}

// executePair 在独立 goroutine 中建会话并推送；失败则把两人放回队首。
func (m *memoryMatcher) executePair(a, b *queueEntry) {
	go func() {
		if err := m.onPair(a.ident, b.ident); err != nil {
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
	return true
}

// removePair 从队列移除下标 i、j（j 可能小于 i）。
func (m *memoryMatcher) removePair(i, j int) {
	if i > j {
		i, j = j, i
	}
	m.queue = append(m.queue[:j], m.queue[j+1:]...)
	m.queue = append(m.queue[:i], m.queue[i+1:]...)
}
