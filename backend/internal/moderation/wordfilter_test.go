package moderation

import (
	"context"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestFilterBlockLevel(t *testing.T) {
	m := NewModerator([]Entry{{Word: "禁止词", Level: LevelBlock}})
	cases := []string{
		"这里有禁止词出现",
		"这 里 有 禁 止 词 出 现", // 插入空格
		"这里*有*禁止~词!",      // 插入符号
		"禁\u200b止\u200d词", // 零宽字符
	}
	for _, c := range cases {
		got, blocked := m.Filter(context.Background(), c)
		if !blocked {
			t.Errorf("文本 %q 应命中拦截级", c)
		}
		if got != "" {
			t.Errorf("拦截时应返回空串, 实际 %q", got)
		}
	}

	// 全角字母转半角、大小写、符号分隔后命中。
	ma := NewModerator([]Entry{{Word: "blockme", Level: LevelBlock}})
	for _, c := range []string{"BLOCK-ME", "ｂｌｏｃｋｍｅ", "bl o ck me"} {
		if _, blocked := ma.Filter(context.Background(), c); !blocked {
			t.Errorf("文本 %q 应命中", c)
		}
	}
}

func TestFilterMaskLevel(t *testing.T) {
	m := NewModerator([]Entry{{Word: "badword", Level: LevelMask}})
	// 大小写与插入空格：mask 应替换原文字符、保留插入的空格。
	got, blocked := m.Filter(context.Background(), "B A D W O R D")
	if blocked {
		t.Fatal("mask 级不应拦截")
	}
	if got != "* * * * * * *" {
		t.Fatalf("打码结果 = %q, 期望 \"* * * * * * *\"（插入空格保持原样）", got)
	}
}

func TestFilterNoMatch(t *testing.T) {
	m := NewModerator([]Entry{{Word: "禁止词", Level: LevelBlock}})
	text := "完全正常的一句话"
	got, blocked := m.Filter(context.Background(), text)
	if blocked || got != text {
		t.Fatalf("无命中应原样返回, blocked=%v got=%q", blocked, got)
	}
}

func TestFilterBlockTakesPrecedence(t *testing.T) {
	m := NewModerator([]Entry{
		{Word: "goodword", Level: LevelMask},
		{Word: "goodwordx", Level: LevelBlock},
	})
	if _, blocked := m.Filter(context.Background(), "a goodwordx b"); !blocked {
		t.Error("应命中拦截级")
	}
}

func TestLoadWordlistFile(t *testing.T) {
	entries, err := LoadWordlistFile("testdata/words_sample.txt")
	if err != nil {
		t.Fatalf("加载词库失败: %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("词条数 = %d, 期望 >=3", len(entries))
	}
	var hasMask, hasBlock bool
	for _, e := range entries {
		switch e.Level {
		case LevelMask:
			hasMask = true
		case LevelBlock:
			hasBlock = true
		}
	}
	if !hasMask || !hasBlock {
		t.Errorf("示例词库应同时含 block 与 mask: block=%v mask=%v", hasBlock, hasMask)
	}

	m := NewModerator(entries)
	if _, blocked := m.Filter(context.Background(), "测试违禁词"); !blocked {
		t.Error("测试违禁词应被拦截")
	}
}

// genWords 生成 n 条随机单词（小写字母，长度 8）。
func genWords(n int) []Entry {
	rng := rand.New(rand.NewSource(42))
	entries := make([]Entry, 0, n)
	buf := make([]byte, 8)
	for i := 0; i < n; i++ {
		for j := range buf {
			buf[j] = byte('a' + rng.Intn(26))
		}
		entries = append(entries, Entry{Word: string(buf), Level: LevelMask})
	}
	return entries
}

func TestFilterPerformance(t *testing.T) {
	m := NewModerator(genWords(100000))
	// 1000 字、不含匹配（全 x），走完整遍历的最坏路径。
	msg := strings.Repeat("x", 1000)

	// 预热。
	m.Filter(context.Background(), msg)

	start := time.Now()
	_, _ = m.Filter(context.Background(), msg)
	elapsed := time.Since(start)
	if elapsed >= time.Millisecond {
		t.Fatalf("10 万词库过滤 1000 字耗时 %v, 期望 < 1ms", elapsed)
	}
}

func BenchmarkModeratorFilter(b *testing.B) {
	m := NewModerator(genWords(100000))
	msg := strings.Repeat("x", 1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = m.Filter(context.Background(), msg)
	}
}
