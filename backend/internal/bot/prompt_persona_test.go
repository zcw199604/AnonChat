package bot

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/bot/persona"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// promptBotPart 对 user 消息只取会话段之前的 bot 设定，system 则取整条消息。
func promptBotPart(t *testing.T, in PromptInput) string {
	t.Helper()
	msgs := BuildMessages(in)
	if in.PersonaMode != PersonaModeUser {
		return msgs[0].Content
	}
	for _, msg := range msgs {
		if msg.Role == "system" {
			t.Fatal("user 注入不应出现 system")
		}
	}
	end := strings.Index(msgs[0].Content, "\n这是一个")
	if end < 0 {
		t.Fatal("缺少会话段")
	}
	return msgs[0].Content[:end]
}

func TestPromptPersonaPrefixStable(t *testing.T) {
	for _, mode := range []string{PersonaModeSystem, PersonaModeUser} {
		t.Run(mode, func(t *testing.T) {
			in := sampleInput(store.ConversationKindMatch, PromptPeer{})
			in.PersonaMode = mode
			in.CoreMode = "dual"
			in.Card.HeartPrompt = "{{char}}喜欢倾听{{user}}"
			in.Card.Lorebook = []persona.LoreEntry{{Enabled: true, Constant: true, Content: "<BOT>的常驻设定"}}
			in.History = []PromptHistory{{Kind: "text", Text: "你好"}, {FromBot: true, Kind: "text", Text: "嗨"}}
			before := promptBotPart(t, in)
			changes := []struct {
				name   string
				change func(*PromptInput)
			}{
				{"另一会话", func(v *PromptInput) {
					v.Kind = store.ConversationKindBottle
					v.Peer = PromptPeer{Province: "浙江"}
					v.BottleText = "新的瓶子原文"
				}},
				{"记忆", func(v *PromptInput) {
					v.Memory = &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "记忆变化"}}}}
				}},
				{"世界书触发", func(v *PromptInput) { v.TriggeredLore = []string{"触发变化"} }},
				{"关系", func(v *PromptInput) {
					v.Relation = PromptRelation{Stage: "熟悉（轻松随意）", MoodLabel: "开心", MoodValue: 7}
				}},
				{"智核", func(v *PromptInput) {
					v.ReasonNotes = &ReasonResult{Thought: "想法变化", Guidance: "要点变化", Expression: "开心"}
				}},
				{"纠正", func(v *PromptInput) { v.FixHint = "纠正变化" }},
				{"时间历史", func(v *PromptInput) {
					v.Now = v.Now.Add(time.Hour)
					v.History = append(v.History, PromptHistory{Kind: "text", Text: "新的消息"})
				}},
			}
			for _, tc := range changes {
				t.Run(tc.name, func(t *testing.T) {
					next := in
					tc.change(&next)
					if got := promptBotPart(t, next); got != before {
						t.Fatalf("bot 前缀变化:\n%s\n---\n%s", before, got)
					}
				})
			}
			if !strings.Contains(before, "小雅喜欢倾听对方") || !strings.Contains(before, "小雅的常驻设定") {
				t.Fatalf("心核和常驻世界书未渲染或替换宏: %s", before)
			}
		})
	}
}

func TestPromptPersonaSections(t *testing.T) {
	for _, mode := range []string{PersonaModeSystem, PersonaModeUser} {
		t.Run(mode, func(t *testing.T) {
			in := sampleInput(store.ConversationKindMatch, PromptPeer{})
			in.PersonaMode, in.CoreMode = mode, "dual"
			in.Card.HeartPrompt = "心核设定"
			in.Card.ExampleDialogue = "示例内容"
			in.Card.PostHistoryInstructions = "{{CHAR}}请照顾<USER>"
			in.Card.Lorebook = []persona.LoreEntry{{Enabled: true, Constant: true, Content: "常驻内容"}}
			in.Memory = &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "只在会话里的记忆"}}}}
			in.Relation = PromptRelation{Stage: "熟悉（轻松随意）", MoodLabel: "开心", MoodValue: 7}
			in.TriggeredLore = []string{"{{char}}的相关设定"}
			in.ReasonNotes = &ReasonResult{Thought: "只在结尾的想法", Guidance: "只在结尾的要点", Expression: "开心"}
			in.FixHint = "只在结尾的纠正"
			in.History = []PromptHistory{{FromBot: true, Kind: "text", Text: "上一句"}}
			msgs := BuildMessages(in)
			prefix := promptBotPart(t, in)
			assertPromptOrder(t, prefix, []string{"身份与外貌", "人设：心核设定", "示例对话：示例内容", "额外要求", "常驻内容", "可用的媒体标签", outputFormatPrompt})
			conv := msgs[1].Content
			if mode == PersonaModeUser {
				conv = msgs[0].Content
			}
			if !strings.Contains(conv, "只在会话里的记忆") {
				t.Fatal("会话段没有记忆")
			}
			tail := msgs[len(msgs)-1].Content
			assertPromptOrder(t, tail, []string{"当前时间", "你和对方的关系：熟悉（轻松随意）", "你现在的情绪：开心（7/10）", "小雅的相关设定", "想法：只在结尾的想法", "回复要点：只在结尾的要点", "这一轮的表情已经选好，你只需要说话", "小雅请照顾对方", "只在结尾的纠正", InstructionReply})
			if strings.Contains(prefix, "只在") || strings.Contains(tail, "只在会话里的记忆") {
				t.Fatal("内容出现在错误段落")
			}
			in.CoreMode = "single"
			if tail := buildTailSystem(in); strings.Contains(tail, "只在结尾的想法") || strings.Contains(tail, "表情已经选好") {
				t.Fatal("single 不应注入智核结论")
			}
			in.Relation = PromptRelation{Stage: "陌生（普通的陌生人社交）"}
			if tail := buildTailSystem(in); !strings.Contains(tail, "陌生") || strings.Contains(tail, "你现在的情绪") {
				t.Fatal("空情绪只渲染关系")
			}
		})
	}
}

func assertPromptOrder(t *testing.T, text string, wants []string) {
	t.Helper()
	last := -1
	for _, want := range wants {
		pos := strings.Index(text, want)
		if pos < 0 || pos <= last {
			t.Fatalf("内容 %q 缺失或顺序错误:\n%s", want, text)
		}
		last = pos
	}
}

func TestPromptExpressions(t *testing.T) {
	in := sampleInput(store.ConversationKindMatch, PromptPeer{})
	in.Expressions = []PromptExpression{{Key: "晚安", Description: "{{char}}道晚安", Sort: 2}, {Key: "b", Description: "后", Sort: 1}, {Key: "a", Description: "先", Sort: 1}}
	original := append([]PromptExpression(nil), in.Expressions...)
	for _, mode := range []string{PersonaModeSystem, PersonaModeUser} {
		in.PersonaMode, in.CoreMode = mode, "single"
		prefix := promptBotPart(t, in)
		if !strings.Contains(prefix, "a（先）、b（后）、晚安（小雅道晚安）") || !strings.Contains(prefix, "想发表情时单独一行写 [表情:表情名]，一轮最多一个。") {
			t.Fatalf("表情词表错误: %s", prefix)
		}
		in.CoreMode = "dual"
		if prefix := promptBotPart(t, in); strings.Contains(prefix, "可用表情") || strings.Contains(prefix, "[表情:") {
			t.Fatal("dual 不应渲染表情词表")
		}
		in.CoreMode = "single"
		saved := in.Expressions
		in.Expressions = nil
		if strings.Contains(promptBotPart(t, in), "[表情:") {
			t.Fatal("无表情不应渲染说明")
		}
		in.Expressions = saved
	}
	if !reflect.DeepEqual(original, in.Expressions) {
		t.Fatal("不应重排原始表情切片")
	}
}

func TestLoreMatching(t *testing.T) {
	entries := []persona.LoreEntry{
		{Enabled: true, Keys: []string{"Cat"}, Content: "晚", Order: 2},
		{Enabled: true, Keys: []string{"没命中", "CAT"}, Content: "早", Order: 1},
		{Enabled: true, CaseSensitive: true, Keys: []string{"Cat"}, Content: "大小写敏感"},
		{Enabled: false, Keys: []string{"cat"}, Content: "已禁用"},
		{Enabled: true, Constant: true, Keys: []string{"cat"}, Content: "常驻"},
		{Enabled: true, Keys: []string{""}, Content: "空关键词"},
	}
	before := append([]persona.LoreEntry(nil), entries...)
	if got := matchLore(entries, []string{"I love cats"}); !reflect.DeepEqual(got, []string{"早", "晚"}) {
		t.Fatalf("命中或排序错误: %v", got)
	}
	if got := matchLore(entries, []string{"Cat"}); len(got) != 3 || got[0] != "大小写敏感" {
		t.Fatalf("大小写敏感命中错误: %v", got)
	}
	if !reflect.DeepEqual(before, entries) {
		t.Fatal("不应修改原始世界书")
	}
	entries = []persona.LoreEntry{{Enabled: true, Keys: []string{"字"}, Content: strings.Repeat("中", 1500)}, {Enabled: true, Keys: []string{"字"}, Content: "超出"}, {Enabled: true, Keys: []string{"字"}, Content: "末尾"}}
	if got := matchLore(entries, []string{"字"}); len(got) != 1 || len([]rune(got[0])) != 1500 {
		t.Fatal("1500 字上限错误")
	}
	entries[0].Content = strings.Repeat("中", 1501)
	if got := matchLore(entries, []string{"字"}); !reflect.DeepEqual(got, []string{"超出", "末尾"}) {
		t.Fatalf("超长条目应丢弃: %v", got)
	}
}

func TestPromptLoreRecentPeerTexts(t *testing.T) {
	in := sampleInput(store.ConversationKindBottle, PromptPeer{})
	for _, key := range []string{"旧消息", "最近消息", "瓶子关键词", "机器人关键词", "图片关键词"} {
		in.Card.Lorebook = append(in.Card.Lorebook, persona.LoreEntry{Enabled: true, Keys: []string{key}, Content: key + "的设定"})
	}
	in.History = []PromptHistory{{Kind: "text", Text: "旧消息"}, {FromBot: true, Kind: "text", Text: "机器人关键词"}}
	for i := 0; i < 6; i++ {
		in.History = append(in.History, PromptHistory{Kind: "text", Text: "最近消息"}, PromptHistory{Kind: "image", Text: "图片关键词"})
	}
	in.BottleText = "瓶子关键词"
	tail := buildTailSystem(in)
	for _, want := range []string{"最近消息的设定", "瓶子关键词的设定"} {
		if !strings.Contains(tail, want) {
			t.Errorf("缺少 %s", want)
		}
	}
	for _, bad := range []string{"旧消息的设定", "机器人关键词的设定", "图片关键词的设定"} {
		if strings.Contains(tail, bad) {
			t.Errorf("不应命中 %s", bad)
		}
	}
}

type greetingRand struct {
	index int64
	bound int64
}

func (r *greetingRand) Int63n(n int64) int64 { r.bound = n; return r.index }

func TestGreetingSelectionAndMacro(t *testing.T) {
	card := PromptCard{Name: "小雅", FirstMessage: "  {{char}}问候{{user}}  ", AlternateGreetings: []string{" ", "<BOT>说你好", ""}}
	before := card.FirstMessage
	r := &greetingRand{index: 1}
	if got := pickGreeting(card, r); got != "小雅说你好" || r.bound != 2 {
		t.Fatalf("挑选开场白错误: %q bound=%d", got, r.bound)
	}
	if got := pickGreeting(card, nil); got != "小雅问候对方" {
		t.Fatalf("首项宏替换错误: %q", got)
	}
	if card.FirstMessage != before || card.AlternateGreetings[1] != "<BOT>说你好" {
		t.Fatal("不应改写原始角色卡")
	}
	if got := pickGreeting(PromptCard{}, r); got != "" {
		t.Fatal("没有开场白应为空")
	}
}

func TestPromptCardFromStoreAndMacro(t *testing.T) {
	lore, _ := json.Marshal([]persona.LoreEntry{{Enabled: true, Constant: true, Content: "{{char}}的家"}})
	stored := store.CharacterCard{Name: "小雅", Description: "{{CHAR}}的背景", HeartPrompt: "{{USER}}的朋友", ReasonPrompt: "智核原文", AlternateGreetings: []string{"<bot>开场"}, PostHistoryInstructions: "记住<USER>", Lorebook: lore, Tools: []string{"get_time"}, CreatorNotes: "后台备注"}
	card := CardFromStore(stored)
	if card.HeartPrompt != stored.HeartPrompt || card.ReasonPrompt != stored.ReasonPrompt || len(card.AlternateGreetings) != 1 || len(card.Lorebook) != 1 || card.PostHistoryInstructions != stored.PostHistoryInstructions {
		t.Fatalf("角色卡投影缺少字段: %+v", card)
	}
	prefix := buildBotSystem(card, nil)
	if strings.Contains(prefix, "{{") || strings.Contains(prefix, "<USER>") || !strings.Contains(prefix, "对方的朋友") || strings.Contains(prefix, "智核原文") || strings.Contains(prefix, "后台备注") {
		t.Fatalf("宏或字段隔离错误: %s", prefix)
	}
	if stored.Description != "{{CHAR}}的背景" || card.Lorebook[0].Content != "{{char}}的家" {
		t.Fatal("渲染不应改写原文")
	}
}

func TestParseReplyExpression(t *testing.T) {
	if got := ParseReply("小雅：\n一\n二\n三"); len(got) != 3 {
		t.Fatalf("空角色名前缀不应占气泡名额: %+v", got)
	}
	for _, text := range []string{"[表情:开心]\n一\n[图片:猫]\n三\n四\n[表情:第二]", "一\n[图片:猫]\n三\n四\n[表情： 开心 ]\n[表情:第二]"} {
		got := ParseReply(text)
		normal, expressions := 0, 0
		for _, b := range got {
			if b.Kind == "expression" {
				expressions++
				if b.Key != "开心" {
					t.Fatalf("表情 key 错误: %+v", b)
				}
			} else {
				normal++
			}
		}
		if len(got) != 4 || normal != 3 || expressions != 1 {
			t.Fatalf("表情不应占普通气泡名额: %+v", got)
		}
	}
	if got := ParseReply("[表情:开心]"); len(got) != 1 || got[0].Kind != "expression" {
		t.Fatalf("单表情错误: %+v", got)
	}
	if got := ParseReply("[沉默]"); len(got) != 0 {
		t.Fatal("沉默语义不应改变")
	}
}

func TestPromptConstantLoreMacroBudget(t *testing.T) {
	card := PromptCard{Name: "{{user}}", Lorebook: []persona.LoreEntry{
		{Enabled: true, Constant: true, Content: "后", Order: 2},
		{Enabled: true, Constant: true, Content: "{{char}}", Order: 1},
		{Constant: true, Content: "禁用"},
	}}
	got := buildBotSystem(card, nil)
	if !strings.Contains(got, "常驻设定：{{user}}\n后") || strings.Contains(got, "禁用") {
		t.Fatalf("常驻条目应排序、过滤且只展开一次: %s", got)
	}
	card.Name = strings.Repeat("名", 1500)
	got = buildBotSystem(card, nil)
	if !strings.Contains(got, "常驻设定："+card.Name+"\n输出格式") {
		t.Fatal("常驻条目应按展开后的 1500 字预算丢弃超额内容")
	}
	card.Name += "名"
	if got := buildBotSystem(card, nil); !strings.Contains(got, "常驻设定：后\n输出格式") {
		t.Fatal("超长常驻条目应跳过")
	}
}
