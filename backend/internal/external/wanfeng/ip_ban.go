package wanfeng

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// IPBanEgress switches an existing fixed pool, without provisioning cloud resources.
// Switch must ignore a report if expected is no longer the active egress.
type IPBanEgress interface {
	Current() string
	Switch(expected string) error
}

// ErrIPBanSwitched allows connection setup to retry after an explicit rejection.
// Business requests are not replayed automatically.
var ErrIPBanSwitched = errors.New("晚风出口 IP 被封禁，已切换代理")

func isIPBan(code, message string) bool {
	message = strings.ToUpper(strings.Join(strings.Fields(message), ""))
	return strings.EqualFold(strings.TrimSpace(code), "IP_BANNED") || strings.Contains(message, "您的IP已被封禁")
}

func (c *APIClient) egressID() string {
	if c.egress == nil {
		return ""
	}
	return c.egress.Current()
}

func (c *APIClient) switchBannedEgress(expected string) error {
	if c.egress == nil || expected == "" {
		return errors.New("未配置可切换的晚风固定 IP 池")
	}
	if err := c.egress.Switch(expected); err != nil {
		c.logger.Warn("晚风 IP 封禁后切换失败", "error", err)
		return err
	}
	if sess := c.sessions.cached(); sess != nil {
		c.sessions.invalidate(sess.ID)
	}
	return nil
}

// checkIPBan is called before response decoding so handshake and media APIs
// use the same rule. Bare HTTP 403 and timeouts do not rotate; the explicit
// keyword is sufficient even if the server changes its error code or format.
func (c *APIClient) checkIPBan(raw []byte, expected string) error {
	var node map[string]any
	code, message := "", string(raw)
	if json.Unmarshal(raw, &node) == nil {
		code = PickString(node, "code")
		message = PickString(node, "message", "msg", "clientMsg")
	}
	if !isIPBan(code, message) {
		return nil
	}
	err := &APIError{Code: "IP_BANNED", Message: "您的 IP 已被封禁"}
	if c.switchBannedEgress(expected) == nil {
		return fmt.Errorf("%w: %w", ErrIPBanSwitched, err)
	}
	return err
}

// IsIPBanMessage 报告提示是否表示 IP 封禁。
func IsIPBanMessage(msg string) bool { return isIPBan("", msg) }
