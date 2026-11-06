// Package jobs 提供定时任务：保留期清理。每轮按 how.md「保留期」表分批清理过期数据。
package jobs

import (
	"context"
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
	nowTS := ts(now)

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

	// 8. 已撤销且无人引用的身份（AUDIT_RETENTION）。
	if err := c.loop(ctx, stats, "identities", func() (int64, error) {
		return c.q.DeleteUnreferencedRevokedIdentities(ctx, auditCutoff)
	}); err != nil {
		return stats, err
	}

	return stats, nil
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
