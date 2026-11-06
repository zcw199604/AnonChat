package verify

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"13800001234", "+8613800001234", false},
		{"138 0000 1234", "+8613800001234", false},
		{"+8613800001234", "+8613800001234", false},
		{"+1 415 555 0100", "+14155550100", false},
		{"+44 7700 900123", "+447700900123", false},
		{"", "", true},
		{"12345", "", true},
		{"23800001234", "", true}, // 不以 1 开头的 11 位
		{"+0123456789", "", true}, // 国家码不能以 0 开头
		{"abcdefghijk", "", true}, // 非数字
		{"1380000123a", "", true}, // 含字母
	}
	for _, c := range cases {
		got, err := NormalizePhone(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("NormalizePhone(%q) 期望报错，实际得到 %q", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizePhone(%q) 意外报错: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("NormalizePhone(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestMaskPhone(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"+8613800001234", "+86 138****1234"},
		{"+14155550100", "+141****0100"},
		{"+447700900123", "+447****0123"},
	}
	for _, c := range cases {
		if got := MaskPhone(c.in); got != c.want {
			t.Errorf("MaskPhone(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}
