package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/bot/persona"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// ReasonMood 是智核给出的本轮情绪。
type ReasonMood struct {
	Label string `json:"label"`
	Value int    `json:"value"`
}

// ReasonToolArguments 是内置工具支持的文本参数。
type ReasonToolArguments struct {
	Query   string `json:"query"`
	Keyword string `json:"keyword"`
}

// ReasonToolCall 是 JSON 文本协议中的工具请求，不使用原生 function calling。
type ReasonToolCall struct {
	Name      string              `json:"name"`
	Arguments ReasonToolArguments `json:"arguments"`
}

// ReasonOutput 是智核的工具请求或最终结论。
type ReasonOutput struct {
	Thought       string           `json:"thought"`
	Guidance      string           `json:"guidance"`
	Mood          ReasonMood       `json:"mood"`
	AffinityDelta int              `json:"affinity_delta"`
	Expression    string           `json:"expression"`
	Silent        bool             `json:"silent"`
	ToolCalls     []ReasonToolCall `json:"tool_calls"`
}

const reasonBasePrompt = "你是角色「%s」的内心（智核）。你不直接对对方说话，而是在角色开口前想清楚：对方说了什么、想要什么、你此刻的情绪、你和对方的关系，以及这一轮该怎么回。你的结论会交给负责说话的心核。"

const reasonOutputPrompt = `只输出一个 JSON 对象，二选一：
请求工具：{"tool_calls":[{"name":"工具名","arguments":{}}]}
最终结论：{"thought":"内心想法","guidance":"回复要点","mood":{"label":"情绪","value":5},"affinity_delta":0,"expression":"表情名或空字符串","silent":false}
thought、guidance 各最多300字，mood.label最多10字，mood.value为0到10，affinity_delta为-5到5。每轮最多请求3个工具；最多两轮工具，然后必须输出最终结论。表情只能选择可用列表中的一个，不发表情则留空。`

var reasonTools = []struct{ name, description string }{
	{"recall_memory", "按 query（最多3个关键词）查找对方的画像与事件"},
	{"get_time", "返回当前本地日期、星期和时间"},
	{"lookup_lore", "按 keyword 查找已启用的世界书设定"},
}

// ParseReasonOutput 容忍代码围栏和前后说明，拒绝不符合二选一协议的对象。
func ParseReasonOutput(text string) (ReasonOutput, error) {
	start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	if start < 0 || end < start {
		return ReasonOutput{}, errors.New("智核输出不是有效 JSON")
	}
	var raw map[string]json.RawMessage
	var out ReasonOutput
	data := []byte(text[start : end+1])
	if json.Unmarshal(data, &raw) != nil || json.Unmarshal(data, &out) != nil {
		return ReasonOutput{}, errors.New("智核输出不是有效 JSON")
	}
	final := false
	for _, key := range []string{"thought", "guidance", "mood", "affinity_delta", "expression", "silent"} {
		if _, ok := raw[key]; ok {
			final = true
		}
	}
	if _, ok := raw["tool_calls"]; ok {
		if len(out.ToolCalls) == 0 || final {
			return ReasonOutput{}, errors.New("智核输出协议无效")
		}
	} else if !final {
		return ReasonOutput{}, errors.New("智核输出缺少结论")
	}
	out.Thought = truncateRunes(out.Thought, 300)
	out.Guidance = truncateRunes(out.Guidance, 300)
	out.Mood.Label = truncateRunes(out.Mood.Label, 10)
	out.Mood.Value = max(0, min(10, out.Mood.Value))
	out.AffinityDelta = max(-5, min(5, out.AffinityDelta))
	return out, nil
}

func reasonToolEnabled(card PromptCard, name string) bool {
	known := false
	for _, tool := range reasonTools {
		if tool.name == name {
			known = true
			break
		}
	}
	if !known {
		return false
	}
	if len(card.Tools) == 0 {
		return true
	}
	for _, enabled := range card.Tools {
		if name == enabled {
			return true
		}
	}
	return false
}

// buildReasonPrefix 只依赖角色卡与表情词表，保持 bot 级缓存前缀稳定。
func buildReasonPrefix(in PromptInput) string {
	var b strings.Builder
	fmt.Fprintf(&b, reasonBasePrompt, in.Card.Name)
	appendField(&b, "智核人设", in.Card.ReasonPrompt)
	appendField(&b, "身份与外貌", truncateRunes(in.Card.Description, 1500))
	appendField(&b, "性格与说话风格", truncateRunes(in.Card.Personality, 1500))
	appendField(&b, "人设", truncateRunes(in.Card.HeartPrompt, 1500))
	var tools []string
	for _, tool := range reasonTools {
		if reasonToolEnabled(in.Card, tool.name) {
			tools = append(tools, tool.name+"："+tool.description)
		}
	}
	appendField(&b, "可用工具", strings.Join(tools, "\n"))
	expressions := append([]PromptExpression(nil), in.Expressions...)
	sort.SliceStable(expressions, func(i, j int) bool {
		if expressions[i].Sort != expressions[j].Sort {
			return expressions[i].Sort < expressions[j].Sort
		}
		return expressions[i].Key < expressions[j].Key
	})
	var labels []string
	for _, expression := range expressions {
		labels = append(labels, expression.Key+"（"+expression.Description+"）")
	}
	appendField(&b, "可用表情", strings.Join(labels, "、"))
	b.WriteString("\n" + reasonOutputPrompt)
	return persona.ReplaceMacros(b.String(), in.Card.Name)
}

func buildReasonMessages(in PromptInput) []ChatMessage {
	var record strings.Builder
	start := max(0, len(in.History)-20)
	for _, h := range in.History[start:] {
		who := "对方"
		if h.FromBot {
			who = "你"
		}
		fmt.Fprintf(&record, "%s：%s\n", who, historyContent(h))
	}
	loc := in.Loc
	if loc == nil {
		loc = fallbackLocation
	}
	fmt.Fprintf(&record, "当前时间：%s\n", in.Now.In(loc).Format("2006-01-02 15:04:05"))
	if in.Relation.Stage != "" {
		fmt.Fprintf(&record, "你和对方的关系：%s。", in.Relation.Stage)
		if in.Relation.MoodLabel != "" {
			fmt.Fprintf(&record, "你现在的情绪：%s（%d/10）。", in.Relation.MoodLabel, in.Relation.MoodValue)
		}
		record.WriteByte('\n')
	}
	lore := in.TriggeredLore
	if lore == nil {
		lore = matchLore(in.Card.Lorebook, loreTexts(in))
	}
	if lore = expandedLore(lore, in.Card.Name); len(lore) > 0 {
		fmt.Fprintf(&record, "相关设定：%s", strings.Join(lore, "\n"))
	}
	setup := buildReasonPrefix(in) + "\n" + buildConversationSystem(in)
	return personaMessages(in.PersonaMode, setup, []ChatMessage{{Role: "user", Content: record.String()}})
}

// recallMemory 对画像与事件做不区分大小写的子串匹配，结果最多八行。
func recallMemory(memory *PromptMemory, query string) string {
	if memory == nil {
		return "（没有相关记忆）"
	}
	keywords := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return r == ',' || r == '，' || unicode.IsSpace(r) })
	if len(keywords) > 3 {
		keywords = keywords[:3]
	}
	matches := func(text string) bool {
		text = strings.ToLower(text)
		for _, key := range keywords {
			if strings.Contains(text, key) {
				return true
			}
		}
		return false
	}
	var lines []string
	fields := profileFields(memory.Profile)
	for _, field := range fields {
		if matches(field.topic + " " + field.subtopic + " " + field.value.Value) {
			lines = append(lines, field.topic+"："+field.subtopic+"="+field.value.Value)
		}
		if len(lines) == 8 {
			return strings.Join(lines, "\n")
		}
	}
	for _, event := range memory.Events {
		if matches(event.Gist) {
			lines = append(lines, event.CreatedAt.Format("01-02")+" "+event.Gist)
		}
		if len(lines) == 8 {
			break
		}
	}
	if len(lines) == 0 {
		return "（没有相关记忆）"
	}
	return strings.Join(lines, "\n")
}

func lookupLore(card PromptCard, keyword string) string {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return "（没有相关设定）"
	}
	var lines []string
	entries := append([]persona.LoreEntry(nil), card.Lorebook...)
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Order < entries[j].Order })
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}
		text := strings.ToLower(entry.Content + " " + strings.Join(entry.Keys, " "))
		if strings.Contains(text, keyword) {
			lines = append(lines, truncateRunes(persona.ReplaceMacros(entry.Content, card.Name), 500))
		}
		if len(lines) == 3 {
			break
		}
	}
	if len(lines) == 0 {
		return "（没有相关设定）"
	}
	return strings.Join(lines, "\n")
}

func reasonLocalTool(in PromptInput, call ReasonToolCall, memory *PromptMemory) string {
	if !reasonToolEnabled(in.Card, call.Name) {
		return "（工具不可用）"
	}
	switch call.Name {
	case "recall_memory":
		return recallMemory(memory, call.Arguments.Query)
	case "lookup_lore":
		return lookupLore(in.Card, call.Arguments.Keyword)
	case "get_time":
		loc := in.Loc
		if loc == nil {
			loc = fallbackLocation
		}
		now := in.Now.In(loc)
		weekdays := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
		return now.Format("2006-01-02") + " " + weekdays[now.Weekday()] + " " + now.Format("15:04:05 MST")
	}
	return "（工具不可用）"
}

// runReasonLoop 限制工具轮数与调用数，任一调用、解析或工具查询失败都降级。
func runReasonLoop(ctx context.Context, in PromptInput, call func(context.Context, []ChatMessage) (ChatResult, error), tool func(ReasonToolCall) (string, error)) *ReasonOutput {
	messages := buildReasonMessages(in)
	finalRequired := false
	for step := 0; step < 3; step++ {
		if ctx.Err() != nil {
			return nil
		}
		result, err := call(ctx, messages)
		if err != nil {
			return nil
		}
		out, err := ParseReasonOutput(result.Content)
		if err != nil {
			return nil
		}
		if len(out.ToolCalls) == 0 {
			valid := false
			for _, expression := range in.Expressions {
				if expression.Key == out.Expression {
					valid = true
					break
				}
			}
			if !valid {
				out.Expression = ""
			}
			return &out
		}
		if step == 2 || finalRequired {
			return nil
		}
		var results []string
		for _, request := range out.ToolCalls[:min(3, len(out.ToolCalls))] {
			value := "（工具不可用）"
			if reasonToolEnabled(in.Card, request.Name) {
				if tool == nil {
					return nil
				}
				value, err = tool(request)
				if err != nil {
					return nil
				}
			}
			results = append(results, request.Name+"："+value)
		}
		content := "工具结果：\n" + strings.Join(results, "\n")
		if step == 1 || len(out.ToolCalls) > 3 {
			content += "\n不要再调用工具，直接输出结论。"
			finalRequired = true
		}
		messages = append(messages, ChatMessage{Role: "assistant", Content: result.Content}, ChatMessage{Role: "user", Content: content})
	}
	return nil
}

// runReason 调用智核并计量，工具召回始终按 bot 与对方身份隔离。
func (e *Engine) runReason(ctx context.Context, bot store.Bot, convID, peerID pgtype.UUID, in PromptInput) *ReasonOutput {
	call := func(ctx context.Context, messages []ChatMessage) (ChatResult, error) {
		if !e.modelAllowed(ctx, bot.ID) {
			return ChatResult{}, errors.New("智核调用已达到限额或不可用")
		}
		client, timeout := e.llm()
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		req := e.modelRequest(bot, "reason")
		req.Messages = messages
		result, err := client.Chat(cctx, req)
		if err == nil || result.PromptTokens+result.CompletionTokens > 0 {
			e.recordUsage(ctx, bot, convID, result, false, "reason")
		}
		return result, err
	}
	tool := func(request ReasonToolCall) (string, error) {
		memory := in.Memory
		if request.Name == "recall_memory" && bot.MemoryEnabled {
			current, err := e.q.GetPeerMemory(ctx, store.GetPeerMemoryParams{BotID: bot.ID, PeerIdentityID: peerID})
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return "", errors.New("读取智核记忆失败")
			}
			if errors.Is(err, pgx.ErrNoRows) {
				memory = nil
			} else {
				memory = &PromptMemory{}
				if json.Unmarshal(current.Profile, &memory.Profile) != nil {
					return "", errors.New("读取智核画像失败")
				}
				events, err := e.q.ListPeerEvents(ctx, store.ListPeerEventsParams{BotID: bot.ID, PeerIdentityID: peerID, Limit: 50})
				if err != nil {
					return "", errors.New("读取智核事件失败")
				}
				for _, event := range events {
					memory.Events = append(memory.Events, PromptMemoryEvent{Gist: event.Gist, CreatedAt: event.CreatedAt.Time})
				}
			}
		}
		if !bot.MemoryEnabled {
			memory = nil
		}
		return reasonLocalTool(in, request, memory), nil
	}
	return runReasonLoop(ctx, in, call, tool)
}
