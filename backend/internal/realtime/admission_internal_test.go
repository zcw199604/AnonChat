package realtime

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
)

func admissionConn(id byte, ip string) *Conn {
	return &Conn{identity: &store.Identity{ID: pgtype.UUID{Bytes: [16]byte{id}, Valid: true}}, ip: ip}
}
func TestAdmitDimensions(t *testing.T) {
	h := NewHub()
	if ok, _, _ := h.admit(admissionConn(1, "a"), 3, 1); !ok {
		t.Fatal("首次应放行")
	}
	if ok, reason, conflicts := h.admit(admissionConn(1, "a"), 3, 1); ok || reason != "duplicate_session" || len(conflicts) != 1 {
		t.Fatalf("重复身份准入: %v %s %d", ok, reason, len(conflicts))
	}
	if ok, _, _ := h.admit(admissionConn(1, "b"), 3, 1); !ok {
		t.Fatal("不同 IP 应放行")
	}
	h.admit(admissionConn(2, "a"), 3, 1)
	h.admit(admissionConn(3, "a"), 3, 1)
	if ok, reason, conflicts := h.admit(admissionConn(1, "a"), 3, 1); ok || reason != "duplicate_session" || len(conflicts) != 1 {
		t.Fatalf("IP 满时应优先返回身份冲突: %v %s %d", ok, reason, len(conflicts))
	}
	if ok, reason, _ := h.admit(admissionConn(4, "a"), 3, 1); ok || reason != "ip_conn_limit" {
		t.Fatalf("IP 上限: %v %s", ok, reason)
	}
	if ok, _, _ := h.admit(admissionConn(1, ""), 3, 1); !ok {
		t.Fatal("空 IP 应放行")
	}
}
func TestAdmitConcurrentDoesNotOversubscribe(t *testing.T) {
	h := NewHub()
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(id byte) {
			defer wg.Done()
			if ok, _, _ := h.admit(admissionConn(id, "a"), 3, 1); ok {
				successes.Add(1)
			}
		}(byte(i))
	}
	wg.Wait()
	if successes.Load() != 3 {
		t.Fatalf("准入 %d 条，期望 3", successes.Load())
	}
}

func TestAdmitReplacesStaleConnection(t *testing.T) {
	settings := moderation.NewLimitSettings(nil, 1, nil)
	g := &Gateway{hub: NewHub(), opts: Options{LimitSettings: settings}}
	old := admissionConn(1, "a")
	old.done = make(chan struct{})
	old.closed = make(chan struct{})
	old.removed = make(chan struct{})
	old.ping = func(context.Context) error { return errors.New("已失效") }
	old.closeConn = func(websocket.StatusCode, string) { g.hub.removeConn(old); close(old.removed) }
	if ok, _ := g.admit(context.Background(), old); !ok {
		t.Fatal("首次准入失败")
	}
	fresh := admissionConn(1, "a")
	if ok, reason := g.admit(context.Background(), fresh); !ok {
		t.Fatalf("失效连接应替换: %s", reason)
	}
	if g.hub.ConnCount(old.identity.ID) != 1 {
		t.Fatal("应只保留新连接")
	}
}

func TestAdmitKeepsLiveConnection(t *testing.T) {
	settings := moderation.NewLimitSettings(nil, 1, nil)
	g := &Gateway{hub: NewHub(), opts: Options{LimitSettings: settings}}
	old := admissionConn(1, "a")
	old.done = make(chan struct{})
	old.ping = func(context.Context) error { return nil }
	g.admit(context.Background(), old)
	fresh := admissionConn(1, "a")
	if ok, reason := g.admit(context.Background(), fresh); ok || reason != "duplicate_session" {
		t.Fatalf("活连接应拒绝新准入: %v %s", ok, reason)
	}
	if g.hub.ConnCount(old.identity.ID) != 1 {
		t.Fatal("拒绝连接不得登记Hub")
	}
}

func TestAdmitReplacesStaleConnectionWithBlockedClose(t *testing.T) {
	g := &Gateway{hub: NewHub(), opts: Options{LimitSettings: moderation.NewLimitSettings(nil, 1, nil)}}
	old := admissionConn(1, "a")
	old.done = make(chan struct{})
	old.closed = make(chan struct{})
	old.removed = make(chan struct{})
	old.ping = func(context.Context) error { return errors.New("已失效") }
	forced := make(chan struct{})
	var forceOnce sync.Once
	old.closeNow = func() { forceOnce.Do(func() { close(forced) }) }
	// 模拟普通关闭握手与 readLoop 都在等待底层连接关闭。
	old.closeConn = func(websocket.StatusCode, string) { <-forced }
	defer old.closeNow()
	go func() {
		<-forced
		g.hub.removeConn(old)
		close(old.removed)
	}()
	g.admit(context.Background(), old)
	fresh := admissionConn(1, "a")
	type result struct {
		ok     bool
		reason string
	}
	admitted := make(chan result, 1)
	go func() {
		ok, reason := g.admit(context.Background(), fresh)
		admitted <- result{ok, reason}
	}()
	select {
	case got := <-admitted:
		if !got.ok {
			t.Fatalf("失效连接应强制关闭并替换: %s", got.reason)
		}
		if g.hub.ConnCount(old.identity.ID) != 1 {
			t.Fatal("应只保留新连接")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("新连接未在 2s 内准入")
	}
}

// probeWaitContext 标记等待者进入共享探活分支，避免靠睡眠猜测调度。
type probeWaitContext struct {
	context.Context
	entered chan struct{}
	once    sync.Once
}

func (c *probeWaitContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.entered) })
	return c.Context.Done()
}

func TestConcurrentProbeReusesResult(t *testing.T) {
	old := admissionConn(1, "a")
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	old.ping = func(context.Context) error { calls.Add(1); close(entered); <-release; return errors.New("失效") }
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if old.probeAlive(context.Background()) == nil {
			t.Error("应返回失败")
		}
	}()
	<-entered
	waiters := make([]*probeWaitContext, 0, 10)
	for i := 0; i < 10; i++ {
		waiter := &probeWaitContext{Context: context.Background(), entered: make(chan struct{})}
		waiters = append(waiters, waiter)
		wg.Add(1)
		go func() {
			defer wg.Done()
			if old.probeAlive(waiter) == nil {
				t.Error("应复用失败")
			}
		}()
	}
	for _, waiter := range waiters {
		<-waiter.entered
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("并发探活%d次，期望1", calls.Load())
	}
}

func TestIPLimitDoesNotProbeDuplicates(t *testing.T) {
	g := &Gateway{hub: NewHub(), opts: Options{LimitSettings: moderation.NewLimitSettings(nil, 1, nil)}}
	for id := byte(1); id <= 3; id++ {
		old := admissionConn(id, "a")
		old.ping = func(context.Context) error { t.Error("IP超限不得探活"); return nil }
		if ok, _ := g.admit(context.Background(), old); !ok {
			t.Fatal("初始连接应允许")
		}
	}
	if ok, reason := g.admit(context.Background(), admissionConn(4, "a")); ok || reason != "ip_conn_limit" {
		t.Fatalf("IP满时应直接拒绝: %v %s", ok, reason)
	}
}
