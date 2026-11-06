package bot

import (
	"testing"
	"time"
)

// TestLoadLocation 验证时区解析与回退。
func TestLoadLocation(t *testing.T) {
	loc := LoadLocation("Asia/Shanghai")
	if loc == nil {
		t.Fatal("Asia/Shanghai 应解析成功")
	}
	// 2026-01-01 00:00 UTC 在 UTC+8 是 08:00。
	utc := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := utc.In(loc).Hour(); got != 8 {
		t.Errorf("Asia/Shanghai 偏移不符: hour=%d, 期望 8", got)
	}

	// 非法时区回退到 UTC+8 固定时区。
	fallback := LoadLocation("Mars/Olympus")
	if fallback == nil {
		t.Fatal("回退时区不应为 nil")
	}
	if got := utc.In(fallback).Hour(); got != 8 {
		t.Errorf("回退时区偏移不符: hour=%d, 期望 8", got)
	}
	if empty := LoadLocation(""); empty == nil || utc.In(empty).Hour() != 8 {
		t.Error("空时区应回退到 UTC+8")
	}
}
