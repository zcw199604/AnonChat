package identity

import (
	"crypto/rand"
	"strings"
)

// crockford 是 Crockford Base32 字符集（去掉易混淆的 I、L、O、U）。
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// PublicCodeLength 是匿名编号长度（10 位 Crockford Base32）。
const PublicCodeLength = 10

// NewPublicCode 用 crypto/rand 生成一个 10 位 Crockford Base32 匿名编号。
func NewPublicCode() (string, error) {
	b := make([]byte, PublicCodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	out := make([]byte, PublicCodeLength)
	for i, v := range b {
		out[i] = crockford[int(v)%len(crockford)]
	}
	return string(out), nil
}

// NormalizePublicCode 把用户输入的匿名编号规范化为大写并去掉空白与连字符。
func NormalizePublicCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	replacer := strings.NewReplacer("-", "", " ", "", "O", "0", "I", "1", "L", "1")
	return replacer.Replace(s)
}
