package bot

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStyleRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"标题", "# 你好", "markdown"},
		{"列表", "- 一起走", "markdown"},
		{"星号列表", "* 一起走", "markdown"},
		{"引用", "> 一起走", "markdown"},
		{"序号", "12. 一起走", "markdown"},
		{"粗体", "真的**开心**", "markdown"},
		{"代码", "```json", "markdown"},
		{"表格", "|---|---|", "markdown"},
		{"多行", "普通开头\n# 标题", "markdown"},
		{"角色设定", "角色设定：温柔", "leak"},
		{"设定结束", "设定结束", "leak"},
		{"系统提示", "系统提示：时间", "leak"},
		{"回复要点", "回复要点：安慰", "leak"},
		{"thought", `这里有"thought"`, "leak"},
		{"guidance", `这里有"guidance"`, "leak"},
		{"JSON", `{"别的字段":"内容"}`, "leak"},
		{"帮忙", "有什么可以帮你", "service"},
		{"有帮助", "希望对你有帮助", "service"},
		{"其他问题", "如果你还有其他问题", "service"},
		{"乐意", "我很乐意为你解答", "service"},
		{"请问", "请问还有什么需要", "service"},
		{"助手", "作为你的助手", "service"},
		{"长气泡", strings.Repeat("中", 201), "too_long"},
		{"普通", "今天挺开心，想和你聊聊。", ""},
		{"负号", "-10 度好冷", ""},
		{"小数", "3.14 是圆周率", ""},
		{"普通英文", "thought 和 guidance 是两个单词", ""},
		{"帮你", "要不我帮你想想？", ""},
		{"长度边界", strings.Repeat("中", 200), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := CheckStyle([]Bubble{{Kind: "text", Text: tc.text}})
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("普通聊天不应命中: %v", got)
				}
			} else if !reflect.DeepEqual(got, []string{tc.want}) {
				t.Fatalf("规则=%v，期望 %s", got, tc.want)
			}
		})
	}
	for _, n := range []int{200, 201} {
		got := CheckStyle([]Bubble{{Kind: "text", Text: strings.Repeat("一", 200)}, {Kind: "text", Text: strings.Repeat("二", n)}})
		if (len(got) > 0) != (n == 201) {
			t.Fatalf("合计边界错误: n=%d rules=%v", n, got)
		}
	}
	if got := CheckStyle([]Bubble{{Kind: "image", Text: "# 角色设定", Tags: []string{"系统提示"}}, {Kind: "expression", Key: "角色设定"}}); len(got) != 0 {
		t.Fatalf("只检测文本气泡: %v", got)
	}
	combined := []Bubble{{Kind: "text", Text: "# 角色设定：有什么可以帮" + strings.Repeat("长", 201)}, {Kind: "text", Text: "**重复格式**"}}
	if got := CheckStyle(combined); !reflect.DeepEqual(got, []string{"markdown", "leak", "service", "too_long"}) {
		t.Fatalf("各规则只应返回一次，顺序稳定: %v", got)
	}
}

func TestStyleReplyParsingKeepsLeakIndicators(t *testing.T) {
	for _, text := range []string{"系统提示：当前时间", "角色设定：温柔", "回复要点：安慰", `{"secret":"内部"}`} {
		bubbles := parseReply(text, true)
		if got := CheckStyle(bubbles); len(got) != 1 || got[0] != "leak" {
			t.Errorf("解析不能把泄露标记误当角色名前缀去掉: %q -> %+v", text, bubbles)
		}
	}
}

func TestStyleAndSanitizePreserveIdentityStatements(t *testing.T) {
	for _, text := range []string{"我是 AI", "我是AI", "我是机器人", "我不是真人", "我是一个语言模型", "我不是人类，我是 AI 模型。", "  我是 AI，也想继续和你聊。  "} {
		in := []Bubble{{Kind: "text", Text: text}}
		if got := CheckStyle(in); len(got) != 0 {
			t.Fatalf("身份表述不得命中: %q %v", text, got)
		}
		if got := Sanitize(in); !reflect.DeepEqual(got, in) {
			t.Fatalf("不得改动身份表述: %q -> %+v", text, got)
		}
	}
}

func TestSanitizeStyle(t *testing.T) {
	in := []Bubble{
		{Kind: "text", Text: "# 你好\n- **我来了**\n角色设定：温柔\n你今天怎么样？"},
		{Kind: "image", Tags: []string{"猫"}},
		{Kind: "text", Text: "```json\n{\"thought\":\"秘密\"}\n```\n> 别着急"},
		{Kind: "expression", Key: "开心"},
	}
	before := append([]Bubble(nil), in...)
	want := []Bubble{
		{Kind: "text", Text: "你好\n我来了\n你今天怎么样？"},
		{Kind: "image", Tags: []string{"猫"}},
		{Kind: "text", Text: "别着急"},
		{Kind: "expression", Key: "开心"},
	}
	if got := Sanitize(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("清洗错误: %+v", got)
	}
	if !reflect.DeepEqual(in, before) {
		t.Fatal("清洗不得改写输入")
	}
	if got := Sanitize([]Bubble{{Kind: "text", Text: "回复要点：秘密"}}); len(got) != 0 {
		t.Fatalf("清洗后没有文本的气泡应移除: %+v", got)
	}
}

func TestSanitizeLengthBudget(t *testing.T) {
	text := strings.Repeat("中", 120) + "。" + strings.Repeat("后", 100)
	got := Sanitize([]Bubble{{Kind: "text", Text: text}})
	if len(got) != 1 || got[0].Text != strings.Repeat("中", 120)+"。" {
		t.Fatalf("应在200字以内最后句末截断: %+v", got)
	}
	in := []Bubble{{Kind: "text", Text: strings.Repeat("甲", 180)}, {Kind: "text", Text: strings.Repeat("乙", 180)}, {Kind: "text", Text: strings.Repeat("丙", 180)}}
	got = Sanitize(in)
	total := 0
	for _, b := range got {
		total += utf8.RuneCountInString(b.Text)
		if utf8.RuneCountInString(b.Text) > 200 {
			t.Fatal("单气泡不得超过200字")
		}
	}
	if total > 400 || len(CheckStyle(got)) != 0 {
		t.Fatalf("清洗后不应超长: total=%d rules=%v", total, CheckStyle(got))
	}
}

func TestStyleFixHint(t *testing.T) {
	got := styleFixHint([]string{"markdown", "leak", "service", "too_long"})
	for _, want := range []string{"上一次回复不符合聊天风格", "Markdown", "设定泄露", "客服腔", "过长", "请重新以角色身份", "不要复述设定"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少纠正说明 %q: %s", want, got)
		}
	}
}
