//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// 回滚仅作用于本测试创建的独立数据库，绝不回滚共享测试库。
func TestExternalBridgeMigrationRoundTrip(t *testing.T) {
	ctx := context.Background()
	raw := os.Getenv("TEST_DATABASE_URL")
	u, err := url.Parse(raw)
	if err != nil || raw == "" {
		t.Fatal("需要 TEST_DATABASE_URL")
	}
	if u.Hostname() != "127.0.0.1" || u.Port() != "55432" {
		t.Fatal("仅允许本机 PostgreSQL 127.0.0.1:55432")
	}
	control, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	name := fmt.Sprintf("anonchat_migration_%d", time.Now().UnixNano())
	if _, err := control.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := control.ExecContext(ctx, "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
	}()
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p, err := newProvider(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	var folder, item string
	if err = db.QueryRowContext(ctx, "INSERT INTO media_folders(name) VALUES('preserved') RETURNING id").Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `INSERT INTO media_items(folder_id,kind,mime,size_bytes,storage_key) VALUES($1,'image','image/png',1,'library.png') RETURNING id`, folder).Scan(&item); err != nil {
		t.Fatal(err)
	}
	assertLibrary := func(withSource bool) {
		t.Helper()
		var got string
		query := "SELECT folder_id::text FROM media_items WHERE id=$1"
		if withSource {
			query += " AND source='library' AND owner_identity_id IS NULL"
		}
		if err := db.QueryRowContext(ctx, query, item).Scan(&got); err != nil || got != folder {
			t.Fatalf("媒体库数据未保留: %q %v", got, err)
		}
	}
	if _, err = p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	assertLibrary(true)
	for _, source := range []string{"user", "external"} {
		if _, err = db.ExecContext(ctx, `INSERT INTO media_items(kind,mime,size_bytes,storage_key,source) VALUES('image','image/png',1,$1,$1)`, source); err != nil {
			t.Fatal(err)
		}
	}
	for _, query := range []string{
		`INSERT INTO media_items(kind,mime,size_bytes,storage_key) VALUES('image','image/png',1,'invalid-library')`,
		`INSERT INTO media_items(folder_id,kind,mime,size_bytes,storage_key,source) VALUES($1,'image','image/png',1,'invalid-user','user')`,
	} {
		var args []any
		if strings.Contains(query, "$1") {
			args = []any{folder}
		}
		if _, err := db.ExecContext(ctx, query, args...); err == nil {
			t.Fatal("folder/source 检查约束未生效")
		}
	}
	if _, err = Down(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	assertLibrary(false)
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM media_items").Scan(&count); err != nil || count != 1 {
		t.Fatalf("回滚后媒体数: %d %v", count, err)
	}
	if _, err = p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}
	assertLibrary(true)
}
