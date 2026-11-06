package verify

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	"github.com/anonchat/anonchat/backend/internal/platform/secretbox"
)

// Encrypt 用 AES-256-GCM 加密，返回 nonce||ciphertext（含 tag）。
// 实现已移到 internal/platform/secretbox（与后台敏感配置共用），此处保留原函数与行为。
func Encrypt(key, plaintext []byte) ([]byte, error) {
	return secretbox.Encrypt(key, plaintext)
}

// Decrypt 解密 Encrypt 的输出。
func Decrypt(key, data []byte) ([]byte, error) {
	return secretbox.Decrypt(key, data)
}

// HMACSHA256 返回 HMAC-SHA256(key, data)。
func HMACSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// GenerateCode 生成 6 位数字验证码。
func GenerateCode() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("生成验证码失败: %w", err)
	}
	n := binary.BigEndian.Uint64(b[:]) % 1000000
	return fmt.Sprintf("%06d", n), nil
}

// HashCode 计算验证码哈希：HMAC-SHA256(hmacKey, phoneHmac||code)。
func HashCode(hmacKey, phoneHmac []byte, code string) []byte {
	buf := make([]byte, 0, len(phoneHmac)+len(code))
	buf = append(buf, phoneHmac...)
	buf = append(buf, code...)
	return HMACSHA256(hmacKey, buf)
}

// SecureEqual 使用常量时间比较两个字节切片。
func SecureEqual(a, b []byte) bool {
	return hmac.Equal(a, b)
}
