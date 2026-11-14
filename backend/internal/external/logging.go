package external

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"
)

// Keep the upstream port unchanged. Its diagnostics include complete untrusted
// frames, error bodies and signed URLs, so all service-side external clients
// receive this restricted handler rather than the application's raw handler.
func externalLogger(logger *slog.Logger) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if _, ok := logger.Handler().(externalLogHandler); ok {
		return logger
	}
	return slog.New(externalLogHandler{inner: logger.Handler()})
}

type externalLogHandler struct{ inner slog.Handler }

func (h externalLogHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}
func (h externalLogHandler) Handle(ctx context.Context, record slog.Record) error {
	clean := slog.NewRecord(record.Time, record.Level, record.Message, record.PC)
	record.Attrs(func(a slog.Attr) bool {
		if safe, ok := externalLogAttr(a); ok {
			clean.AddAttrs(safe)
		}
		return true
	})
	return h.inner.Handle(ctx, clean)
}
func (h externalLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		if safe, ok := externalLogAttr(a); ok {
			clean = append(clean, safe)
		}
	}
	return externalLogHandler{inner: h.inner.WithAttrs(clean)}
}
func (h externalLogHandler) WithGroup(name string) slog.Handler {
	return externalLogHandler{inner: h.inner.WithGroup(name)}
}

func externalLogAttr(a slog.Attr) (slog.Attr, bool) {
	// Never resolve arbitrary LogValuers or stringify upstream values.
	switch a.Key {
	case "error":
		if a.Value.Kind() == slog.KindAny {
			return slog.String("error_type", fmt.Sprintf("%T", a.Value.Any())), true
		}
	case "provider":
		if a.Value.Kind() == slog.KindString && (a.Value.String() == "chat2019" || a.Value.String() == "wanfeng") {
			return a, true
		}
	case "mod":
		if a.Value.Kind() == slog.KindString && a.Value.String() == "wanfeng-client" {
			return a, true
		}
	case "reason":
		if a.Value.Kind() == slog.KindString {
			switch a.Value.String() {
			case "connection_failed", "forceout", "identity_invalid", "handshake_or_ip_ban", "timeout", "cancelled", "connection_lost":
				return a, true
			}
		}
	case "kind":
		if a.Value.Kind() == slog.KindString && (a.Value.String() == "text" || a.Value.String() == "image" || a.Value.String() == "video" || a.Value.String() == "audio") {
			return a, true
		}
	case "sys_content":
		// 只用于外站服务端系统提示（如 random 被拒的原因），不是对方用户内容；
		// 调用方负责仅在系统 code 时附加，这里再兜底限制长度与类型。
		if a.Value.Kind() == slog.KindString && utf8.RuneCountInString(a.Value.String()) <= 100 {
			return a, true
		}
	case "error_msg":
		// 只用于 chat2019 断线诊断：该错误来自 coder/websocket 的网络/关闭路径，不含
		// 对方消息内容，因此可以带出原文以定位断线原因。晚风错误可能含签名 URL 等上游
		// 凭据，调用方不附加此属性；这里再兜底限制类型与 200 rune 长度。
		if a.Value.Kind() == slog.KindString && utf8.RuneCountInString(a.Value.String()) <= 200 {
			return a, true
		}
	case "media", "showCity", "attempt", "failures", "count", "elapsed", "code", "state":
		switch a.Value.Kind() {
		case slog.KindBool, slog.KindInt64, slog.KindUint64, slog.KindDuration:
			return a, true
		}
	}
	return slog.Attr{}, false
}
