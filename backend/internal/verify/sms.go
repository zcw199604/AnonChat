package verify

import (
	"context"
	"log/slog"
)

// SMSProvider 是短信发送的抽象，便于在开发/测试/生产间替换实现。
type SMSProvider interface {
	// Send 把验证码发送到 phoneE164（已规范化为 E.164）。
	Send(ctx context.Context, phoneE164, code string) error
}

// ConsoleProvider 是开发用短信实现：把验证码打到日志里，号码做脱敏。
// 生产环境禁止使用（config.Load 会拒绝 SMS_PROVIDER=console + APP_ENV=production）。
type ConsoleProvider struct {
	logger *slog.Logger
}

// NewConsoleProvider 创建开发用短信提供方。
func NewConsoleProvider(logger *slog.Logger) *ConsoleProvider {
	return &ConsoleProvider{logger: logger}
}

// Send 输出脱敏号码与验证码（验证码可输出，属于开发便利）。
func (p *ConsoleProvider) Send(_ context.Context, phoneE164, code string) error {
	p.logger.Info("开发环境短信验证码", "phone", MaskPhone(phoneE164), "code", code)
	return nil
}
