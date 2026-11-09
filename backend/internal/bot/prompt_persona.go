package bot

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/anonchat/anonchat/backend/internal/bot/persona"
)

// PromptExpression 是角色卡表情的提示词投影。
type PromptExpression struct {
	Key, Description string
	Sort             int32
}

// PromptRelation 接收已计算的关系阶段，阶段映射由 P4 的记忆层提供。
type PromptRelation struct {
	Stage, MoodLabel string
	MoodValue        int
}

// ReasonResult 是供心核使用的智核结论，解析和执行由 P5 接入。
type ReasonResult struct{ Thought, Guidance, Expression string }

// loreMaxRunes 是常驻设定和本轮相关设定各自的内容字数上限。
const loreMaxRunes = 1500

// matchLore 只匹配启用的非常驻条目，排序与裁剪均不修改原始世界书。
func matchLore(entries []persona.LoreEntry, texts []string) []string {
	return limitLore(orderedLore(entries, false, texts))
}

func orderedLore(entries []persona.LoreEntry, constant bool, texts []string) []string {
	selected := make([]persona.LoreEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.Enabled || entry.Constant != constant {
			continue
		}
		matched := constant
		for _, key := range entry.Keys {
			if key == "" || matched {
				continue
			}
			for _, text := range texts {
				if !entry.CaseSensitive {
					if strings.Contains(strings.ToLower(text), strings.ToLower(key)) {
						matched = true
						break
					}
				} else if strings.Contains(text, key) {
					matched = true
					break
				}
			}
		}
		if matched {
			selected = append(selected, entry)
		}
	}
	sort.SliceStable(selected, func(i, j int) bool { return selected[i].Order < selected[j].Order })
	out := make([]string, 0, len(selected))
	for _, entry := range selected {
		out = append(out, entry.Content)
	}
	return out
}

func limitLore(contents []string) []string {
	out := make([]string, 0, len(contents))
	total := 0
	for _, content := range contents {
		n := utf8.RuneCountInString(content)
		if n == 0 || total+n > loreMaxRunes {
			continue
		}
		out = append(out, content)
		total += n
	}
	return out
}

// expandedLore 按实际展开后的字数裁剪，避免角色名展开宏后突破上限。
func expandedLore(contents []string, name string) []string {
	expanded := make([]string, 0, len(contents))
	for _, content := range contents {
		expanded = append(expanded, persona.ReplaceMacros(content, name))
	}
	return limitLore(expanded)
}

// constantLore 按宏展开后的字数选择原文，由第 1 段统一展开，避免二次替换。
func constantLore(card PromptCard) []string {
	var out []string
	total := 0
	for _, content := range orderedLore(card.Lorebook, true, nil) {
		n := utf8.RuneCountInString(persona.ReplaceMacros(content, card.Name))
		if n == 0 || total+n > loreMaxRunes {
			continue
		}
		out = append(out, content)
		total += n
	}
	return out
}

// loreTexts 只读取最近六条对方文本，再附上瓶子原文。
func loreTexts(in PromptInput) []string {
	texts := make([]string, 0, 7)
	for i := len(in.History) - 1; i >= 0 && len(texts) < 6; i-- {
		h := in.History[i]
		if !h.FromBot && h.Kind == "text" {
			texts = append(texts, h.Text)
		}
	}
	if in.BottleText != "" {
		texts = append(texts, in.BottleText)
	}
	return texts
}

func promptExpressions(in PromptInput) []PromptExpression {
	if in.CoreMode == "single" {
		return in.Expressions
	}
	return nil
}

// pickGreeting 只从非空开场白中选择，随机源由引擎在后续阶段传入。
func pickGreeting(card PromptCard, rnd RandSource) string {
	greetings := make([]string, 0, 1+len(card.AlternateGreetings))
	for _, text := range append([]string{card.FirstMessage}, card.AlternateGreetings...) {
		if text = strings.TrimSpace(text); text != "" {
			greetings = append(greetings, text)
		}
	}
	if len(greetings) == 0 {
		return ""
	}
	return persona.ReplaceMacros(greetings[(pacer{rnd: rnd}).randIntn(len(greetings))], card.Name)
}

func renderPromptTail(in PromptInput, stamp, instruction string) string {
	parts := make([]string, 0, 6)
	if in.Relation.Stage != "" {
		relation := "你和对方的关系：" + in.Relation.Stage + "。"
		if in.Relation.MoodLabel != "" {
			relation += fmt.Sprintf("你现在的情绪：%s（%d/10）。", in.Relation.MoodLabel, in.Relation.MoodValue)
		}
		parts = append(parts, relation)
	}
	lore := in.TriggeredLore
	if lore == nil {
		lore = matchLore(in.Card.Lorebook, loreTexts(in))
	}
	if lore = expandedLore(lore, in.Card.Name); len(lore) > 0 {
		parts = append(parts, "相关设定："+strings.Join(lore, "\n"))
	}
	if in.CoreMode == "dual" && in.ReasonNotes != nil {
		if in.ReasonNotes.Thought != "" {
			parts = append(parts, "想法："+in.ReasonNotes.Thought)
		}
		if in.ReasonNotes.Guidance != "" {
			parts = append(parts, "回复要点："+in.ReasonNotes.Guidance)
		}
		if in.ReasonNotes.Expression != "" {
			parts = append(parts, "这一轮的表情已经选好，你只需要说话")
		}
	}
	if text := strings.TrimSpace(in.Card.PostHistoryInstructions); text != "" {
		parts = append(parts, persona.ReplaceMacros(text, in.Card.Name))
	}
	if in.FixHint != "" {
		parts = append(parts, in.FixHint)
	}
	if in.PersonaMode == PersonaModeUser && len(parts) == 0 {
		return fmt.Sprintf("系统提示：当前时间 %s。%s", stamp, instruction)
	}
	prefix := fmt.Sprintf("当前时间：%s", stamp)
	if in.PersonaMode == PersonaModeUser {
		prefix = fmt.Sprintf("系统提示：当前时间 %s。", stamp)
	}
	return strings.Join(append(append([]string{prefix}, parts...), instruction), "\n")
}
