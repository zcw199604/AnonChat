package bot

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// sampleInput 返回一个可用于比较前缀的输入。
func sampleInput(kind store.ConversationKind, peer PromptPeer) PromptInput {
	return PromptInput{
		Card: PromptCard{
			Name:         "小雅",
			Description:  "二十五岁的插画师",
			Personality:  "温柔、话少、喜欢倾听",
			Scenario:     "深夜的画室",
			FirstMessage: "在吗",
			SystemPrompt: "不要使用网络用语",
		},
		Instruction: InstructionReply,
		MediaTags:   []string{"夜景", "自拍", "咖啡"},
		Kind:        kind,
		Peer:        peer,
		Now:         time.Date(2026, 11, 6, 23, 5, 0, 0, time.UTC),
		Loc:         time.FixedZone("UTC+8", 8*3600),
	}
}

// TestBuildMessagesBotSystemIsPrefixStable 验证同一 bot 在不同对话里第 1 条 system 逐字节相同。
func TestBuildMessagesBotSystemIsPrefixStable(t *testing.T) {
	inA := sampleInput(store.ConversationKindMatch, PromptPeer{Gender: "female", Age: i16(24), Tags: []string{"音乐"}})
	inB := sampleInput(store.ConversationKindBottle, PromptPeer{Gender: "male", Province: "浙江", Tags: []string{"读书"}})
	inB.Now = inA.Now.Add(48 * time.Hour)
	inB.BottleText = "今天有点难过"

	a := BuildMessages(inA)
	b := BuildMessages(inB)
	if len(a) < 3 || len(b) < 3 {
		t.Fatalf("消息条数不足: %d / %d", len(a), len(b))
	}
	if a[0].Role != "system" || b[0].Role != "system" {
		t.Fatalf("第 1 条应为 system: %q / %q", a[0].Role, b[0].Role)
	}
	if a[0].Content != b[0].Content {
		t.Fatalf("bot 级 system 前缀不稳定:\n--- A ---\n%s\n--- B ---\n%s", a[0].Content, b[0].Content)
	}
	// 时间只出现在最后一条 system 里。
	if a[len(a)-1].Role != "system" || !strings.Contains(a[len(a)-1].Content, "当前时间") {
		t.Fatalf("最后一条应是带当前时间的 system: %+v", a[len(a)-1])
	}
	if strings.Contains(a[0].Content, "当前时间") {
		t.Error("bot 级 system 不应包含时间")
	}
	// bot 级 system 也不应包含对话相关内容。
	for _, forbidden := range []string{"匹配", "漂流瓶", "对方的公开资料", "浙江"} {
		if strings.Contains(a[0].Content, forbidden) {
			t.Errorf("bot 级 system 不应包含 %q", forbidden)
		}
	}
}

// TestBuildMessagesMediaTagsSorted 验证媒体词表按字典序输出。
func TestBuildMessagesMediaTagsSorted(t *testing.T) {
	in := sampleInput(store.ConversationKindMatch, PromptPeer{})
	in.MediaTags = []string{"b", "a", "c"}
	msgs := BuildMessages(in)
	if !strings.Contains(msgs[0].Content, "a、b、c") {
		t.Errorf("媒体标签未排序: %s", msgs[0].Content)
	}
	// 没有媒体时不输出该段。
	in.MediaTags = nil
	if strings.Contains(BuildMessages(in)[0].Content, "可用的媒体标签") {
		t.Error("没有媒体时不应输出媒体词表")
	}
}

// TestBuildMessagesHistoryPrefixAppend 验证同一对话追加历史后，前面的消息不变。
func TestBuildMessagesHistoryPrefixAppend(t *testing.T) {
	in := sampleInput(store.ConversationKindMatch, PromptPeer{Gender: "female"})
	// 30 轮（60 条）以内的历史追加不应改变前缀。
	in.History = []PromptHistory{
		{FromBot: false, Kind: "text", Text: "你好"},
		{FromBot: true, Kind: "text", Text: "嗨"},
	}
	before := BuildMessages(in)

	in.History = append(in.History,
		PromptHistory{FromBot: false, Kind: "text", Text: "在干嘛"},
		PromptHistory{FromBot: true, Kind: "text", Text: "画画"},
	)
	after := BuildMessages(in)

	// 前 3 条（两个 system + 第一条历史合并后的 user 消息）应逐字节相同。
	// 因为合并规则，历史条数变化时最后一条 user 消息会合并，这里比较前两条 system。
	for i := 0; i < 2; i++ {
		if before[i] != after[i] {
			t.Fatalf("第 %d 条消息在追加历史后发生变化:\n%s\n%s", i, before[i].Content, after[i].Content)
		}
	}
	// 最后一条始终是"时间 + 本轮指令"的 system（不属于前缀）。
	tail := after[len(after)-1]
	if tail.Role != "system" || !strings.Contains(tail.Content, "当前时间") || !strings.Contains(tail.Content, InstructionReply) {
		t.Errorf("最后一条 system 不符: %+v", tail)
	}
}

// TestHistoryStart 验证历史截断边界（按 30 条整块丢弃）。
func TestHistoryStart(t *testing.T) {
	cases := []struct {
		n, max, step, want int
	}{
		{n: 0, max: 60, step: 30, want: 0},
		{n: 60, max: 60, step: 30, want: 0},
		{n: 61, max: 60, step: 30, want: 30},
		{n: 90, max: 60, step: 30, want: 30},
		{n: 91, max: 60, step: 30, want: 60},
		{n: 120, max: 60, step: 30, want: 60},
		{n: 121, max: 60, step: 30, want: 90},
		{n: 10, max: 60, step: 0, want: 0},
	}
	for _, c := range cases {
		if got := historyStart(c.n, c.max, c.step); got != c.want {
			t.Errorf("historyStart(%d,%d,%d) = %d, 期望 %d", c.n, c.max, c.step, got, c.want)
		}
	}
}

// TestParseReply 覆盖多行、媒体行、全角冒号、角色名前缀、[沉默] 与截断。
func TestParseReply(t *testing.T) {
	t.Run("多行文本", func(t *testing.T) {
		got := ParseReply("你好呀\n\n今天过得怎么样？")
		if len(got) != 2 || got[0].Text != "你好呀" || got[1].Text != "今天过得怎么样？" {
			t.Fatalf("解析结果不符: %+v", got)
		}
		if got[0].Kind != "text" {
			t.Errorf("Kind = %q", got[0].Kind)
		}
	})

	t.Run("媒体行与全角冒号", func(t *testing.T) {
		got := ParseReply("给你看张照片\n[图片：自拍，微笑]\n[视频:夜景]")
		if len(got) != 3 {
			t.Fatalf("应解析出 3 条, 实际 %+v", got)
		}
		if got[1].Kind != "image" || len(got[1].Tags) != 2 || got[1].Tags[0] != "自拍" || got[1].Tags[1] != "微笑" {
			t.Errorf("图片气泡不符: %+v", got[1])
		}
		if got[2].Kind != "video" || len(got[2].Tags) != 1 || got[2].Tags[0] != "夜景" {
			t.Errorf("视频气泡不符: %+v", got[2])
		}
	})

	t.Run("角色名前缀", func(t *testing.T) {
		got := ParseReply("小雅：晚安啦\n小雅: 做个好梦")
		if len(got) != 2 || got[0].Text != "晚安啦" || got[1].Text != "做个好梦" {
			t.Fatalf("角色名前缀未去掉: %+v", got)
		}
	})

	t.Run("沉默", func(t *testing.T) {
		if got := ParseReply("[沉默]"); len(got) != 0 {
			t.Errorf("[沉默] 应返回空, 实际 %+v", got)
		}
		if got := ParseReply("  \n[沉默]\n "); len(got) != 0 {
			t.Errorf("前后有空行的 [沉默] 应返回空, 实际 %+v", got)
		}
	})

	t.Run("超过三条截断", func(t *testing.T) {
		got := ParseReply("一\n二\n三\n四\n五")
		if len(got) != maxBubbles {
			t.Fatalf("气泡数 = %d, 期望 %d", len(got), maxBubbles)
		}
		if got[2].Text != "三" {
			t.Errorf("截断内容不符: %+v", got)
		}
	})

	t.Run("文本气泡长度上限", func(t *testing.T) {
		long := strings.Repeat("字", maxBubbleRunes+50)
		got := ParseReply(long)
		if len(got) != 1 || len([]rune(got[0].Text)) != maxBubbleRunes {
			t.Fatalf("超长文本未被截断: %d", len([]rune(got[0].Text)))
		}
	})

	t.Run("空输出", func(t *testing.T) {
		if got := ParseReply("   \n\n "); len(got) != 0 {
			t.Errorf("空输出应返回空, 实际 %+v", got)
		}
	})
}

// TestBuildMessagesNeverClaimsHuman 验证平台说明里没有"否认自己是 AI"之类的指令。
func TestBuildMessagesNeverClaimsHuman(t *testing.T) {
	msgs := BuildMessages(sampleInput(store.ConversationKindMatch, PromptPeer{}))
	system := msgs[0].Content
	for _, forbidden := range []string{"不是 AI", "不是AI", "真人", "否认", "坚称"} {
		if strings.Contains(system, forbidden) {
			t.Errorf("平台说明不应包含 %q: %s", forbidden, system)
		}
	}
	for _, want := range []string{"角色身份", "口语化", "Markdown", "3 条"} {
		if !strings.Contains(system, want) {
			t.Errorf("平台说明应包含 %q: %s", want, system)
		}
	}
}

// TestBuildMessagesTailInstruction 验证本轮指令出现在最后一条 system。
func TestBuildMessagesTailInstruction(t *testing.T) {
	in := sampleInput(store.ConversationKindBottle, PromptPeer{})
	in.BottleText = "想找个人说说话"
	in.Instruction = InstructionBottle
	msgs := BuildMessages(in)
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.Content, InstructionBottle) {
		t.Errorf("最后一条 system 应包含本轮指令: %s", last.Content)
	}
	// 瓶子原文属于对话级 system。
	if !strings.Contains(msgs[1].Content, "想找个人说说话") {
		t.Errorf("对话级 system 应包含瓶子原文: %s", msgs[1].Content)
	}
	// 时间格式为"周X HH:MM"（UTC+8 的 2026-11-07 07:05）。
	if !strings.Contains(last.Content, "07:05") || !regexp.MustCompile(`周[日一二三四五六] 07:05`).MatchString(last.Content) {
		t.Errorf("时间格式不符（应为\"周X 07:05\"）: %s", last.Content)
	}
}

// TestCacheKey 验证缓存键格式。
func TestCacheKey(t *testing.T) {
	key := CacheKey(genTestUUID(t))
	if !strings.HasPrefix(key, "anonchat-bot-") {
		t.Errorf("缓存键 = %q", key)
	}
}
