package external

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/config"
)

func TestExternalLogsNeverEchoUpstreamData(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "bridge", true: "configured"}[configured], func(t *testing.T) {
			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			var b *Bridge
			if configured {
				var err error
				b, err = NewConfiguredBridge(BridgeDeps{Logger: logger}, &config.Config{})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				b = NewBridge(BridgeDeps{Logger: logger})
			}
			defer b.Close()
			// Exact attribute shapes used by the unchanged ported client and by
			// commandFailed/media refresh errors, including With/WithGroup.
			log := b.deps.Logger.With("identityID", "private-identity", "mod", "wanfeng-client")
			log.Warn("收到晚风致命错误", "data", map[string]any{"identityToken": "secret-token", "cookie": "secret-cookie", "content": "private-body"})
			log.Debug("媒体接收失败", "url", "https://media.test/x?token=secret-url", "error", errors.New("secret-error private-body"), "kind", "image")
			log.With("message", "private-with").WithGroup("nested").Warn("外站错误", "error", errors.New("secret-group"), "city", "private-city")
			b.deps.Logger.Warn("external command failed", "provider", "wanfeng", "media", true, "error", errors.New("secret-command"))
			for _, secret := range []string{"secret-", "private-"} {
				if strings.Contains(buf.String(), secret) {
					t.Fatalf("日志泄露: %s", &buf)
				}
			}
			if !strings.Contains(buf.String(), "external command failed") || !strings.Contains(buf.String(), `"provider":"wanfeng"`) {
				t.Fatalf("丢失诊断事件或provider: %s", &buf)
			}
		})
	}
}

func TestExternalLogReasonWhitelist(t *testing.T) {
	var buf bytes.Buffer
	logger := externalLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	allowed := []string{"connection_failed", "forceout", "identity_invalid", "handshake_or_ip_ban", "timeout", "cancelled", "connection_lost"}
	for _, reason := range allowed {
		logger.Warn("外站连接失败", "provider", "chat2019", "reason", reason)
	}
	// 非白名单取值与非字符串取值必须被丢弃。
	logger.Warn("外站连接失败", "provider", "wanfeng", "reason", "secret-reason")
	logger.Warn("外站连接失败", "provider", "wanfeng", "reason", 3)
	out := buf.String()
	for _, reason := range allowed {
		if !strings.Contains(out, `"reason":"`+reason+`"`) {
			t.Fatalf("白名单reason被丢弃: %s: %s", reason, out)
		}
	}
	if strings.Contains(out, "secret-reason") {
		t.Fatalf("非白名单reason泄露: %s", out)
	}
	if strings.Count(out, `"reason"`) != len(allowed) {
		t.Fatalf("非白名单reason未被丢弃: %s", out)
	}
}

func TestExternalLogSysContentWhitelist(t *testing.T) {
	var buf bytes.Buffer
	logger := externalLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	keep := strings.Repeat("拒", 100)
	logger.Info("外站匹配中收到帧", "provider", "chat2019", "code", -2, "sys_content", keep)
	// 超过 100 个 rune 与非法类型必须被丢弃。
	logger.Info("外站匹配中收到帧", "provider", "chat2019", "code", -2, "sys_content", keep+"超出")
	logger.Info("外站匹配中收到帧", "provider", "chat2019", "code", -2, "sys_content", 7)
	out := buf.String()
	if !strings.Contains(out, `"sys_content":"`+keep+`"`) {
		t.Fatalf("100 rune 的 sys_content 被丢弃: %s", out)
	}
	if strings.Count(out, `"sys_content"`) != 1 {
		t.Fatalf("超长或非字符串 sys_content 未被丢弃: %s", out)
	}
}

func TestExternalLogErrorMsgWhitelist(t *testing.T) {
	var buf bytes.Buffer
	logger := externalLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	keep := strings.Repeat("断", 200)
	logger.Warn("外站连接失败", "provider", "chat2019", "reason", "connection_failed", "error_msg", keep)
	// 超过 200 个 rune 与非字符串取值必须被丢弃。
	logger.Warn("外站连接失败", "provider", "chat2019", "reason", "connection_failed", "error_msg", keep+"超出")
	logger.Warn("外站连接失败", "provider", "chat2019", "reason", "connection_failed", "error_msg", 7)
	out := buf.String()
	if !strings.Contains(out, `"error_msg":"`+keep+`"`) {
		t.Fatalf("200 rune 的 error_msg 被丢弃: %s", out)
	}
	if strings.Count(out, `"error_msg"`) != 1 {
		t.Fatalf("超长或非字符串 error_msg 未被丢弃: %s", out)
	}
}
