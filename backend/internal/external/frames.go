package external

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (b *Bridge) emitFrame(l *link, f map[string]any) {
	// Backpressure preserves arrival order; disconnecting unblocks a producer.
	select {
	case <-l.done:
		return
	case l.inbox <- inboundEvent{frame: f}:
	}
}
func (b *Bridge) inboundLoop(l *link) {
	defer b.wg.Done()
	for {
		select {
		case <-l.done:
			return
		case ev := <-l.inbox:
			l.mu.Lock()
			if l.closed || l.stopping.Load() {
				l.mu.Unlock()
				return
			}
			if ev.openGate {
				if l.attempt == ev.attempt && l.state == linkInRoom {
					l.gateOpen = true
					pending := l.pending
					l.pending = nil
					for _, f := range pending {
						b.handleFrameLocked(l, f)
					}
				}
			} else {
				code := frameCode(ev.frame)
				if l.state == linkMatching {
					// 只记帧类型码，绝不记录帧内 uid/昵称/正文等内容。
					attrs := []any{"provider", l.provider.Name(), "code", code, "state", int(l.state)}
					if isSystemFrameCode(code) {
						// 系统帧的 content 是服务端提示文字（如 random 被拒的原因），不是对方用户内容，
						// 因此仅在系统 code 时带出，并按 rune 截断上限防止超长内容。
						attrs = append(attrs, "sys_content", truncateRunes(wanfeng.PickString(ev.frame, "content"), maxSysContentRunes))
					}
					b.deps.Logger.Info("外站匹配中收到帧", attrs...)
				}
				if (l.state == linkActivating || (l.state == linkInRoom && !l.gateOpen)) && code != 15 {
					if len(l.pending) < 50 {
						l.pending = append(l.pending, ev.frame)
					} else {
						b.count(l.provider.Name(), func(c *Counters) { c.PendingDropped++ })
					}
				} else {
					b.handleFrameLocked(l, ev.frame)
				}
			}
			l.mu.Unlock()
		}
	}
}
func frameCode(f map[string]any) int {
	switch v := f["code"].(type) {
	case int:
		return v
	case float64:
		return int(v)
	default:
		return 0
	}
}

// maxSysContentRunes 限制随诊断日志带出的系统提示文字长度（按 rune 计）。
const maxSysContentRunes = 100

// isSystemFrameCode 判断 code 是否为 chat2019 服务端系统帧：这些帧的 content 是服务端提示文字
// （如 random 被拒的原因），不是对方用户内容，因此可以在诊断日志里带出 sys_content。
func isSystemFrameCode(code int) bool {
	switch code {
	case -2, -1, 13, 14, 16:
		return true
	default:
		return false
	}
}

// truncateRunes 按 rune 截断到最多 n 个字符，避免多字节内容被截成半个字符。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func (b *Bridge) handleFrameLocked(l *link, f map[string]any) {
	switch frameCode(f) {
	case 15:
		if l.state == linkIdle {
			_ = l.sess.leaveRoom()
			return
		}
		if l.state != linkMatching {
			b.deps.Logger.Debug("ignored external user info", "provider", l.provider.Name(), "state", l.state)
			return
		}
		uid := wanfeng.PickString(f, "sel_userid")
		if uid == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		p := PeerProfile{Nickname: wanfeng.PickString(f, "sel_userNikename"), Sex: wanfeng.PickString(f, "sel_userSex"), Age: wanfeng.PickString(f, "sel_userAge"), Address: wanfeng.PickString(f, "sel_userAddress")}
		peer, err := b.resolve(ctx, l.provider.Name(), uid, p)
		if err != nil {
			cancel()
			b.idleLocked(l)
			return
		}
		if _, blocked := l.req.Context.Blocked[peer.IdentityID]; blocked {
			cancel()
			_ = l.sess.leaveRoom()
			if err := l.sess.startMatch(); err != nil {
				b.connectionLost(l, err)
			}
			return
		}
		ident, err := b.loadIdentity(ctx, peer.IdentityID)
		cancel()
		if err != nil {
			b.idleLocked(l)
			return
		}
		l.peerUID, l.peerNick, l.peerIdent, l.peer = uid, p.Nickname, peer.IdentityID, &ident
		l.state = linkActivating
		l.gateOpen = false
		l.pending = nil
		attempt := l.attempt
		b.mu.Lock()
		valid := b.validAttempt(l.req) && b.providerEnabled(l.provider.Name())
		if valid {
			b.wg.Add(1)
		}
		b.mu.Unlock()
		if !valid {
			b.idleLocked(l)
			return
		}
		go b.activate(l, attempt, peer)
	case 7:
		if l.state != linkInRoom || !l.gateOpen {
			return
		}
		from := wanfeng.PickString(wanfeng.PickMap(f, "fromuser"), "id")
		hash := md5.Sum([]byte(l.sess.selfID()))
		if from == hex.EncodeToString(hash[:]) || from != l.peerUID {
			return
		}
		body := wanfeng.PickString(f, "content", "msg")
		if body == "" {
			body = wanfeng.PickString(wanfeng.PickMap(f, "fromuser"), "content", "msg")
		}
		tid := wanfeng.PickString(f, "tid", "Tid")
		if tid == "" {
			tid = wanfeng.PickString(wanfeng.PickMap(f, "fromuser"), "tid", "Tid")
		}
		if token, ok := wanfeng.ParseMediaToken(body); ok && b.receiveMediaLocked(l, token, tid) {
			return
		}
		b.sendInboundLocked(l, body, tid)
	case 0:
		if l.provider.Name() != "wanfeng" || l.state != linkInRoom || !l.gateOpen {
			return
		}
		body := wanfeng.PickString(f, "content")
		if strings.HasPrefix(body, "对方发送的图片") || strings.HasPrefix(body, "对方发送的视频") {
			kind := "图片"
			if strings.HasPrefix(body, "对方发送的视频") {
				kind = "视频"
			}
			reason := ""
			if strings.Contains(body, "加载失败") {
				reason = "加载失败"
			} else if strings.Contains(body, "已过期") {
				reason = "已过期"
			}
			if reason != "" {
				b.sendInboundLocked(l, "["+kind+reason+"]", "")
			}
		}
	case 16:
		if l.provider.Name() == "wanfeng" && l.state == linkInRoom {
			conv := l.convID
			// End calls observers, including this Bridge. Never call it while holding link.mu.
			b.runAsync(func() { b.endConversation(conv) })
		} else {
			b.deps.Logger.Debug("忽略外站系统帧", "provider", l.provider.Name(), "code", 16)
		}
	}
}
func (b *Bridge) activate(l *link, attempt uint64, peer match.ExternalPeer) {
	defer b.wg.Done()
	hooks := match.ExternalHooks{
		OnCreated: func(ctx context.Context, tx pgx.Tx, id pgtype.UUID) error {
			return store.New(tx).InsertExternalConversation(ctx, store.InsertExternalConversationParams{ConversationID: id, Provider: l.provider.Name(), UserIdentityID: l.userID, PeerIdentityID: peer.IdentityID, RemotePeerUid: l.peerUID})
		},
		Activate: func(id pgtype.UUID, publish func()) bool {
			l.mu.Lock()
			defer l.mu.Unlock()
			b.mu.Lock()
			valid := !l.closed && !l.stopping.Load() && l.attempt == attempt && l.state == linkActivating && b.validAttempt(l.req) && b.providerEnabled(l.provider.Name())
			if valid {
				l.convID = id
				l.state = linkInRoom
				b.convs[id] = l
			}
			b.mu.Unlock()
			if !valid {
				return false
			}
			publish()
			b.count(l.provider.Name(), func(c *Counters) { c.Found++ })
			b.deps.Logger.Info("外站匹配成功", "provider", l.provider.Name(), "elapsed", time.Since(l.matchStartedAt))
			return true
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := b.found(ctx, l.userID, attempt, peer, hooks)
	if err != nil {
		l.mu.Lock()
		if !l.closed && l.attempt == attempt {
			b.idleLocked(l)
		}
		l.mu.Unlock()
		return
	}
	select {
	case <-l.done:
	case l.inbox <- inboundEvent{openGate: true, attempt: attempt}:
	}
}
func (b *Bridge) sendInboundLocked(l *link, body, tid string) {
	runes := []rune(body)
	if len(runes) > 1000 {
		body = string(runes[:1000])
	}
	if strings.TrimSpace(body) == "" {
		return
	}
	id := uuid.New()
	if tid != "" {
		id = uuid.NewSHA1(uuid.NameSpaceURL, []byte(l.provider.Name()+"|"+l.peerUID+"|"+tid))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, inserted, err := b.send(ctx, conversation.SendInput{Identity: l.peer, ConversationID: l.convID, ClientMsgID: pgtype.UUID{Bytes: id, Valid: true}, Kind: "text", Body: body, SkipRateLimit: true})
	if err != nil {
		var business *conversation.SendError
		if errors.As(err, &business) && business.Code == "content_blocked" {
			b.deps.Logger.Debug("丢弃外站被过滤消息", "provider", l.provider.Name())
		}
		return
	}
	if inserted {
		b.count(l.provider.Name(), func(c *Counters) { c.RelayedIn++ })
	}
}
func (b *Bridge) idleLocked(l *link) {
	if err := l.sess.leaveRoom(); err != nil {
		b.connectionLost(l, err)
		return
	}
	old := l.convID
	l.convID = pgtype.UUID{}
	l.state = linkIdle
	l.gateOpen = false
	l.pending = nil
	clear(l.inboundTokens)
	clear(l.outboundTokens)
	b.mu.Lock()
	if b.convs[old] == l {
		delete(b.convs, old)
	}
	b.mu.Unlock()
	if l.idleTimer != nil {
		l.idleTimer.Stop()
	}
	l.idleTimer = time.AfterFunc(time.Minute, func() {
		// Serialize expiry with a new StartMatch on this identity.
		b.mu.Lock()
		a := b.attempts[l.userID]
		b.mu.Unlock()
		if a != nil {
			a.op.Lock()
			defer a.op.Unlock()
		}
		l.mu.Lock()
		idle := !l.closed && l.state == linkIdle
		l.mu.Unlock()
		if idle {
			b.closeLink(l, false)
		}
	})
}
func (b *Bridge) endConversation(id pgtype.UUID) {
	if !id.Valid || b.end == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.end(ctx, id, "peer_left"); err != nil {
		b.deps.Logger.Error("结束外站会话失败", "conversation_id", id.String(), "error", err)
	}
}
func (b *Bridge) OnConversationEnded(id pgtype.UUID) {
	b.mu.Lock()
	l := b.convs[id]
	b.mu.Unlock()
	if l == nil {
		return
	}
	// Observer callbacks must return immediately; DB/transport work stays outside caller.
	b.runAsync(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if !l.closed && !l.stopping.Load() && l.convID == id && l.state == linkInRoom {
			b.idleLocked(l)
		}
	})
}
func (b *Bridge) OnMessage(ev conversation.MessageEvent) {
	b.mu.Lock()
	l := b.convs[ev.ConversationID]
	b.mu.Unlock()
	if l == nil || ev.SenderIdentityID != l.userID {
		return
	}
	select {
	case <-l.done:
		return
	case l.outbox <- outboundItem{id: ev.MessageID, convID: ev.ConversationID}:
	default:
		b.commandFailed(l, errors.New("外站发送队列已满"), ev.Kind == "image" || ev.Kind == "video")
	}
}
func (b *Bridge) outboundLoop(l *link) {
	defer b.wg.Done()
	for {
		select {
		case <-l.done:
			return
		case item := <-l.outbox:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			row, err := b.loadRelay(ctx, item.id)
			cancel()
			if err != nil {
				b.commandFailed(l, err, false)
				continue
			}
			l.mu.Lock()
			isMedia := row.Kind == "image" || row.Kind == "video"
			if !l.closed && !l.stopping.Load() && l.state == linkInRoom && row.ConversationID == item.convID && row.ConversationID == l.convID && row.SenderIdentityID == l.userID {
				if row.Kind == "text" {
					err = l.sess.sendText(l.peerUID, l.peerNick, row.Body)
					if err == nil {
						b.count(l.provider.Name(), func(c *Counters) { c.RelayedOut++ })
					}
				} else if row.Kind == "image" || row.Kind == "video" {
					mediaCtx, mediaCancel := l.mediaContext()
					err = b.sendMediaLocked(mediaCtx, l, row)
					mediaCancel()
					if err == nil {
						b.count(l.provider.Name(), func(c *Counters) { c.RelayedOut++ })
					}
					if err == nil && strings.TrimSpace(row.Body) != "" {
						isMedia = false
						err = l.sess.sendText(l.peerUID, l.peerNick, row.Body)
					}
				}
			}
			l.mu.Unlock()
			if err != nil {
				b.commandFailed(l, err, isMedia)
			}
		}
	}
}
