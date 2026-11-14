package external

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type BridgeDeps struct {
	Pool               *pgxpool.Pool
	Settings           *SettingsCache
	Match              *match.Service
	Conv               *conversation.Service
	Media              *media.Service
	Hub                realtime.Realtime
	EncKey             []byte
	Chat2019           chat2019.Config
	Wanfeng            *wanfeng.APIClient
	WanfengDialer      *websocket.Dialer
	WanfengMediaHosts  []string
	MediaMaxBytes      int64
	NewIdentityPerHour int
	Logger             *slog.Logger
	// onClose 在 Bridge.Close 收尾时执行一次（例如关闭晚风 SCF 隧道）。
	onClose func()
}
type provider interface {
	Name() string
	available(pgtype.UUID) (bool, string)
	open(context.Context, *link) (session, error)
}
type session interface {
	startMatch() error
	cancelMatch() error
	leaveRoom() error
	sendText(string, string, string) error
	sendMedia(string, string, string, string) error
	selfID() string
	close()
}
type linkState int

const (
	linkMatching linkState = iota
	linkActivating
	linkInRoom
	linkIdle
)

type inboundEvent struct {
	frame    map[string]any
	openGate bool
	attempt  uint64
}
type outboundItem struct {
	id     int64
	convID pgtype.UUID
}
type link struct {
	stopping                      atomic.Bool
	mu                            sync.Mutex
	userID                        pgtype.UUID
	provider                      provider
	sess                          session
	state                         linkState
	matchStartedAt                time.Time
	closed                        bool
	attempt                       uint64
	req                           match.ExternalRequest
	convID                        pgtype.UUID
	peerUID, peerNick             string
	peerIdent                     pgtype.UUID
	peer                          *store.Identity
	gateOpen                      bool
	pending                       []map[string]any
	inboundTokens, outboundTokens map[string]pgtype.UUID
	inbox                         chan inboundEvent
	outbox                        chan outboundItem
	done                          chan struct{}
	mediaCtx                      context.Context
	stopMedia                     context.CancelFunc
	idleTimer                     *time.Timer
}
type intent struct {
	latestAttempt, cancelledUpTo uint64
	touched                      time.Time
	op                           sync.Mutex
}
type Counters struct {
	Started        uint64 `json:"started"`
	Found          uint64 `json:"found"`
	Cancelled      uint64 `json:"cancelled"`
	RelayedIn      uint64 `json:"relayedIn"`
	RelayedOut     uint64 `json:"relayedOut"`
	MediaFailed    uint64 `json:"mediaFailed"`
	TextFailed     uint64 `json:"textFailed"`
	PendingDropped uint64 `json:"pendingDropped"`
}
type ProviderStatus struct {
	Name          string    `json:"name"`
	Enabled       bool      `json:"enabled"`
	Available     bool      `json:"available"`
	Reason        string    `json:"reason"`
	Links         int       `json:"links"`
	Matching      int       `json:"matching"`
	InRoom        int       `json:"inRoom"`
	Idle          int       `json:"idle"`
	CooldownUntil time.Time `json:"cooldownUntil"`
	LastError     string    `json:"lastError"`
	LastErrorAt   time.Time `json:"lastErrorAt"`
	Counters      Counters  `json:"counters"`
}
type StatusSnapshot struct {
	Providers []ProviderStatus `json:"providers"`
	Online    int              `json:"online"`
	Queue     int              `json:"queue"`
}
type Bridge struct {
	chatMedia *chat2019.Media
	// onClose 是可选的收尾函数，在 Close 完成所有链接清理后调用一次。
	onClose          func()
	wg               sync.WaitGroup
	deps             BridgeDeps
	bindings         *BindingStore
	peers            *PeerStore
	limiter          *IdentityLimiter
	providers        []provider
	mu               sync.Mutex // Never acquire link.mu while holding this mutex.
	settings         Settings
	closed           bool
	links            map[pgtype.UUID]*link
	convs            map[pgtype.UUID]*link
	attempts         map[pgtype.UUID]*intent
	stats            map[string]ProviderStatus
	identityCooldown map[string]map[pgtype.UUID]time.Time
	now              func() time.Time
	choose           func(int) int
	resolve          func(context.Context, string, string, PeerProfile) (match.ExternalPeer, error)
	found            func(context.Context, pgtype.UUID, uint64, match.ExternalPeer, match.ExternalHooks) (pgtype.UUID, error)
	send             func(context.Context, conversation.SendInput) (store.Message, bool, error)
	end              func(context.Context, pgtype.UUID, string) error
	loadIdentity     func(context.Context, pgtype.UUID) (store.Identity, error)
	loadRelay        func(context.Context, int64) (store.GetMessageForRelayRow, error)
}

func NewBridge(d BridgeDeps) *Bridge {
	d.Logger = externalLogger(d.Logger)
	b := &Bridge{deps: d, onClose: d.onClose, bindings: NewBindingStore(d.Pool, d.EncKey), peers: NewPeerStore(d.Pool), limiter: NewIdentityLimiter(d.NewIdentityPerHour), links: make(map[pgtype.UUID]*link), convs: make(map[pgtype.UUID]*link), attempts: make(map[pgtype.UUID]*intent), stats: make(map[string]ProviderStatus), identityCooldown: make(map[string]map[pgtype.UUID]time.Time), now: time.Now, choose: rand.IntN}
	b.chatMedia = chat2019.NewMedia(d.Chat2019, d.MediaMaxBytes)
	if d.Settings != nil {
		b.settings = d.Settings.Plan()
	}
	b.providers = []provider{&chat2019Provider{b}, &wanfengProvider{b}}
	b.resolve = b.peers.Resolve
	q := store.New(d.Pool)
	b.loadIdentity = q.GetIdentityByID
	b.loadRelay = q.GetMessageForRelay
	if d.Match != nil {
		b.found = d.Match.ExternalFound
	}
	if d.Conv != nil {
		b.send = d.Conv.SendAs
		b.end = d.Conv.End
	}
	return b
}
func (b *Bridge) Plan() match.ExternalPlan {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.settings
	return match.ExternalPlan{Enabled: s.Enabled && !b.closed, OnlineThreshold: s.OnlineThreshold, LocalWait: s.LocalWait, MatchTimeout: s.ExternalMatchTimeout}
}
func (b *Bridge) providerEnabled(name string) bool {
	return b.settings.Enabled && ((name == "chat2019" && b.settings.Chat2019Enabled) || (name == "wanfeng" && b.settings.WanfengEnabled))
}

// validAttempt is called with b.mu held. No I/O takes place under b.mu.
func (b *Bridge) validAttempt(r match.ExternalRequest) bool {
	a := b.attempts[r.Identity.ID]
	return !b.closed && a != nil && r.Attempt == a.latestAttempt && r.Attempt > a.cancelledUpTo
}
func (b *Bridge) capacity(p provider, id pgtype.UUID, reuse bool) bool {
	if b.closed || !b.providerEnabled(p.Name()) || b.now().Before(b.stats[p.Name()].CooldownUntil) || b.now().Before(b.identityCooldown[p.Name()][id]) {
		return false
	}
	if reuse {
		return true
	}
	limit := b.settings.Chat2019MaxSessions
	if p.Name() == "wanfeng" {
		limit = b.settings.WanfengMaxSessions
	}
	n := 0
	for _, l := range b.links {
		if l.provider.Name() == p.Name() {
			n++
		}
	}
	return n < limit
}
func (b *Bridge) StartMatch(ctx context.Context, r match.ExternalRequest) error {
	if r.Identity == nil {
		return match.ErrNoExternalProvider
	}
	b.mu.Lock()
	now := b.now()
	for id, a := range b.attempts {
		if b.links[id] == nil && now.Sub(a.touched) > time.Hour {
			delete(b.attempts, id)
		}
	}
	a := b.attempts[r.Identity.ID]
	if a == nil {
		a = &intent{}
		b.attempts[r.Identity.ID] = a
	}
	if r.Attempt <= a.cancelledUpTo || r.Attempt < a.latestAttempt || b.closed {
		b.mu.Unlock()
		return nil
	}
	a.latestAttempt = r.Attempt
	a.touched = now
	b.mu.Unlock()
	a.op.Lock()
	defer a.op.Unlock()
	b.mu.Lock()
	valid := b.validAttempt(r)
	b.mu.Unlock()
	if !valid {
		return nil
	}
	// A newer attempt supersedes a matching connection even at the station limit.
	// It cannot reuse that socket because late results carry no upstream attempt ID.
	b.mu.Lock()
	previous := b.links[r.Identity.ID]
	b.mu.Unlock()
	if previous != nil {
		previous.mu.Lock()
		obsolete := !previous.closed && previous.attempt < r.Attempt && previous.state != linkIdle
		previous.mu.Unlock()
		if obsolete {
			b.closeLink(previous, true)
		}
	}
	n := len(b.providers)
	if n == 0 {
		return match.ErrNoExternalProvider
	}
	first := b.choose(n)
	for i := 0; i < n; i++ {
		p := b.providers[(first+i)%n]
		if ok, _ := p.available(r.Identity.ID); !ok {
			continue
		}
		b.mu.Lock()
		old := b.links[r.Identity.ID]
		b.mu.Unlock()
		if old != nil {
			old.mu.Lock()
			if !old.closed && !old.stopping.Load() && old.attempt == r.Attempt && old.state != linkIdle {
				old.mu.Unlock()
				return nil
			}
			reuse := !old.closed && !old.stopping.Load() && old.state == linkIdle && old.provider.Name() == p.Name()
			b.mu.Lock()
			allowed := b.validAttempt(r) && b.capacity(p, r.Identity.ID, reuse)
			b.mu.Unlock()
			if reuse && allowed {
				if old.idleTimer != nil {
					old.idleTimer.Stop()
				}
				old.req = r
				old.attempt = r.Attempt
				old.state = linkMatching
				old.matchStartedAt = b.now()
				old.gateOpen = false
				old.pending = nil
				err := old.sess.startMatch()
				old.mu.Unlock()
				if err == nil {
					b.count(p.Name(), func(c *Counters) { c.Started++ })
					b.deps.Logger.Info("外站开始匹配", "provider", p.Name(), "attempt", r.Attempt)
					return nil
				}
				b.closeLink(old, false)
				b.cooldown(old, err)
				continue
			}
			old.mu.Unlock()
			if !allowed {
				continue
			}
			b.closeLink(old, false)
		}
		mediaCtx, stopMedia := context.WithCancel(context.Background())
		l := &link{userID: r.Identity.ID, provider: p, req: r, attempt: r.Attempt, state: linkMatching, matchStartedAt: b.now(), inbox: make(chan inboundEvent, 128), outbox: make(chan outboundItem, 128), done: make(chan struct{}), inboundTokens: make(map[string]pgtype.UUID), outboundTokens: make(map[string]pgtype.UUID), mediaCtx: mediaCtx, stopMedia: stopMedia}
		l.mu.Lock()
		b.mu.Lock()
		valid = b.validAttempt(r)
		if !valid || !b.capacity(p, r.Identity.ID, false) {
			b.mu.Unlock()
			l.mu.Unlock()
			stopMedia()
			if !valid {
				return nil
			}
			continue
		}
		b.links[l.userID] = l
		b.wg.Add(2)
		b.mu.Unlock()
		go b.inboundLoop(l)
		go b.outboundLoop(l)
		sess, err := p.open(ctx, l)
		l.sess = sess
		b.mu.Lock()
		valid = b.validAttempt(r) && b.providerEnabled(p.Name())
		b.mu.Unlock()
		valid = valid && !l.stopping.Load()
		if err == nil && valid {
			err = sess.startMatch()
		}
		l.mu.Unlock()
		if !valid {
			b.closeLink(l, true)
			return nil
		}
		if err != nil {
			b.closeLink(l, false)
			b.cooldown(l, err)
			continue
		}
		b.count(p.Name(), func(c *Counters) { c.Started++ })
		b.deps.Logger.Info("外站开始匹配", "provider", p.Name(), "attempt", r.Attempt)
		return nil
	}
	return match.ErrNoExternalProvider
}
func (b *Bridge) CancelMatch(id pgtype.UUID, attempt uint64) {
	b.mu.Lock()
	a := b.attempts[id]
	if a == nil {
		a = &intent{}
		b.attempts[id] = a
	}
	a.cancelledUpTo = max(a.cancelledUpTo, attempt)
	a.touched = b.now()
	l := b.links[id]
	b.mu.Unlock()
	a.op.Lock()
	defer a.op.Unlock()
	if l == nil {
		return
	}
	l.mu.Lock()
	cancel := !l.closed && l.attempt == attempt && l.state == linkMatching
	startedAt := l.matchStartedAt
	l.mu.Unlock()
	if cancel && b.closeLink(l, true) {
		b.count(l.provider.Name(), func(c *Counters) { c.Cancelled++ })
		// 该入口同时承载匹配窗口超时与用户取消，无法区分时统一记为 cancelled。
		b.deps.Logger.Info("外站匹配取消", "provider", l.provider.Name(), "reason", "cancelled", "elapsed", time.Since(startedAt))
	}
}
func (b *Bridge) count(name string, fn func(*Counters)) {
	b.mu.Lock()
	s := b.stats[name]
	fn(&s.Counters)
	b.stats[name] = s
	b.mu.Unlock()
}

// closeLink invalidates activation while holding link.mu, before any external effect.
func (b *Bridge) closeLink(l *link, cancelMatching bool) bool {
	// Cancel I/O before waiting for the lock held by a media relay.
	if l.stopMedia != nil {
		l.stopMedia()
	}
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return false
	}
	l.stopping.Store(true)
	l.closed = true
	if l.idleTimer != nil {
		l.idleTimer.Stop()
	}
	close(l.done)
	sess, conv, state := l.sess, l.convID, l.state
	l.pending = nil
	l.gateOpen = false
	l.mu.Unlock()
	b.mu.Lock()
	if b.links[l.userID] == l {
		delete(b.links, l.userID)
	}
	if b.convs[conv] == l {
		delete(b.convs, conv)
	}
	b.mu.Unlock()
	if sess != nil {
		if cancelMatching && state == linkMatching {
			_ = sess.cancelMatch()
		}
		sess.close()
	}
	if conv.Valid && b.end != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.end(ctx, conv, "peer_left"); err != nil {
			b.deps.Logger.Error("结束外站会话失败", "error", err)
		}
	}
	return true
}
func (b *Bridge) connectionLost(l *link, err error) {
	// Callbacks can run synchronously inside a session operation while link.mu is
	// held. Latch invalidation immediately; cleanup acquires the link lock and
	// marks it closed before touching the network or ending a conversation.
	first := l.stopping.CompareAndSwap(false, true)
	cooled := false
	if first || errors.Is(err, ErrIdentityReset) || errors.Is(err, chat2019.ErrForceout) || errors.Is(err, ErrWanfengHandshake) || errors.Is(err, wanfeng.ErrIPBanSwitched) || (err != nil && wanfeng.IsIPBanMessage(err.Error())) {
		cooled = err != nil && !errors.Is(err, ErrNewIdentityLimited)
		b.cooldown(l, err)
	}
	if !cooled && err != nil {
		// cooldown 已为带分类错误的断线记录过 Warn("外站连接失败")，这里只补记其余断线，避免重复。
		b.deps.Logger.Warn("外站连接断开", "provider", l.provider.Name(), "reason", "connection_lost", "error", err)
	}
	if first {
		b.runAsync(func() { b.closeLink(l, false) })
	}
}
func (b *Bridge) cooldown(l *link, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, ErrNewIdentityLimited) {
		b.deps.Logger.Info("外站新身份限流", "provider", l.provider.Name())
		return
	}
	b.mu.Lock()
	now := b.now()
	name := l.provider.Name()
	s := b.stats[name]
	// Store categories only: upstream error strings can contain credentials or message bodies.
	previous := s
	s.LastError = "connection_failed"
	s.LastErrorAt = now
	switch {
	case errors.Is(err, chat2019.ErrForceout) && name == "chat2019":
		if b.identityCooldown[name] == nil {
			b.identityCooldown[name] = make(map[pgtype.UUID]time.Time)
		}
		b.identityCooldown[name][l.userID] = now.Add(90 * time.Second)
		s.LastError = "forceout"
	case errors.Is(err, ErrIdentityReset):
		if b.identityCooldown[name] == nil {
			b.identityCooldown[name] = make(map[pgtype.UUID]time.Time)
		}
		b.identityCooldown[name][l.userID] = now.Add(10 * time.Minute)
		s.LastError = "identity_invalid"
	case wanfeng.IsIPBanMessage(err.Error()) || errors.Is(err, wanfeng.ErrIPBanSwitched) || errors.Is(err, ErrWanfengHandshake):
		s.CooldownUntil = now.Add(10 * time.Minute)
		s.LastError = "handshake_or_ip_ban"
	default:
		if until := now.Add(time.Minute); until.After(s.CooldownUntil) {
			s.CooldownUntil = until
		}
		if previous.LastError == "identity_invalid" || previous.LastError == "forceout" || previous.LastError == "handshake_or_ip_ban" {
			s.LastError = previous.LastError
		}
	}
	b.stats[name] = s
	b.mu.Unlock()
	// 只记录分类结果；error 由 externalLogger 折叠为类型名，绝不落上游错误原文。
	b.deps.Logger.Warn("外站连接失败", "provider", name, "reason", s.LastError, "error", err)
}
func (b *Bridge) ApplySettings(s Settings) {
	b.mu.Lock()
	b.settings = s
	ls := make([]*link, 0, len(b.links))
	for _, l := range b.links {
		if !b.providerEnabled(l.provider.Name()) {
			l.stopping.Store(true)
			ls = append(ls, l)
		}
	}
	b.mu.Unlock()
	for _, l := range ls {
		b.closeLink(l, true)
	}
}
func (b *Bridge) Close() {
	b.mu.Lock()
	b.closed = true
	ls := make([]*link, 0, len(b.links))
	for _, l := range b.links {
		l.stopping.Store(true)
		ls = append(ls, l)
	}
	b.mu.Unlock()
	for _, l := range ls {
		b.closeLink(l, true)
	}
	b.wg.Wait()
	// 隧道等外部资源必须在所有链接停用之后再关闭；重复调用 Close 只执行一次。
	b.mu.Lock()
	onClose := b.onClose
	b.onClose = nil
	b.mu.Unlock()
	if onClose != nil {
		onClose()
	}
}
func (b *Bridge) Status() StatusSnapshot {
	b.mu.Lock()
	out := StatusSnapshot{Providers: make([]ProviderStatus, 0, len(b.providers))}
	ls := make([]*link, 0, len(b.links))
	for _, p := range b.providers {
		s := b.stats[p.Name()]
		s.Name = p.Name()
		s.Enabled = b.providerEnabled(p.Name()) && !b.closed
		s.Available = s.Enabled && !b.now().Before(s.CooldownUntil)
		if !s.Enabled {
			s.Reason = "disabled"
		} else if !s.Available {
			s.Reason = "cooldown"
		}
		out.Providers = append(out.Providers, s)
	}
	for _, l := range b.links {
		ls = append(ls, l)
	}
	b.mu.Unlock()
	for i, p := range b.providers {
		if out.Providers[i].Available {
			ok, reason := p.available(pgtype.UUID{})
			out.Providers[i].Available = ok
			out.Providers[i].Reason = reason
		}
	}
	for _, l := range ls {
		l.mu.Lock()
		if !l.closed {
			for i := range out.Providers {
				if out.Providers[i].Name == l.provider.Name() {
					s := &out.Providers[i]
					s.Links++
					switch l.state {
					case linkMatching:
						s.Matching++
					case linkInRoom:
						s.InRoom++
					case linkIdle:
						s.Idle++
					}
				}
			}
		}
		l.mu.Unlock()
	}
	if b.deps.Hub != nil {
		out.Online = b.deps.Hub.OnlineCount()
	}
	if b.deps.Match != nil {
		out.Queue = b.deps.Match.QueueLen()
	}
	return out
}
func (b *Bridge) commandFailed(l *link, err error, isMedia bool) {
	b.deps.Logger.Warn("external command failed", "provider", l.provider.Name(), "media", isMedia, "error", err)
	b.count(l.provider.Name(), func(c *Counters) {
		if isMedia {
			c.MediaFailed++
		} else {
			c.TextFailed++
		}
	})
	if !isMedia {
		return
	}
	if b.deps.Hub != nil {
		env, e := realtime.NewEnvelope(realtime.TypeError, "", map[string]string{"code": "media_rejected", "message": "对方站点暂不支持该媒体，发送失败"})
		if e == nil {
			b.deps.Hub.SendToIdentity(l.userID, env)
		}
	}
}

// runAsync accounts for callbacks which may finish conversations during shutdown.
func (b *Bridge) runAsync(fn func()) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.wg.Add(1)
	b.mu.Unlock()
	go func() { defer b.wg.Done(); fn() }()
}
