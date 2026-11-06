package profile

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestFrontendConstsMatch 断言前端 src/lib/profileConsts.ts 与后端 consts.json 完全一致
// （how.md 3.2：两份常量必须一致，避免前后端校验口径不同）。
func TestFrontendConstsMatch(t *testing.T) {
	path := filepath.Join("..", "..", "..", "frontend", "src", "lib", "profileConsts.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取前端常量文件失败: %v", err)
	}

	provinces := extractStringArray(t, string(data), "PROVINCES")
	tags := extractStringArray(t, string(data), "TAGS")

	c := Get()
	if len(provinces) != len(c.Provinces) {
		t.Fatalf("省份数量不一致：前端 %d / 后端 %d", len(provinces), len(c.Provinces))
	}
	for i := range provinces {
		if provinces[i] != c.Provinces[i] {
			t.Errorf("第 %d 个省份不一致：前端 %q / 后端 %q", i, provinces[i], c.Provinces[i])
		}
	}
	if len(tags) != len(c.Tags) {
		t.Fatalf("标签数量不一致：前端 %d / 后端 %d", len(tags), len(c.Tags))
	}
	for i := range tags {
		if tags[i] != c.Tags[i] {
			t.Errorf("第 %d 个标签不一致：前端 %q / 后端 %q", i, tags[i], c.Tags[i])
		}
	}
}

// extractStringArray 从 TS 源码里提取 `export const NAME = [ 'a', 'b' ] as const` 的字符串元素。
func extractStringArray(t *testing.T, src, name string) []string {
	t.Helper()
	re := regexp.MustCompile(`(?s)export const ` + name + ` = \[(.*?)\] as const`)
	m := re.FindStringSubmatch(src)
	if m == nil {
		t.Fatalf("未找到前端常量 %s", name)
	}
	strRe := regexp.MustCompile(`'([^']*)'`)
	matches := strRe.FindAllStringSubmatch(m[1], -1)
	out := make([]string, 0, len(matches))
	for _, mm := range matches {
		out = append(out, mm[1])
	}
	return out
}
