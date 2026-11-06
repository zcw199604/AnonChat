package verify

import (
	"bytes"
	"testing"
)

func testKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i + 7)
	}
	return k
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey()
	plain := []byte("+8613800001234")
	enc, err := Encrypt(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(enc, plain) {
		t.Error("密文不应包含明文")
	}
	dec, err := Decrypt(key, enc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(dec, plain) {
		t.Errorf("解密结果 = %q, 期望 %q", dec, plain)
	}
	// 相同明文两次加密应产生不同密文（随机 nonce）。
	enc2, _ := Encrypt(key, plain)
	if bytes.Equal(enc, enc2) {
		t.Error("两次加密结果相同，nonce 未随机化")
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	enc, _ := Encrypt(testKey(), []byte("secret"))
	other := testKey()
	other[0] ^= 0xff
	if _, err := Decrypt(other, enc); err == nil {
		t.Error("错误密钥解密应失败")
	}
}

func TestHMACSHA256(t *testing.T) {
	a := HMACSHA256(testKey(), []byte("data"))
	b := HMACSHA256(testKey(), []byte("data"))
	if !bytes.Equal(a, b) {
		t.Error("相同输入 HMAC 应一致")
	}
	if len(a) != 32 {
		t.Errorf("HMAC 长度 = %d, 期望 32", len(a))
	}
	if bytes.Equal(a, HMACSHA256(testKey(), []byte("data2"))) {
		t.Error("不同输入 HMAC 不应相同")
	}
}

func TestGenerateCode(t *testing.T) {
	for i := 0; i < 100; i++ {
		c, err := GenerateCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(c) != 6 {
			t.Fatalf("验证码 %q 长度应为 6", c)
		}
		for _, r := range c {
			if r < '0' || r > '9' {
				t.Fatalf("验证码 %q 含非数字字符", c)
			}
		}
	}
}

func TestHashCodeDeterministicAndUnique(t *testing.T) {
	key := testKey()
	phone := HMACSHA256(key, []byte("+8613800001234"))
	h1 := HashCode(key, phone, "123456")
	h2 := HashCode(key, phone, "123456")
	if !bytes.Equal(h1, h2) {
		t.Error("相同输入验证码哈希应一致")
	}
	if bytes.Equal(h1, HashCode(key, phone, "123457")) {
		t.Error("不同验证码哈希不应相同")
	}
	if !SecureEqual(h1, h2) {
		t.Error("SecureEqual 相同值应返回 true")
	}
	if SecureEqual(h1, HashCode(key, phone, "000000")) {
		t.Error("SecureEqual 不同值应返回 false")
	}
}
