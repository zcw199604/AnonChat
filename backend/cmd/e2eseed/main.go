// Command e2eseed 仅在 APP_ENV=test 下创建 E2E 管理员账号与一个可用激活码。
// 密码从标准输入读取（非交互式参数），复用 admin.CreateUser 与 vip.Service.CreateBatch。
// 激活码明文通过 --out 指定的 JSON 文件交给 E2E 测试读取（明文只出现这一次）。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/platform/db"
	"github.com/anonchat/anonchat/backend/internal/vip"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "e2eseed 错误:", err)
		os.Exit(1)
	}
}

// seedFile 是交给 E2E 读取的种子数据。
type seedFile struct {
	AdminUsername   string   `json:"adminUsername"`
	AdminPassword   string   `json:"adminPassword"`
	ActivationCodes []string `json:"activationCodes"`
	VipDays         int32    `json:"vipDays"`
	BroadcastCards  int32    `json:"broadcastCards"`
}

func run() error {
	if os.Getenv("APP_ENV") != "test" {
		return errors.New("e2eseed 只允许在 APP_ENV=test 下运行")
	}
	fs := flag.NewFlagSet("e2eseed", flag.ContinueOnError)
	username := fs.String("username", "e2eadmin", "管理员用户名")
	out := fs.String("out", "", "把激活码等种子数据写入该 JSON 文件（可选）")
	vipDays := fs.Int("vip-days", 30, "激活码的 VIP 天数")
	cards := fs.Int("broadcast-cards", 3, "激活码附带的广播卡张数")
	count := fs.Int("count", 20, "生成的可用激活码数量（E2E 重复运行会消耗）")
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, url, db.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	user, err := admin.CreateUser(ctx, pool, *username, password)
	if err != nil {
		return err
	}
	fmt.Println("E2E 管理员已创建:", *username)

	// 创建一个可用激活码（用于 E2E 的 VIP 兑换流程）。
	vipSvc := vip.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, codes, err := vipSvc.CreateBatch(ctx, vip.CreateBatchInput{
		Label:          "E2E 测试批次",
		VipDays:        int32(*vipDays),
		BroadcastCards: int32(*cards),
		Count:          int32(*count),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
		CreatedBy:      user.ID,
	})
	if err != nil {
		return err
	}
	fmt.Printf("E2E 激活码已创建: %d 个（首个 %s）\n", len(codes), codes[0])

	if *out != "" {
		seed := seedFile{
			AdminUsername:   *username,
			AdminPassword:   password,
			ActivationCodes: codes,
			VipDays:         int32(*vipDays),
			BroadcastCards:  int32(*cards),
		}
		data, err := json.MarshalIndent(seed, "", "  ")
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*out, data, 0o600); err != nil {
			return err
		}
		fmt.Println("E2E 种子文件已写入:", *out)
	}
	return nil
}
