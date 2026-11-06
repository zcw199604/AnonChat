package story

import (
	"testing"
	"time"
)

// TestValidSession 覆盖有效场次判定的边界（how.md 4：4:59 与 5:00、4 条与 5 条消息）。
func TestValidSession(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		duration time.Duration
		m1, m2   int64
		want     bool
	}{
		{"5 分钟且双方各 5 条", 5 * time.Minute, 5, 5, true},
		{"4 分 59 秒", 4*time.Minute + 59*time.Second, 5, 5, false},
		{"正好 5 分钟", 5 * time.Minute, 5, 5, true},
		{"一方只有 4 条", 5 * time.Minute, 4, 5, false},
		{"另一方只有 4 条", 5 * time.Minute, 5, 4, false},
		{"时长足够但都是 4 条", 10 * time.Minute, 4, 4, false},
	}
	for _, c := range cases {
		got := validSession(start, start.Add(c.duration), DefaultMinDuration, c.m1, c.m2)
		if got != c.want {
			t.Errorf("%s: validSession = %v, 期望 %v", c.name, got, c.want)
		}
	}
}

func TestValidateCreate(t *testing.T) {
	ok := CreateInput{
		Title:    "雨夜书店",
		Opening:  "雨下得很大，书店里只剩下一个客人。",
		RoleA:    "店主",
		RoleB:    "旅人",
		HostRole: "a",
	}
	if msg := validateCreate(ok); msg != "" {
		t.Fatalf("合法输入应通过，却返回 %q", msg)
	}
	bad := ok
	bad.Title = "雨"
	if msg := validateCreate(bad); msg == "" {
		t.Error("标题过短应被拒绝")
	}
	bad = ok
	bad.Opening = "太短"
	if msg := validateCreate(bad); msg == "" {
		t.Error("开场过短应被拒绝")
	}
	bad = ok
	bad.RoleB = ok.RoleA
	if msg := validateCreate(bad); msg == "" {
		t.Error("两个角色名相同应被拒绝")
	}
	bad = ok
	bad.HostRole = "c"
	if msg := validateCreate(bad); msg == "" {
		t.Error("非法角色非法应被拒绝")
	}
}
