// Package migrations 把 goose SQL 迁移文件嵌入二进制，并提供 up/down 执行入口。
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // 注册 database/sql 的 pgx 驱动
	"github.com/pressly/goose/v3"
)

//go:embed *.sql
var files embed.FS

// newProvider 创建一个使用嵌入文件系统的 goose Provider。
func newProvider(db *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, db, files)
}

// Up 执行全部未应用的迁移。
func Up(ctx context.Context, db *sql.DB) ([]*goose.MigrationResult, error) {
	provider, err := newProvider(db)
	if err != nil {
		return nil, fmt.Errorf("初始化迁移 Provider 失败: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("执行迁移 up 失败: %w", err)
	}
	return results, nil
}

// Down 回滚一个（或全部）迁移。若 all 为 true 则回滚到空库。
func Down(ctx context.Context, db *sql.DB, all bool) ([]*goose.MigrationResult, error) {
	provider, err := newProvider(db)
	if err != nil {
		return nil, fmt.Errorf("初始化迁移 Provider 失败: %w", err)
	}
	var results []*goose.MigrationResult
	if all {
		results, err = provider.DownTo(ctx, 0)
	} else {
		r, e := provider.Down(ctx)
		if r != nil {
			results = append(results, r)
		}
		err = e
	}
	if err != nil {
		return nil, fmt.Errorf("执行迁移 down 失败: %w", err)
	}
	return results, nil
}
