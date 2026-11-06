package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

// TokenBytes 是身份令牌的随机字节数（128 位以上）。
const TokenBytes = 32

// CookieName 是承载身份令牌的 Cookie 名（__Host- 前缀要求 Secure、Path=/、无 Domain）。
const CookieName = "__Host-anon"

// GenerateToken 生成 32 字节随机令牌，使用 base64url 无填充编码。
func GenerateToken() (string, error) {
	b := make([]byte, TokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成令牌失败: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken 返回令牌的 sha256 哈希，数据库只存这个哈希。
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
