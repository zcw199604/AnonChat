package vip

import (
	"regexp"
	"testing"
)

var codePattern = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}-[0-9A-HJKMNP-TV-Z]{4}$`)

func TestGenerateCodeFormat(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 200; i++ {
		code, err := GenerateCode()
		if err != nil {
			t.Fatalf("生成激活码失败: %v", err)
		}
		if !codePattern.MatchString(code) {
			t.Fatalf("激活码 %q 不符合 XXXX-XXXX-XXXX-XXXX 格式", code)
		}
		seen[NormalizeCode(code)] = struct{}{}
	}
	if len(seen) != 200 {
		t.Errorf("200 次生成得到 %d 个不同激活码，存在碰撞", len(seen))
	}
}

func TestNormalizeCode(t *testing.T) {
	cases := map[string]string{
		"abcd-efgh-jkmn-pqrs": "ABCDEFGHJKMNPQRS",
		" ABCD EFGH JKMN PQRS ": "ABCDEFGHJKMNPQRS",
		"OOOO-1111-LLLL-IIII":   "0000111111111111",
	}
	for in, want := range cases {
		if got := NormalizeCode(in); got != want {
			t.Errorf("NormalizeCode(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

func TestHashCodeStable(t *testing.T) {
	a := HashCode(NormalizeCode("abcd-efgh-jkmn-pqrs"))
	b := HashCode(NormalizeCode("ABCDEFGHJKMNPQRS"))
	if len(a) != 32 {
		t.Fatalf("哈希长度 = %d, 期望 32（sha256）", len(a))
	}
	if string(a) != string(b) {
		t.Error("同一激活码的哈希应一致")
	}
	if string(HashCode("ABCDEFGHJKMNPQRT")) == string(a) {
		t.Error("不同激活码的哈希不应相同")
	}
}
