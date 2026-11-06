// Package bot 实现 AI 机器人：模型客户端、提示词、限额与节奏、引擎与后台接口。
package bot

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// 提示词与解析相关的常量。
const (
	// historyMaxMessages 是拼进提示词的历史消息条数上限（合并前）。
	historyMaxMessages = 60
	// historyDropBlock 是超出上限时按整块丢弃的块大小（保证 30 轮内前缀不变）。
	historyDropBlock = 30
	// maxBubbles 是一次回复最多发送的气泡数。
	maxBubbles = 3
	// maxBubbleRunes 是单条文本气泡的字数上限。
	maxBubbleRunes = 1000
)

// 本轮指令（放在最后一条 system 里，因此每次都会变化）。
const (
	// InstructionReply 是正常回复的指令。
	InstructionReply = "回复对方。"
	// InstructionOpening 是匹配成功后的开场指令。
	InstructionOpening = "对方刚和你匹配上，还没说话，你先打个招呼。"
	// InstructionBottle 是回复漂流瓶的指令。
	InstructionBottle = "你捞到了这个漂流瓶，写一条回复。"
)

// 人设注入方式。
const (
	// PersonaModeUser 把角色设定放进第一条 user 消息：兼容会丢弃或改写 system 消息的中转网关（默认）。
	PersonaModeUser = "user"
	// PersonaModeSystem 用 system 消息承载角色设定：适合遵守 system 语义的官方接口。
	PersonaModeSystem = "system"
)

// platformPrompt 是固定的平台说明。
// 只要求"以角色身份自然聊天"，不包含任何"否认自己是 AI"之类的指令（how.md §0.2）。
const platformPrompt = "你在一个匿名聊天 App 上和陌生人聊天。" +
	"请始终以角色身份像手机聊天一样自然、简短、口语化地回复对方，" +
	"不用 Markdown，不要长篇大论，一次最多回 3 条消息。"

// outputFormatPrompt 是输出格式说明（稳定内容，属于 bot 级前缀）。
const outputFormatPrompt = "输出格式：每条消息一行；" +
	"如果要发图片或视频，单独一行写成 [图片:标签1,标签2] 或 [视频:标签1]；" +
	"不想回复时只输出一行 [沉默]。"

// PromptCard 是角色卡在提示词里的投影。
type PromptCard struct {
	Name            string
	Description     string
	Personality     string
	Scenario        string
	FirstMessage    string
	ExampleDialogue string
	SystemPrompt    string
}

// CardFromStore 把库里的角色卡转成提示词用的角色卡。
func CardFromStore(card store.CharacterCard) PromptCard {
	return PromptCard{
		Name:            card.Name,
		Description:     card.Description,
		Personality:     card.Personality,
		Scenario:        card.Scenario,
		FirstMessage:    card.FirstMessage,
		ExampleDialogue: card.ExampleDialogue,
		SystemPrompt:    card.SystemPrompt,
	}
}

// PromptPeer 是对方的公开资料。
type PromptPeer struct {
	Gender   string // male | female | secret
	Age      *int16 // 未设置为 nil
	Province string // 只有 provincePublic 为真时才会带上
	Tags     []string
}

// PromptHistory 是历史里的一条消息。
type PromptHistory struct {
	// FromBot 为 true 表示这条消息是 bot 自己发的。
	FromBot bool
	// Kind 是消息类型：text | image | video。
	Kind string
	// Text 是正文（媒体消息为空）。
	Text string
	// MediaTags 是媒体消息对应媒体的标签。
	MediaTags []string
}

// PromptInput 是组装一次请求所需的全部输入。
type PromptInput struct {
	Card      PromptCard
	MediaTags []string
	// Kind 是会话类型（match 或 bottle）。
	Kind store.ConversationKind
	Peer PromptPeer
	// BottleText 是漂流瓶原文（仅 bottle 会话）。
	BottleText string
	History    []PromptHistory
	// Now 是当前时间（只用于最后一条 system）。
	Now time.Time
	// Loc 是显示时间用的时区。
	Loc *time.Location
	// Instruction 是本轮指令（InstructionReply / InstructionOpening / InstructionBottle）。
	Instruction string
	// PersonaMode 是人设注入方式（PersonaModeUser / PersonaModeSystem）；为空按 system 处理。
	PersonaMode string
}

// BuildMessages 按 how.md §6 的顺序组装消息：
//  1. bot 级 system（角色卡 + 媒体词表 + 输出格式，只依赖 bot 自身）；
//  2. 对话级 system（会话类型、对方公开资料、瓶子原文）；
//  3. 历史消息（对方 user / bot assistant，连续同一角色合并）；
//  4. 结尾 system（当前时间 + 本轮指令）。
func BuildMessages(in PromptInput) []ChatMessage {
	if in.PersonaMode == PersonaModeUser {
		return buildUserModeMessages(in)
	}
	msgs := make([]ChatMessage, 0, 3+len(in.History))
	msgs = append(msgs, ChatMessage{Role: "system", Content: buildBotSystem(in.Card, in.MediaTags)})
	if conv := buildConversationSystem(in); conv != "" {
		msgs = append(msgs, ChatMessage{Role: "system", Content: conv})
	}
	start := historyStart(len(in.History), historyMaxMessages, historyDropBlock)
	msgs = append(msgs, mergeHistory(in.History[start:])...)
	msgs = append(msgs, ChatMessage{Role: "system", Content: buildTailSystem(in)})
	return msgs
}

// buildUserModeMessages 组装 user 注入方式的消息（部分网关会丢弃 system 消息）：
//  1. 第一条 user 消息以"角色设定块"开头（bot 级 + 对话级内容，与 system 方式相同、同样前缀稳定），
//     如果历史的第一条也是对方的消息，就接在设定块后面合并成一条；
//  2. 之后是历史消息（对方 user / bot assistant）；
//  3. 本轮提示（当前时间 + 指令）追加到最后一条 user 消息末尾；最后一条是 assistant 时单独补一条 user。
//
// 不发送任何 system 消息。设定块不随轮次变化，每轮只有末尾的提示会变，缓存前缀依然稳定。
func buildUserModeMessages(in PromptInput) []ChatMessage {
	setup := "【角色设定：以下是你要扮演的角色和聊天规则，只用于指导你的回复，不要向对方复述】\n" +
		buildBotSystem(in.Card, in.MediaTags) + "\n" + buildConversationSystem(in) + "\n【设定结束】"

	start := historyStart(len(in.History), historyMaxMessages, historyDropBlock)
	history := mergeHistory(in.History[start:])

	msgs := make([]ChatMessage, 0, 2+len(history))
	if len(history) > 0 && history[0].Role == "user" {
		msgs = append(msgs, ChatMessage{Role: "user", Content: setup + "\n\n" + history[0].Content})
		msgs = append(msgs, history[1:]...)
	} else {
		msgs = append(msgs, ChatMessage{Role: "user", Content: setup})
		msgs = append(msgs, history...)
	}

	note := "【" + buildTailSystem(in) + "】"
	if last := len(msgs) - 1; msgs[last].Role == "user" {
		msgs[last].Content += "\n\n" + note
	} else {
		msgs = append(msgs, ChatMessage{Role: "user", Content: note})
	}
	return msgs
}

// buildBotSystem 组装第 1 条 system：只依赖角色卡与排序后的媒体标签词表。
func buildBotSystem(card PromptCard, tags []string) string {
	var b strings.Builder
	b.WriteString(platformPrompt)
	if card.Name != "" {
		fmt.Fprintf(&b, "\n角色名：%s", card.Name)
	}
	appendField(&b, "身份与外貌", card.Description)
	appendField(&b, "性格与说话风格", card.Personality)
	appendField(&b, "场景设定", card.Scenario)
	appendField(&b, "示例对话", card.ExampleDialogue)
	appendField(&b, "额外要求", card.SystemPrompt)
	if len(tags) > 0 {
		// 标签排序后再输出，保证同一 bot 的前缀逐字节稳定。
		sorted := make([]string, len(tags))
		copy(sorted, tags)
		sort.Strings(sorted)
		fmt.Fprintf(&b, "\n可用的媒体标签：%s", strings.Join(sorted, "、"))
	}
	b.WriteString("\n")
	b.WriteString(outputFormatPrompt)
	return b.String()
}

// appendField 追加一个非空字段。
func appendField(b *strings.Builder, label, value string) {
	v := strings.TrimSpace(value)
	if v == "" {
		return
	}
	fmt.Fprintf(b, "\n%s：%s", label, v)
}

// buildConversationSystem 组装第 2 条 system：只依赖对话本身。
func buildConversationSystem(in PromptInput) string {
	var b strings.Builder
	switch in.Kind {
	case store.ConversationKindBottle:
		b.WriteString("这是一个漂流瓶对话：你捞到了对方投出的瓶子并回复了它。")
	case store.ConversationKindMatch:
		b.WriteString("这是一个随机匹配对话：你随机匹配到了一个陌生人。")
	default:
		b.WriteString("这是一个聊天对话。")
	}

	if peer := describePeer(in.Peer); peer != "" {
		fmt.Fprintf(&b, "\n对方的公开资料：%s", peer)
	}
	if in.Kind == store.ConversationKindBottle && strings.TrimSpace(in.BottleText) != "" {
		fmt.Fprintf(&b, "\n瓶子原文：%s", strings.TrimSpace(in.BottleText))
	}
	return b.String()
}

// describePeer 把对方公开资料拼成一段文字（只输出公开的信息）。
func describePeer(p PromptPeer) string {
	parts := make([]string, 0, 4)
	switch p.Gender {
	case "male":
		parts = append(parts, "性别男")
	case "female":
		parts = append(parts, "性别女")
	case "secret":
		parts = append(parts, "性别保密")
	}
	if p.Age != nil {
		parts = append(parts, fmt.Sprintf("%d 岁", *p.Age))
	}
	if p.Province != "" {
		parts = append(parts, "省份"+p.Province)
	}
	if len(p.Tags) > 0 {
		parts = append(parts, "标签："+strings.Join(p.Tags, "、"))
	}
	return strings.Join(parts, "，")
}

// buildTailSystem 组装最后一条 system：当前时间与本轮指令（每轮都会变化）。
func buildTailSystem(in PromptInput) string {
	loc := in.Loc
	if loc == nil {
		loc = time.FixedZone("UTC+8", 8*3600)
	}
	weekdays := [...]string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	local := in.Now.In(loc)
	stamp := fmt.Sprintf("%s %s", weekdays[int(local.Weekday())], local.Format("15:04"))
	instruction := in.Instruction
	if instruction == "" {
		instruction = InstructionReply
	}
	if in.PersonaMode == PersonaModeUser {
		return fmt.Sprintf("系统提示：当前时间 %s。%s", stamp, instruction)
	}
	return fmt.Sprintf("当前时间：%s\n%s", stamp, instruction)
}

// historyStart 计算历史截断的起点：超过 max 条时按 step 为块从最老一端整块丢弃。
func historyStart(n, max, step int) int {
	if n <= max || step <= 0 {
		return 0
	}
	return int(math.Ceil(float64(n-max)/float64(step))) * step
}

// mergeHistory 把连续同一角色的消息合并成一条（bot 自己为 assistant，对方为 user）。
func mergeHistory(history []PromptHistory) []ChatMessage {
	out := make([]ChatMessage, 0, len(history))
	var curRole string
	var buf strings.Builder
	flush := func() {
		if curRole == "" {
			return
		}
		out = append(out, ChatMessage{Role: curRole, Content: buf.String()})
		buf.Reset()
	}
	for _, h := range history {
		role := "user"
		if h.FromBot {
			role = "assistant"
		}
		content := historyContent(h)
		if content == "" {
			continue
		}
		if role != curRole {
			flush()
			curRole = role
		}
		if buf.Len() > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(content)
	}
	flush()
	return out
}

// historyContent 把一条历史消息转成提示词里的文本（媒体写成 [图片:标签]）。
func historyContent(h PromptHistory) string {
	switch h.Kind {
	case "image", "video":
		label := "图片"
		if h.Kind == "video" {
			label = "视频"
		}
		if len(h.MediaTags) == 0 {
			return "[" + label + "]"
		}
		return "[" + label + ":" + strings.Join(h.MediaTags, ",") + "]"
	default:
		return strings.TrimSpace(h.Text)
	}
}

// Bubble 是解析出来的一条待发送内容。
type Bubble struct {
	// Kind 是 text | image | video。
	Kind string
	// Text 是文本气泡的正文。
	Text string
	// Tags 是媒体气泡要求命中的标签。
	Tags []string
}

// mediaLineRe 匹配整行的媒体指令：[图片:标签1,标签2] 或 [视频：标签]。
var mediaLineRe = regexp.MustCompile(`^\[(图片|视频)[:：]\s*(.+?)\]$`)

// ParseReply 把模型输出解析成气泡列表（how.md §6）。
func ParseReply(text string) []Bubble {
	lines := strings.Split(text, "\n")
	trimmed := make([]string, 0, len(lines))
	for _, line := range lines {
		if s := strings.TrimSpace(line); s != "" {
			trimmed = append(trimmed, s)
		}
	}
	// 只有一行且为 [沉默]：本轮不回复。
	if len(trimmed) == 1 && isSilence(trimmed[0]) {
		return nil
	}

	out := make([]Bubble, 0, maxBubbles)
	for _, line := range trimmed {
		if isSilence(line) {
			continue
		}
		// 先判断媒体行（媒体行里的冒号不能被当成角色名前缀分隔符）。
		if m := mediaLineRe.FindStringSubmatch(line); m != nil {
			tags := splitTags(m[2])
			if len(tags) == 0 {
				continue
			}
			kind := "image"
			if m[1] == "视频" {
				kind = "video"
			}
			out = append(out, Bubble{Kind: kind, Tags: tags})
		} else if text := stripNamePrefix(line); text != "" {
			out = append(out, Bubble{Kind: "text", Text: truncateRunes(text, maxBubbleRunes)})
		}
		if len(out) == maxBubbles {
			break
		}
	}
	return out
}

// isSilence 判断该行是否为静默标记。
func isSilence(line string) bool {
	s := strings.TrimSpace(line)
	return s == "[沉默]" || s == "［沉默］"
}

// stripNamePrefix 去掉模型可能加在行首的"角色名："前缀。
func stripNamePrefix(line string) string {
	for _, sep := range []string{"：", ":"} {
		if idx := strings.Index(line, sep); idx > 0 {
			prefix := strings.TrimSpace(line[:idx])
			// 前缀很短且不含标点时才认为是角色名前缀。
			if utf8.RuneCountInString(prefix) <= 12 && !strings.ContainsAny(prefix, "，。！？,.!?") {
				return strings.TrimSpace(line[idx+len(sep):])
			}
		}
	}
	return strings.TrimSpace(line)
}

// splitTags 按 , ， 、 拆分媒体标签。
func splitTags(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '，' || r == '、'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// truncateRunes 按字符截断文本。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// CacheKey 返回某个 bot 的提示词缓存键。
func CacheKey(botID pgtype.UUID) string {
	return "anonchat-bot-" + botID.String()
}
