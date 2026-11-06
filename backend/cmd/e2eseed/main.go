// Command e2eseed 仅在 APP_ENV=test 下创建 E2E 管理员账号。
// 密码从标准输入读取（非交互式参数），复用 admin.CreateUser。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/platform/db"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2eseed 错误:", err)
		os.Exit(1)
	}
}

func run() error {
	if os.Getenv("APP_ENV") != "test" {
		return errors.New("e2eseed 只允许在 APP_ENV=test 下运行")
	}
	fs := flag.NewFlagSet("e2eseed", flag.ContinueOnError)
	username := fs.String("username", "e2eadmin", "管理员用户名")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	password := strings.TrimSpace(string(raw))
	if password == "" {
		return errors.New("必须从标准输入提供密码")
	}

	url := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if url == "" {
		return errors.New("必须设置 DATABASE_URL")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, url, db.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	if _, err := admin.CreateUser(ctx, pool, *username, password); err != nil {
		return err
	}
	fmt.Println("E2E 管理员已创建:", *username)
	return nil
}
