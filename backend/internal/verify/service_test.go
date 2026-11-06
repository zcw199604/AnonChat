package verify

import (
	"io"
	"log/slog"
	"testing"
)

// TestNewServiceRequiresKeys 验证密钥为空或长度不对时 NewService 返回错误（R3 修复）。
func TestNewServiceRequiresKeys(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	cases := []struct {
		name string
		enc  []byte
		hmac []byte
	}{
		{"均为空", nil, nil},
		{"enc 为空", nil, make([]byte, 32)},
		{"hmac 为空", make([]byte, 32), nil},
		{"enc 长度不对", make([]byte, 16), make([]byte, 32)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewService(nil, logger, nil, c.enc, c.hmac); err == nil {
				t.Fatal("期望返回错误，实际为 nil")
			}
		})
	}

	if _, err := NewService(nil, logger, nil, make([]byte, 32), make([]byte, 32)); err != nil {
		t.Fatalf("合法 32 字节密钥应成功，实际报错: %v", err)
	}
}
