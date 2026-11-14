package external

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anonchat/anonchat/backend/internal/config"
)

// scfPoolCLI 串行执行 scfproxy CLI 的池维护操作。
// CLI 在失败时保留创建意图，后续维护可以安全地继续执行。
type scfPoolCLI struct {
	cfg        *config.Config
	logger     *slog.Logger
	executable string
	ctx        context.Context
	cancel     context.CancelFunc
	requested  chan struct{}
	done       chan struct{}
}

// startSCFPoolCLI 检查运行前提并同步执行一次建池/补位，然后转到后台周期维护。
func startSCFPoolCLI(cfg *config.Config, logger *slog.Logger) (*scfPoolCLI, error) {
	if !cfg.ExternalWanfengSCFEnabled || cfg.SCFProxyPoolFile == "" || cfg.SCFProxyPoolSize < 2 || cfg.SCFProxyCommandTimeout <= 0 || cfg.SCFProxyReconcileInterval <= 0 {
		return nil, fmt.Errorf("SCF 托管配置无效：需要固定池、至少 2 个节点及正数超时/维护间隔")
	}
	// 腾讯云凭证只在运行环境中读取，不写入 Config、命令行参数、池文件或日志。
	for _, name := range []string{"TENCENTCLOUD_SECRET_ID", "TENCENTCLOUD_SECRET_KEY"} {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			return nil, fmt.Errorf("SCF 托管模式缺少 %s", name)
		}
	}
	if cfg.SCFProxyToken == "" {
		return nil, fmt.Errorf("SCF 托管模式缺少 SCFPROXY_TOKEN")
	}
	executable, err := exec.LookPath(cfg.SCFProxyCLIPath)
	if err != nil {
		return nil, fmt.Errorf("SCFPROXY_CLI_PATH 不可执行: %w", err)
	}
	info, err := os.Stat(cfg.SCFProxyPackagePath)
	if err != nil {
		return nil, fmt.Errorf("SCFPROXY_PACKAGE_PATH 不可读: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("SCFPROXY_PACKAGE_PATH 必须是部署包文件")
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SCFProxyPoolFile), 0700); err != nil {
		return nil, fmt.Errorf("创建 SCF 代理池目录失败: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &scfPoolCLI{cfg: cfg, logger: logger, executable: executable, ctx: ctx, cancel: cancel, requested: make(chan struct{}, 1), done: make(chan struct{})}
	// replenish 会初始化空池并恢复被中断的操作；已有池会保留当前主用出口。
	if err := m.reconcile(); err != nil {
		cancel()
		return nil, err
	}
	go m.run()
	return m, nil
}

func (m *scfPoolCLI) reconcile() error {
	ctx, cancel := context.WithTimeout(m.ctx, m.cfg.SCFProxyCommandTimeout)
	defer cancel()
	args := []string{
		"pool", "replenish",
		"--pool", m.cfg.SCFProxyPoolFile,
		"--regions", m.cfg.SCFProxyRegions,
		"--size", strconv.Itoa(m.cfg.SCFProxyPoolSize),
		"--prefix", m.cfg.SCFProxyPoolPrefix,
		"--package", m.cfg.SCFProxyPackagePath,
		"--yes",
	}
	cmd := exec.CommandContext(ctx, m.executable, args...)
	// token 只经子进程环境传入，不出现在命令行参数里。
	cmd.Env = append(os.Environ(), "SCFPROXY_TOKEN="+m.cfg.SCFProxyToken)
	cmd.WaitDelay = 5 * time.Second
	output, err := cmd.CombinedOutput()
	text := m.redact(string(output))
	if err != nil {
		return fmt.Errorf("scfproxy pool replenish 失败: %w；%s", err, strings.TrimSpace(text))
	}
	m.logger.Info("SCF 代理池维护完成", "output", strings.TrimSpace(text))
	return nil
}

// redact 把 token 和腾讯云凭证的值从 CLI 输出中抹掉，日志与错误都只能用这份文本。
func (m *scfPoolCLI) redact(text string) string {
	for _, secret := range []string{
		m.cfg.SCFProxyToken,
		os.Getenv("TENCENTCLOUD_SECRET_ID"),
		os.Getenv("TENCENTCLOUD_SECRET_KEY"),
		os.Getenv("TENCENTCLOUD_SESSION_TOKEN"),
	} {
		if secret = strings.TrimSpace(secret); secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return text
}

// request 触发一次后台补位；已有待处理请求时直接丢弃（池维护是幂等的）。
func (m *scfPoolCLI) request() {
	select {
	case <-m.ctx.Done():
		return
	default:
	}
	select {
	case m.requested <- struct{}{}:
	default:
	}
}

func (m *scfPoolCLI) run() {
	defer close(m.done)
	ticker := time.NewTicker(m.cfg.SCFProxyReconcileInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
		case <-m.requested:
		}
		if m.ctx.Err() != nil {
			return
		}
		if err := m.reconcile(); err != nil && m.ctx.Err() == nil {
			m.logger.Error("SCF 代理池自动补位失败，将定时重试", "error", err)
		}
	}
}

// Close 取消进行中的 CLI 子进程并等待后台协程退出。
func (m *scfPoolCLI) Close() {
	m.cancel()
	<-m.done
}
