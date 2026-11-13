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
