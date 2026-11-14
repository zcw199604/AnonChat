package external

import (
	"regexp"
	"strings"

	"github.com/anonchat/anonchat/backend/internal/identity"
)

// nicknameSuffixPattern 匹配昵称末尾的“#数字”编号后缀（历史上由 identity.GenerateNickname 追加）。
var nicknameSuffixPattern = regexp.MustCompile(`#\d+$`)

// externalNickname 把绑定里保存的昵称转换成可发往外站的形式。
// chat2019 等外站拒绝含过多数字的昵称，因此历史昵称“社恐的考拉#1968”会被还原为“社恐的考拉”；
// 若去掉编号后为空或仍含 ASCII 数字，则重新生成一个不带编号的昵称。
func externalNickname(stored string) string {
	nick := strings.TrimSpace(nicknameSuffixPattern.ReplaceAllString(strings.TrimSpace(stored), ""))
	if nick == "" || hasASCIIDigit(nick) {
		nick = identity.GeneratePlainNickname()
	}
	return nick
}

// hasASCIIDigit 报告 s 是否含有任意 ASCII 数字。
func hasASCIIDigit(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			return true
		}
	}
	return false
}
