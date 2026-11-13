package external

import (
	"fmt"
	"net/url"
	"time"

	"github.com/anonchat/anonchat/backend/internal/config"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
)

// NewConfiguredBridge 只装配客户端和地址守卫，不在启动时连接外站。
func NewConfiguredBridge(deps BridgeDeps, cfg *config.Config) (*Bridge, error) {
	deps.Logger = externalLogger(deps.Logger)
	chatGuard, err := NewNetGuard(Policy{NoRedirect: true}, "")
	if err != nil {
		return nil, err
	}
	timeout := cfg.ExternalTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deps.EncKey = cfg.SettingsEncKey
	deps.MediaMaxBytes = int64(cfg.MediaMaxUploadMB) << 20
	deps.NewIdentityPerHour = cfg.ExternalWanfengNewIdentityPerHour
	deps.Chat2019 = chat2019.Config{RandServerURL: cfg.ExternalChat2019RandServerURL, FallbackWS: cfg.ExternalChat2019FallbackWS, ImgServerURL: cfg.ExternalChat2019ImgServerURL, HTTPClient: chatGuard.HTTPClient(timeout), WSDialer: chatGuard.WSDialContext, Logger: deps.Logger}
	if cfg.ExternalWanfengBaseURL != "" {
		base, err := url.Parse(cfg.ExternalWanfengBaseURL)
		if err != nil || base.Hostname() == "" || (base.Scheme != "http" && base.Scheme != "https") {
			return nil, fmt.Errorf("晚风 BaseURL 非法")
		}
		hosts := append([]string{base.Hostname()}, cfg.ExternalWanfengMediaHosts...)
		if cfg.ExternalWanfengWSURL != "" {
			ws, err := url.Parse(cfg.ExternalWanfengWSURL)
			if err != nil || ws.Hostname() == "" || (ws.Scheme != "ws" && ws.Scheme != "wss") {
				return nil, fmt.Errorf("晚风 WSURL 非法")
			}
			hosts = append(hosts, ws.Hostname())
		}
		guard, err := NewNetGuard(Policy{AllowedHosts: hosts}, cfg.ExternalWanfengHTTPProxy)
		if err != nil {
			return nil, err
		}
		proto, err := wanfeng.DefaultProtocol().ApplyOverrides(cfg.ExternalWanfengProtocolOverrides)
		if err != nil {
			return nil, fmt.Errorf("晚风协议覆盖非法: %w", err)
		}
		deps.Wanfeng = wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: cfg.ExternalWanfengBaseURL, WSURL: cfg.ExternalWanfengWSURL, HTTPClient: guard.HTTPClient(timeout), Protocol: proto, Logger: deps.Logger})
		deps.WanfengDialer = guard.GorillaDialer()
		deps.WanfengMediaHosts = hosts
	}
	return NewBridge(deps), nil
}
