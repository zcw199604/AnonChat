// Package db 封装 PostgreSQL 连接池的创建与健康检查。
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Options 是连接池的可调参数。
type Options struct {
	// MaxConns 是连接池最大连接数，默认 20。
	MaxConns int32
	// MinConns 是连接池最小空闲连接数。
	MinConns int32
	// MaxConnLifetime 是单个连接的最大存活时间。
	MaxConnLifetime time.Duration
}

// DefaultOptions 返回默认的连接池参数。
func DefaultOptions() Options {
	return Options{
		MaxConns:        20,
		MinConns:        0,
		MaxConnLifetime: time.Hour,
	}
}

// Connect 使用给定 URL 创建连接池，并做一次 Ping 验证连通性。
func Connect(ctx context.Context, databaseURL string, opts Options) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("解析 DATABASE_URL 失败: %w", err)
	}
	if opts.MaxConns > 0 {
		cfg.MaxConns = opts.MaxConns
	}
	cfg.MinConns = opts.MinConns
	if opts.MaxConnLifetime > 0 {
		cfg.MaxConnLifetime = opts.MaxConnLifetime
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建连接池失败: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("数据库连接失败: %w", err)
	}
	return pool, nil
}
