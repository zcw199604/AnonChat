package verify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Encrypt 用 AES-256-GCM 加密，返回 nonce||ciphertext（含 tag）。
func Encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 密码块失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 失败: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("生成 nonce 失败: %w", err)
	}
	// Seal 把密文追加到 nonce 后面：nonce||ciphertext。
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt 解密 Encrypt 的输出。
func Decrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 AES 密码块失败: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("创建 GCM 失败: %w", err)
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return nil, fmt.Errorf("密文过短")
	}
	nonce, ct := data[:ns], data[ns:]
	plain, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("解密失败: %w", err)
	}
	return plain, nil
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
