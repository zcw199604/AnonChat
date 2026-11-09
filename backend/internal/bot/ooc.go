package bot

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	styleMarkdownLine   = regexp.MustCompile(`(?m)^[\t ]*(?:#|[-*>] |[0-9]+\. )`)
	styleMarkdownPrefix = regexp.MustCompile(`^[\t ]*(?:#+[\t ]*|[-*>] |[0-9]+\. )`)
)

var styleLeakPhrases = []string{"角色设定", "设定结束", "系统提示", "回复要点", `"thought"`, `"guidance"`}
var styleServicePhrases = []string{"有什么可以帮", "希望对你有帮助", "如果你还有其他问题", "我很乐意为你", "请问还有什么", "作为你的助手"}

// CheckStyle 返回文本气泡命中的聊天风格规则。
// 规则只检查格式、设定泄露、客服腔和长度，不检查任何身份表述。
func CheckStyle(bubbles []Bubble) []string {
	var markdown, leak, service, tooLong bool
	total := 0
	for _, bubble := range bubbles {
		if bubble.Kind != "text" {
			continue
		}
		text := bubble.Text
		markdown = markdown || styleMarkdownLine.MatchString(text) || strings.Contains(text, "**") || strings.Contains(text, "```") || strings.Contains(text, "|---")
		for _, line := range strings.Split(text, "\n") {
			leak = leak || styleLeaks(line)
		}
		service = service || containsStylePhrase(text, styleServicePhrases)
		n := utf8.RuneCountInString(text)
		total += n
		tooLong = tooLong || n > 200
	}
	var out []string
	for _, rule := range []struct {
		name string
		hit  bool
	}{{"markdown", markdown}, {"leak", leak}, {"service", service}, {"too_long", tooLong || total > 400}} {
		if rule.hit {
			out = append(out, rule.name)
		}
	}
	return out
}

func containsStylePhrase(text string, phrases []string) bool {
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}

func styleLeaks(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), `{"`) || containsStylePhrase(text, styleLeakPhrases)
}

// Sanitize 清理重试后仍然不符合聊天风格的文本。
// 不改写输入；非文本气泡保持原样，普通身份表述也不作替换。
func Sanitize(bubbles []Bubble) []Bubble {
	out := make([]Bubble, 0, len(bubbles))
	remaining := 400
	for _, bubble := range bubbles {
		if bubble.Kind != "text" {
			out = append(out, bubble)
			continue
		}
		var lines []string
		for _, line := range strings.Split(bubble.Text, "\n") {
			if styleLeaks(line) {
				continue
			}
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				continue
			}
			for {
				cleaned := styleMarkdownPrefix.ReplaceAllString(line, "")
				if cleaned == line {
					break
				}
				line = cleaned
			}
			line = strings.ReplaceAll(strings.ReplaceAll(line, "**", ""), "```", "")
			if strings.Contains(line, "|---") {
				line = strings.ReplaceAll(line, "|---", "")
				line = strings.Trim(line, "|-: ")
			}
			// Markdown 标记去除后可能显露出位于行首的 JSON。
			if !styleLeaks(line) && strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		bubble.Text = styleTruncate(strings.Join(lines, "\n"), min(200, remaining))
		if bubble.Text == "" {
			continue
		}
		remaining -= utf8.RuneCountInString(bubble.Text)
		out = append(out, bubble)
	}
	return out
}

// styleTruncate 超长时优先停在预算内最后一个句末标点。
func styleTruncate(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	for i := limit - 1; i >= 0; i-- {
		if strings.ContainsRune("。！？.!?", runes[i]) {
			return string(runes[:i+1])
		}
	}
	return string(runes[:limit])
}

func styleFixHint(rules []string) string {
	labels := map[string]string{"markdown": "Markdown", "leak": "设定泄露", "service": "客服腔", "too_long": "回复过长"}
	var names []string
	for _, rule := range rules {
		if name := labels[rule]; name != "" {
			names = append(names, name)
		}
	}
	return "上一次回复不符合聊天风格（" + strings.Join(names, "、") + "）。请重新以角色身份像手机聊天一样简短口语化地回复，不要用 Markdown，不要像客服，不要复述设定。"
}
