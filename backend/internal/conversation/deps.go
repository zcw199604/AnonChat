// Package conversation 实现统一的会话与消息管道：会话/成员仓储、发送管道、
// 已读与阅后即焚、历史分页。四种聊天形态（match/room/bottle/private）共用本包。
package conversation

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// RateLimiter 是发送管道的限流钩子（本批使用 AllowAll，任务 5.2 接入真实实现）。
type RateLimiter = moderation.RateLimiter

// ContentFilter 是内容过滤钩子：返回处理后文本或拦截（本批 noop，任务 5.4 接入）。
type ContentFilter interface {
	Filter(ctx context.Context, text string) (filtered string, blocked bool)
}

// BlockChecker 查询屏蔽关系（双向：谁屏蔽了该身份或被该身份屏蔽都算相关）。
type BlockChecker interface {
	// Related 返回与 identityID 存在任一方向屏蔽关系的身份集合。
	Related(ctx context.Context, identityID pgtype.UUID) (map[pgtype.UUID]struct{}, error)
}

// NoopFilter 是不做任何过滤的实现。
type NoopFilter struct{}

// Filter 原样返回文本，不拦截。
func (NoopFilter) Filter(_ context.Context, text string) (string, bool) { return text, false }

// SQLBlockChecker 基于 blocks 表的 BlockChecker 实现。
type SQLBlockChecker struct {
	q *store.Queries
}

// NewSQLBlockChecker 创建基于数据库的屏蔽检查器。
func NewSQLBlockChecker(pool *pgxpool.Pool) *SQLBlockChecker {
	return &SQLBlockChecker{q: store.New(pool)}
}

// Related 实现 BlockChecker（单向记录，双向生效）。
func (b *SQLBlockChecker) Related(ctx context.Context, identityID pgtype.UUID) (map[pgtype.UUID]struct{}, error) {
	rows, err := b.q.ListBlocksRelated(ctx, identityID)
	if err != nil {
		return nil, err
	}
	return toSet(rows), nil
}

// toSet 把 UUID 切片转成集合。
func toSet(ids []pgtype.UUID) map[pgtype.UUID]struct{} {
	set := make(map[pgtype.UUID]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
