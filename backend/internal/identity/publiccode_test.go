package identity

import (
	"regexp"
	"testing"
)

var publicCodePattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{10}$`)

func TestNewPublicCodeFormat(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		code, err := NewPublicCode()
		if err != nil {
			t.Fatalf("生成匿名编号失败: %v", err)
		}
		if !publicCodePattern.MatchString(code) {
			t.Fatalf("匿名编号 %q 不符合格式（10 位 Crockford Base32）", code)
		}
		seen[code] = struct{}{}
	}
	if len(seen) < 190 {
		t.Errorf("200 次生成只得到 %d 个不同编号，随机性不足", len(seen))
	}
}

func TestNormalizePublicCode(t *testing.T) {
	if got := NormalizePublicCode(" ab-cd 0o1i "); got != "ABCD0011" {
		t.Errorf("规范化结果 = %q，期望 ABCD0011", got)
	}
}
