package realtime

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// IdentityResolver 从明文令牌解析身份（生产实现为 identity.Service.Resolve）。
type IdentityResolver interface {
	Resolve(ctx context.Context, token string) (*store.Identity, error)
}

// BanChecker 判断身份/客户端是否被封禁（V1 只提供 AllowAll 实现，真正实现在任务 5.4）。
type BanChecker interface {
	IsBanned(ctx context.Context, ident *store.Identity, clientIP string) bool
}

// AccessLogger 写入访问审计日志（生产实现为 identity.Service.LogAccess）。
type AccessLogger interface {
	LogAccess(ctx context.Context, identityID pgtype.UUID, event, ip, userAgent string)
}

// Broker 负责消息扇出（V2 会是 Redis 实现）。
type Broker interface {
	// Broadcast 向某会话的全部订阅连接广播，exclude 中的身份（键集合）不接收。
	Broadcast(conversationID pgtype.UUID, env Envelope, exclude map[pgtype.UUID]struct{})
	// SendToIdentity 向某身份的全部连接发送。
	SendToIdentity(identityID pgtype.UUID, env Envelope)
}

// Presence 维护按 key 的在线身份集合（V2 会是 Redis 实现）。
// 以连接为登记单位、按身份去重计数：同一身份多连接只计一次，某身份在某 key 下的连接数降到 0 才算离开。
type Presence interface {
	Add(key string, c *Conn)
	Remove(key string, c *Conn)
	Count(key string) int
	// PresenceHas 报告某身份是否在某个 presence key 下在线。
	PresenceHas(key string, identityID pgtype.UUID) bool
	// TryAdd 原子地把连接登记到 presence key：同身份已在 key 下则不受上限约束，
	// 否则达到 limit 时拒绝。返回是否成功与当前在线身份数。
	TryAdd(key string, c *Conn, limit int) (ok bool, online int)
}

// Subscriber 维护连接/身份对会话的订阅（V2 会是 Redis 实现）。
type Subscriber interface {
	Subscribe(c *Conn, conv pgtype.UUID)
	Unsubscribe(c *Conn, conv pgtype.UUID)
	SubscribeIdentity(identityID, conv pgtype.UUID)
}

// ConnStats 提供连接相关计数（V2 会是 Redis 实现）。
type ConnStats interface {
	// ConnCount 返回某身份当前的连接数。
	ConnCount(identityID pgtype.UUID) int
	// OnlineCount 返回当前在线的身份数（按身份去重）。
	OnlineCount() int
}

// Realtime 汇总业务模块所需的实时能力（V1 内存实现，V2 由 Redis 实现替换）。
type Realtime interface {
	Presence
	Broker
	Subscriber
	ConnStats
}

// AllowAllBans 是不封禁任何人的 BanChecker 实现。
type AllowAllBans struct{}

// IsBanned 始终返回 false。
func (AllowAllBans) IsBanned(context.Context, *store.Identity, string) bool { return false }
