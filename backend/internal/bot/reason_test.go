package bot

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/anonchat/anonchat/backend/internal/bot/persona"
)

func TestReasonParseAndBounds(t *testing.T) {
	text := fmt.Sprintf("说明\n```json\n{\"thought\":%q,\"guidance\":%q,\"mood\":{\"label\":%q,\"value\":99},\"affinity_delta\":-99,\"expression\":\"开心\",\"silent\":true}\n```", strings.Repeat("想", 301), strings.Repeat("说", 301), strings.Repeat("乐", 11))
	got, err := ParseReasonOutput(text)
	if err != nil || utf8.RuneCountInString(got.Thought) != 300 || utf8.RuneCountInString(got.Guidance) != 300 || utf8.RuneCountInString(got.Mood.Label) != 10 || got.Mood.Value != 10 || got.AffinityDelta != -5 || !got.Silent {
		t.Fatalf("字段截断错误: %+v %v", got, err)
	}
	got, err = ParseReasonOutput(`{"thought":"想法","mood":{"value":-2},"affinity_delta":9}`)
	if err != nil || got.Mood.Value != 0 || got.AffinityDelta != 5 {
		t.Fatal("数值边界错误")
	}
	got, err = ParseReasonOutput(`{"tool_calls":[{"name":"recall_memory","arguments":{"query":"考试"}}]}`)
	if err != nil || len(got.ToolCalls) != 1 || got.ToolCalls[0].Arguments.Query != "考试" {
		t.Fatalf("工具解析错误: %+v %v", got, err)
	}
	for _, text := range []string{"", "不是JSON", "null", `{}`, `{"thought":4}`, `{"tool_calls":[]}`, `{"tool_calls":[{}],"thought":"混合"}`, `{"thought":"a"} {"thought":"b"}`} {
		if _, err := ParseReasonOutput(text); err == nil {
			t.Errorf("应拒绝无效协议: %q", text)
		}
	}
}

func TestReasonPromptStableAndHistory(t *testing.T) {
	for _, mode := range []string{PersonaModeSystem, PersonaModeUser} {
		in := PromptInput{PersonaMode: mode, Card: PromptCard{Name: "小雅", ReasonPrompt: "先倾听", Description: strings.Repeat("背", 1501), Personality: "温柔", HeartPrompt: "喜欢音乐", Tools: []string{"get_time"}}, Expressions: []PromptExpression{{Key: "开心", Description: "笑"}}, Now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC), Loc: time.UTC, Relation: PromptRelation{Stage: "熟悉"}, Memory: &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "小林"}}}}, TriggeredLore: []string{"本轮设定"}}
		for i := 0; i < 21; i++ {
			in.History = append(in.History, PromptHistory{Kind: "text", Text: fmt.Sprintf("消息%02d", i), FromBot: i%2 == 0})
		}
		before := buildReasonPrefix(in)
		next := in
		next.Memory = nil
		next.Now = next.Now.Add(time.Hour)
		next.Relation = PromptRelation{}
		next.TriggeredLore = []string{"变化"}
		next.History = nil
		if before != buildReasonPrefix(next) {
			t.Fatal("会话/每轮内容影响智核前缀")
		}
		for _, want := range []string{"内心（智核）", "先倾听", strings.Repeat("背", 1500), "get_time", "开心（笑）", "tool_calls", "affinity_delta"} {
			if !strings.Contains(before, want) {
				t.Errorf("缺少前缀字段 %q", want)
			}
		}
		if strings.Contains(before, strings.Repeat("背", 1501)) || strings.Contains(before, "recall_memory") || strings.Contains(before, "lookup_lore") {
			t.Fatal("概要或工具白名单错误")
		}
		msgs := buildReasonMessages(in)
		all := ""
		for _, m := range msgs {
			all += m.Content
			if mode == PersonaModeUser && m.Role == "system" {
				t.Fatal("user注入不应使用system")
			}
		}
		for _, want := range []string{"小林", "消息01", "消息20", "当前时间", "熟悉", "本轮设定"} {
			if !strings.Contains(all, want) {
				t.Errorf("上下文缺少%q", want)
			}
		}
		if strings.Contains(all, "消息00") {
			t.Fatal("只能包含最近20条历史")
		}
	}
}

func TestReasonTools(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 34, 56, 0, time.UTC)
	memory := &PromptMemory{Profile: MemoryProfile{"basic_info": {"称呼": {Value: "小林"}}, "interest": {"Game": {Value: "CAT"}}}, Events: []PromptMemoryEvent{{Gist: "下周考试", CreatedAt: now}}}
	if got := recallMemory(memory, " cat，考试\u3000小林 ignored"); !strings.Contains(got, "CAT") || !strings.Contains(got, "11-09 下周考试") || !strings.Contains(got, "小林") {
		t.Fatalf("记忆关键词错误: %s", got)
	}
	if recallMemory(memory, "nothing") != "（没有相关记忆）" || recallMemory(nil, "考试") != "（没有相关记忆）" {
		t.Fatal("空记忆错误")
	}
	for i := 0; i < 12; i++ {
		memory.Events = append(memory.Events, PromptMemoryEvent{Gist: fmt.Sprintf("考试%d", i), CreatedAt: now})
	}
	if len(strings.Split(recallMemory(memory, "考试"), "\n")) != 8 {
		t.Fatal("召回最多8行")
	}
	card := PromptCard{Lorebook: []persona.LoreEntry{{Enabled: true, Keys: []string{"CAT"}, Content: "一"}, {Enabled: false, Content: "cat禁用"}, {Enabled: true, Content: strings.Repeat("猫", 501)}, {Enabled: true, Content: "cat二"}, {Enabled: true, Content: "cat三"}, {Enabled: true, Content: "cat四"}}}
	if got := lookupLore(card, "cat"); got != "一\ncat二\ncat三" {
		t.Fatalf("世界书匹配或上限错误:%q", got)
	}
	if got := lookupLore(card, "猫"); utf8.RuneCountInString(got) != 500 {
		t.Fatal("世界书单条最多500字")
	}
	in := PromptInput{Card: PromptCard{Tools: []string{"get_time"}}, Now: now, Loc: time.FixedZone("UTC+8", 8*3600)}
	got := reasonLocalTool(in, ReasonToolCall{Name: "get_time"}, nil)
	if !strings.Contains(got, "2026-11-09") || !strings.Contains(got, "星期一") || !strings.Contains(got, "20:34:56") {
		t.Fatalf("本地时间错误:%q", got)
	}
	for _, name := range []string{"recall_memory", "lookup_lore", "unknown"} {
		if got := reasonLocalTool(in, ReasonToolCall{Name: name}, memory); got != "（工具不可用）" {
			t.Fatal("未启用/未知工具不得执行")
		}
	}
}

func TestReasonLoopLimitsAndFallback(t *testing.T) {
	in := PromptInput{Card: PromptCard{Tools: []string{"get_time"}}, Expressions: []PromptExpression{{Key: "开心"}}}
	responses := []string{`{"tool_calls":[{"name":"get_time"},{"name":"get_time"},{"name":"get_time"}]}`, `{"tool_calls":[{"name":"get_time"}]}`, `{"thought":"想法","guidance":"自然回应","expression":"无效"}`}
	var requests [][]ChatMessage
	tools := 0
	call := func(_ context.Context, messages []ChatMessage) (ChatResult, error) {
		requests = append(requests, append([]ChatMessage(nil), messages...))
		return ChatResult{Content: responses[len(requests)-1]}, nil
	}
	got := runReasonLoop(context.Background(), in, call, func(ReasonToolCall) (string, error) { tools++; return "时间", nil })
	if got == nil || got.Thought != "想法" || got.Expression != "" || len(requests) != 3 || tools != 4 {
		t.Fatalf("工具上限或结论错误: %+v requests=%d tools=%d", got, len(requests), tools)
	}
	final := requests[2]
	if !strings.Contains(final[len(final)-1].Content, "不要再调用工具，直接输出结论。") {
		t.Fatal("第三次调用前必须要求最终结论")
	}
	for _, response := range []string{`{"tool_calls":[{"name":"get_time"}]}`, "坏JSON"} {
		calls := 0
		got := runReasonLoop(context.Background(), in, func(context.Context, []ChatMessage) (ChatResult, error) {
			calls++
			return ChatResult{Content: response}, nil
		}, func(ReasonToolCall) (string, error) { return "结果", nil })
		if got != nil || calls > 3 {
			t.Fatal("解析失败或超轮应降级")
		}
	}
	if got := runReasonLoop(context.Background(), in, func(context.Context, []ChatMessage) (ChatResult, error) { return ChatResult{}, errors.New("失败") }, nil); got != nil {
		t.Fatal("调用失败应降级")
	}
	if got := runReasonLoop(context.Background(), in, func(context.Context, []ChatMessage) (ChatResult, error) {
		return ChatResult{Content: `{"tool_calls":[{"name":"recall_memory"}]}`}, nil
	}, func(ReasonToolCall) (string, error) { return "", errors.New("查询失败") }); got != nil {
		t.Fatal("工具失败应降级")
	}
	got = runReasonLoop(context.Background(), in, func(context.Context, []ChatMessage) (ChatResult, error) {
		return ChatResult{Content: `{"expression":"开心","silent":true}`}, nil
	}, nil)
	if got == nil || got.Expression != "开心" || !got.Silent {
		t.Fatal("合法表情和沉默应保留")
	}
	if !reflect.DeepEqual(in.Card.Tools, []string{"get_time"}) {
		t.Fatal("不能修改输入")
	}
}

func TestReasonWhitelistAndStableVocabulary(t *testing.T) {
	in := PromptInput{Card: PromptCard{Tools: []string{"lookup_lore", "get_time"}}, Expressions: []PromptExpression{{Key: "b", Sort: 2}, {Key: "a", Sort: 1}}}
	before := buildReasonPrefix(in)
	next := in
	next.Card.Tools = []string{"get_time", "lookup_lore"}
	next.Expressions = []PromptExpression{{Key: "a", Sort: 1}, {Key: "b", Sort: 2}}
	if before != buildReasonPrefix(next) {
		t.Fatal("白名单与表情输入顺序不能影响稳定前缀")
	}
	calls := 0
	out := runReasonLoop(context.Background(), in, func(_ context.Context, messages []ChatMessage) (ChatResult, error) {
		calls++
		if calls == 1 {
			return ChatResult{Content: `{"tool_calls":[{"name":"recall_memory"},{"name":"execute_python"}]}`}, nil
		}
		last := messages[len(messages)-1].Content
		if strings.Count(last, "（工具不可用）") != 2 {
			t.Fatal("禁用或未知工具应返回工具不可用")
		}
		return ChatResult{Content: `{"guidance":"正常说话"}`}, nil
	}, func(ReasonToolCall) (string, error) { t.Fatal("禁用或未知工具不得执行"); return "", nil })
	if out == nil || calls != 2 {
		t.Fatal("不可用工具结果应交给智核继续决定")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out := runReasonLoop(ctx, in, func(context.Context, []ChatMessage) (ChatResult, error) {
		t.Fatal("已取消不能调用模型")
		return ChatResult{}, nil
	}, nil); out != nil {
		t.Fatal("取消应降级")
	}
}

func TestReasonExcessToolCallsRequireFinal(t *testing.T) {
	in := PromptInput{Card: PromptCard{Tools: []string{"get_time"}}}
	calls, tools := 0, 0
	out := runReasonLoop(context.Background(), in, func(_ context.Context, messages []ChatMessage) (ChatResult, error) {
		calls++
		if calls == 1 {
			return ChatResult{Content: `{"tool_calls":[{"name":"get_time"},{"name":"get_time"},{"name":"get_time"},{"name":"get_time"}]}`}, nil
		}
		if calls == 2 && !strings.Contains(messages[len(messages)-1].Content, "不要再调用工具，直接输出结论。") {
			t.Error("一次请求超过3个工具应立即要求最终结论")
		}
		return ChatResult{Content: `{"tool_calls":[{"name":"get_time"}]}`}, nil
	}, func(ReasonToolCall) (string, error) { tools++; return "时间", nil })
	if out != nil || calls != 2 || tools != 3 {
		t.Fatalf("超过3次后仍请求工具应降级：out=%+v calls=%d tools=%d", out, calls, tools)
	}
}
