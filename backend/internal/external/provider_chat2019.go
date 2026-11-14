package external

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/jackc/pgx/v5/pgtype"
)

type chat2019Provider struct{ b *Bridge }

func (p *chat2019Provider) Name() string { return "chat2019" }
func (p *chat2019Provider) available(pgtype.UUID) (bool, string) {
	if p.b.deps.Chat2019.HTTPClient == nil || p.b.deps.Chat2019.WSDialer == nil {
		return false, "chat2019 未装配"
	}
	return true, ""
}
func (p *chat2019Provider) open(ctx context.Context, l *link) (session, error) {
	binding, err := p.b.bindings.Chat2019(ctx, l.userID)
	if err != nil {
		return nil, err
	}
	sex := "男"
	if l.req.Context.Profile.Gender == "female" {
		sex = "女"
	}
	self := chat2019.Self{UID: binding.RemoteUid, Nickname: externalNickname(binding.Nickname), Sex: sex}
	conn, err := chat2019.Dial(ctx, p.b.deps.Chat2019, self, func(f map[string]any) { p.b.emitFrame(l, f) }, func(expected bool, err error) {
		if !expected {
			p.b.connectionLost(l, err)
		}
	})
	if err != nil {
		return nil, err
	}
	return &chat2019Session{conn: conn, self: self}, nil
}

type chat2019FrameSender interface {
	Send(map[string]any) error
	Close()
}
type chat2019Session struct {
	conn chat2019FrameSender
	self chat2019.Self
}

func (s *chat2019Session) startMatch() error {
	return s.conn.Send(map[string]any{"act": "random", "id": s.self.UID, "userAge": "0"})
}
func (s *chat2019Session) cancelMatch() error { return s.randomOut() }
func (s *chat2019Session) leaveRoom() error   { return s.randomOut() }
func (s *chat2019Session) randomOut() error {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return err
	}
	return s.conn.Send(map[string]any{"act": "randomOut", "id": s.self.UID, "msg": hex.EncodeToString(id)})
}
func (s *chat2019Session) sendText(uid, nick, text string) error {
	return s.conn.Send(map[string]any{"act": fmt.Sprintf("touser_%s_%s", uid, nick), "id": s.self.UID, "msg": text})
}
func (s *chat2019Session) sendMedia(uid, nick, kind, token string) error {
	return s.sendText(uid, nick, "["+token+"]")
}
func (s *chat2019Session) selfID() string { return s.self.UID }
func (s *chat2019Session) close()         { s.conn.Close() }
