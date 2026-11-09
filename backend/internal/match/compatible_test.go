package match

import "testing"

func strp(s string) *string { return &s }
func i16p(v int16) *int16   { return &v }

// TestCompatible 覆盖兼容判定的组合（how.md 3.4）：
// 目前只有清流模式生效，性别/同省偏好在任何用户（含 VIP）身上都不参与匹配。
func TestCompatible(t *testing.T) {
	base := PrefProfile{Gender: "female", MatchGender: "any"}
	cases := []struct {
		name string
		a, b PrefProfile
		want bool
	}{
		{
			name: "普通用户互相兼容",
			a:    PrefProfile{Gender: "male", MatchGender: "any"},
			b:    base,
			want: true,
		},
		{
			name: "清流与非清流不配对",
			a:    PrefProfile{CleanMode: true, MatchGender: "any"},
			b:    PrefProfile{CleanMode: false, MatchGender: "any"},
			want: false,
		},
		{
			name: "双方都清流可以配对",
			a:    PrefProfile{CleanMode: true, MatchGender: "any"},
			b:    PrefProfile{CleanMode: true, MatchGender: "any"},
			want: true,
		},
		{
			name: "VIP 只匹配女生且对方是女生",
			a:    PrefProfile{VIP: true, MatchGender: "female"},
			b:    base,
			want: true,
		},
		{
			name: "VIP 的性别偏好不再阻止配对（对方是男生）",
			a:    PrefProfile{VIP: true, MatchGender: "female"},
			b:    PrefProfile{Gender: "male"},
			want: true,
		},
		{
			name: "性别保密的人也兼容",
			a:    PrefProfile{VIP: true, MatchGender: "female"},
			b:    PrefProfile{Gender: "secret"},
			want: true,
		},
		{
			name: "非 VIP 的性别偏好被忽略",
			a:    PrefProfile{VIP: false, MatchGender: "female"},
			b:    PrefProfile{Gender: "male"},
			want: true,
		},
		{
			name: "双向性别偏好都不再阻止配对",
			a:    PrefProfile{VIP: true, MatchGender: "male", Gender: "female"},
			b:    PrefProfile{VIP: true, MatchGender: "male", Gender: "female"},
			want: true,
		},
		{
			name: "同省偏好不再阻止配对（两省相同）",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{Province: strp("浙江")},
			want: true,
		},
		{
			name: "VIP 的同省偏好不再阻止配对（省份不同）",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{Province: strp("江苏")},
			want: true,
		},
		{
			name: "VIP 的同省偏好不再阻止配对（对方未设置省份）",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{},
			want: true,
		},
		{
			name: "非 VIP 的同省开关被忽略",
			a:    PrefProfile{VIP: false, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{Province: strp("江苏")},
			want: true,
		},
		{
			name: "年龄不影响硬条件",
			a:    PrefProfile{Age: i16p(30), MatchGender: "any"},
			b:    PrefProfile{Age: i16p(60), MatchGender: "any"},
			want: true,
		},
	}
	for _, c := range cases {
		if got := compatible(c.a, c.b); got != c.want {
			t.Errorf("%s: compatible = %v, 期望 %v", c.name, got, c.want)
		}
	}
}

func TestTagScore(t *testing.T) {
	if got := tagScore([]string{"音乐", "电影"}, []string{"电影", "游戏"}); got != 1 {
		t.Errorf("交集大小 = %d, 期望 1", got)
	}
	if got := tagScore(nil, []string{"音乐"}); got != 0 {
		t.Errorf("空标签交集应为 0，得到 %d", got)
	}
	if got := tagScore([]string{"音乐", "电影"}, []string{"音乐", "电影"}); got != 2 {
		t.Errorf("完全重合交集应为 2，得到 %d", got)
	}
}

// TestCompatibleIgnoresProvince 保留原有的“性别保密 / 省份”组合检查：
// 偏好不参与匹配，所以预置了性别与同省偏好的资料也应兼容。
func TestCompatibleIgnoresProvince(t *testing.T) {
	a := PrefProfile{VIP: true, MatchGender: "any"}
	b := PrefProfile{Gender: "secret"}
	if !compatible(a, b) {
		t.Error("不限性别时应与保密性别的用户兼容")
	}

	p := PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")}
	q := PrefProfile{Gender: "secret"}
	if !compatible(p, q) {
		t.Error("VIP 的同省偏好不应阻止配对")
	}
}
