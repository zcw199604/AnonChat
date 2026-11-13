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
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
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
	MediaID        string `json:"mediaId"`
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
	// MediaURL 是媒体消息的访问地址，仅媒体消息携带（how.md 3.2）。
	MediaURL string `json:"mediaUrl,omitempty"`
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

// SendInput 是一次发送的参数。
type SendInput struct {
	Identity        *store.Identity
	ConversationID  pgtype.UUID
	ClientMsgID     pgtype.UUID
	Kind            string // text | image | video
	Body            string
	MediaID         pgtype.UUID // image/video 必填
	RequireOneToOne bool        // 用户媒体仅限 match/bottle/private；bot 与 Bridge 默认不限制。
	// SkipRateLimit 为 true 时跳过单人限流与房间发言间隔限制。
	// bot 跨多个会话发送，不受单人限流约束（how.md 3.1）。
	SkipRateLimit bool
	// Ack 在落库之后、广播之前回调（幂等命中同样会回调，此时不广播）。
	// 用于给发起方回 ack；bot 通过 SendAs 发送时留空。
	Ack func(msg store.Message)
}

// MediaForSend 查找发送者有权发送的用户上传媒体。
type MediaForSend interface {
	GetUserMediaForSend(context.Context, pgtype.UUID, pgtype.UUID) (store.MediaItem, error)
}

func WithMedia(m MediaForSend) Option { return func(s *Service) { s.media = m } }

// SetMedia 在注册请求处理之前装配媒体服务。
func (s *Service) SetMedia(m MediaForSend) { s.media = m }

// SendError 是发送管道返回的业务错误，Code 与现有错误码一致。
type SendError struct {
	Code string
	Msg  string
}

// Error 实现 error 接口。
func (e *SendError) Error() string { return e.Code + ": " + e.Msg }

// sendErr 构造业务错误。
func sendErr(code, msg string) *SendError { return &SendError{Code: code, Msg: msg} }

// SendAs 执行一次发送：校验 → 成员/会话状态 → 限流 → 内容过滤 → 落库 → ack 回调 → 广播 → 通知观察者。
// 返回落库的消息；inserted=false 表示幂等命中（不广播、不通知观察者）。
// 错误以 *SendError 表示（Code/Msg 与用户侧错误码一致），其余错误为内部错误。
func (s *Service) SendAs(ctx context.Context, in SendInput) (store.Message, bool, error) {
	if in.Identity == nil {
		return store.Message{}, false, sendErr("internal", "服务器内部错误")
	}
	ident := in.Identity
	body := strings.TrimSpace(in.Body)

	// 消息类型与内容校验：text 要求正文非空；image/video 要求 media_id 有效，正文可空。
	switch in.Kind {
	case "text":
		if body == "" || utf8.RuneCountInString(body) > maxBodyRunes {
			return store.Message{}, false, sendErr("invalid_request", "消息内容为空或超过上限")
		}
	case "image", "video":
		if !in.MediaID.Valid {
			return store.Message{}, false, sendErr("invalid_request", "媒体消息缺少 mediaId")
		}
		if utf8.RuneCountInString(body) > maxBodyRunes {
			return store.Message{}, false, sendErr("invalid_request", "消息内容超过上限")
		}
	default:
		return store.Message{}, false, sendErr("invalid_request", "不支持的消息类型")
	}

	convID := in.ConversationID
	member, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.Message{}, false, sendErr("not_member", "你不在该会话中")
		}
		return store.Message{}, false, err
	}
	if member.LeftAt.Valid {
		return store.Message{}, false, sendErr("not_member", "你已离开该会话")
	}

	conv, err := s.q.GetConversation(ctx, convID)
	if err != nil {
		return store.Message{}, false, err
	}
	if conv.EndedAt.Valid {
		return store.Message{}, false, sendErr("conversation_ended", "会话已结束")
	}
	if in.RequireOneToOne && conv.Kind != store.ConversationKindMatch && conv.Kind != store.ConversationKindBottle && conv.Kind != store.ConversationKindPrivate {
		return store.Message{}, false, sendErr("invalid_request", "该会话不支持发送媒体")
	}

	// 幂等重发先返回原 ack，不消耗额度，也不再次广播。
	existing, err := s.q.GetMessageByIdentityClientID(ctx, store.GetMessageByIdentityClientIDParams{SenderIdentityID: ident.ID, ClientMsgID: in.ClientMsgID})
	if err == nil {
		if in.Ack != nil {
			in.Ack(existing)
		}
		return existing, false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.Message{}, false, err
	}

	// 限流钩子（bot 发送时跳过）。
	if !in.SkipRateLimit && s.limitSettings != nil {
		checks := []moderation.RuleCheck{{Rule: moderation.RuleMsgPerSecond, Key: "msg:" + ident.ID.String()}, {Rule: moderation.RuleMsgPerMinute, Key: "msg:" + ident.ID.String()}}
		if conv.Kind == store.ConversationKindRoom {
			checks = append(checks, moderation.RuleCheck{Rule: moderation.RuleRoomMsgInterval, Key: "room_msg:" + convID.String() + "|" + ident.ID.String()})
		}
		if ok, hit := s.limitSettings.AllowAll(ctx, checks...); !ok {
			rule := s.limitSettings.Current().Rules[hit]
			message := "房间内发言过快"
			if hit == moderation.RuleMsgPerSecond {
				message = fmt.Sprintf("发送过快，每秒最多 %d 条", s.limitSettings.EffectiveLimit(rule))
			}
			if hit == moderation.RuleMsgPerMinute {
				message = fmt.Sprintf("发送过于频繁，每分钟最多 %d 条", s.limitSettings.EffectiveLimit(rule))
			}
			return store.Message{}, false, sendErr("rate_limited", message)
		}
	} else if !in.SkipRateLimit {
		if !s.limiter.Allow(ctx, "msg:"+ident.ID.String()) {
			return store.Message{}, false, sendErr("rate_limited", "发送过于频繁")
		}
		// 房间内发言间隔：同一身份在同一房间每秒最多 1 条。
		if conv.Kind == store.ConversationKindRoom && s.roomMsgLimiter != nil {
			if !s.roomMsgLimiter.Allow(ctx, "room_msg:"+convID.String()+"|"+ident.ID.String()) {
				return store.Message{}, false, sendErr("rate_limited", "房间内发言过快")
			}
		}
	}

	// 内容过滤钩子：只对非空正文执行（媒体消息的正文可以为空）。
	filtered := body
	if body != "" {
		var blocked bool
		filtered, blocked = s.filter.Filter(ctx, body)
		if blocked {
			return store.Message{}, false, sendErr("content_blocked", "内容包含违规信息")
		}
	}

	// 落库（幂等）。
	msg, err := s.q.InsertMessage(ctx, store.InsertMessageParams{
		ConversationID:   convID,
		SenderIdentityID: ident.ID,
		SenderMemberID:   member.MemberID,
		ClientMsgID:      in.ClientMsgID,
		Kind:             in.Kind,
		Body:             filtered,
		MediaID:          in.MediaID,
	})
	inserted := true
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" && pgErr.ConstraintName == "messages_media_id_fkey" {
			return store.Message{}, false, sendErr("invalid_request", "媒体已失效，请重新上传")
		}
		if errors.Is(err, pgx.ErrNoRows) {
			// 冲突：查出原消息，回相同 ack，不再广播。
			inserted = false
			existing, gerr := s.q.GetMessageByIdentityClientID(ctx, store.GetMessageByIdentityClientIDParams{
				SenderIdentityID: ident.ID,
				ClientMsgID:      in.ClientMsgID,
			})
			if gerr != nil {
				return store.Message{}, false, gerr
			}
			msg = existing
		} else {
			return store.Message{}, false, err
		}
	}

	// 先回 ack（幂等命中也回），再决定是否广播。
	if in.Ack != nil {
		in.Ack(msg)
	}
	if !inserted {
		return msg, false, nil
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
			MediaURL:      mediaURL(msg.MediaID),
		},
	})
	if eerr != nil {
		return store.Message{}, false, eerr
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

	// 真正插入并广播之后才通知观察者；幂等命中已在上面提前返回。
	s.notifyMessage(MessageEvent{
		ConversationID:   convID,
		ConversationKind: conv.Kind,
		MessageID:        msg.ID,
		SenderIdentityID: ident.ID,
		SenderMemberID:   member.MemberID,
		Kind:             msg.Kind,
	})
	return msg, true, nil
}

// HandleSend 处理 msg.send：校验 → SendAs（成员/会话状态 → 限流 → 内容过滤 → 落库 → ack → 广播）。
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
	body := strings.TrimSpace(p.Body)
	var mediaID pgtype.UUID
	requireOneToOne := false
	switch p.Kind {
	case "text":
		if body == "" || utf8.RuneCountInString(body) > maxBodyRunes {
			cl.Error(env.ID, "invalid_request", "消息内容为空或超过上限")
			return
		}
	case "image", "video":
		requireOneToOne = true
		mediaID, err = parseUUID(p.MediaID)
		if err != nil || s.media == nil {
			cl.Error(env.ID, "invalid_request", "媒体不存在或不属于你")
			return
		}
		item, lookupErr := s.media.GetUserMediaForSend(ctx, mediaID, cl.Identity().ID)
		if lookupErr != nil {
			var apiErr *httpx.APIError
			if errors.Is(lookupErr, pgx.ErrNoRows) || (errors.As(lookupErr, &apiErr) && apiErr.Code == httpx.CodeNotFound) {
				cl.Error(env.ID, "invalid_request", "媒体不存在或不属于你")
			} else {
				s.internalError(cl, env.ID, lookupErr)
			}
			return
		}
		if item.Kind != p.Kind {
			cl.Error(env.ID, "invalid_request", "媒体不存在或不属于你")
			return
		}
		if utf8.RuneCountInString(body) > maxBodyRunes {
			cl.Error(env.ID, "invalid_request", "消息内容超过上限")
			return
		}
	default:
		cl.Error(env.ID, "invalid_request", "不支持的消息类型")
		return
	}

	_, _, err = s.SendAs(ctx, SendInput{
		Identity:        cl.Identity(),
		ConversationID:  convID,
		ClientMsgID:     clientMsgID,
		Kind:            p.Kind,
		Body:            body,
		MediaID:         mediaID,
		RequireOneToOne: requireOneToOne,
		Ack: func(msg store.Message) {
			cl.Ack(env.ID, AckPayload{MessageID: msg.ID, CreatedAt: formatTS(msg.CreatedAt)})
		},
	})
	if err != nil {
		var se *SendError
		if errors.As(err, &se) {
			cl.Error(env.ID, se.Code, se.Msg)
			return
		}
		s.internalError(cl, env.ID, err)
	}
}

// mediaURL 返回媒体消息的用户侧访问地址；media_id 无效时为空（不输出该字段）。
func mediaURL(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return "/api/media/" + id.String()
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
	s.BroadcastTyping(ctx, convID, ident.ID, member.MemberID)
}

// BroadcastTyping 把「正在输入」广播给会话其他成员（供 bot 使用：bot 没有 WS 连接）。
// 与会话内 typing 事件一致：排除发送者自己，以及与之存在屏蔽关系的身份。
func (s *Service) BroadcastTyping(ctx context.Context, conversationID, identityID pgtype.UUID, memberID int32) {
	out, eerr := realtime.NewEnvelope(realtime.TypeTyping, "", typingPayload{
		ConversationID: conversationID.String(),
		MemberID:       memberID,
	})
	if eerr != nil {
		s.logger.Warn("构造 typing 事件失败", "error", eerr)
		return
	}
	exclude := map[pgtype.UUID]struct{}{identityID: {}}
	if s.blocker != nil {
		if b, berr := s.blocker.Related(ctx, identityID); berr == nil {
			for id := range b {
				exclude[id] = struct{}{}
			}
		}
	}
	if s.broker != nil {
		s.broker.Broadcast(conversationID, out, exclude)
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
