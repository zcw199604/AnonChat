package external

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// nicknameSuffixPattern 匹配昵称末尾的“#数字”编号后缀（历史上由 identity.GenerateNickname 追加）。
var nicknameSuffixPattern = regexp.MustCompile(`#\d+$`)

// externalNickname 把绑定里保存的昵称转换成可发往外站的形式。
// chat2019 等外站拒绝含过多数字的昵称，因此历史昵称“社恐的考拉#1968”会被还原为“社恐的考拉”；
// 若去掉编号后为空或仍含 ASCII 数字，则重新生成一个不带编号的昵称。
//
// 等价于 externalNicknameFor("", stored)：只考虑绑定昵称。
func externalNickname(stored string) string { return externalNicknameFor("", stored) }

// externalNicknameFor 把本站昵称转换成可发往外站的形式（出站昵称透传）。
//  1. 优先使用 current（我方用户当前的本站昵称）；
//  2. 若为空或清洗后仍含 ASCII 数字，回退到 fallback（绑定里保存的昵称）；
//  3. 都不可用时随机生成一个不带编号的昵称。
//
// “清洗”指去掉首尾空白与末尾的“#数字”编号后缀。
func externalNicknameFor(current, fallback string) string {
	for _, candidate := range []string{current, fallback} {
		if nick := cleanExternalNickname(candidate); nick != "" {
			return nick
		}
	}
	return identity.GenerateNickname()
}

// outboundNickname 决定发往外站使用的昵称（出站透传）：
// 优先我方用户当前的本站昵称，回退绑定昵称，最后随机生成。ident 可能为 nil（防御）。
func outboundNickname(ident *store.Identity, fallback string) string {
	current := ""
	if ident != nil {
		current = ident.Nickname
	}
	return externalNicknameFor(current, fallback)
}

// cleanExternalNickname 去掉首尾空白与末尾编号后缀。
// 结果为空或仍含 ASCII 数字时返回空串，表示该昵称无法发往外站。
func cleanExternalNickname(s string) string {
	nick := strings.TrimSpace(nicknameSuffixPattern.ReplaceAllString(strings.TrimSpace(s), ""))
	if nick == "" || hasASCIIDigit(nick) {
		return ""
	}
	return nick
}

// sanitizeExternalPeerNickname 把外站对方的昵称清洗成我方影子身份的昵称：
// 去掉首尾空白与控制字符（含换行），按 rune 截断到 16 个字符，为空则随机生成。
// 外站昵称允许带数字（只用于我方显示，不参与出站）。
func sanitizeExternalPeerNickname(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	nick := strings.TrimSpace(b.String())
	if runes := []rune(nick); len(runes) > externalPeerNicknameMaxRunes {
		nick = string(runes[:externalPeerNicknameMaxRunes])
	}
	if nick == "" {
		return identity.GenerateNickname()
	}
	return nick
}

// externalPeerNicknameMaxRunes 是影子身份昵称的最大字符数（与外站昵称长度无关的上限）。
const externalPeerNicknameMaxRunes = 16

// hasASCIIDigit 报告 s 是否含有任意 ASCII 数字。
func hasASCIIDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}
