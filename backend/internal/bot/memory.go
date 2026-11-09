package bot

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// MemoryValue 保存画像值及其最后更新时间，时间按 RFC3339 落库。
type MemoryValue struct {
	Value string    `json:"v"`
	T     time.Time `json:"t"`
}

// MemoryProfile 按固定 topic、subtopic 保存画像。
type MemoryProfile map[string]map[string]MemoryValue

// ProfileChange 是记忆模型提出的一项画像修改。
type ProfileChange struct {
	Topic    string `json:"topic"`
	Subtopic string `json:"subtopic"`
	Value    string `json:"value"`
	Action   string `json:"action"`
}

// MemoryEventInput 是记忆模型提出的事件。
type MemoryEventInput struct {
	Gist string   `json:"gist"`
	Tags []string `json:"tags"`
}

// MemoryExtraction 是一次记忆抽取的结果。
type MemoryExtraction struct {
	Profile       []ProfileChange    `json:"profile"`
	Events        []MemoryEventInput `json:"events"`
	AffinityDelta int                `json:"affinity_delta"`
}

// PromptMemory 是提示词所需的结构化画像与最近事件。
type PromptMemory struct {
	Profile MemoryProfile
	Events  []PromptMemoryEvent
}

// PromptMemoryEvent 保存事件文字及发生时间。
type PromptMemoryEvent struct {
	Gist      string
	CreatedAt time.Time
}

const memoryExtractPrompt = `你负责整理聊天机器人角色对聊天对象的长期记忆。
只记录对方亲口说出的关于他自己的事实、你们之间的约定或称呼，以及对话中发生的值得以后想起的事情。不要猜测，不要记录角色自己的设定。
禁止记录手机号、证件号、精确住址、各类账号和密码。
画像只允许以下 6 个 topic：basic_info（基本信息）、interest（兴趣）、life（生活）、psychological（性格与状态）、relationship（和你的关系）、preference（聊天偏好）。
每个 topic 最多 10 个 subtopic，subtopic 最多 20 字，值最多 100 字；只保留有用且明确的事实。
只输出 JSON，格式如下：
{"profile":[{"topic":"basic_info","subtopic":"称呼","value":"小林","action":"set"}],"events":[{"gist":"他说下周要考驾照","tags":["考试"]}],"affinity_delta":2}
action 只能是 set 或 delete；events 最多 3 条，每条 gist 最多 120 字。
affinity_delta 在 -10 到 10 之间，表示这段对话后角色对对方的好感变化：对方友善、真诚就加，冒犯、敷衍就减。`

var memoryTopics = []struct{ key, label string }{
	{"basic_info", "基本信息"},
	{"interest", "兴趣"},
	{"life", "生活"},
	{"psychological", "性格与状态"},
	{"relationship", "和你的关系"},
	{"preference", "聊天偏好"},
}

var (
	memoryNumberPII = regexp.MustCompile(`[0-9](?:[ -]*[0-9]){6,}`)
	memoryEmailPII  = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
)

// mergeProfile 在副本上合并画像，先按 topic、再按整体容量淘汰最旧项。
func mergeProfile(profile MemoryProfile, changes []ProfileChange, now time.Time) MemoryProfile {
	out := make(MemoryProfile)
	for _, topic := range memoryTopics {
		keys := make([]string, 0, len(profile[topic.key]))
		for key := range profile[topic.key] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			v := profile[topic.key][key]
			setProfileValue(out, topic.key, key, v.Value, v.T)
		}
	}
	for _, change := range changes {
		if !validMemoryTopic(change.Topic) {
			continue
		}
		key := truncateRunes(strings.TrimSpace(change.Subtopic), 20)
		switch change.Action {
		case "set":
			setProfileValue(out, change.Topic, key, change.Value, now.UTC())
		case "delete":
			delete(out[change.Topic], key)
		}
	}
	for _, topic := range memoryTopics {
		entries := profileFields(MemoryProfile{topic.key: out[topic.key]})
		for len(entries) > 10 {
			delete(out[topic.key], entries[0].subtopic)
			entries = entries[1:]
		}
	}
	for _, field := range profileFields(out) {
		encoded, _ := json.Marshal(out)
		if len(encoded) <= 6000 {
			break
		}
		delete(out[field.topic], field.subtopic)
	}
	for topic, values := range out {
		if len(values) == 0 {
			delete(out, topic)
		}
	}
	return out
}

func validMemoryTopic(key string) bool {
	for _, topic := range memoryTopics {
		if topic.key == key {
			return true
		}
	}
	return false
}

func setProfileValue(profile MemoryProfile, topic, key, value string, at time.Time) {
	key = truncateRunes(strings.TrimSpace(key), 20)
	value = truncateRunes(scrubPII(strings.TrimSpace(value)), 100)
	if key == "" || value == "" {
		return
	}
	if profile[topic] == nil {
		profile[topic] = make(map[string]MemoryValue)
	}
	profile[topic][key] = MemoryValue{Value: value, T: at}
}

// scrubPII 在写入前移除长数字串和邮箱，不改动非敏感文字。
func scrubPII(text string) string {
	return memoryNumberPII.ReplaceAllString(memoryEmailPII.ReplaceAllString(text, "***"), "***")
}

// affinityStage 将好感度映射成心核可读的关系阶段。
func affinityStage(value int) string {
	switch {
	case value <= -40:
		return "反感（冷淡、不耐烦）"
	case value <= -10:
		return "戒备（保持距离）"
	case value <= 19:
		return "陌生（普通的陌生人社交）"
	case value <= 49:
		return "熟悉（轻松随意）"
	case value <= 79:
		return "好感（主动、亲近）"
	default:
		return "亲密（非常信任、依恋）"
	}
}

// ParseMemoryOutput 容忍 JSON 外的说明或代码围栏，错误中不包含模型正文。
func ParseMemoryOutput(text string) (MemoryExtraction, error) {
	start, end := strings.IndexByte(text, '{'), strings.LastIndexByte(text, '}')
	var result MemoryExtraction
	if start < 0 || end < start || json.Unmarshal([]byte(text[start:end+1]), &result) != nil {
		return MemoryExtraction{}, errors.New("记忆抽取结果不是有效 JSON")
	}
	result.AffinityDelta = max(-10, min(10, result.AffinityDelta))
	events := make([]MemoryEventInput, 0, min(3, len(result.Events)))
	for _, event := range result.Events {
		event.Gist = truncateRunes(scrubPII(strings.TrimSpace(event.Gist)), 120)
		if event.Gist != "" && len(events) < 3 {
			events = append(events, event)
		}
	}
	result.Events = events
	return result, nil
}

type memoryProfileField struct {
	topic, subtopic string
	value           MemoryValue
}

// profileFields 按更新时间从旧到新排列，同一时刻按键排序以保持结果稳定。
func profileFields(profile MemoryProfile) []memoryProfileField {
	var fields []memoryProfileField
	for _, topic := range memoryTopics {
		for key, value := range profile[topic.key] {
			if strings.TrimSpace(value.Value) != "" {
				fields = append(fields, memoryProfileField{topic: topic.key, subtopic: key, value: value})
			}
		}
	}
	sort.Slice(fields, func(i, j int) bool {
		if !fields[i].value.T.Equal(fields[j].value.T) {
			return fields[i].value.T.Before(fields[j].value.T)
		}
		if fields[i].topic != fields[j].topic {
			return fields[i].topic < fields[j].topic
		}
		return fields[i].subtopic < fields[j].subtopic
	})
	return fields
}

// RenderMemory 是记忆唯一的渲染入口，超出 1500 字时先砍旧事件，再砍旧画像。
func RenderMemory(memory *PromptMemory) string {
	if memory == nil {
		return ""
	}
	fields := profileFields(memory.Profile)
	events := make([]PromptMemoryEvent, 0, len(memory.Events))
	for _, event := range memory.Events {
		if strings.TrimSpace(event.Gist) != "" {
			events = append(events, event)
		}
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].CreatedAt.Before(events[j].CreatedAt) })
	for {
		text := renderMemoryParts(fields, events)
		if utf8.RuneCountInString(text) <= 1500 {
			return text
		}
		if len(events) > 0 {
			events = events[1:]
		} else if len(fields) > 0 {
			fields = fields[1:]
		}
	}
}

func renderMemoryParts(fields []memoryProfileField, events []PromptMemoryEvent) string {
	if len(fields) == 0 && len(events) == 0 {
		return ""
	}
	const header = "【你对对方的记忆（来自之前的聊天。自然地想起就好，不要逐条复述，也不要提\"记忆\"\"资料\"这类词）】"
	parts := []string{header}
	for _, topic := range memoryTopics {
		var values []string
		for _, field := range fields {
			if field.topic == topic.key {
				values = append(values, field.subtopic+"="+field.value.Value)
			}
		}
		if len(values) > 0 {
			sort.Strings(values)
			parts = append(parts, topic.label+"："+strings.Join(values, "；"))
		}
	}
	if len(events) > 0 {
		parts = append(parts, "最近的事：")
		for _, event := range events {
			parts = append(parts, fmt.Sprintf("- %s %s", event.CreatedAt.Format("01-02"), event.Gist))
		}
	}
	return strings.Join(parts, "\n")
}
