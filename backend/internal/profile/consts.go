// Package profile 负责用户资料与匹配偏好的常量、校验、更新、随机/自定义昵称、
// 匿名编号生成以及公开资料视图（how.md 3.2、3.3）。
package profile

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

//go:embed consts.json
var constsJSON []byte

// Consts 是资料相关的常量集合，与前端 src/lib/profileConsts.ts 保持一致。
type Consts struct {
	Provinces   []string `json:"provinces"`
	Tags        []string `json:"tags"`
	MaxTags     int      `json:"maxTags"`
	MinAge      int      `json:"minAge"`
	MaxAge      int      `json:"maxAge"`
	MinNickname int      `json:"minNickname"`
	MaxNickname int      `json:"maxNickname"`
}

var (
	constsOnce  sync.Once
	constsVal   Consts
	provinceSet map[string]struct{}
	tagSet      map[string]struct{}
)

// Get 返回常量（进程内只解析一次）。
func Get() Consts {
	constsOnce.Do(func() {
		// 常量文件随二进制嵌入，解析失败属于构建期错误，直接 panic 暴露。
		if err := json.Unmarshal(constsJSON, &constsVal); err != nil {
			panic(fmt.Sprintf("解析 profile 常量失败: %v", err))
		}
		provinceSet = make(map[string]struct{}, len(constsVal.Provinces))
		for _, p := range constsVal.Provinces {
			provinceSet[p] = struct{}{}
		}
		tagSet = make(map[string]struct{}, len(constsVal.Tags))
		for _, t := range constsVal.Tags {
			tagSet[t] = struct{}{}
		}
	})
	return constsVal
}

// Provinces 返回省级行政区列表。
func Provinces() []string { return Get().Provinces }

// Tags 返回标签预设列表。
func Tags() []string { return Get().Tags }

// IsProvince 判断是否为合法省份。
func IsProvince(s string) bool {
	Get()
	_, ok := provinceSet[s]
	return ok
}

// IsTag 判断是否为合法标签。
func IsTag(s string) bool {
	Get()
	_, ok := tagSet[s]
	return ok
}

// ValidateTags 校验标签数量与取值，返回错误描述（空字符串表示通过）。
func ValidateTags(tags []string) string {
	c := Get()
	if len(tags) > c.MaxTags {
		return fmt.Sprintf("标签最多选 %d 个", c.MaxTags)
	}
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		if !IsTag(t) {
			return "标签取值非法"
		}
		if _, dup := seen[t]; dup {
			return "标签重复"
		}
		seen[t] = struct{}{}
	}
	return ""
}

// ValidateAge 校验年龄区间（18–80，nil 表示不填）。
func ValidateAge(age *int) string {
	if age == nil {
		return ""
	}
	c := Get()
	if *age < c.MinAge || *age > c.MaxAge {
		return fmt.Sprintf("年龄需在 %d–%d 之间", c.MinAge, c.MaxAge)
	}
	return ""
}

// ValidateGender 校验性别取值。
func ValidateGender(g string) string {
	switch g {
	case "male", "female", "secret":
		return ""
	default:
		return "性别取值非法"
	}
}

// ValidateMatchGender 校验匹配性别偏好。
func ValidateMatchGender(g string) string {
	switch g {
	case "any", "male", "female":
		return ""
	default:
		return "匹配性别取值非法"
	}
}

// ValidateProvince 校验省份（空字符串表示不设置）。
func ValidateProvince(p string) string {
	if p == "" {
		return ""
	}
	if !IsProvince(p) {
		return "省份取值非法"
	}
	return ""
}

// NormalizeNickname 去掉首尾空白并校验长度、控制字符，返回规范化后的昵称。
func NormalizeNickname(raw string) (string, string) {
	c := Get()
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "昵称不能为空"
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", "昵称不能包含控制字符"
		}
	}
	n := utf8.RuneCountInString(s)
	if n < c.MinNickname || n > c.MaxNickname {
		return "", fmt.Sprintf("昵称需 %d–%d 个字符", c.MinNickname, c.MaxNickname)
	}
	return s, ""
}

// MatchProfile 是匹配用的资料快照（how.md 3.4）。
type MatchProfile struct {
	Gender        string
	Age           *int16
	Province      *string
	Tags          []string
	CleanMode     bool
	MatchGender   string
	MatchSameProv bool
	VIP           bool
}

// PublicProfile 是给对方看的公开资料（不含任何身份标识）。
type PublicProfile struct {
	Gender    *string  `json:"gender"`
	Age       *int16   `json:"age"`
	Province  *string  `json:"province"`
	Tags      []string `json:"tags"`
	CleanMode bool     `json:"cleanMode"`
}

// NewPublicProfile 依据资料生成公开视图：性别为 secret 时输出 null；
// 省份只在 province_public=true 时输出。
func NewPublicProfile(gender string, age *int16, province *string, provincePublic bool, tags []string, cleanMode bool) PublicProfile {
	var g *string
	if gender == "male" || gender == "female" {
		v := gender
		g = &v
	}
	var p *string
	if provincePublic && province != nil && *province != "" {
		v := *province
		p = &v
	}
	if tags == nil {
		tags = []string{}
	}
	return PublicProfile{Gender: g, Age: age, Province: p, Tags: tags, CleanMode: cleanMode}
}
