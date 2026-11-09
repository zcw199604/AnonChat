package bot

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestMemoryProfileMerge(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour)
	profile := MemoryProfile{"basic_info": {"称呼": {Value: "旧称呼", T: old}, "职业": {Value: "学生", T: old}}}
	before, _ := json.Marshal(profile)
	got := mergeProfile(profile, []ProfileChange{
		{Topic: "basic_info", Subtopic: "称呼", Value: "新称呼", Action: "set"},
		{Topic: "basic_info", Subtopic: "职业", Action: "delete"},
		{Topic: "unknown", Subtopic: "不保留", Value: "内容", Action: "set"},
		{Topic: "interest", Subtopic: "音乐", Value: "摇滚", Action: "guess"},
		{Topic: "interest", Subtopic: " ", Value: "空键", Action: "set"},
		{Topic: "interest", Subtopic: strings.Repeat("键", 21), Value: strings.Repeat("值", 101), Action: "set"},
		{Topic: "life", Subtopic: "联系", Value: "号码 123-4567，邮箱 nobody@example.test", Action: "set"},
	}, now)
	if v := got["basic_info"]["称呼"]; v.Value != "新称呼" || !v.T.Equal(now) {
		t.Fatalf("set 未更新内容与时间: %+v", v)
	}
	if _, ok := got["basic_info"]["职业"]; ok || len(got["unknown"]) != 0 || len(got["interest"]) != 1 {
		t.Fatalf("delete、topic 或 action 过滤错误: %+v", got)
	}
	if v := got["interest"][strings.Repeat("键", 20)].Value; utf8.RuneCountInString(v) != 100 {
		t.Fatalf("字段长度未裁剪: %q", v)
	}
	if v := got["life"]["联系"].Value; v != "号码 ***，邮箱 ***" {
		t.Fatalf("画像值未清洗: %q", v)
	}
	after, _ := json.Marshal(profile)
	if string(after) != string(before) {
		t.Fatal("合并不得改写输入画像")
	}
	encoded, _ := json.Marshal(got)
	if !strings.Contains(string(encoded), `"t":"2026-11-09T12:00:00Z"`) {
		t.Fatal("更新时间应使用 RFC3339")
	}
}

func TestMemoryProfileCapacity(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	profile := MemoryProfile{"interest": {}}
	for i := 0; i < 11; i++ {
		profile["interest"][fmt.Sprintf("项%02d", i)] = MemoryValue{Value: "内容", T: now.Add(time.Duration(i) * time.Second)}
	}
	got := mergeProfile(profile, nil, now)
	if len(got["interest"]) != 10 {
		t.Fatalf("每 topic 应最多 10 项: %d", len(got["interest"]))
	}
	if _, ok := got["interest"]["项00"]; ok {
		t.Fatal("应淘汰 t 最旧项")
	}
	for n, topic := range []string{"basic_info", "interest", "life", "psychological", "relationship", "preference"} {
		if profile[topic] == nil {
			profile[topic] = map[string]MemoryValue{}
		}
		for i := 0; i < 10; i++ {
			profile[topic][fmt.Sprintf("项%02d", i)] = MemoryValue{Value: strings.Repeat("值", 100), T: now.Add(time.Duration(n*10+i) * time.Second)}
		}
	}
	got = mergeProfile(profile, nil, now)
	encoded, _ := json.Marshal(got)
	if len(encoded) > 6000 {
		t.Fatalf("画像超出 6000 字节: %d", len(encoded))
	}
	if _, ok := got["basic_info"]["项00"]; ok || got["preference"]["项09"].Value == "" {
		t.Fatal("整体容量应从最旧项开始淘汰，并保留最新项")
	}
	if len(profile["basic_info"]) != 10 {
		t.Fatal("容量裁剪不得改写输入")
	}
}

func TestPIIScrub(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"123456", "123456"},
		{"1234567", "***"},
		{"前123 45-67后", "前***后"},
		{"联系 Nobody+tag@Example.test。", "联系 ***。"},
		{"账号 123-456-789 和 a.b@example.test", "账号 *** 和 ***"},
		{"普通文字和 2026 年", "普通文字和 2026 年"},
	} {
		if got := scrubPII(tc.in); got != tc.want {
			t.Errorf("scrubPII(%q)=%q，期望 %q", tc.in, got, tc.want)
		}
	}
}

func TestMemoryRender(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	memory := &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "小林", T: now}}, "interest": {"游戏": {Value: "原神", T: now}}}, Events: []PromptMemoryEvent{{Gist: "他说下周要考驾照", CreatedAt: now.Add(-24 * time.Hour)}}}
	before, _ := json.Marshal(memory)
	got := RenderMemory(memory)
	for _, want := range []string{"【你对对方的记忆", "基本信息：称呼=小林", "兴趣：游戏=原神", "最近的事：\n- 11-08 他说下周要考驾照"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染缺少 %q: %s", want, got)
		}
	}
	after, _ := json.Marshal(memory)
	if string(before) != string(after) {
		t.Fatal("渲染不得修改输入")
	}
	if RenderMemory(nil) != "" || RenderMemory(&PromptMemory{}) != "" {
		t.Fatal("没有画像或事件时不渲染记忆块")
	}
}

func TestMemoryRenderBudget(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	memory := &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "保留画像", T: now}}}}
	for i := 0; i < 8; i++ {
		memory.Events = append(memory.Events, PromptMemoryEvent{Gist: fmt.Sprintf("事件%d", i) + strings.Repeat("事", 200), CreatedAt: now.Add(time.Duration(i) * time.Hour)})
	}
	got := RenderMemory(memory)
	if utf8.RuneCountInString(got) > 1500 || !strings.Contains(got, "保留画像") || strings.Contains(got, "事件0") || !strings.Contains(got, "事件7") {
		t.Fatal("超长时应先砍最旧事件，保留画像与较新事件")
	}
	for i := 0; i < 20; i++ {
		memory.Profile["basic_info"][fmt.Sprintf("画像%02d", i)] = MemoryValue{Value: strings.Repeat("值", 100), T: now.Add(time.Duration(i+1) * time.Hour)}
	}
	got = RenderMemory(memory)
	if utf8.RuneCountInString(got) > 1500 || strings.Contains(got, "最近的事") || strings.Contains(got, "画像00=") || !strings.Contains(got, "画像19=") {
		t.Fatal("画像本身超限时应先删完事件，再砍最旧画像")
	}
}

func TestAffinityStages(t *testing.T) {
	for _, tc := range []struct {
		values []int
		want   string
	}{
		{[]int{-100, -40}, "反感（冷淡、不耐烦）"},
		{[]int{-39, -10}, "戒备（保持距离）"},
		{[]int{-9, 0, 19}, "陌生（普通的陌生人社交）"},
		{[]int{20, 49}, "熟悉（轻松随意）"},
		{[]int{50, 79}, "好感（主动、亲近）"},
		{[]int{80, 100}, "亲密（非常信任、依恋）"},
	} {
		for _, value := range tc.values {
			if got := affinityStage(value); got != tc.want {
				t.Errorf("affinityStage(%d)=%q，期望 %q", value, got, tc.want)
			}
		}
	}
}

func TestMemoryOutputParsing(t *testing.T) {
	got, err := ParseMemoryOutput("思考后给出：\n```json\n" + `{"profile":[{"topic":"basic_info","subtopic":"称呼","value":"小林","action":"set"}],"events":[{"gist":"邮箱 nobody@example.test","tags":["约定"]},{"gist":"` + strings.Repeat("事", 121) + `"},{"gist":"第三条"},{"gist":"第四条"}],"affinity_delta":99}` + "\n```")
	if err != nil || len(got.Profile) != 1 || got.Profile[0].Value != "小林" || got.AffinityDelta != 10 || len(got.Events) != 3 || got.Events[0].Gist != "邮箱 ***" || utf8.RuneCountInString(got.Events[1].Gist) != 120 {
		t.Fatalf("容错解析或限制错误: %+v %v", got, err)
	}
	negative, err := ParseMemoryOutput(`{"affinity_delta":-99}`)
	if err != nil || negative.AffinityDelta != -10 {
		t.Fatal("负向 delta 应截断到 -10")
	}
	for _, input := range []string{"", "不是 JSON", "null", `{"profile":`, `{"events":"错误类型"}`, `{"a":{}} {"b":{}}`} {
		if _, err := ParseMemoryOutput(input); err == nil {
			t.Errorf("应拒绝无效抽取结果: %q", input)
		}
	}
	for _, text := range []string{"亲口说出", "不要猜测", "手机号", "证件号", "精确住址", "账号", "密码", "basic_info", "interest", "life", "psychological", "relationship", "preference", "delete", "set", "affinity_delta"} {
		if !strings.Contains(memoryExtractPrompt, text) {
			t.Errorf("抽取提示词缺少约束 %q", text)
		}
	}
}
