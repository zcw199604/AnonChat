package realtime

import "github.com/jackc/pgx/v5/pgtype"

// IdentityObserver 观察某身份的连接数在 0/1 之间的切换（用于匹配断线宽限、房间清理等）。
// OnIdentityOnline 在该身份连接数从 0 变 1 时调用；OnIdentityOffline 在从 1 变 0 时调用。
type IdentityObserver interface {
	OnIdentityOnline(identityID pgtype.UUID)
	OnIdentityOffline(identityID pgtype.UUID)
}

// ConnObserver 观察单个连接的开闭（用于房间按连接登记与清理）。
type ConnObserver interface {
	OnConnOpened(c *Conn)
	OnConnClosed(c *Conn)
}
