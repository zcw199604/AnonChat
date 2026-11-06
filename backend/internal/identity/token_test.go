package identity

import (
	"bytes"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	// 32 字节 base64url 无填充 = 43 字符。
	if len(tok) != 43 {
		t.Fatalf("令牌长度 = %d, 期望 43", len(tok))
	}
	if tok == "" || containsPadding(tok) {
		t.Fatalf("令牌 %q 不应含填充", tok)
	}
	tok2, _ := GenerateToken()
	if tok == tok2 {
		t.Fatal("两次生成的令牌相同")
	}
}

func containsPadding(s string) bool {
	return bytes.ContainsAny([]byte(s), "=")
}

func TestHashToken(t *testing.T) {
	h1 := HashToken("abc")
	h2 := HashToken("abc")
	if !bytes.Equal(h1, h2) {
		t.Fatal("同一令牌的哈希应一致")
	}
	if len(h1) != 32 {
		t.Fatalf("sha256 哈希长度 = %d, 期望 32", len(h1))
	}
	if bytes.Equal(h1, HashToken("abd")) {
		t.Fatal("不同令牌的哈希不应相同")
	}
}
