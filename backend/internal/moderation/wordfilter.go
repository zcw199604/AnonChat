package moderation

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"
)

// Level 是敏感词的处理级别。
type Level int

const (
	// LevelBlock 命中即拦截。
	LevelBlock Level = iota
	// LevelMask 命中即把原文中对应字符替换为 *。
	LevelMask
)

// Entry 是一条敏感词条目。
type Entry struct {
	Word  string
	Level Level
}

// acOut 是 AC 自动机上某个匹配（词长 + 是否拦截）。同一节点可能因后缀而携带多条。
type acOut struct {
	length int
	block  bool
}

// acNode 是 AC 自动机节点。
type acNode struct {
	next  map[rune]*acNode
	fail  *acNode
	depth int
	outs  []acOut
}

// Moderator 是基于 AC 自动机的敏感词过滤器。
type Moderator struct {
	root *acNode
}

// NewModerator 用给定词条构建 AC 自动机。
func NewModerator(entries []Entry) *Moderator {
	root := &acNode{next: make(map[rune]*acNode)}
	for _, e := range entries {
		runes := normalizeRunes(e.Word)
		if len(runes) == 0 {
			continue
		}
		node := root
		for _, r := range runes {
			nx := node.next[r]
			if nx == nil {
				nx = &acNode{next: make(map[rune]*acNode), depth: node.depth + 1}
				node.next[r] = nx
			}
			node = nx
		}
		node.outs = append(node.outs, acOut{length: len(runes), block: e.Level == LevelBlock})
	}
	m := &Moderator{root: root}
	m.buildFail()
	return m
}

// buildFail 用 BFS 构建失败指针并合并后缀输出。
func (m *Moderator) buildFail() {
	queue := make([]*acNode, 0, len(m.root.next))
	for _, child := range m.root.next {
		child.fail = m.root
		queue = append(queue, child)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.fail != nil {
			cur.outs = append(cur.outs, cur.fail.outs...)
		}
		for r, child := range cur.next {
			f := cur.fail
			for f != nil {
				if nx, ok := f.next[r]; ok {
					child.fail = nx
					break
				}
				f = f.fail
			}
			if child.fail == nil {
				child.fail = m.root
			}
			queue = append(queue, child)
		}
	}
}

// Filter 实现 conversation.ContentFilter：命中拦截级返回 blocked，命中打码级返回打码后的原文。
func (m *Moderator) Filter(_ context.Context, text string) (string, bool) {
	if m == nil || m.root == nil || len(text) == 0 {
		return text, false
	}
	orig := []rune(text)
	norm, pos := normalizeWithMapping(orig)
	if len(norm) == 0 {
		return text, false
	}

	mask := make([]bool, len(orig))
	node := m.root
	for i, r := range norm {
		for {
			if nx, ok := node.next[r]; ok {
				node = nx
				break
			}
			if node == m.root {
				break
			}
			node = node.fail
		}
		for _, o := range node.outs {
			if o.block {
				return "", true
			}
			start := i - o.length + 1
			for k := start; k <= i; k++ {
				if k >= 0 && k < len(pos) {
					mask[pos[k]] = true
				}
			}
		}
	}

	changed := false
	for j, mk := range mask {
		if mk {
			orig[j] = '*'
			changed = true
		}
	}
	if changed {
		return string(orig), false
	}
	return text, false
}

// LoadWordlistFile 从文件加载词库（UTF-8，每行 词<TAB>级别）。
func LoadWordlistFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开词库失败: %w", err)
	}
	defer f.Close()

	var entries []Entry
	scanner := bufio.NewScanner(f)
	// 词库行可能较长，放宽单行上限。
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		fields := strings.SplitN(line, "\t", 2)
		word := strings.TrimSpace(fields[0])
		if word == "" {
			continue
		}
		level := LevelBlock
		if len(fields) == 2 {
			switch strings.TrimSpace(fields[1]) {
			case "mask":
				level = LevelMask
			case "", "block":
				level = LevelBlock
			default:
				return nil, fmt.Errorf("未知的敏感词级别 %q（行内词=%q）", fields[1], word)
			}
		}
		entries = append(entries, Entry{Word: word, Level: level})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取词库失败: %w", err)
	}
	return entries, nil
}

// normalizeRunes 把词条归一化为可匹配的 rune 序列（不做位置映射）。
func normalizeRunes(s string) []rune {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if nr, keep := normalizeRune(r); keep {
			out = append(out, nr)
		}
	}
	return out
}

// normalizeWithMapping 归一化原文并返回“归一化下标 → 原文 rune 下标”的映射。
func normalizeWithMapping(orig []rune) ([]rune, []int) {
	norm := make([]rune, 0, len(orig))
	pos := make([]int, 0, len(orig))
	for i, r := range orig {
		if nr, keep := normalizeRune(r); keep {
			norm = append(norm, nr)
			pos = append(pos, i)
		}
	}
	return norm, pos
}

// normalizeRune 对单个字符做归一化：全角转半角、转小写；对空白/标点/符号/零宽字符返回 keep=false。
func normalizeRune(r rune) (rune, bool) {
	// 零宽字符。
	switch r {
	case 0x200B, 0x200C, 0x200D, 0x200E, 0x200F, 0x2060, 0xFEFF:
		return 0, false
	}
	// 全角 ASCII 转半角。
	if r >= 0xFF01 && r <= 0xFF5E {
		r -= 0xFEE0
	}
	// 各类空白。
	if unicode.IsSpace(r) || unicode.Is(unicode.Z, r) {
		return 0, false
	}
	// 标点与符号。
	if unicode.IsPunct(r) || unicode.IsSymbol(r) {
		return 0, false
	}
	return unicode.ToLower(r), true
}
