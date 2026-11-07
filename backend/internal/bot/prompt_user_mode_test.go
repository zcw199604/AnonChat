package bot

import (
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// userModeInput 返回 user 注入方式的样例输入。
func userModeInput() PromptInput {
	in := sampleInput(store.ConversationKindMatch, PromptPeer{Gender: "female", Age: i16(24)})
	in.PersonaMode = PersonaModeUser
	return in
}

// TestUserModeHasNoSystemMessages 验证 user 方式不发送任何 system 消息，且角色设定在第一条 user 消息里。
func TestUserModeHasNoSystemMessages(t *testing.T) {
	in := userModeInput()
	in.History = []PromptHistory{{Kind: "text", Text: "你叫什么名字"}}
	msgs := BuildMessages(in)
	for i, m := range msgs {
		if m.Role == "system" {
			t.Fatalf("第 %d 条不应是 system: %+v", i, m)
		}
	}
	first := msgs[0]
	if first.Role != "user" {
		t.Fatalf("第一条应为 user: %+v", first)
	}
	for _, want := range []string{"【角色设定", "角色名：小雅", "二十五岁的插画师", "额外要求：不要使用网络用语", "【设定结束】", "你叫什么名字"} {
		if !strings.Contains(first.Content, want) {
			t.Errorf("第一条 user 消息缺少 %q:\n%s", want, first.Content)
		}
	}
	// 设定块在前，对方的第一句话在后。
	if strings.Index(first.Content, "【设定结束】") > strings.Index(first.Content, "你叫什么名字") {
		t.Error("角色设定应位于对方消息之前")
	}
}

// TestUserModePrefixStable 验证同一对话追加历史、换了时间之后，第一条消息（设定块）逐字节不变。
func TestUserModePrefixStable(t *testing.T) {
	in := userModeInput()
	in.History = []PromptHistory{
		{Kind: "text", Text: "你好"},
		{FromBot: true, Kind: "text", Text: "嗨"},
		{Kind: "text", Text: "在干嘛"},
	}
	before := BuildMessages(in)

	in.History = append(in.History,
		PromptHistory{FromBot: true, Kind: "text", Text: "画画"},
		PromptHistory{Kind: "text", Text: "画什么"},
	)
	in.Now = in.Now.Add(10 * time.Minute)
	after := BuildMessages(in)

	// 前两条（设定块 + 第一句、bot 的第一句回复）应完全相同；时间只出现在最后一条。
	for i := 0; i < 2; i++ {
		if before[i] != after[i] {
			t.Fatalf("第 %d 条在追加历史后变化:\n%s\n---\n%s", i, before[i].Content, after[i].Content)
		}
	}
	if strings.Contains(after[0].Content, "当前时间") {
		t.Error("设定块不应包含时间")
	}
	last := after[len(after)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "画什么") || !strings.Contains(last.Content, "当前时间") {
		t.Errorf("本轮提示应追加在最后一条 user 消息末尾: %+v", last)
	}
}

// TestUserModeOpeningWithoutHistory 验证开场（没有历史）时只有一条 user 消息：设定块 + 开场指令。
func TestUserModeOpeningWithoutHistory(t *testing.T) {
	in := userModeInput()
	in.Instruction = InstructionOpening
	msgs := BuildMessages(in)
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("开场应只有一条 user 消息: %+v", msgs)
	}
	if !strings.Contains(msgs[0].Content, "【角色设定") || !strings.Contains(msgs[0].Content, InstructionOpening) {
		t.Errorf("开场消息内容不符:\n%s", msgs[0].Content)
	}
}

// TestUserModeLastAssistantGetsNoteMessage 验证最后一条是 bot 自己的消息时，单独补一条 user 提示，且角色严格交替。
func TestUserModeLastAssistantGetsNoteMessage(t *testing.T) {
	in := userModeInput()
	in.History = []PromptHistory{{FromBot: true, Kind: "text", Text: "在吗"}}
	msgs := BuildMessages(in)
	want := []string{"user", "assistant", "user"}
	if len(msgs) != len(want) {
		t.Fatalf("消息条数 %d，期望 %d: %+v", len(msgs), len(want), msgs)
	}
	for i, r := range want {
		if msgs[i].Role != r {
			t.Fatalf("第 %d 条角色为 %q，期望 %q", i, msgs[i].Role, r)
		}
	}
	if !strings.Contains(msgs[2].Content, "当前时间") {
		t.Errorf("补充的 user 消息应包含本轮提示: %q", msgs[2].Content)
	}
}

// TestUserModeSameBotSharesSetupPrefix 验证同一 bot 在不同对话里，设定块中 bot 级部分逐字节相同（跨对话缓存）。
func TestUserModeSameBotSharesSetupPrefix(t *testing.T) {
	a := userModeInput()
	b := userModeInput()
	b.Kind = store.ConversationKindBottle
	b.BottleText = "今天有点难过"
	ma, mb := BuildMessages(a), BuildMessages(b)
	botPart := buildBotSystem(a.Card, a.MediaTags)
	for _, m := range []ChatMessage{ma[0], mb[0]} {
		if !strings.Contains(m.Content, botPart) {
			t.Fatalf("设定块应包含完整的 bot 级内容:\n%s", m.Content)
		}
	}
}
