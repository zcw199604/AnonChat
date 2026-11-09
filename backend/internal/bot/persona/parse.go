package persona

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	maxUploadBytes    = 50 << 20
	maxJSONBytes      = 5 << 20
	maxTextBytes      = 1 << 20
	maxArchiveBytes   = 100 << 20
	maxArchiveEntries = 300
	maxExpressions    = 100
)

var pngSignature = []byte("\x89PNG\r\n\x1a\n")
var expressionKey = regexp.MustCompile(`^[A-Za-z0-9_一-鿿-]{1,32}$`)
var macro = regexp.MustCompile(`(?i)\{\{(char|user)\}\}|<(bot|user)>`)
var allowedTools = map[string]bool{"recall_memory": true, "get_time": true, "lookup_lore": true}

// Parse 按内容签名选择 PNG、zip 或 ST JSON 解析，不信任上传文件名。
func Parse(fileName string, data []byte, opts ...Option) (Result, error) {
	if len(data) > maxUploadBytes {
		return Result{}, errors.New("上传文件不能超过 50 MB")
	}
	o := options{maxImageBytes: 100 << 20}
	for _, opt := range opts {
		opt(&o)
	}
	var r Result
	var err error
	switch {
	case bytes.HasPrefix(data, pngSignature):
		r, err = parsePNG(data)
	case bytes.HasPrefix(data, []byte("PK\x03\x04")), bytes.HasPrefix(data, []byte("PK\x05\x06")):
		r, err = parseKRP(fileName, data, o)
	default:
		r, err = parseST(data)
	}
	if err != nil {
		return Result{}, err
	}
	r.Card.Name = strings.TrimSpace(r.Card.Name)
	if utf8.RuneCountInString(r.Card.Name) > 40 {
		r.Card.Name = string([]rune(r.Card.Name)[:40])
		r.Warnings = append(r.Warnings, "角色名已截断到 40 字")
	}
	tools := make([]string, 0, len(r.Card.Tools))
	seen := map[string]bool{}
	for _, tool := range r.Card.Tools {
		if !allowedTools[tool] {
			r.Warnings = append(r.Warnings, "已忽略未知工具 "+tool)
			continue
		}
		if !seen[tool] {
			tools = append(tools, tool)
			seen[tool] = true
		}
	}
	r.Card.Tools = tools
	if r.Card.AlternateGreetings == nil {
		r.Card.AlternateGreetings = []string{}
	}
	if r.Card.Lorebook == nil {
		r.Card.Lorebook = []LoreEntry{}
	}
	if r.Card.Extensions == nil {
		r.Card.Extensions = map[string]any{}
	}
	if r.Expressions == nil {
		r.Expressions = []ExpressionFile{}
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	if err = Validate(r.Card); err != nil {
		return Result{}, err
	}
	return r, nil
}

// Validate 校验导入与手动编辑共用的角色卡字段，未知工具由手动编辑直接拒绝。
func Validate(card Card) error {
	name := strings.TrimSpace(card.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		return errors.New("角色名长度需为 1–40 字")
	}
	for _, s := range []string{card.Description, card.Personality, card.Scenario, card.FirstMessage, card.ExampleDialogue, card.SystemPrompt, card.HeartPrompt, card.ReasonPrompt, card.PostHistoryInstructions, card.CreatorNotes} {
		if utf8.RuneCountInString(s) > 20000 {
			return errors.New("单个角色卡文本字段最多 20000 字")
		}
	}
	if len(card.AlternateGreetings) > 20 {
		return errors.New("备选开场白最多 20 条")
	}
	for _, s := range card.AlternateGreetings {
		if utf8.RuneCountInString(s) > 2000 {
			return errors.New("单条备选开场白最多 2000 字")
		}
	}
	if len(card.Lorebook) > 100 {
		return errors.New("世界书最多 100 条")
	}
	for _, entry := range card.Lorebook {
		if utf8.RuneCountInString(entry.Content) > 2000 {
			return errors.New("单条世界书内容最多 2000 字")
		}
		if len(entry.Keys) > 20 {
			return errors.New("单条世界书关键词最多 20 个")
		}
	}
	for _, tool := range card.Tools {
		if !allowedTools[tool] {
			return errors.New("包含未知的内置工具")
		}
	}
	return nil
}

// ValidExpressionKey 判断表情标识是否满足数据库约束。
func ValidExpressionKey(key string) bool { return expressionKey.MatchString(key) }

// ReplaceMacros 只替换已支持的角色与对方宏，原始卡片不变。
func ReplaceMacros(text, name string) string {
	return macro.ReplaceAllStringFunc(text, func(s string) string {
		if strings.EqualFold(s, "{{char}}") || strings.EqualFold(s, "<bot>") {
			return name
		}
		return "对方"
	})
}
