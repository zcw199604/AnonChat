package moderation

import "time"

type RuleKey string

const (
	RuleIdentityCreate      RuleKey = "identity_create"
	RuleMsgPerSecond        RuleKey = "msg_per_second"
	RuleMsgPerMinute        RuleKey = "msg_per_minute"
	RuleRoomMsgInterval     RuleKey = "room_msg_interval"
	RuleMatchStart          RuleKey = "match_start"
	RuleWSConnPerIPIdentity RuleKey = "ws_conn_per_ip_identity"
	RuleWSConnPerIP         RuleKey = "ws_conn_per_ip"
	RuleSMSPerPhone         RuleKey = "sms_per_phone"
	RuleSMSPerPhoneDaily    RuleKey = "sms_per_phone_daily"
	RuleSMSPerIPDaily       RuleKey = "sms_per_ip_daily"
	RuleBottlePost          RuleKey = "bottle_post"
	RuleBottlePick          RuleKey = "bottle_pick"
	RulePrivateLinkCreate   RuleKey = "private_link_create"
	RulePrivateLinkPassword RuleKey = "private_link_password"
	RuleAdminLogin          RuleKey = "admin_login"
	RuleReportDaily         RuleKey = "report_daily"
	RuleNicknameShuffle     RuleKey = "nickname_shuffle"
	RuleNicknameSet         RuleKey = "nickname_set"
	RuleVIPRedeemPerID      RuleKey = "vip_redeem_per_id"
	RuleVIPRedeemPerIP      RuleKey = "vip_redeem_per_ip"
	RuleBroadcastPost       RuleKey = "broadcast_post"
	RuleStoryCreate         RuleKey = "story_create"
	RuleBotSettingsTest     RuleKey = "bot_settings_test"
)

// RuleMeta 描述规则的固定属性，默认窗口上限为身份审计保留期 180 天。
type RuleMeta struct {
	Key                  RuleKey `json:"key"`
	Name                 string  `json:"name"`
	Group                string  `json:"group"`
	Kind                 string  `json:"kind"`
	Algorithm            string  `json:"algorithm"`
	KeyPrefix            string  `json:"-"`
	BanSubject           string  `json:"banSubject"`
	DefaultLimit         int     `json:"-"`
	DefaultWindowSeconds int     `json:"-"`
	DefaultBanSeconds    int     `json:"-"`
	MinWindowSeconds     int     `json:"minWindowSeconds"`
	MaxWindowSeconds     int     `json:"maxWindowSeconds"`
}

type Rule struct {
	Key           RuleKey   `json:"key"`
	Limit         int       `json:"limit"`
	WindowSeconds int       `json:"windowSeconds"`
	BanSeconds    int       `json:"banSeconds"`
	Enabled       bool      `json:"enabled"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

var ruleRegistry = []RuleMeta{
	{RuleIdentityCreate, "创建身份", "identity", "frequency", "fixed_window", "identity_create:", "ip", 5, 86400, 86400, 1, 15552000},
	{RuleMsgPerSecond, "每秒发送消息", "message", "frequency", "token_bucket", "msg:", "identity", 1, 1, 0, 1, 15552000},
	{RuleMsgPerMinute, "每分钟发送消息", "message", "frequency", "fixed_window", "msg:", "identity", 10, 60, 0, 1, 15552000},
	{RuleRoomMsgInterval, "房间发言间隔", "message", "frequency", "token_bucket", "room_msg:", "", 1, 1, 0, 1, 15552000},
	{RuleMatchStart, "开始或切换匹配", "match", "frequency", "fixed_window", "match_start:", "identity", 15, 60, 0, 1, 15552000},
	{RuleWSConnPerIPIdentity, "同网络同身份连接", "ws", "concurrent", "fixed_window", "", "", 1, 0, 0, 0, 0},
	{RuleWSConnPerIP, "同网络连接", "ws", "concurrent", "fixed_window", "", "", 3, 0, 0, 0, 0},
	{RuleSMSPerPhone, "手机号短信冷却", "other", "frequency", "fixed_window", "sms_cool:", "", 1, 60, 0, 1, 15552000},
	{RuleSMSPerPhoneDaily, "手机号每日短信", "other", "frequency", "fixed_window", "sms_day:", "", 5, 86400, 0, 1, 15552000},
	{RuleSMSPerIPDaily, "IP每日短信", "other", "frequency", "fixed_window", "sms_ip:", "ip", 20, 86400, 0, 1, 15552000},
	{RuleBottlePost, "投放漂流瓶", "other", "frequency", "fixed_window", "bottle_post:", "identity", 10, 86400, 0, 1, 15552000},
	{RuleBottlePick, "捞取漂流瓶", "other", "frequency", "fixed_window", "bottle_pick:", "identity", 50, 86400, 0, 1, 15552000},
	{RulePrivateLinkCreate, "创建私密链接", "other", "frequency", "fixed_window", "private_create:", "identity", 20, 86400, 0, 1, 15552000},
	{RulePrivateLinkPassword, "私密链接密码错误", "other", "frequency", "fixed_window", "pw_", "", 5, 600, 0, 1, 15552000},
	{RuleAdminLogin, "管理员登录失败", "other", "frequency", "fixed_window", "admin_login_", "", 5, 900, 0, 1, 15552000},
	{RuleReportDaily, "每日举报", "other", "frequency", "fixed_window", "report:", "identity", 20, 86400, 0, 1, 15552000},
	{RuleNicknameShuffle, "随机昵称", "other", "frequency", "fixed_window", "nickname_shuffle:", "identity", 10, 60, 0, 1, 15552000},
	{RuleNicknameSet, "设置昵称", "other", "frequency", "fixed_window", "nickname_set:", "identity", 10, 86400, 0, 1, 15552000},
	{RuleVIPRedeemPerID, "身份兑换激活码", "other", "frequency", "fixed_window", "vip_redeem:", "identity", 5, 600, 0, 1, 15552000},
	{RuleVIPRedeemPerIP, "IP兑换激活码", "other", "frequency", "fixed_window", "vip_redeem_ip:", "", 20, 600, 0, 1, 15552000},
	{RuleBroadcastPost, "提交广播", "other", "frequency", "fixed_window", "broadcast_post:", "identity", 5, 86400, 0, 1, 15552000},
	{RuleStoryCreate, "发起故事", "other", "frequency", "fixed_window", "story_create:", "identity", 10, 86400, 0, 1, 15552000},
	{RuleBotSettingsTest, "测试模型连接", "other", "frequency", "fixed_window", "bot_settings_test:", "", 10, 60, 0, 1, 15552000},
}

func Registry() []RuleMeta { return append([]RuleMeta(nil), ruleRegistry...) }
func Meta(key RuleKey) (RuleMeta, bool) {
	for _, m := range ruleRegistry {
		if m.Key == key {
			return m, true
		}
	}
	return RuleMeta{}, false
}

func RuleRegistry() []RuleMeta { return Registry() }
