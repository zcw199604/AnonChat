package external

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/jackc/pgx/v5/pgtype"
)

// These are retrospective coverage tests for existing behavior (TDD-EXEMPT).
func TestBridgeSkipsUnavailableProvidersAndRejectsAllUnavailable(t *testing.T) {
	for _, bothUnavailable := range []bool{false, true} {
		t.Run(map[bool]string{false: "skip-first", true: "both-unavailable"}[bothUnavailable], func(t *testing.T) {
			b, p := testBridge(t)
			q := &fakeProvider{name: "wanfeng"}
			b.providers = []provider{p, q}
			p.availableFn = func(pgtype.UUID) (bool, string) { return false, "unavailable" }
			q.availableFn = func(pgtype.UUID) (bool, string) { return !bothUnavailable, "unavailable" }
			r := bridgeRequest(1)
			err := b.StartMatch(context.Background(), r)
			if bothUnavailable {
				if !errors.Is(err, match.ErrNoExternalProvider) || p.opens.Load() != 0 || q.opens.Load() != 0 {
					t.Fatalf("all unavailable: err=%v opens=%d/%d", err, p.opens.Load(), q.opens.Load())
				}
				return
			}
			if err != nil || p.opens.Load() != 0 || q.opens.Load() != 1 {
				t.Fatalf("skip unavailable: err=%v opens=%d/%d", err, p.opens.Load(), q.opens.Load())
			}
		})
	}
}

func TestBridgeConversationEndedLeavesAndReusesIdleSession(t *testing.T) {
	b, p := testBridge(t)
	r := bridgeRequest(1)
	l := startBridge(t, b, r)
	b.emitFrame(l, matchedFrame())
	waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.gateOpen })
	l.mu.Lock()
	conv := l.convID
	l.mu.Unlock()
	b.OnConversationEnded(conv)
	waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.state == linkIdle })
	s := l.sess.(*fakeSession)
	s.mu.Lock()
	if s.leaves != 1 || s.closes != 0 {
		t.Errorf("end should leave without closing: leaves=%d closes=%d", s.leaves, s.closes)
	}
	s.mu.Unlock()
	b.mu.Lock()
	_, mapped := b.convs[conv]
	b.mu.Unlock()
	if mapped {
		t.Fatal("ended conversation remained mapped")
	}
	r.Attempt = 2
	if next := startBridge(t, b, r); next != l || p.opens.Load() != 1 {
		t.Fatal("session left by conversation end was not reused")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.starts != 2 || s.leaves != 1 || s.closes != 0 {
		t.Fatalf("reuse operations: starts=%d leaves=%d closes=%d", s.starts, s.leaves, s.closes)
	}
}

func TestBridgeIPBanCooldownRejectsUntilExpiry(t *testing.T) {
	b, _ := testBridge(t)
	fixed := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	b.now = func() time.Time { return fixed }
	l := startBridge(t, b, bridgeRequest(1))
	b.closeLink(l, false)
	b.cooldown(l, errors.New("您的 IP 已被封禁"))
	status := b.Status().Providers[0]
	if status.Available || status.Reason != "cooldown" || status.LastError != "handshake_or_ip_ban" || !status.CooldownUntil.Equal(fixed.Add(10*time.Minute)) || !status.LastErrorAt.Equal(fixed) {
		t.Fatalf("IP cooldown status=%+v", status)
	}
	if err := b.StartMatch(context.Background(), bridgeRequest(2)); !errors.Is(err, match.ErrNoExternalProvider) {
		t.Fatalf("IP-banned station was still selected: %v", err)
	}
	fixed = fixed.Add(10*time.Minute + time.Second)
	startBridge(t, b, bridgeRequest(3))
}

func TestBridgeLowerCapacityKeepsExistingSessions(t *testing.T) {
	b, p := testBridge(t)
	b.mu.Lock()
	s := b.settings
	b.mu.Unlock()
	s.Chat2019MaxSessions = 2
	b.ApplySettings(s)
	first := startBridge(t, b, bridgeRequest(1))
	second := startBridge(t, b, bridgeRequest(2))
	s.Chat2019MaxSessions = 1
	b.ApplySettings(s)
	for _, l := range []*link{first, second} {
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed {
			t.Fatal("lower capacity closed an existing session")
		}
	}
	if err := b.StartMatch(context.Background(), bridgeRequest(3)); !errors.Is(err, match.ErrNoExternalProvider) || p.opens.Load() != 2 {
		t.Fatalf("lower capacity admitted new session: err=%v opens=%d", err, p.opens.Load())
	}
}

func TestBridgeProviderDisableOnlyClosesThatProvider(t *testing.T) {
	b, p := testBridge(t)
	q := &fakeProvider{name: "wanfeng"}
	b.providers = []provider{p, q}
	chat := startBridge(t, b, bridgeRequest(1))
	b.choose = func(int) int { return 1 }
	wf := startBridge(t, b, bridgeRequest(2))
	for _, l := range []*link{chat, wf} {
		b.emitFrame(l, matchedFrame())
		waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.gateOpen })
	}
	var mu sync.Mutex
	var ended []pgtype.UUID
	b.end = func(_ context.Context, id pgtype.UUID, reason string) error {
		mu.Lock()
		defer mu.Unlock()
		ended = append(ended, id)
		if reason != "peer_left" {
			t.Errorf("end reason=%q", reason)
		}
		return nil
	}
	b.mu.Lock()
	s := b.settings
	b.mu.Unlock()
	s.Chat2019Enabled = false
	b.ApplySettings(s)
	chat.mu.Lock()
	chatClosed, chatConv := chat.closed, chat.convID
	chat.mu.Unlock()
	wf.mu.Lock()
	wfClosed := wf.closed
	wf.mu.Unlock()
	if !chatClosed || wfClosed {
		t.Fatalf("disable isolation: chatClosed=%v wfClosed=%v", chatClosed, wfClosed)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ended) != 1 || ended[0] != chatConv {
		t.Fatalf("ended conversations=%v", ended)
	}
}

func TestBridgeIgnoresLateOlderStart(t *testing.T) {
	b, p := testBridge(t)
	r := bridgeRequest(2)
	l := startBridge(t, b, r)
	r.Attempt = 1
	if err := b.StartMatch(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.attempt != 2 || l.closed || p.opens.Load() != 1 {
		t.Fatalf("old start changed current link: attempt=%d closed=%v opens=%d", l.attempt, l.closed, p.opens.Load())
	}
}
