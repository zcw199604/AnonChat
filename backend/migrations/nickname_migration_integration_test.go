//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestNicknameDropSuffixMigration 验证 00011 只去掉生成器格式的编号后缀，
// 自定义昵称保持不变；下行迁移为有损操作，回滚不还原编号。
func TestNicknameDropSuffixMigration(t *testing.T) {
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
	name := fmt.Sprintf("anonchat_nickname_%d", time.Now().UnixNano())
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
	if _, err = p.UpTo(ctx, 10); err != nil {
		t.Fatal(err)
	}

	type sample struct {
		nickname string
		want     string
	}
	samples := []sample{
		{nickname: "社恐的考拉#1968", want: "社恐的考拉"}, // 生成器格式：去掉编号
		{nickname: "迷路的水獭#3721", want: "迷路的水獭"},
		{nickname: "小明#1234", want: "小明#1234"}, // 不含“的”：自定义昵称不动
		{nickname: "abc", want: "abc"},
		{nickname: "安静的猫", want: "安静的猫"}, // 已无编号：保持不变
	}
	ids := make([]string, 0, len(samples))
	for i, s := range samples {
		var id string
		if err = db.QueryRowContext(ctx,
			"INSERT INTO identities(token_hash, nickname, avatar_seed, public_code) VALUES($1,$2,$3,$4) RETURNING id",
			[]byte(fmt.Sprintf("hash-%d", i)), s.nickname, fmt.Sprintf("seed-%d", i), fmt.Sprintf("CODE%06d", i),
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	// 外站绑定表同样保存了昵称，需要一起去编号（主键是 identity_id+provider）。
	bindings := []struct{ identity, provider, nickname string }{
		{ids[0], "chat2019", "忧郁的熊猫#4096"},
		{ids[1], "wanfeng", "小明#1234"},
	}
	for i, b := range bindings {
		if _, err = db.ExecContext(ctx,
			"INSERT INTO external_bindings(identity_id, provider, remote_uid, nickname) VALUES($1,$2,$3,$4)",
			b.identity, b.provider, fmt.Sprintf("remote-%d", i), b.nickname,
		); err != nil {
			t.Fatal(err)
		}
	}

	if _, err = p.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}

	assert := func(stage string) {
		t.Helper()
		for i, s := range samples {
			var got string
			if err := db.QueryRowContext(ctx, "SELECT nickname FROM identities WHERE id=$1", ids[i]).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != s.want {
				t.Errorf("%s：identities[%d] nickname = %q, 期望 %q", stage, i, got, s.want)
			}
		}
		rows, err := db.QueryContext(ctx, "SELECT nickname FROM external_bindings ORDER BY remote_uid")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var nick string
			if err := rows.Scan(&nick); err != nil {
				t.Fatal(err)
			}
			got = append(got, nick)
		}
		want := []string{"忧郁的熊猫", "小明#1234"}
		if len(got) != len(want) {
			t.Fatalf("%s：external_bindings 行数 = %d, 期望 %d", stage, len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s：external_bindings[%d] nickname = %q, 期望 %q", stage, i, got[i], want[i])
			}
		}
	}
	assert("up 之后")

	// Down 是有损操作：不还原编号，数据保持不变。
	if _, err = Down(ctx, db, false); err != nil {
		t.Fatal(err)
	}
	assert("down 之后")
}
