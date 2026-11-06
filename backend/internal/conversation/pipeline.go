package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 消息正文长度上限（按 Unicode 字符计数）。
const maxBodyRunes = 1000

// Client 抽象一次连接，便于发送管道测试与真实 WS 连接解耦。
type Client interface {
	Identity() *store.Identity
	Ack(reqID string, p AckPayload)
	Error(reqID string, code, msg string)
}

// AckPayload 是 ack 消息的负载。
type AckPayload struct {
	MessageID int64  `json:"messageId"`
	CreatedAt string `json:"createdAt"`
}

// msg.send 载荷。
type sendPayload struct {
	ConversationID string `json:"conversationId"`
	ClientMsgID    string `json:"clientMsgId"`
	Kind           string `json:"kind"`
	Body           string `json:"body"`
}

// msg.read 载荷。
type readPayload struct {
	ConversationID string `json:"conversationId"`
	MessageID      int64  `json:"messageId"`
}

// conv.subscribe 载荷。
type subscribePayload struct {
	ConversationIDs []string `json:"conversationIds"`
}

// msg.new 负载。
type msgNewPayload struct {
	ConversationID string        `json:"conversationId"`
	Message        msgNewMessage `json:"message"`
}

type msgNewMessage struct {
	ID            int64  `json:"id"`
	MemberID      int32  `json:"memberId"`
	Nickname      string `json:"nickname"`
	AvatarSeed    string `json:"avatarSeed"`
	Kind          string `json:"kind"`
	Body          string `json:"body"`
	CreatedAt     string `json:"createdAt"`
	BurnAfterRead bool   `json:"burnAfterRead"`
}

// msg.burned 负载。
type msgBurnedPayload struct {
	ConversationID string  `json:"conversationId"`
	MessageIDs     []int64 `json:"messageIds"`
}

// conv.ended 负载。
type convEndedPayload struct {
	ConversationID string `json:"conversationId"`
	Reason         string `json:"reason"`
}

// connClient 把 realtime.Conn 适配为 Client。
type connClient struct{ c *realtime.Conn }

func (cc connClient) Identity() *store.Identity { return cc.c.Identity() }

func (cc connClient) Ack(reqID string, p AckPayload) {
	cc.c.SendEvent(realtime.TypeAck, reqID, p)
}

func (cc connClient) Error(reqID, code, msg string) {
	cc.c.SendError(reqID, realtime.ErrorCode(code), msg)
}

// RegisterWS 把 msg.send / msg.read / conv.subscribe 注册到 WS 路由器。
// 聊天类消息统一挂 RequireVerified 中间件。
func (s *Service) RegisterWS(router *realtime.Router, verificationMode string) {
	router.Handle(realtime.TypeMsgSend, realtime.RequireVerified(verificationMode)(
		func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
			s.HandleSend(ctx, connClient{c}, env)
		}))
	router.Handle(realtime.TypeMsgRead, realtime.RequireVerified(verificationMode)(
		func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
			s.HandleRead(ctx, connClient{c}, env)
		}))
	router.Handle(realtime.TypeConvSubscribe,
		func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
			s.HandleSubscribe(ctx, c, env)
		})
	router.Handle(realtime.TypeTyping, realtime.RequireVerified(verificationMode)(
		func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
			s.HandleTyping(ctx, c, env)
		}))
}

// HandleSend 处理 msg.send：校验 → 成员/会话状态 → 限流 → 内容过滤 → 落库 → ack → 广播。
func (s *Service) HandleSend(ctx context.Context, cl Client, env realtime.Envelope) {
	var p sendPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		cl.Error(env.ID, "invalid_request", "载荷格式错误")
		return
	}
	convID, err := parseUUID(p.ConversationID)
	if err != nil {
		cl.Error(env.ID, "invalid_request", "会话 ID 非法")
		return
	}
	clientMsgID, err := parseUUID(p.ClientMsgID)
	if err != nil {
		cl.Error(env.ID, "invalid_request", "clientMsgId 非法")
		return
	}
	if p.Kind != "text" {
		cl.Error(env.ID, "invalid_request", "V1 只支持文本消息")
		return
	}
	body := strings.TrimSpace(p.Body)
	if body == "" || utf8.RuneCountInString(body) > maxBodyRunes {
		cl.Error(env.ID, "invalid_request", "消息内容为空或超过上限")
		return
	}

	ident := cl.Identity()
	member, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			cl.Error(env.ID, "not_member", "你不在该会话中")
			return
		}
		s.internalError(cl, env.ID, err)
		return
	}
	if member.LeftAt.Valid {
		cl.Error(env.ID, "not_member", "你已离开该会话")
		return
	}

	conv, err := s.q.GetConversation(ctx, convID)
	if err != nil {
		s.internalError(cl, env.ID, err)
		return
	}
	if conv.EndedAt.Valid {
		cl.Error(env.ID, "conversation_ended", "会话已结束")
		return
	}

	// 限流钩子。
	if !s.limiter.Allow(ctx, "msg:"+ident.ID.String()) {
		cl.Error(env.ID, "rate_limited", "发送过于频繁")
		return
	}
	// 房间内发言间隔：同一身份在同一房间每秒最多 1 条。
	if conv.Kind == store.ConversationKindRoom && s.roomMsgLimiter != nil {
		if !s.roomMsgLimiter.Allow(ctx, "room_msg:"+convID.String()+"|"+ident.ID.String()) {
			cl.Error(env.ID, "rate_limited", "房间内发言过快")
			return
		}
	}

	// 内容过滤钩子（本批 noop）。
	filtered, blocked := s.filter.Filter(ctx, body)
	if blocked {
		cl.Error(env.ID, "content_blocked", "内容包含违规信息")
		return
	}

	// 落库（幂等）。
	msg, err := s.q.InsertMessage(ctx, store.InsertMessageParams{
		ConversationID:   convID,
		SenderIdentityID: ident.ID,
		SenderMemberID:   member.MemberID,
		ClientMsgID:      clientMsgID,
		Kind:             "text",
		Body:             filtered,
	})
	inserted := true
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// 冲突：查出原消息，回相同 ack，不再广播。
			inserted = false
			existing, gerr := s.q.GetMessageByIdentityClientID(ctx, store.GetMessageByIdentityClientIDParams{
				SenderIdentityID: ident.ID,
				ClientMsgID:      clientMsgID,
			})
			if gerr != nil {
				s.internalError(cl, env.ID, gerr)
				return
			}
			msg = existing
		} else {
			s.internalError(cl, env.ID, err)
			return
		}
	}

	// 先回 ack。
	cl.Ack(env.ID, AckPayload{MessageID: msg.ID, CreatedAt: formatTS(msg.CreatedAt)})
	if !inserted {
		return
	}

	// 再广播给会话全部订阅连接，排除“屏蔽了发送者”的身份。
	// 昵称按会话类型处理：房间保留昵称、故事用角色名、1v1 置空（前端显示匿名代号）。
	env2, eerr := realtime.NewEnvelope(realtime.TypeMsgNew, "", msgNewPayload{
		ConversationID: convID.String(),
		Message: msgNewMessage{
			ID:            msg.ID,
			MemberID:      member.MemberID,
			Nickname:      s.nicknameFor(ctx, conv, ident.Nickname, member.MemberID),
			AvatarSeed:    ident.AvatarSeed,
			Kind:          msg.Kind,
			Body:          msg.Body,
			CreatedAt:     formatTS(msg.CreatedAt),
			BurnAfterRead: conv.BurnAfterRead,
		},
	})
	if eerr != nil {
		s.internalError(cl, env.ID, eerr)
		return
	}
	var exclude map[pgtype.UUID]struct{}
	if s.blocker != nil {
		if b, berr := s.blocker.Related(ctx, ident.ID); berr == nil {
			exclude = b
		}
	}
	if s.broker != nil {
		s.broker.Broadcast(convID, env2, exclude)
	}
}

// HandleRead 处理 msg.read：更新 last_read_message_id；阅后即焚会话标记 read_at。
func (s *Service) HandleRead(ctx context.Context, cl Client, env realtime.Envelope) {
	var p readPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		cl.Error(env.ID, "invalid_request", "载荷格式错误")
		return
	}
	convID, err := parseUUID(p.ConversationID)
	if err != nil {
		cl.Error(env.ID, "invalid_request", "会话 ID 非法")
		return
	}
	ident := cl.Identity()
	member, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			cl.Error(env.ID, "not_member", "你不在该会话中")
			return
		}
		s.internalError(cl, env.ID, err)
		return
	}
	if member.LeftAt.Valid {
		cl.Error(env.ID, "not_member", "你已离开该会话")
		return
	}
	msgID := p.MessageID
	if err := s.q.UpdateLastRead(ctx, store.UpdateLastReadParams{
		ConversationID:    convID,
		IdentityID:        ident.ID,
		LastReadMessageID: &msgID,
	}); err != nil {
		s.internalError(cl, env.ID, err)
		return
	}
	conv, err := s.q.GetConversation(ctx, convID)
	if err != nil {
		s.internalError(cl, env.ID, err)
		return
	}
	if conv.BurnAfterRead {
		if _, err := s.q.MarkMessagesRead(ctx, store.MarkMessagesReadParams{
			ConversationID:   convID,
			ID:               msgID,
			SenderIdentityID: ident.ID,
		}); err != nil {
			s.internalError(cl, env.ID, err)
		}
	}
}

// typingPayload 是 typing 事件的负载（不含身份信息，只有会话内 memberId）。
type typingPayload struct {
	ConversationID string `json:"conversationId"`
	MemberID       int32  `json:"memberId"`
}

// HandleTyping 处理 typing：校验成员与会话状态后，把「正在输入」广播给会话其他成员。
func (s *Service) HandleTyping(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	var p sendPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "载荷格式错误")
		return
	}
	convID, err := parseUUID(p.ConversationID)
	if err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "会话 ID 非法")
		return
	}
	ident := c.Identity()
	member, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
	if err != nil || member.LeftAt.Valid {
		c.SendError(env.ID, realtime.CodeNotMember, "你不在该会话中")
		return
	}
	conv, err := s.q.GetConversation(ctx, convID)
	if err != nil || conv.EndedAt.Valid {
		c.SendError(env.ID, realtime.CodeConversationEnded, "会话已结束")
		return
	}
	out, eerr := realtime.NewEnvelope(realtime.TypeTyping, "", typingPayload{
		ConversationID: convID.String(),
		MemberID:       member.MemberID,
	})
	if eerr != nil {
		return
	}
	// 只发给会话订阅者；排除发送者自己，以及与之存在屏蔽关系的身份。
	exclude := map[pgtype.UUID]struct{}{ident.ID: {}}
	if s.blocker != nil {
		if b, berr := s.blocker.Related(ctx, ident.ID); berr == nil {
			for id := range b {
				exclude[id] = struct{}{}
			}
		}
	}
	if s.broker != nil {
		s.broker.Broadcast(convID, out, exclude)
	}
}

// HandleSubscribe 处理 conv.subscribe：只订阅自己是未离开成员的会话，其余以 not_member 列出。
func (s *Service) HandleSubscribe(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	var p subscribePayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "载荷格式错误")
		return
	}
	ident := c.Identity()
	var rejected []string
	for _, raw := range p.ConversationIDs {
		convID, err := parseUUID(raw)
		if err != nil {
			rejected = append(rejected, raw)
			continue
		}
		member, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
		if err != nil || member.LeftAt.Valid {
			rejected = append(rejected, raw)
			continue
		}
		c.Subscribe(convID)
	}
	if len(rejected) > 0 {
		c.SendError(env.ID, realtime.CodeNotMember, fmt.Sprintf("无法订阅会话: %s", strings.Join(rejected, ", ")))
	}
}

// internalError 记录并回 internal 错误。
func (s *Service) internalError(cl Client, reqID string, err error) {
	s.logger.Error("会话管道内部错误", "error", err)
	cl.Error(reqID, "internal", "服务器内部错误")
}

// parseUUID 解析 UUID 字符串。
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, errors.New("UUID 为空")
	}
	return u, nil
}

// formatTS 把时间戳格式化为 RFC3339。
func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
