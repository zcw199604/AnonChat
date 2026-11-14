package external

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/zcw199604/scfproxy/pkg/scfproxy"

	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
)

// newWanfengSCF 装配晚风专用的 SCF 云函数隧道。
// 返回的清理函数先停托管维护、再关闭隧道，可安全地挂到 Bridge.Close 上。
func newWanfengSCF(cfg *config.Config, logger *slog.Logger) (*scfproxy.Proxy, wanfeng.IPBanEgress, func(), error) {
	if logger == nil {
		logger = slog.Default()
	}
	var manager *scfPoolCLI
	if cfg.SCFProxyManaged {
		var err error
		manager, err = startSCFPoolCLI(cfg, logger)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	proxy, err := scfproxy.New(scfproxy.Config{
		PoolFile: cfg.SCFProxyPoolFile,
		Token:    cfg.SCFProxyToken,
		Strategy: scfproxy.StickyByHost,
		OnSwitch: func(_, _ scfproxy.EndpointInfo, _ string) {
			// 切换出口后让托管模式补齐池，被清理的退役节点由 CLI 负责回收。
			if manager != nil {
				manager.request()
			}
		},
		Logger: logger.With("mod", "scfproxy"),
	})
	if err != nil {
		if manager != nil {
			manager.Close()
		}
		return nil, nil, nil, fmt.Errorf("初始化晚风 SCF 隧道失败: %w", err)
	}
	cleanup := func() {
		if manager != nil {
			manager.Close()
		}
		_ = proxy.Close()
	}
	logger.Info("晚风已启用 SCF 隧道", "managed", cfg.SCFProxyManaged)
	return proxy, &scfEgress{proxy: proxy, logger: logger}, cleanup, nil
}

// scfEgress 实现晚风 IP 封禁后的出口切换。
// 串行化封禁上报并比对上报告的原出口，避免并发/过期报告把刚切好的健康备用再切走。
type scfEgress struct {
	mu     sync.Mutex
	proxy  *scfproxy.Proxy
	logger *slog.Logger
}

func (e *scfEgress) Current() string { return e.proxy.Active().URL }

func (e *scfEgress) Switch(expected string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.Current() != expected {
		return nil
	}
	old, next, err := e.proxy.Switch("wanfeng: IP_BANNED")
	if err != nil {
		return err
	}
	logger := e.logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.Warn("晚风出口 IP 被封禁，已切换 SCF 出口", "oldIP", old.EgressIP, "newIP", next.EgressIP)
	return nil
}
