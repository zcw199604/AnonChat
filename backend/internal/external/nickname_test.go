package external

import (
	"regexp"
	"strings"
	"testing"
)

// generatedPlainNicknamePattern 是新生成昵称应满足的格式：形容词+的+动物。
var generatedPlainNicknamePattern = regexp.MustCompile(`^[\p{Han}]+的[\p{Han}]+$`)

func assertPlainNickname(t *testing.T, got string) {
	t.Helper()
	if got == "" {
		t.Fatal("昵称为空")
	}
	if !generatedPlainNicknamePattern.MatchString(got) {
		t.Fatalf("新生成昵称 %q 不符合格式", got)
	}
	if hasASCIIDigit(got) || strings.Contains(got, "#") {
		t.Fatalf("新生成昵称 %q 含数字或 #", got)
	}
}

func TestExternalNickname(t *testing.T) {
	tests := []struct {
		name       string
		stored     string
		want       string // 非空时要求完全相等
		regenerate bool   // true 表示应重新生成一个无数字昵称
	}{
		{name: "去掉历史编号后缀", stored: "社恐的考拉#1968", want: "社恐的考拉"},
		{name: "无编号原样保留", stored: "安静的猫", want: "安静的猫"},
		{name: "两侧空白被去掉", stored: "  安静的猫  ", want: "安静的猫"},
		{name: "含数字改为重新生成", stored: "abc123", regenerate: true},
		{name: "空串改为重新生成", stored: "", regenerate: true},
		{name: "只有编号改为重新生成", stored: "#1968", regenerate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalNickname(tt.stored)
			if tt.regenerate {
				assertPlainNickname(t, got)
				return
			}
			if got != tt.want {
				t.Fatalf("externalNickname(%q) = %q, 期望 %q", tt.stored, got, tt.want)
			}
		})
	}
}

func TestExternalNicknameIsStableForPlainInput(t *testing.T) {
	// 已无数字的昵称必须原样透传，不能每次连接都换名字。
	for _, stored := range []string{"社恐的考拉", "安静的水獭"} {
		for i := 0; i < 10; i++ {
			if got := externalNickname(stored); got != stored {
				t.Fatalf("externalNickname(%q) = %q, 期望原样返回", stored, got)
			}
		}
	}
}
