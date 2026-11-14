package external

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/config"
)

// 托管模式测试用假 CLI 脚本代替真实 scfproxy CLI：子进程只记录参数并生成假池文件，
// 不访问腾讯云，也不模拟云函数。

const managedTestCLI = "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$SCF_TEST_ARGS\"\nprintf 'call\\n' >> \"$SCF_TEST_ARGS.calls\"\nif [ ! -f \"$SCFPROXY_POOL_FILE\" ]; then cp \"$SCF_TEST_POOL_TEMPLATE\" \"$SCFPROXY_POOL_FILE\"; fi\n"

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func prepareManagedSCFCLI(t *testing.T, script string) (*config.Config, string) {
	t.Helper()
	dir := t.TempDir()
	cli := filepath.Join(dir, "scfproxy")
	argsFile := filepath.Join(dir, "args.txt")
	pool := filepath.Join(dir, "scf", "pool.json")
	packagePath := filepath.Join(dir, "scf-server.zip")
	if err := os.WriteFile(cli, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(packagePath, []byte("test package"), 0600); err != nil {
		t.Fatal(err)
	}
	// 假 CLI 通过环境变量拿到池路径（真实 CLI 也由参数与环境变量共同决定）。
	t.Setenv("SCFPROXY_POOL_FILE", pool)
	t.Setenv("SCF_TEST_ARGS", argsFile)
	t.Setenv("SCF_TEST_POOL_TEMPLATE", writeSCFTestPool(t, 2))
	t.Setenv("TENCENTCLOUD_SECRET_ID", "test-secret-id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "test-secret-key")
	cfg := &config.Config{
		ExternalWanfengSCFEnabled: true,
		SCFProxyPoolFile:          pool,
		SCFProxyToken:             "test-tunnel-token",
		SCFProxyManaged:           true,
		SCFProxyCLIPath:           cli,
		SCFProxyPackagePath:       packagePath,
		SCFProxyRegions:           "ap-shanghai,ap-beijing,ap-chengdu",
		SCFProxyPoolSize:          2,
		SCFProxyPoolPrefix:        "anonchat",
		SCFProxyCommandTimeout:    30 * time.Second,
		SCFProxyReconcileInterval: time.Minute,
	}
	return cfg, argsFile
}

func wantManagedArgs(cfg *config.Config) string {
	return "pool\nreplenish\n--pool\n" + cfg.SCFProxyPoolFile +
		"\n--regions\n" + cfg.SCFProxyRegions +
		"\n--size\n" + "2" +
		"\n--prefix\n" + cfg.SCFProxyPoolPrefix +
		"\n--package\n" + cfg.SCFProxyPackagePath +
		"\n--yes\n"
}

func TestManagedSCFPoolBootstrapsUsingCLI(t *testing.T) {
	// 前缀必须取配置值：默认 anonchat，与 liao 的 scfproxy 前缀区分。
	for _, prefix := range []string{"anonchat", "anonchat-test"} {
		t.Run(prefix, func(t *testing.T) {
			cfg, argsFile := prepareManagedSCFCLI(t, managedTestCLI)
			cfg.SCFProxyPoolPrefix = prefix
			_, egress, cleanup, err := newWanfengSCF(cfg, testLogger())
			if err != nil {
				t.Fatalf("空池未通过 CLI 初始化: %v", err)
			}
			defer cleanup()
			if egress == nil || egress.Current() != "ws://127.0.0.1:19001" {
				t.Fatal("初始化后的池没有接上隧道")
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal(err)
			}
			if string(args) != wantManagedArgs(cfg) {
				t.Fatalf("CLI 参数不匹配: %q", args)
			}
			if !strings.Contains(string(args), "--prefix\n"+prefix+"\n") {
				t.Fatalf("--prefix 未使用配置值: %q", args)
			}
			// 令牌与腾讯云凭证只能经环境变量传入，不得出现在命令行参数中。
			for _, secret := range []string{"test-tunnel-token", "test-secret-id", "test-secret-key"} {
				if strings.Contains(string(args), secret) {
					t.Fatalf("凭证出现在命令行参数中: %s", secret)
				}
			}
			// 池目录必须被创建（首次启动时 /data/scf 可能为空）。
			if _, err := os.Stat(filepath.Dir(cfg.SCFProxyPoolFile)); err != nil {
				t.Fatalf("池目录未创建: %v", err)
			}
		})
	}
}

func waitSCFCLICalls(t *testing.T, path string, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path + ".calls")
		if strings.Count(string(data), "call\n") >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("CLI 未被调用 %d 次", count)
}

func TestManagedSCFPoolSwitchTriggersBackgroundCLI(t *testing.T) {
	cfg, argsFile := prepareManagedSCFCLI(t, managedTestCLI)
	_, egress, cleanup, err := newWanfengSCF(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	// 封禁切换会触发 OnSwitch → manager.request()，即后台再次补位。
	if err := egress.Switch(egress.Current()); err != nil {
		t.Fatal(err)
	}
	waitSCFCLICalls(t, argsFile, 2)
	if egress.Current() != "ws://127.0.0.1:19002" {
		t.Fatal("补位把刚切换的主用出口又改掉了")
	}
}

func TestManagedSCFPoolRetriesAndCancelsRunningCLI(t *testing.T) {
	script := "#!/bin/sh\nmode=normal\nif [ -f \"$SCF_TEST_ARGS.slow\" ]; then mode=slow; elif [ -f \"$SCF_TEST_ARGS.fail\" ]; then mode=fail; fi\n" + strings.TrimPrefix(managedTestCLI, "#!/bin/sh\n") + "case $mode in slow) exec sleep 30;; fail) exit 1;; esac\n"
	cfg, argsFile := prepareManagedSCFCLI(t, script)
	cfg.SCFProxyReconcileInterval = time.Second
	m, err := startSCFPoolCLI(cfg, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := os.WriteFile(argsFile+".fail", nil, 0600); err != nil {
		t.Fatal(err)
	}
	m.request()
	waitSCFCLICalls(t, argsFile, 2)
	// 失败后按周期重试，不需要再次封禁。
	waitSCFCLICalls(t, argsFile, 3)
	if err := os.WriteFile(argsFile+".slow", nil, 0600); err != nil {
		t.Fatal(err)
	}
	m.request()
	waitSCFCLICalls(t, argsFile, 4)
	started := time.Now()
	m.Close()
	if time.Since(started) > 3*time.Second {
		t.Fatal("关闭时没有取消正在运行的 CLI")
	}
}

func TestManagedSCFPoolCommandTimeout(t *testing.T) {
	cfg, _ := prepareManagedSCFCLI(t, "#!/bin/sh\nexec sleep 30\n")
	cfg.SCFProxyCommandTimeout = time.Second
	started := time.Now()
	m, err := startSCFPoolCLI(cfg, testLogger())
	if err == nil || m != nil || !strings.Contains(err.Error(), "pool replenish") || time.Since(started) > 5*time.Second {
		t.Fatalf("CLI 超时未被强制执行: %v", err)
	}
}

func TestManagedSCFPoolRequiresPrerequisitesAndRedactsOutput(t *testing.T) {
	cfg, argsFile := prepareManagedSCFCLI(t, "#!/bin/sh\nprintf '%s %s %s %s' \"$TENCENTCLOUD_SECRET_ID\" \"$TENCENTCLOUD_SECRET_KEY\" \"$SCFPROXY_TOKEN\" \"$TENCENTCLOUD_SESSION_TOKEN\"\nexit 1\n")
	// 缺少腾讯云凭证时必须在执行 CLI 之前失败。
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "")
	if m, err := startSCFPoolCLI(cfg, testLogger()); err == nil || m != nil {
		t.Fatal("缺少凭证仍被接受")
	}
	if _, err := os.Stat(argsFile); !os.IsNotExist(err) {
		t.Fatal("凭证校验之前就运行了 CLI")
	}
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "test-secret-key")
	t.Setenv("TENCENTCLOUD_SESSION_TOKEN", "test-session-token")
	// CLI 失败时错误信息不得泄漏凭证。
	m, err := startSCFPoolCLI(cfg, testLogger())
	if err == nil || m != nil {
		t.Fatal("CLI 失败没有阻止启动")
	}
	for _, secret := range []string{"test-secret-id", "test-secret-key", "test-tunnel-token", "test-session-token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("CLI 错误泄漏了凭证: %s", secret)
		}
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("CLI 输出未脱敏: %v", err)
	}
}

func TestManagedSCFPoolRejectsMissingDeploymentPackage(t *testing.T) {
	cfg, _ := prepareManagedSCFCLI(t, managedTestCLI)
	cfg.SCFProxyPackagePath = filepath.Join(t.TempDir(), "missing.zip")
	if m, err := startSCFPoolCLI(cfg, testLogger()); err == nil || m != nil || !strings.Contains(err.Error(), "SCFPROXY_PACKAGE_PATH") {
		t.Fatalf("缺少部署包未被拒绝: %v", err)
	}
	// 目录不是合法部署包。
	if m, err := startSCFPoolCLI(func() *config.Config { c := *cfg; c.SCFProxyPackagePath = t.TempDir(); return &c }(), testLogger()); err == nil || m != nil {
		t.Fatal("目录被当成部署包")
	}
}
