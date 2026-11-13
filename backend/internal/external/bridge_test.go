package external

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeSession struct {
	mu                              sync.Mutex
	starts, cancels, leaves, closes int
	texts                           []string
	err                             error
}

func (s *fakeSession) startMatch() error  { s.mu.Lock(); defer s.mu.Unlock(); s.starts++; return s.err }
func (s *fakeSession) cancelMatch() error { s.mu.Lock(); defer s.mu.Unlock(); s.cancels++; return nil }
func (s *fakeSession) leaveRoom() error   { s.mu.Lock(); defer s.mu.Unlock(); s.leaves++; return nil }
func (s *fakeSession) sendText(_, _, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.texts = append(s.texts, text)
	return s.err
}
func (s *fakeSession) sendMedia(_, _, _, _ string) error { return errors.New("P5") }
func (s *fakeSession) selfID() string                    { return "self" }
func (s *fakeSession) close()                            { s.mu.Lock(); s.closes++; s.mu.Unlock() }

type fakeProvider struct {
	name        string
	openFn      func(*link) (session, error)
	availableFn func(pgtype.UUID) (bool, string)
	opens       atomic.Int32
}

func (p *fakeProvider) Name() string { return p.name }
func (p *fakeProvider) available(id pgtype.UUID) (bool, string) {
	if p.availableFn != nil {
		return p.availableFn(id)
	}
	return true, ""
}
func (p *fakeProvider) open(_ context.Context, l *link) (session, error) {
	p.opens.Add(1)
	if p.openFn != nil {
		return p.openFn(l)
	}
	return &fakeSession{}, nil
}
func bridgeUUID() pgtype.UUID { return pgtype.UUID{Bytes: uuid.New(), Valid: true} }
func bridgeRequest(a uint64) match.ExternalRequest {
	return match.ExternalRequest{Identity: &store.Identity{ID: bridgeUUID()}, Attempt: a}
}
func testBridge(t *testing.T) (*Bridge, *fakeProvider) {
	t.Helper()
	b := NewBridge(BridgeDeps{})
	b.ApplySettings(Settings{Enabled: true, Chat2019Enabled: true, WanfengEnabled: true, Chat2019MaxSessions: 1, WanfengMaxSessions: 1, ExternalMatchTimeout: 10 * time.Second})
	p := &fakeProvider{name: "chat2019"}
	b.providers = []provider{p}
	b.choose = func(int) int { return 0 }
	b.resolve = func(context.Context, string, string, PeerProfile) (match.ExternalPeer, error) {
		return match.ExternalPeer{IdentityID: bridgeUUID()}, nil
	}
	b.loadIdentity = func(_ context.Context, id pgtype.UUID) (store.Identity, error) { return store.Identity{ID: id}, nil }
	b.found = func(_ context.Context, _ pgtype.UUID, _ uint64, _ match.ExternalPeer, h match.ExternalHooks) (pgtype.UUID, error) {
		id := bridgeUUID()
		if !h.Activate(id, func() {}) {
			return pgtype.UUID{}, match.ErrActivationRejected
		}
		return id, nil
	}
	b.send = func(context.Context, conversation.SendInput) (store.Message, bool, error) {
		return store.Message{}, true, nil
	}
	b.end = func(context.Context, pgtype.UUID, string) error { return nil }
	t.Cleanup(b.Close)
	return b, p
}
func startBridge(t *testing.T, b *Bridge, r match.ExternalRequest) *link {
	t.Helper()
	if err := b.StartMatch(context.Background(), r); err != nil {
		t.Fatalf("StartMatch: %v", err)
	}
	b.mu.Lock()
	l := b.links[r.Identity.ID]
	b.mu.Unlock()
	if l == nil {
		t.Fatal("未创建link")
	}
	return l
}
func waitBridge(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("等待Bridge状态超时")
}
func TestBridgeAttemptsCapacityAndCancelledBeforeStart(t *testing.T) {
	b, p := testBridge(t)
	r := bridgeRequest(1)
	if !b.Plan().Enabled {
		t.Error("外站Plan未启用")
	}
	b.CancelMatch(r.Identity.ID, 1)
	if err := b.StartMatch(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if p.opens.Load() != 0 {
		t.Fatal("Cancel先到的Start仍打开连接")
	}
	r.Attempt = 2
	l := startBridge(t, b, r)
	other := bridgeRequest(3)
	if err := b.StartMatch(context.Background(), other); !errors.Is(err, match.ErrNoExternalProvider) {
		t.Fatalf("容量限制: %v", err)
	}
	b.CancelMatch(r.Identity.ID, 1)
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if closed {
		t.Fatal("旧Cancel关闭新代次")
	}
	b.CancelMatch(r.Identity.ID, 2)
	l.mu.Lock()
	closed = l.closed
	l.mu.Unlock()
	if !closed {
		t.Fatal("当前Cancel未关闭link")
	}
	r.Attempt = 3
	startBridge(t, b, r)
	if p.opens.Load() != 2 {
		t.Fatal("匹配取消后的link被复用")
	}
}
func TestBridgeSelectsBeforeReusingAndOwnIdleDoesNotUseCapacity(t *testing.T) {
	b, p := testBridge(t)
	q := &fakeProvider{name: "wanfeng"}
	b.providers = []provider{p, q}
	r := bridgeRequest(1)
	l := startBridge(t, b, r)
	l.mu.Lock()
	l.state = linkIdle
	l.mu.Unlock()
	r.Attempt = 2
	again := startBridge(t, b, r)
	if again != l || p.opens.Load() != 1 {
		t.Fatal("自己的空闲link未在容量满时复用")
	}
	again.mu.Lock()
	again.state = linkIdle
	again.mu.Unlock()
	b.choose = func(int) int { return 1 }
	r.Attempt = 3
	next := startBridge(t, b, r)
	if next.provider.Name() != "wanfeng" {
		t.Fatal("先复用了旧站，未每轮随机选站")
	}
	l.mu.Lock()
	closed := l.closed
	l.mu.Unlock()
	if !closed {
		t.Fatal("另一站闲置link未关闭")
	}
}
func TestBridgeOpenCancellationAndFailureFallback(t *testing.T) {
	b, p := testBridge(t)
	q := &fakeProvider{name: "wanfeng"}
	b.providers = []provider{p, q}
	p.openFn = func(*link) (session, error) { return nil, errors.New("offline") }
	r := bridgeRequest(1)
	l := startBridge(t, b, r)
	if l.provider != q {
		t.Fatal("连接失败未尝试另一站")
	}
	s := b.Status()
	if len(s.Providers) == 0 {
		t.Fatal("没有状态")
	}
	b2, p2 := testBridge(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	p2.openFn = func(*link) (session, error) { close(entered); <-release; return &fakeSession{}, nil }
	req := bridgeRequest(1)
	started := make(chan struct{})
	go func() { _ = b2.StartMatch(context.Background(), req); close(started) }()
	<-entered
	cancelled := make(chan struct{})
	go func() { b2.CancelMatch(req.Identity.ID, 1); close(cancelled) }()
	waitBridge(t, func() bool {
		b2.mu.Lock()
		defer b2.mu.Unlock()
		return b2.attempts[req.Identity.ID].cancelledUpTo == 1
	})
	close(release)
	<-started
	<-cancelled
	b2.mu.Lock()
	left := b2.links[req.Identity.ID]
	b2.mu.Unlock()
	if left != nil {
		t.Fatal("open期间取消未生效")
	}
}
func matchedFrame() map[string]any {
	return map[string]any{"code": 15, "sel_userid": "peer", "sel_userNikename": "name", "sel_userSex": "女", "sel_userAge": "23", "sel_userAddress": "广东省"}
}
func textFrame(from, tid, body string) map[string]any {
	return map[string]any{"code": 7, "fromuser": map[string]any{"id": from}, "tid": tid, "content": body}
}
func TestBridgeActivationGateAndInboundOrdering(t *testing.T) {
	b, _ := testBridge(t)
	r := bridgeRequest(1)
	l := startBridge(t, b, r)
	published := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var received []conversation.SendInput
	b.found = func(_ context.Context, _ pgtype.UUID, _ uint64, _ match.ExternalPeer, h match.ExternalHooks) (pgtype.UUID, error) {
		id := bridgeUUID()
		if !h.Activate(id, func() { close(published) }) {
			return pgtype.UUID{}, match.ErrActivationRejected
		}
		<-release
		return id, nil
	}
	b.send = func(_ context.Context, in conversation.SendInput) (store.Message, bool, error) {
		mu.Lock()
		received = append(received, in)
		mu.Unlock()
		return store.Message{}, true, nil
	}
	b.emitFrame(l, matchedFrame())
	<-published
	for i := range 52 {
		b.emitFrame(l, textFrame("peer", fmt.Sprint(i), fmt.Sprint(i)))
	}
	waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.pending) == 50 })
	mu.Lock()
	n := len(received)
	mu.Unlock()
	if n != 0 {
		t.Fatal("ExternalFound返回前打开闸门")
	}
	close(release)
	waitBridge(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(received) == 50 })
	mu.Lock()
	defer mu.Unlock()
	for i, in := range received {
		if in.Body != fmt.Sprint(i) || !in.SkipRateLimit || in.Identity.ID != l.peerIdent {
			t.Fatalf("顺序/发送身份错误: %+v", in)
		}
	}
}
func TestBridgeRejectsActivationOnEveryClosePath(t *testing.T) {
	for _, mode := range []string{"lost", "settings", "close"} {
		t.Run(mode, func(t *testing.T) {
			b, _ := testBridge(t)
			l := startBridge(t, b, bridgeRequest(1))
			hooks := make(chan match.ExternalHooks, 1)
			release := make(chan struct{})
			done := make(chan bool, 1)
			b.found = func(_ context.Context, _ pgtype.UUID, _ uint64, _ match.ExternalPeer, h match.ExternalHooks) (pgtype.UUID, error) {
				hooks <- h
				<-release
				activated := h.Activate(bridgeUUID(), func() { t.Error("已关闭link发布了found") })
				done <- activated
				return pgtype.UUID{}, match.ErrActivationRejected
			}
			b.emitFrame(l, matchedFrame())
			<-hooks
			finished := make(chan struct{})
			go func() {
				switch mode {
				case "lost":
					b.connectionLost(l, errors.New("lost"))
				case "settings":
					b.ApplySettings(Settings{})
				case "close":
					b.Close()
				}
				close(finished)
			}()
			waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.closed })
			close(release)
			if <-done {
				t.Fatal("失效激活未拒绝")
			}
			<-finished
		})
	}
}
func TestBridgeEchoWrongPeerSystemNoticeAndProviderLeave(t *testing.T) {
	for _, name := range []string{"chat2019", "wanfeng"} {
		t.Run(name, func(t *testing.T) {
			b, p := testBridge(t)
			p.name = name
			l := startBridge(t, b, bridgeRequest(1))
			var count atomic.Int32
			var ended atomic.Int32
			b.send = func(context.Context, conversation.SendInput) (store.Message, bool, error) {
				count.Add(1)
				return store.Message{}, true, nil
			}
			b.end = func(context.Context, pgtype.UUID, string) error { ended.Add(1); return nil }
			b.emitFrame(l, matchedFrame())
			waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.gateOpen })
			sum := md5.Sum([]byte("self"))
			b.emitFrame(l, textFrame(hex.EncodeToString(sum[:]), "echo", "echo"))
			b.emitFrame(l, textFrame("stranger", "other", "other"))
			b.emitFrame(l, textFrame("peer", "one", "hello"))
			b.emitFrame(l, map[string]any{"code": 0, "content": "对方发送的图片加载失败"})
			b.emitFrame(l, map[string]any{"code": 16})
			want := int32(1)
			if name == "wanfeng" {
				want = 2
			}
			waitBridge(t, func() bool { return count.Load() == want })
			if name == "wanfeng" {
				waitBridge(t, func() bool { return ended.Load() == 1 })
			} else {
				time.Sleep(10 * time.Millisecond)
				if ended.Load() != 0 {
					t.Fatal("chat2019错误处理code16")
				}
			}
		})
	}
}
func TestBridgeOutboundVerifiesStoredSenderAndConversation(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	b.emitFrame(l, matchedFrame())
	waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.gateOpen })
	l.mu.Lock()
	conv := l.convID
	l.mu.Unlock()
	b.loadRelay = func(_ context.Context, id int64) (store.GetMessageForRelayRow, error) {
		row := store.GetMessageForRelayRow{ConversationID: conv, SenderIdentityID: l.userID, Kind: "text", Body: "[x.jpg]"}
		if id == 1 {
			row.ConversationID = bridgeUUID()
		}
		if id == 2 {
			row.SenderIdentityID = bridgeUUID()
		}
		return row, nil
	}
	for _, id := range []int64{1, 2, 3} {
		b.OnMessage(conversation.MessageEvent{ConversationID: conv, SenderIdentityID: l.userID, MessageID: id})
	}
	s := l.sess.(*fakeSession)
	waitBridge(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.texts) == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.texts[0] != "[x.jpg]" {
		t.Fatal("正文被媒体解析")
	}
}

func TestBridgeFailureRequeueBlockedPeerAndCooldowns(t *testing.T) {
	b, p := testBridge(t)
	r := bridgeRequest(1)
	blocked := bridgeUUID()
	r.Context.Blocked = map[pgtype.UUID]struct{}{blocked: {}}
	b.resolve = func(context.Context, string, string, PeerProfile) (match.ExternalPeer, error) {
		return match.ExternalPeer{IdentityID: blocked}, nil
	}
	l := startBridge(t, b, r)
	b.emitFrame(l, matchedFrame())
	s := l.sess.(*fakeSession)
	waitBridge(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.starts == 2 && s.leaves == 1 })
	l.mu.Lock()
	if l.state != linkMatching {
		t.Error("屏蔽对象改变匹配阶段")
	}
	l.mu.Unlock()
	b.closeLink(l, false)
	for _, tc := range []struct {
		err      error
		delay    time.Duration
		identity bool
	}{{errors.New("offline"), time.Minute, false}, {ErrWanfengHandshake, 10 * time.Minute, false}, {chat2019.ErrForceout, 90 * time.Second, true}, {ErrIdentityReset, 10 * time.Minute, true}} {
		fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		b.now = func() time.Time { return fixed }
		b.cooldown(l, tc.err)
		b.mu.Lock()
		got := b.stats[p.Name()].CooldownUntil
		if tc.identity {
			got = b.identityCooldown[p.Name()][l.userID]
		}
		b.mu.Unlock()
		if !got.Equal(fixed.Add(tc.delay)) {
			t.Fatalf("冷却=%v want=%v", got, fixed.Add(tc.delay))
		}
	}
	b2, _ := testBridge(t)
	b2.found = func(context.Context, pgtype.UUID, uint64, match.ExternalPeer, match.ExternalHooks) (pgtype.UUID, error) {
		return pgtype.UUID{}, errors.New("db error")
	}
	l2 := startBridge(t, b2, bridgeRequest(1))
	b2.emitFrame(l2, matchedFrame())
	waitBridge(t, func() bool { l2.mu.Lock(); defer l2.mu.Unlock(); return l2.state == linkIdle })
}
func TestBridgeNonExternalMessageDoesNotReadDatabase(t *testing.T) {
	b, _ := testBridge(t)
	b.loadRelay = func(context.Context, int64) (store.GetMessageForRelayRow, error) {
		t.Error("非外站会话查库")
		return store.GetMessageForRelayRow{}, nil
	}
	b.OnMessage(conversation.MessageEvent{ConversationID: bridgeUUID(), MessageID: 123})
}
func TestBridgeDisconnectInvalidatesActivationImmediately(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	hooks := make(chan match.ExternalHooks, 1)
	done := make(chan struct{})
	b.found = func(_ context.Context, _ pgtype.UUID, _ uint64, _ match.ExternalPeer, h match.ExternalHooks) (pgtype.UUID, error) {
		hooks <- h
		<-done
		return pgtype.UUID{}, match.ErrActivationRejected
	}
	b.emitFrame(l, matchedFrame())
	h := <-hooks
	// Simulate disconnect delivered while a link operation is holding its lock.
	l.mu.Lock()
	b.connectionLost(l, errors.New("connection lost"))
	l.mu.Unlock()
	if h.Activate(bridgeUUID(), func() { t.Error("断线已送达仍发布found") }) {
		t.Error("断线未立即作废激活")
	}
	close(done)
}

func TestBridgePublishedBeforeConcurrentHotClose(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	entered := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	var events []string
	b.found = func(_ context.Context, _ pgtype.UUID, _ uint64, _ match.ExternalPeer, h match.ExternalHooks) (pgtype.UUID, error) {
		id := bridgeUUID()
		if !h.Activate(id, func() { close(entered); <-release; mu.Lock(); events = append(events, "found"); mu.Unlock() }) {
			return pgtype.UUID{}, match.ErrActivationRejected
		}
		return id, nil
	}
	b.end = func(context.Context, pgtype.UUID, string) error {
		mu.Lock()
		events = append(events, "ended")
		mu.Unlock()
		return nil
	}
	b.emitFrame(l, matchedFrame())
	<-entered
	done := make(chan struct{})
	go func() { b.ApplySettings(Settings{}); close(done) }()
	close(release)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(events) != "[found ended]" {
		t.Fatalf("事件顺序=%v", events)
	}
}
func TestBridgeReapsInactiveAttemptAndRetainsActive(t *testing.T) {
	b, _ := testBridge(t)
	r := bridgeRequest(1)
	l := startBridge(t, b, r)
	old := bridgeUUID()
	b.mu.Lock()
	b.attempts[old] = &intent{touched: time.Now().Add(-2 * time.Hour), cancelledUpTo: 9}
	b.mu.Unlock()
	next := bridgeRequest(2)
	_ = b.StartMatch(context.Background(), next)
	b.mu.Lock()
	_, left := b.attempts[old]
	_, active := b.attempts[l.userID]
	b.mu.Unlock()
	if left || !active {
		t.Fatalf("清理错误 inactive=%v active=%v", left, active)
	}
}

func TestBridgeNewAttemptReplacesOldMatchingLinkAtCapacity(t *testing.T) {
	b, p := testBridge(t)
	r := bridgeRequest(1)
	old := startBridge(t, b, r)
	r.Attempt = 2
	next := startBridge(t, b, r)
	if next == old || p.opens.Load() != 2 {
		t.Fatal("新代次复用了旧匹配连接")
	}
	b.CancelMatch(r.Identity.ID, 1)
	next.mu.Lock()
	defer next.mu.Unlock()
	if next.closed {
		t.Fatal("旧取消关闭新连接")
	}
}
