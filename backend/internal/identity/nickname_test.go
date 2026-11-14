package identity

import (
	"regexp"
	"strings"
	"testing"
)

// nicknamePattern 校验昵称格式：形容词 + “的” + 名词，不含 # 与编号。
var nicknamePattern = regexp.MustCompile(`^[\p{Han}]+的[\p{Han}]+$`)

func TestGenerateNicknameFormat(t *testing.T) {
	hasDigit := regexp.MustCompile(`[0-9]`)
	for i := 0; i < 500; i++ {
		n := GenerateNickname()
		if !nicknamePattern.MatchString(n) {
			t.Fatalf("昵称 %q 不符合格式", n)
		}
		if hasDigit.MatchString(n) {
			t.Fatalf("昵称 %q 含 ASCII 数字", n)
		}
		if strings.Contains(n, "#") {
			t.Fatalf("昵称 %q 含 #", n)
		}
		if !strings.Contains(n, "的") {
			t.Fatalf("昵称 %q 不含「的」", n)
		}
	}
}

func TestGenerateNicknameHasVariety(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 500; i++ {
		seen[GenerateNickname()] = struct{}{}
	}
	if len(seen) < 400 {
		t.Errorf("500 次生成只得到 %d 个不同昵称，随机性不足", len(seen))
	}
}

func TestGeneratePlainNicknameEqualsGenerateNickname(t *testing.T) {
	for i := 0; i < 20; i++ {
		if n := GeneratePlainNickname(); !nicknamePattern.MatchString(n) {
			t.Fatalf("GeneratePlainNickname() = %q 不符合格式", n)
		}
	}
}

func TestWordListsSize(t *testing.T) {
	if len(adjectives) < 200 {
		t.Errorf("形容词词表只有 %d 个，要求不少于 200", len(adjectives))
	}
	if len(nouns) < 200 {
		t.Errorf("名词词表只有 %d 个，要求不少于 200", len(nouns))
	}
	if combos := len(adjectives) * len(nouns); combos < 40000 {
		t.Errorf("组合数只有 %d，要求不少于 40000", combos)
	}
}

// TestWordListsValid 校验词表本身：无重复、每个词 1–3 个汉字且不含 ASCII 数字与字母。
func TestWordListsValid(t *testing.T) {
	wordPattern := regexp.MustCompile(`^\p{Han}{1,3}$`)
	for _, tc := range []struct {
		name  string
		words []string
	}{
		{"形容词词表", adjectives},
		{"名词词表", nouns},
	} {
		seen := make(map[string]struct{}, len(tc.words))
		for _, w := range tc.words {
			if !wordPattern.MatchString(w) {
				t.Errorf("%s 中的 %q 不是 1–3 个汉字", tc.name, w)
			}
			if strings.ContainsAny(w, "的#") {
				t.Errorf("%s 中的 %q 不应含「的」或「#」", tc.name, w)
			}
			if _, dup := seen[w]; dup {
				t.Errorf("%s 中的 %q 重复", tc.name, w)
			}
			seen[w] = struct{}{}
		}
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
