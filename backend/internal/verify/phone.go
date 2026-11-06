package verify

import (
	"fmt"
	"regexp"
	"strings"
)

// e164Pattern 匹配 E.164：+ 后紧跟非 0 数字，总长度 7–15 位。
var e164Pattern = regexp.MustCompile(`^\+[1-9]\d{6,14}$`)

// cnMobilePattern 匹配中国大陆 11 位手机号（1 开头）。
var cnMobilePattern = regexp.MustCompile(`^1\d{10}$`)

// 清洗电话时要去除的分隔符。
var phoneSeparators = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", "\t", "")

// NormalizePhone 把输入号码规范化为 E.164：
//   - 以 + 开头的国际号码，校验格式后原样返回；
//   - 中国大陆 11 位手机号，自动补 +86 前缀；
//   - 其它情况返回错误。
func NormalizePhone(raw string) (string, error) {
	s := phoneSeparators.Replace(strings.TrimSpace(raw))
	if s == "" {
		return "", fmt.Errorf("电话号码为空")
	}
	if strings.HasPrefix(s, "+") {
		if !e164Pattern.MatchString(s) {
			return "", fmt.Errorf("非法的国际号码: %q", raw)
		}
		return s, nil
	}
	if cnMobilePattern.MatchString(s) {
		return "+86" + s, nil
	}
	return "", fmt.Errorf("无法识别的电话号码: %q", raw)
}

// MaskPhone 对规范化后的号码做脱敏，用于后台展示。
// 中国号码形如 "+86 138****1234"，其它号码保留国家码前 3 位与末 4 位。
func MaskPhone(normalized string) string {
	if strings.HasPrefix(normalized, "+86") && len(normalized) == 14 {
		n := normalized[3:] // 11 位
		return "+86 " + n[:3] + "****" + n[7:]
	}
	digits := strings.TrimPrefix(normalized, "+")
	if len(digits) >= 8 {
		return "+" + digits[:3] + "****" + digits[len(digits)-4:]
	}
	return "+****"
}
