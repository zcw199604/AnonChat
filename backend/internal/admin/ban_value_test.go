package admin

import (
	"testing"

	"github.com/anonchat/anonchat/backend/internal/moderation"
)

// TestNormalizeBanValue 验证封禁主体值的规范化规则（IP 统一形式、空白裁剪、非法值报错）。
func TestNormalizeBanValue(t *testing.T) {
	const ident = "8d1f2e4a-0000-4000-8000-000000000001"
	cases := []struct {
		name        string
		subjectType string
		value       string
		want        string
		wantErr     bool
	}{
		{name: "identity 原样保留", subjectType: moderation.SubjectIdentity, value: ident, want: ident},
		{name: "identity 去空白", subjectType: moderation.SubjectIdentity, value: "  " + ident + " ", want: ident},
		{name: "identity 非 UUID 报错", subjectType: moderation.SubjectIdentity, value: "abc", wantErr: true},
		{name: "account 原样保留", subjectType: moderation.SubjectAccount, value: ident, want: ident},
		{name: "account 空值报错", subjectType: moderation.SubjectAccount, value: "   ", wantErr: true},
		{name: "ipv4 去空白", subjectType: moderation.SubjectIP, value: " 1.2.3.4 ", want: "1.2.3.4"},
		{name: "ipv6 压缩形式", subjectType: moderation.SubjectIP, value: "2001:0db8:0000:0000:0000:0000:0000:0001", want: "2001:db8::1"},
		{name: "非法 ip 报错", subjectType: moderation.SubjectIP, value: "not-an-ip", wantErr: true},
		{name: "未知类型报错", subjectType: "unknown", value: "x", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeBanValue(tc.subjectType, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望报错，实际得到 %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("未期望报错: %v", err)
			}
			if got != tc.want {
				t.Fatalf("规范化结果 = %q, 期望 %q", got, tc.want)
			}
		})
	}
}

// TestNormalizeBanValueRejectsInvalidIPv4 单独验证带前导零的 IPv4 会被拒绝。
func TestNormalizeBanValueRejectsInvalidIPv4(t *testing.T) {
	if _, err := normalizeBanValue(moderation.SubjectIP, "001.002.003.004"); err == nil {
		t.Fatal("带前导零的 IPv4 应被判为非法")
	}
}
