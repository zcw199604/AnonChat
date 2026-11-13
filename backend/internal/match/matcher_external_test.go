package match

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type externalCancel struct {
	id      pgtype.UUID
	attempt uint64
}
type fakeExternalMatcher struct {
	mu        sync.Mutex
	plan      ExternalPlan
	err       error
	started   chan ExternalRequest
	cancelled chan externalCancel
}

func newFakeExternal() *fakeExternalMatcher {
	return &fakeExternalMatcher{plan: ExternalPlan{Enabled: true, OnlineThreshold: 5, LocalWait: 10 * time.Second, MatchTimeout: 10 * time.Second}, started: make(chan ExternalRequest, 32), cancelled: make(chan externalCancel, 32)}
}
func (f *fakeExternalMatcher) Plan() ExternalPlan { f.mu.Lock(); defer f.mu.Unlock(); return f.plan }
func (f *fakeExternalMatcher) StartMatch(_ context.Context, r ExternalRequest) error {
	f.started <- r
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}
func (f *fakeExternalMatcher) CancelMatch(id pgtype.UUID, attempt uint64) {
	f.cancelled <- externalCancel{id, attempt}
}
func (f *fakeExternalMatcher) disable() { f.mu.Lock(); f.plan.Enabled = false; f.mu.Unlock() }

// 单步驱动与 Run 使用相同的 process/tick 方法；WithClock 控制业务时间。
// 不启动匹配循环，所有队列状态断言都在同一 goroutine 中完成。
func externalUnit(t *testing.T, online int) (*memoryMatcher, *fakeExternalMatcher, *fakeClock, chan pair) {
	t.Helper()
	clock := &fakeClock{}
	clock.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	s := New(nil, nil, nil, nil, nil, time.Hour, time.Second, WithClock(clock.Now))
	ext := newFakeExternal()
	s.SetExternalMatcher(ext, func() int { return online })
	out := make(chan pair, 8)
	s.matcher.onPair = func(a, b *queueEntry) error { out <- pair{a.ident.ID, b.ident.ID}; return nil }
	return s.matcher, ext, clock, out
}
func externalEnqueue(m *memoryMatcher, n byte) { m.process(command{kind: cmdEnqueue, ident: ident(n)}) }
func externalStart(t *testing.T, f *fakeExternalMatcher) ExternalRequest {
	t.Helper()
	select {
	case r := <-f.started:
		return r
	case <-time.After(300 * time.Millisecond):
		t.Fatal("外站匹配未开始")
		return ExternalRequest{}
	}
}
func externalCancelled(t *testing.T, f *fakeExternalMatcher, id pgtype.UUID, attempt uint64) {
	t.Helper()
	select {
	case c := <-f.cancelled:
		if c.id != id || c.attempt != attempt {
			t.Fatalf("取消代次不符: %+v", c)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("外站匹配未取消")
	}
}
func externalNoStart(t *testing.T, f *fakeExternalMatcher) {
	t.Helper()
	select {
	case r := <-f.started:
		t.Fatalf("不应启动外站: %+v", r)
	case <-time.After(10 * time.Millisecond):
	}
}
func externalClaimEntry(m *memoryMatcher, id pgtype.UUID, attempt uint64) externalClaim {
	var claimed externalClaim
	m.process(command{kind: cmdClaimAttempt, id: id, attempt: attempt, claim: &claimed})
	return claimed
}
func externalPump(t *testing.T, m *memoryMatcher, kind cmdKind) {
	t.Helper()
	select {
	case c := <-m.cmds:
		if c.kind != kind {
			t.Fatalf("命令=%v want %v", c.kind, kind)
		}
		m.process(c)
		if c.done != nil {
			close(c.done)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("未收到命令 %v", kind)
	}
}

func TestExternalStagesAndOnlineThreshold(t *testing.T) {
	for _, online := range []int{0, 1, 5, 6} {
		t.Run(time.Duration(online).String(), func(t *testing.T) {
			m, ext, clock, _ := externalUnit(t, online)
			externalEnqueue(m, 1)
			if online <= 5 {
				r := externalStart(t, ext)
				if r.Attempt == 0 || r.Identity.ID != uuidN(1) || m.queue[0].phase != phaseExternal {
					t.Fatalf("立即外站阶段不符: %+v", r)
				}
			} else {
				externalNoStart(t, ext)
				clock.Advance(9 * time.Second)
				m.tryExternal()
				externalNoStart(t, ext)
				clock.Advance(time.Second)
				m.tryExternal()
				externalStart(t, ext)
			}
			clock.Advance(10 * time.Second)
			m.tryExternal()
			if m.queue[0].phase != phaseFallback {
				t.Fatalf("外站超时应进入兜底: %+v", m.queue[0])
			}
			externalCancelled(t, ext, uuidN(1), m.queue[0].attempt)
		})
	}
}

func TestExternalLocalPairingAndRemovalCancel(t *testing.T) {
	for _, action := range []string{"pair", "cancel", "leave", "replace"} {
		t.Run(action, func(t *testing.T) {
			m, ext, _, out := externalUnit(t, 1)
			externalEnqueue(m, 1)
			r := externalStart(t, ext)
			switch action {
			case "pair":
				externalEnqueue(m, 2)
				expectPair(t, out, uuidN(1), uuidN(2))
				externalNoStart(t, ext)
			case "cancel":
				m.process(command{kind: cmdCancel, id: uuidN(1)})
			case "leave":
				m.process(command{kind: cmdLeave, id: uuidN(1)})
			case "replace":
				externalEnqueue(m, 1)
				next := externalStart(t, ext)
				if next.Attempt <= r.Attempt {
					t.Fatal("重新入队复用了代次")
				}
			}
			externalCancelled(t, ext, uuidN(1), r.Attempt)
		})
	}
	// 本站有人时优先本站，外站不应启动。
	m, ext, _, out := externalUnit(t, 10)
	externalEnqueue(m, 1)
	externalEnqueue(m, 2)
	expectPair(t, out, uuidN(1), uuidN(2))
	externalNoStart(t, ext)
}

func TestExternalClaimRequiresCurrentUnexpiredAttempt(t *testing.T) {
	for _, reason := range []string{"success", "wrong-id", "wrong-attempt", "cancelled", "timeout", "fallback", "local", "reentered"} {
		t.Run(reason, func(t *testing.T) {
			m, ext, clock, _ := externalUnit(t, 1)
			externalEnqueue(m, 1)
			r := externalStart(t, ext)
			id, attempt := uuidN(1), r.Attempt
			switch reason {
			case "wrong-id":
				id = uuidN(9)
			case "wrong-attempt":
				attempt++
			case "cancelled":
				m.process(command{kind: cmdCancel, id: id})
			case "timeout":
				clock.Advance(10 * time.Second) // 尚未 tick，也必须拒绝。
			case "fallback":
				clock.Advance(10 * time.Second)
				m.tryExternal()
			case "local":
				m.queue[0].phase = phaseLocal
			case "reentered":
				externalEnqueue(m, 1)
				externalStart(t, ext)
			}
			claim := externalClaimEntry(m, id, attempt)
			if reason == "success" {
				if claim.entry == nil || m.QueueLen() != 0 || m.intents[id].inflight != 1 {
					t.Fatalf("认领失败: %+v", claim)
				}
			} else if claim.entry != nil {
				t.Fatalf("应拒绝 %s 的认领", reason)
			}
		})
	}
}

func TestExternalFailedIgnoresStaleResults(t *testing.T) {
	m, ext, _, _ := externalUnit(t, 1)
	externalEnqueue(m, 1)
	r := externalStart(t, ext)
	e := m.queue[0]
	m.process(command{kind: cmdExternalFailed, entry: e, attempt: r.Attempt + 1})
	if e.phase != phaseExternal {
		t.Fatal("错误代次改变了阶段")
	}
	m.process(command{kind: cmdExternalFailed, entry: e, attempt: r.Attempt})
	if e.phase != phaseFallback {
		t.Fatal("外站失败未进入兜底")
	}
	externalEnqueue(m, 1)
	next := externalStart(t, ext)
	m.process(command{kind: cmdExternalFailed, entry: e, attempt: r.Attempt})
	if m.queue[0].phase != phaseExternal || m.queue[0].attempt != next.Attempt {
		t.Fatal("旧失败污染了新请求")
	}
}

func TestExternalRequeueUsesIntentVersion(t *testing.T) {
	for _, action := range []string{"failure", "cancel", "leave", "reenter", "cancel-reenter-cancel", "success"} {
		t.Run(action, func(t *testing.T) {
			m, ext, _, _ := externalUnit(t, 1)
			externalEnqueue(m, 1)
			r := externalStart(t, ext)
			c := externalClaimEntry(m, uuidN(1), r.Attempt)
			if c.entry == nil {
				t.Fatal("应认领成功")
			}
			m.cleanupIntents()
			if m.intents[uuidN(1)] == nil {
				t.Fatal("在途意图被清除")
			}
			switch action {
			case "cancel":
				m.process(command{kind: cmdCancel, id: uuidN(1)})
			case "leave":
				m.process(command{kind: cmdLeave, id: uuidN(1)})
			case "reenter":
				externalEnqueue(m, 1)
				externalStart(t, ext)
			case "cancel-reenter-cancel":
				m.process(command{kind: cmdCancel, id: uuidN(1)})
				externalEnqueue(m, 1)
				externalStart(t, ext)
				m.process(command{kind: cmdCancel, id: uuidN(1)})
			}
			kind := cmdRequeueExternal
			if action == "success" {
				kind = cmdExternalDone
			}
			m.process(command{kind: kind, entry: c.entry, intentSeq: c.intentSeq})
			if action == "failure" {
				if len(m.queue) != 1 || m.queue[0] != c.entry || c.entry.phase != phaseFallback || c.entry.fallbackPending || !c.entry.retryAt.IsZero() {
					t.Fatal("失败未以兜底阶段回队")
				}
			} else if action == "reenter" {
				if len(m.queue) != 1 || m.queue[0] == c.entry {
					t.Fatal("覆盖了新请求")
				}
			} else if len(m.queue) != 0 {
				t.Fatal("取消或成功后不应回队")
			}
			if m.intents[uuidN(1)].inflight != 0 {
				t.Fatal("未释放在途计数")
			}
			m.cleanupIntents()
			if len(m.queue) == 0 && m.intents[uuidN(1)] != nil {
				t.Fatal("无在途且出队的意图未清理")
			}
		})
	}
}

func TestExternalFallbackTimingAndHotDisable(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "unregistered", "hot-disable", "start-failed"} {
		t.Run(mode, func(t *testing.T) {
			m, ext, clock, _ := externalUnit(t, 1)
			if mode == "disabled" {
				ext.disable()
			}
			if mode == "unregistered" {
				m.external = nil
			}
			if mode == "start-failed" {
				ext.err = ErrNoExternalProvider
			}
			provider := newFakeBotProvider(botCandidate(100))
			provider.setAfter(time.Hour)
			out := make(chan botPair, 1)
			m.setBotFallback(provider, func(e *queueEntry, c *BotCandidate) error { out <- botPair{e.ident.ID, c.Identity.ID}; return nil }, nil)
			externalEnqueue(m, 1)
			if mode == "disabled" || mode == "unregistered" {
				clock.Advance(20 * time.Second)
				m.tryExternal()
				m.tryFallback()
				if provider.callCount() != 0 {
					t.Fatal("关闭时未保持原兜底时间")
				}
				clock.Advance(time.Hour)
				m.tryFallback()
			} else {
				r := externalStart(t, ext)
				if mode == "start-failed" {
					externalPump(t, m, cmdExternalFailed)
				} else {
					m.tryFallback()
					if provider.callCount() != 0 {
						t.Fatal("外站阶段不应尝试 bot")
					}
					if mode == "hot-disable" {
						ext.disable()
					} else {
						clock.Advance(10 * time.Second)
					}
					m.tryExternal()
					externalCancelled(t, ext, uuidN(1), r.Attempt)
				}
				m.tryFallback()
			}
			externalPump(t, m, cmdClaim)
			select {
			case <-out:
			case <-time.After(300 * time.Millisecond):
				t.Fatal("未立即进入 bot 兜底")
			}
		})
	}
}

func TestExternalStartFailureDoesNotBlockMatcher(t *testing.T) {
	m, ext, _, _ := externalUnit(t, 1)
	ext.err = errors.New("offline")
	externalEnqueue(m, 1)
	externalStart(t, ext)
	externalPump(t, m, cmdExternalFailed)
	if m.queue[0].phase != phaseFallback {
		t.Fatal("StartMatch 失败未回退")
	}
}

func TestExternalIntentSurvivesUntilAllInflightFinish(t *testing.T) {
	m, ext, _, _ := externalUnit(t, 1)
	externalEnqueue(m, 1)
	first := externalStart(t, ext)
	a := externalClaimEntry(m, uuidN(1), first.Attempt)
	externalEnqueue(m, 1)
	second := externalStart(t, ext)
	b := externalClaimEntry(m, uuidN(1), second.Attempt)
	if a.entry == nil || b.entry == nil {
		t.Fatal("两轮都应能认领")
	}
	m.process(command{kind: cmdCancel, id: uuidN(1)})
	m.process(command{kind: cmdRequeueExternal, entry: a.entry, intentSeq: a.intentSeq})
	m.cleanupIntents()
	if m.intents[uuidN(1)] == nil || m.intents[uuidN(1)].inflight != 1 {
		t.Fatal("仍有在途操作时清除了意图")
	}
	m.process(command{kind: cmdRequeueExternal, entry: b.entry, intentSeq: b.intentSeq})
	m.cleanupIntents()
	if len(m.queue) != 0 || m.intents[uuidN(1)] != nil {
		t.Fatal("已取消的旧操作不应回队，全部完成后应清理意图")
	}
}

func TestExternalBotCreationFailurePreservesFallbackPhaseAndRetry(t *testing.T) {
	m, ext, clock, _ := externalUnit(t, 1)
	provider := newFakeBotProvider(botCandidate(100))
	m.setBotFallback(provider, func(*queueEntry, *BotCandidate) error { return errInvalidPair }, nil)
	externalEnqueue(m, 1)
	externalStart(t, ext)
	clock.Advance(10 * time.Second)
	m.tryExternal()
	m.tryFallback()
	externalPump(t, m, cmdClaim)
	externalPump(t, m, cmdRequeueFront)
	if len(m.queue) != 1 || m.queue[0].phase != phaseFallback {
		t.Fatal("bot 建会话失败没有保持兜底阶段")
	}
	m.tryExternal()
	externalNoStart(t, ext)
	m.tryFallback()
	if provider.callCount() != 1 {
		t.Fatal("未到 retryAt 就重试了")
	}
	clock.Advance(time.Second)
	m.tryFallback()
	externalPump(t, m, cmdClaim)
	externalPump(t, m, cmdRequeueFront)
	if provider.callCount() != 2 {
		t.Fatal("未按原 after 间隔重试")
	}
}

func TestExternalClaimAfterLegacyRequeueRestoresIntent(t *testing.T) {
	m, ext, _, _ := externalUnit(t, 1)
	externalEnqueue(m, 1)
	r := externalStart(t, ext)
	e := m.queue[0]
	// 原有 bot 认领/建会话失败回队流程不增加外站 inflight。
	var claimed bool
	m.process(command{kind: cmdClaim, entry: e, claimed: &claimed})
	if !claimed {
		t.Fatal("原有认领失败")
	}
	m.cleanupIntents()
	if m.intents[uuidN(1)] != nil {
		t.Fatal("无在途且出队时应能清理意图")
	}
	m.process(command{kind: cmdRequeueFront, entries: []*queueEntry{e}})
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("旧流程回队后的外站认领发生 panic: %v", p)
		}
	}()
	claim := externalClaimEntry(m, uuidN(1), r.Attempt)
	if claim.entry != e || m.intents[uuidN(1)].inflight != 1 {
		t.Fatal("回队后认领未恢复意图记录")
	}
	m.process(command{kind: cmdCancel, id: uuidN(1)})
	m.process(command{kind: cmdRequeueExternal, entry: claim.entry, intentSeq: claim.intentSeq})
	if len(m.queue) != 0 {
		t.Fatal("恢复意图后仍须遵守取消版本")
	}
}
