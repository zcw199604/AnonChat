package match

import "testing"

func strp(s string) *string { return &s }
func i16p(v int16) *int16   { return &v }

// TestCompatible 覆盖双向兼容判定的全部组合（how.md 3.4）。
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
			name: "VIP 只匹配女生但对方是男生",
			a:    PrefProfile{VIP: true, MatchGender: "female"},
			b:    PrefProfile{Gender: "male"},
			want: false,
		},
		{
			name: "性别保密的人不满足任何性别筛选",
			a:    PrefProfile{VIP: true, MatchGender: "female"},
			b:    PrefProfile{Gender: "secret"},
			want: false,
		},
		{
			name: "非 VIP 的性别偏好被服务端忽略",
			a:    PrefProfile{VIP: false, MatchGender: "female"},
			b:    PrefProfile{Gender: "male"},
			want: true,
		},
		{
			name: "双向：一方筛选不通过即不配对",
			a:    PrefProfile{VIP: true, MatchGender: "male", Gender: "female"},
			b:    PrefProfile{VIP: true, MatchGender: "male", Gender: "female"},
			want: false,
		},
		{
			name: "同省匹配且两省相同",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{Province: strp("浙江")},
			want: true,
		},
		{
			name: "同省匹配但省份不同",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{Province: strp("江苏")},
			want: false,
		},
		{
			name: "同省匹配但对方未设置省份",
			a:    PrefProfile{VIP: true, MatchSameProv: true, Province: strp("浙江")},
			b:    PrefProfile{},
			want: false,
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

func TestProvinceMatchesSecretGender(t *testing.T) {
	// gender=secret 的人可以满足「不限性别」，但永不满足具体性别筛选。
	a := PrefProfile{VIP: true, MatchGender: "any"}
	b := PrefProfile{Gender: "secret"}
	if !compatible(a, b) {
		t.Error("不限性别时应与保密性别的用户兼容")
	}
}
