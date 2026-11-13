package external

import "testing"

func TestPeerProfileMapping(t *testing.T) {
	for _, tc := range []struct {
		p        PeerProfile
		gender   string
		age      int16
		province string
	}{
		{PeerProfile{Sex: "男", Age: "18", Address: "北京市海淀区"}, "male", 18, "北京"},
		{PeerProfile{Sex: "女", Age: "80", Address: "广西壮族自治区南宁市"}, "female", 80, "广西"},
		{PeerProfile{Sex: "unknown", Age: "17", Address: "overseas"}, "secret", 0, ""},
		{PeerProfile{Sex: "", Age: "81", Address: "浙江省杭州市"}, "secret", 0, "浙江"},
		{PeerProfile{Sex: "女", Age: "not-age", Address: "内蒙古自治区呼和浩特市"}, "female", 0, "内蒙古"},
		{PeerProfile{Sex: "女", Age: "23", Address: "新疆维吾尔自治区乌鲁木齐市"}, "female", 23, "新疆"},
	} {
		got := mapPeerProfile(tc.p)
		if got.Gender != tc.gender {
			t.Errorf("gender=%q want=%q", got.Gender, tc.gender)
		}
		if tc.age == 0 {
			if got.Age != nil {
				t.Errorf("无效年龄没有映射为NULL: %v", *got.Age)
			}
		} else if got.Age == nil || *got.Age != tc.age {
			t.Errorf("age=%v want=%d", got.Age, tc.age)
		}
		if tc.province == "" {
			if got.Province != nil || got.ProvincePublic {
				t.Errorf("未知地址不应公开: %+v", got)
			}
		} else if got.Province == nil || *got.Province != tc.province || !got.ProvincePublic {
			t.Errorf("province=%v public=%v want=%q", got.Province, got.ProvincePublic, tc.province)
		}
	}
}
