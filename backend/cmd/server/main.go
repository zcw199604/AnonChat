// Command server 是 AnonChat 后端入口。
// 子命令：serve | migrate up|down | admin create。
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // 注册 database/sql 的 pgx 驱动
	"golang.org/x/term"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/api"
	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/anonchat/anonchat/backend/internal/jobs"
	"github.com/anonchat/anonchat/backend/internal/platform/db"
	"github.com/anonchat/anonchat/backend/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

// run 解析子命令并分发。
func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	// healthcheck 不需要完整配置，最先处理。
	if args[0] == "healthcheck" {
		return cmdHealthcheck(args[1:])
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)

	switch args[0] {
	case "serve":
		return cmdServe(cfg, logger)
	case "migrate":
		return cmdMigrate(cfg, logger, args[1:])
	case "admin":
		return cmdAdmin(cfg, logger, args[1:])
	default:
		return usage()
	}
}

// usage 打印用法并返回错误。
func usage() error {
	return errors.New("用法: server <serve|migrate up|migrate down|admin create>")
}

// newLogger 根据环境创建 JSON 日志器。
func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	if !cfg.IsProduction() {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

// openSQLDB 打开一个用于迁移的 database/sql 连接。
func openSQLDB(databaseURL string) (*sql.DB, error) {
	sqlDB, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	return sqlDB, nil
}

// cmdMigrate 执行数据库迁移。
func cmdMigrate(cfg *config.Config, logger *slog.Logger, args []string) error {
	if len(args) == 0 {
		return errors.New("用法: server migrate <up|down>")
	}

	sqlDB, err := openSQLDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	switch args[0] {
	case "up":
		results, err := migrations.Up(ctx, sqlDB)
		if err != nil {
			return err
		}
		logger.Info("迁移 up 完成", "applied", len(results))
		for _, r := range results {
			logger.Info("已应用迁移", "version", r.Source.Version, "path", r.Source.Path)
		}
		return nil
	case "down":
		results, err := migrations.Down(ctx, sqlDB, true)
		if err != nil {
			return err
		}
		logger.Info("迁移 down 完成", "rolled_back", len(results))
		return nil
	default:
		return fmt.Errorf("未知的 migrate 子命令: %q", args[0])
	}
}

// cmdAdmin 处理管理员相关子命令。
func cmdAdmin(cfg *config.Config, logger *slog.Logger, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("用法: server admin create --username <name>")
	}
	fs := flag.NewFlagSet("admin create", flag.ContinueOnError)
	username := fs.String("username", "", "管理员用户名")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if strings.TrimSpace(*username) == "" {
		return errors.New("必须提供 --username")
	}

	password, err := readPasswordTwice()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, cfg.DatabaseURL, db.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	user, err := admin.CreateUser(ctx, pool, *username, password)
	if err != nil {
		return err
	}
	logger.Info("管理员已创建", "username", user.Username)
	return nil
}

// readPasswordTwice 交互式读取两次密码（不回显，必须一致）。
func readPasswordTwice() (string, error) {
	fmt.Print("请输入密码: ")
	first, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	fmt.Print("请再次输入密码: ")
	second, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", fmt.Errorf("读取密码失败: %w", err)
	}
	if string(first) != string(second) {
		return "", errors.New("两次输入的密码不一致")
	}
	return string(first), nil
}

// cmdServe 启动 HTTP 服务，启动前自动执行迁移。
func cmdServe(cfg *config.Config, logger *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 启动时自动执行迁移。
	sqlDB, err := openSQLDB(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	if _, err := migrations.Up(ctx, sqlDB); err != nil {
		sqlDB.Close()
		return err
	}
	sqlDB.Close()

	// 创建连接池。
	pool, err := db.Connect(ctx, cfg.DatabaseURL, db.DefaultOptions())
	if err != nil {
		return err
	}
	defer pool.Close()

	srv, err := newHTTPServer(cfg, pool, logger)
	if err != nil {
		return err
	}
	httpServer, apiServer := srv.http, srv.api

	// 后台任务：保留期清理 + 阅后即焚扫描，随 ctx 取消停止。
	cleaner := jobs.New(pool, logger, jobs.Options{
		MessageRetention: cfg.MessageRetention,
		AuditRetention:   cfg.AuditRetention,
		BottleTTL:        cfg.BottleTTL,
		Interval:         cfg.RetentionInterval,
	})
	go cleaner.Start(ctx)
	apiServer.StartBackground(ctx)

	// 在独立 goroutine 中监听，主 goroutine 等待退出信号。
	errCh := make(chan error, 1)
	go func() {
		logger.Info("HTTP 服务启动", "addr", cfg.HTTPAddr, "env", cfg.Env)
		errCh <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("收到退出信号，正在关闭服务")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// 先停止接收新连接（包括新的 WS 升级）。
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("关闭服务失败: %w", err)
	}
	// 再关闭全部已建立的 WebSocket 连接（关闭码 1001）。
	apiServer.ShutdownWS()
	logger.Info("服务已关闭")
	return nil
}

// servers 汇总构造出的 HTTP 服务与 api.Server。
type servers struct {
	http *http.Server
	api  *api.Server
}

// healthcheckURL 由监听地址推导出健康检查 URL（容器内固定请求 127.0.0.1）。
func healthcheckURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		// 形如 ":8080" 或纯端口
		h := strings.TrimPrefix(addr, ":")
		if h == "" || h == "0.0.0.0" {
			return "http://127.0.0.1:" + h + "/healthz"
		}
		return "http://" + strings.TrimPrefix(addr, "//") + "/healthz"
	}
	if host == "" || host == "0.0.0.0" || host == "::" || host == "[::]" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/healthz"
}

// probeHealth 请求健康检查 URL，200 视为健康。
func probeHealth(url string, timeout time.Duration) error {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("健康检查请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("健康检查状态码 %d", resp.StatusCode)
	}
	return nil
}

// cmdHealthcheck 供容器健康检查使用：健康退出码 0，否则非 0。
func cmdHealthcheck(args []string) error {
	fs := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	defAddr := os.Getenv("HTTP_ADDR")
	if defAddr == "" {
		defAddr = "127.0.0.1:8080"
	}
	addr := fs.String("addr", defAddr, "服务监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return probeHealth(healthcheckURL(*addr), 3*time.Second)
}

// newHTTPServer 组装依赖并返回配置好的 HTTP 服务与 api.Server。
func newHTTPServer(cfg *config.Config, pool *pgxpool.Pool, logger *slog.Logger) (servers, error) {
	deps, err := api.NewDefaultDeps(cfg, pool, logger)
	if err != nil {
		return servers{}, err
	}
	apiServer := api.NewServer(deps)
	return servers{
		http: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           apiServer.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		},
		api: apiServer,
	}, nil
}
