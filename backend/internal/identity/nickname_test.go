package identity

import (
	"regexp"
	"testing"
)

// nicknamePattern 校验昵称格式：形容词+的+动物+#4位数字。
var nicknamePattern = regexp.MustCompile(`^[\p{Han}]+的[\p{Han}]+#\d{4}$`)

func TestGenerateNicknameFormat(t *testing.T) {
	for i := 0; i < 200; i++ {
		n := GenerateNickname()
		if !nicknamePattern.MatchString(n) {
			t.Fatalf("昵称 %q 不符合格式", n)
		}
	}
}

func TestGenerateNicknameHasVariety(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		seen[GenerateNickname()] = struct{}{}
	}
	if len(seen) < 100 {
		t.Errorf("200 次生成只得到 %d 个不同昵称，随机性不足", len(seen))
	}
}

func TestWordListsSize(t *testing.T) {
	if len(adjectives) < 30 {
		t.Errorf("形容词词表只有 %d 个，要求不少于 30", len(adjectives))
	}
	if len(animals) < 30 {
		t.Errorf("动物词表只有 %d 个，要求不少于 30", len(animals))
	}
}

func TestGenerateAvatarSeed(t *testing.T) {
	seed := GenerateAvatarSeed()
	if len(seed) != 32 {
		t.Fatalf("avatarSeed 长度 = %d, 期望 32（16 字节十六进制）", len(seed))
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(seed) {
		t.Errorf("avatarSeed %q 不是小写十六进制", seed)
	}
	if GenerateAvatarSeed() == seed {
		t.Error("两次生成的 avatarSeed 相同，随机性不足")
	}
}
