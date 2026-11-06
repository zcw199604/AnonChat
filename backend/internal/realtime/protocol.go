// Package realtime 实现 WebSocket 实时网关：握手鉴权、连接管理、协议编解码与消息分发。
// V1 使用进程内内存实现（Hub/Broker/Presence），V2 会替换为 Redis 实现。
package realtime

import (
	"encoding/json"
	"time"
)

// MessageType 是 WS 协议信封中的消息类型（"t" 字段）。
// 取值严格对应 how.md「WebSocket 协议」表格中的全部类型（含后续任务将使用的）。
type MessageType string

// 客户端 → 服务端 消息类型。
const (
	TypePing          MessageType = "ping"
	TypeMatchStart    MessageType = "match.start"
	TypeMatchCancel   MessageType = "match.cancel"
	TypeMatchNext     MessageType = "match.next"
	TypeMatchLeave    MessageType = "match.leave"
	TypeRoomJoin      MessageType = "room.join"
	TypeRoomLeave     MessageType = "room.leave"
	TypeConvSubscribe MessageType = "conv.subscribe"
	TypeMsgSend       MessageType = "msg.send"
	TypeMsgRead       MessageType = "msg.read"
	TypeTyping        MessageType = "typing"
)

// 服务端 → 客户端 消息类型。
const (
	TypePong         MessageType = "pong"
	TypeAck          MessageType = "ack"
	TypeError        MessageType = "error"
	TypeHello        MessageType = "hello"
	TypeMatchWaiting MessageType = "match.waiting"
	TypeMatchFound   MessageType = "match.found"
	TypeMatchEnded   MessageType = "match.ended"
	TypeConvEnded    MessageType = "conv.ended"
	TypeRoomPresence MessageType = "room.presence"
	TypeMsgNew       MessageType = "msg.new"
	TypeMsgBurned    MessageType = "msg.burned"
	TypeBottleReply  MessageType = "bottle.reply"
	TypeKicked       MessageType = "kicked"
	// 故事书（how.md 3.6）。
	TypeStoryStarted MessageType = "story.started"
	TypeStoryEnded   MessageType = "story.ended"
	TypeStoryConsent MessageType = "story.consent"
)

// ErrorCode 是 WS 协议与 REST 共用的错误码（"d.code" 字段）。
// 取值对应 how.md「错误码」列表，含 V2 新增项。
type ErrorCode string

const (
	CodeUnauthorized         ErrorCode = "unauthorized"
	CodeVerificationRequired ErrorCode = "verification_required"
	CodeBanned               ErrorCode = "banned"
	CodeRateLimited          ErrorCode = "rate_limited"
	CodeContentBlocked       ErrorCode = "content_blocked"
	CodeNotMember            ErrorCode = "not_member"
	CodeConversationEnded    ErrorCode = "conversation_ended"
	CodeRoomFull             ErrorCode = "room_full"
	CodeLinkFull             ErrorCode = "link_full"
	CodeLinkExpired          ErrorCode = "link_expired"
	CodeBadPassword          ErrorCode = "bad_password"
	CodeEmptySea             ErrorCode = "empty_sea"
	CodeQuotaExceeded        ErrorCode = "quota_exceeded"
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeInternal             ErrorCode = "internal"
	// V2 新增。
	CodeMuted           ErrorCode = "muted"
	CodeCaptchaRequired ErrorCode = "captcha_required"
	CodeMediaRejected   ErrorCode = "media_rejected"
)

// Kicked 原因。
const (
	KickReasonBanned        = "banned"
	KickReasonIdentityReset = "identity_reset"
)

// Envelope 是 WS 协议信封：客户端上行 {"t","id","d"}，服务端下行额外带 "ts"（毫秒）。
type Envelope struct {
	Type MessageType     `json:"t"`
	ID   string          `json:"id,omitempty"`
	Data json.RawMessage `json:"d,omitempty"`
	TS   int64           `json:"ts,omitempty"`
}

// ErrorPayload 是 error 消息的负载。
type ErrorPayload struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// HelloPayload 是 hello 消息的负载。
type HelloPayload struct {
	Nickname   string   `json:"nickname"`
	AvatarSeed string   `json:"avatarSeed"`
	Resumable  []string `json:"resumable"`
}

// KickedPayload 是 kicked 消息的负载。
type KickedPayload struct {
	Reason string `json:"reason"`
}

// NewEnvelope 构造一个带 ts 的服务端信封；payload 为 nil 时不设置 d。
func NewEnvelope(t MessageType, id string, payload any) (Envelope, error) {
	env := Envelope{Type: t, ID: id, TS: time.Now().UnixMilli()}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, err
		}
		env.Data = raw
	}
	return env, nil
}

// Marshal 序列化信封。
func (e Envelope) Marshal() ([]byte, error) {
	return json.Marshal(e)
}
