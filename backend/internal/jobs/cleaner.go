// Package jobs 提供定时任务：保留期清理。每轮按 how.md「保留期」表分批清理过期数据。
package jobs

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// 默认参数。
const (
	defaultBatchSize = 5000
	defaultInterval  = 10 * time.Minute
	smsRetention     = 24 * time.Hour
	// botUsageRetention 是 bot 用量记录的固定保留期（窗口上限最多 7 天，不受影响）。
	botUsageRetention = 30 * 24 * time.Hour
)

// Options 是清理任务的可调参数。
type Options struct {
	MessageRetention time.Duration
	AuditRetention   time.Duration
	BottleTTL        time.Duration
	Interval         time.Duration
	BatchSize        int
}

// Cleaner 执行保留期清理。
type Cleaner struct {
	q      *store.Queries
	logger *slog.Logger
	opts   Options
	now    func() time.Time
}

// Option 用于配置 Cleaner（测试注入时钟）。
type Option func(*Cleaner)

// WithClock 注入时钟。
func WithClock(fn func() time.Time) Option {
	return func(c *Cleaner) {
		if fn != nil {
			c.now = fn
		}
	}
}

// New 创建清理器。
func New(pool *pgxpool.Pool, logger *slog.Logger, opts Options, options ...Option) *Cleaner {
	if opts.BatchSize <= 0 {
		opts.BatchSize = defaultBatchSize
	}
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	c := &Cleaner{
		q:      store.New(pool),
		logger: logger,
		opts:   opts,
		now:    time.Now,
	}
	for _, o := range options {
		o(c)
	}
	return c
}

// Start 启动定时清理，直到 ctx 取消。启动时先执行一轮。
func (c *Cleaner) Start(ctx context.Context) {
	if _, err := c.RunOnce(ctx); err != nil {
		c.logger.Warn("保留期清理首轮失败", "error", err)
	}
	ticker := time.NewTicker(c.opts.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := c.RunOnce(ctx); err != nil {
				c.logger.Warn("保留期清理失败", "error", err)
			}
		}
	}
}

// RunOnce 执行一轮清理，返回各表删除行数。
func (c *Cleaner) RunOnce(ctx context.Context) (map[string]int64, error) {
	now := c.now()
	stats := make(map[string]int64)

	msgCutoff := ts(now.Add(-c.opts.MessageRetention))
	auditCutoff := ts(now.Add(-c.opts.AuditRetention))
	bottleCutoff := ts(now.Add(-c.opts.BottleTTL))
	smsCutoff := ts(now.Add(-smsRetention))
	botUsageCutoff := ts(now.Add(-botUsageRetention))
	nowTS := ts(now)

	// 记忆清理先于身份与会话清理，以便各项删除数量不被级联删除掩盖。
	// 三项独立执行；某项失败仍继续其他清理，最后统一返回错误。
	var memoryErrors []error
	for _, task := range []struct {
		name string
		run  func() (int64, error)
	}{
		{"bot_memories_expired", func() (int64, error) { return c.q.DeleteExpiredBotMemories(ctx, nowTS) }},
		{"bot_memories_revoked", func() (int64, error) { return c.q.DeleteRevokedBotMemories(ctx) }},
		{"bot_memory_cursors", func() (int64, error) { return c.q.DeleteOldBotMemoryCursors(ctx, ts(now.Add(-8*24*time.Hour))) }},
	} {
		start := time.Now()
		n, err := task.run()
		stats[task.name] = n
		if err != nil {
			// 数据库错误可能携带画像值，不传递到 Start 的日志。
			memoryErrors = append(memoryErrors, errors.New(task.name+" 清理失败"))
			c.logger.Warn("记忆清理失败", "table", task.name, "rows", n)
			continue
		}
		c.logger.Info("保留期清理", "table", task.name, "rows", n, "duration_ms", time.Since(start).Milliseconds())
	}

	// 1. 过期消息（分批）。
	if err := c.loop(ctx, stats, "messages", func() (int64, error) {
		return c.q.DeleteExpiredMessages(ctx, msgCutoff)
	}); err != nil {
		return stats, err
	}

	// 2. 已结束且消息为空的会话：先删引用（bottle_picks / private_links），再分批删会话。
	if n, err := c.q.DeleteBottlePicksForEmptyEndedConversations(ctx, msgCutoff); err != nil {
		return stats, err
	} else {
		stats["conversation_bottle_picks"] = n
	}
	if n, err := c.q.DeletePrivateLinksForEmptyEndedConversations(ctx, msgCutoff); err != nil {
		return stats, err
	} else {
		stats["conversation_private_links"] = n
	}
	if err := c.loop(ctx, stats, "conversations", func() (int64, error) {
		return c.q.DeleteEmptyEndedConversations(ctx, msgCutoff)
	}); err != nil {
		return stats, err
	}

	// 3. 过期漂流瓶与私密链接。
	if err := c.loop(ctx, stats, "bottles", func() (int64, error) {
		return c.q.DeleteExpiredBottles(ctx, nowTS)
	}); err != nil {
		return stats, err
	}
	if n, err := c.q.DeleteOldBottlePicks(ctx, bottleCutoff); err != nil {
		return stats, err
	} else {
		stats["bottle_picks"] = n
	}
	if n, err := c.q.DeleteExpiredPrivateLinks(ctx, nowTS); err != nil {
		return stats, err
	} else {
		stats["private_links"] = n
	}

	// 4. 短信验证码（固定 1 天）。
	if n, err := c.q.DeleteOldSmsCodes(ctx, smsCutoff); err != nil {
		return stats, err
	} else {
		stats["sms_codes"] = n
	}

	// 5. 访问审计日志（AUDIT_RETENTION）。
	if n, err := c.q.DeleteOldAccessLogs(ctx, auditCutoff); err != nil {
		return stats, err
	} else {
		stats["access_logs"] = n
	}

	// 6. 过期管理员会话。
	if n, err := c.q.DeleteExpiredAdminSessions(ctx, nowTS); err != nil {
		return stats, err
	} else {
		stats["admin_sessions"] = n
	}

	// 7. 早于 AUDIT_RETENTION 的举报快照（reports.snapshot 180 天）。
	if err := c.loop(ctx, stats, "reports", func() (int64, error) {
		return c.q.DeleteOldReports(ctx, auditCutoff)
	}); err != nil {
		return stats, err
	}

	// 8. 故事书：等待超时的故事置为 expired；结束且未生成作品的故事在 7 天后删除。
	//    story_works 属于用户主动发布的内容，不受 7 天留存约束（why.md 风险表）。
	if n, err := c.q.ExpireWaitingStories(ctx); err != nil {
		return stats, err
	} else {
		stats["stories_expired"] = n
	}
	if err := c.loop(ctx, stats, "stories", func() (int64, error) {
		return c.q.DeleteOldEndedStories(ctx, msgCutoff)
	}); err != nil {
		return stats, err
	}

	// 9. 广播：展示结束 7 天后，或已驳回/下架 7 天后删除。
	if err := c.loop(ctx, stats, "broadcasts", func() (int64, error) {
		return c.q.DeleteExpiredBroadcasts(ctx, msgCutoff)
	}); err != nil {
		return stats, err
	}

	// 10. 已撤销且无人引用的身份（AUDIT_RETENTION）。
	if err := c.loop(ctx, stats, "identities", func() (int64, error) {
		return c.q.DeleteUnreferencedRevokedIdentities(ctx, auditCutoff)
	}); err != nil {
		return stats, err
	}

	// 11. bot 用量记录（固定 30 天）。
	if err := c.loop(ctx, stats, "bot_usage", func() (int64, error) {
		return c.q.DeleteOldBotUsage(ctx, botUsageCutoff)
	}); err != nil {
		return stats, err
	}

	return stats, errors.Join(memoryErrors...)
}

// loop 按 batchSize 循环删除直到不足一批。
func (c *Cleaner) loop(ctx context.Context, stats map[string]int64, name string, fn func() (int64, error)) error {
	start := time.Now()
	var total int64
	for {
		n, err := fn()
		if err != nil {
			return err
		}
		total += n
		if n < int64(c.opts.BatchSize) {
			break
		}
	}
	stats[name] = total
	c.logger.Info("保留期清理", "table", name, "rows", total, "duration_ms", time.Since(start).Milliseconds())
	return nil
}

// ts 把 time.Time 转成 parameter 化的时间戳。
func ts(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
