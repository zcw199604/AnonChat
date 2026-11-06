package profile

import "testing"

func TestConstsLoaded(t *testing.T) {
	c := Get()
	if len(c.Provinces) != 34 {
		t.Fatalf("省份数量 = %d, 期望 34", len(c.Provinces))
	}
	if len(c.Tags) != 18 {
		t.Fatalf("标签数量 = %d, 期望 18", len(c.Tags))
	}
	if c.MaxTags != 5 || c.MinAge != 18 || c.MaxAge != 80 {
		t.Fatalf("常量取值异常: %+v", c)
	}
	if !IsProvince("浙江") || IsProvince("火星") {
		t.Error("省份校验失败")
	}
	if !IsTag("音乐") || IsTag("不存在") {
		t.Error("标签校验失败")
	}
}

func TestValidateAge(t *testing.T) {
	age := func(v int) *int { return &v }
	if msg := ValidateAge(nil); msg != "" {
		t.Errorf("未设置年龄应通过，却返回 %q", msg)
	}
	if msg := ValidateAge(age(17)); msg == "" {
		t.Error("17 岁应被拒绝")
	}
	if msg := ValidateAge(age(18)); msg != "" {
		t.Errorf("18 岁应通过，却返回 %q", msg)
	}
	if msg := ValidateAge(age(80)); msg != "" {
		t.Errorf("80 岁应通过，却返回 %q", msg)
	}
	if msg := ValidateAge(age(81)); msg == "" {
		t.Error("81 岁应被拒绝")
	}
}

func TestValidateTags(t *testing.T) {
	if msg := ValidateTags(nil); msg != "" {
		t.Errorf("空标签应通过，却返回 %q", msg)
	}
	if msg := ValidateTags([]string{"音乐", "电影"}); msg != "" {
		t.Errorf("合法标签应通过，却返回 %q", msg)
	}
	if msg := ValidateTags([]string{"音乐", "音乐"}); msg == "" {
		t.Error("重复标签应被拒绝")
	}
	if msg := ValidateTags([]string{"不存在的标签"}); msg == "" {
		t.Error("非法标签应被拒绝")
	}
	if msg := ValidateTags([]string{"音乐", "电影", "游戏", "动漫", "读书", "运动"}); msg == "" {
		t.Error("超过 5 个标签应被拒绝")
	}
}

func TestValidateProvinceAndGender(t *testing.T) {
	if msg := ValidateProvince(""); msg != "" {
		t.Errorf("空省份应通过，却返回 %q", msg)
	}
	if msg := ValidateProvince("浙江"); msg != "" {
		t.Errorf("合法省份应通过，却返回 %q", msg)
	}
	if msg := ValidateProvince("火星"); msg == "" {
		t.Error("非法省份应被拒绝")
	}
	for _, g := range []string{"male", "female", "secret"} {
		if msg := ValidateGender(g); msg != "" {
			t.Errorf("性别 %q 应通过，却返回 %q", g, msg)
		}
	}
	if msg := ValidateGender("other"); msg == "" {
		t.Error("非法性别应被拒绝")
	}
}

func TestNormalizeNickname(t *testing.T) {
	got, msg := NormalizeNickname("  小明  ")
	if msg != "" || got != "小明" {
		t.Fatalf("规范化结果 = %q, msg = %q", got, msg)
	}
	if _, msg := NormalizeNickname("a"); msg == "" {
		t.Error("1 个字符应被拒绝")
	}
	if _, msg := NormalizeNickname("超过了十二个字符的昵称名字"); msg == "" {
		t.Error("超过 12 个字符应被拒绝")
	}
	if _, msg := NormalizeNickname("带\x00控制字符"); msg == "" {
		t.Error("控制字符应被拒绝")
	}
}
