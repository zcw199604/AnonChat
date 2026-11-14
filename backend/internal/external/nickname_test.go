package external

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/anonchat/anonchat/backend/internal/store"
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

func TestExternalNicknameFor(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		fallback string
		want     string // 非空时要求完全相等
		generate bool   // true 表示应重新生成一个无数字昵称
	}{
		{name: "优先使用当前本站昵称", current: "安静的猫", fallback: "随机的考拉", want: "安静的猫"},
		{name: "当前昵称带编号先去编号", current: "社恐的考拉#1968", fallback: "随机的考拉", want: "社恐的考拉"},
		{name: "当前昵称含数字回退绑定昵称", current: "abc123", fallback: "忧郁的熊猫", want: "忧郁的熊猫"},
		{name: "当前昵称只有编号回退绑定昵称", current: "#1968", fallback: "忧郁的熊猫", want: "忧郁的熊猫"},
		{name: "当前昵称为空回退绑定昵称", current: "", fallback: "忧郁的熊猫", want: "忧郁的熊猫"},
		{name: "两者都含数字则随机生成", current: "123", fallback: "abc1", generate: true},
		{name: "两者都为空则随机生成", current: "", fallback: "", generate: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := externalNicknameFor(tt.current, tt.fallback)
			if tt.generate {
				assertPlainNickname(t, got)
				return
			}
			if got != tt.want {
				t.Fatalf("externalNicknameFor(%q, %q) = %q, 期望 %q", tt.current, tt.fallback, got, tt.want)
			}
		})
	}
}

// TestOutboundNickname 覆盖 provider 出站昵称的选择：本站昵称优先 → 绑定昵称 → 随机。
func TestOutboundNickname(t *testing.T) {
	if got := outboundNickname(&store.Identity{Nickname: "温柔的水獭"}, "绑定的考拉"); got != "温柔的水獭" {
		t.Errorf("出站应优先使用本站昵称, 实际 %q", got)
	}
	if got := outboundNickname(&store.Identity{Nickname: "用户123"}, "绑定的考拉"); got != "绑定的考拉" {
		t.Errorf("本站昵称含数字应回退绑定昵称, 实际 %q", got)
	}
	if got := outboundNickname(nil, "绑定的考拉"); got != "绑定的考拉" {
		t.Errorf("身份为 nil 应回退绑定昵称, 实际 %q", got)
	}
	if got := outboundNickname(nil, ""); got == "" || hasASCIIDigit(got) {
		t.Errorf("都不可用时应随机生成无数字昵称, 实际 %q", got)
	}
	// 任何情况下发往外站的昵称都不能含 ASCII 数字。
	for _, ident := range []*store.Identity{nil, {Nickname: "a1"}, {Nickname: "安静的猫"}} {
		if got := outboundNickname(ident, "b2"); hasASCIIDigit(got) {
			t.Errorf("出站昵称含数字: %q", got)
		}
	}
}

func TestSanitizeExternalPeerNickname(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "原样保留", raw: "外站的小明", want: "外站的小明"},
		{name: "去掉两侧空白", raw: "  小明  ", want: "小明"},
		{name: "去掉换行与控制字符", raw: "小\n明\t啊", want: "小明啊"},
		{name: "允许数字", raw: "小明2024", want: "小明2024"},
		{name: "按 rune 截断到16", raw: "一二三四五六七八九十一二三四五六七八", want: "一二三四五六七八九十一二三四五六"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeExternalPeerNickname(tt.raw); got != tt.want {
				t.Fatalf("sanitizeExternalPeerNickname(%q) = %q, 期望 %q", tt.raw, got, tt.want)
			}
		})
	}
	for _, raw := range []string{"", "   ", "\n\t", "\u0000\u0001"} {
		got := sanitizeExternalPeerNickname(raw)
		if !generatedPlainNicknamePattern.MatchString(got) {
			t.Fatalf("空白昵称 %q 应回退为随机昵称, 实际 %q", raw, got)
		}
	}
}

// TestSanitizeExternalPeerNicknameRuneTruncation 验证截断按 rune 而非字节进行（不产生半个汉字）。
func TestSanitizeExternalPeerNicknameRuneTruncation(t *testing.T) {
	got := sanitizeExternalPeerNickname("一二三四五六七八九十一二三四五六七八")
	if runes := len([]rune(got)); runes != 16 {
		t.Fatalf("截断后字符数 = %d, 期望 16 (%q)", runes, got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("截断产生了非法 UTF-8: %q", got)
	}
}
