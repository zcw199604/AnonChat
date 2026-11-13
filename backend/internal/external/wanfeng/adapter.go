package wanfeng

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 本文件只做「协议翻译」：前端 act 协议 ↔ 晚风 type 帧。
// 这里不持有连接/状态，便于单测覆盖；对方改字段时优先改 Protocol 的候选键，而不是改这里的分支。

// 前端 act 协议中与固定动作相关的常量（来自现有前端实现，不建议改动）。
const (
	actSign         = "sign"
	actRandom       = "random"
	actRandomOut    = "randomOut"
	actToUserPrefix = "touser_"
	// actWarningReport 是前端「拉黑该用户」用的 act（沿用 chat2019 语义）。
	actWarningReport = "warningreport"
	// actReportUserPrefix 是「举报」act：reportuser_<uid>_<原因>_<备注>，原因/备注可省略。
	actReportUserPrefix = "reportuser_"
	// actUnblockUserPrefix 是「解除拉黑」act：unblockuser_<uid>。
	actUnblockUserPrefix = "unblockuser_"
	// actRecallPrefix 是「撤回」act：recall_<tid>。
	actRecallPrefix = "recall_"
	// actReadPrefix 是「标记已读」act：read_<uid>。
	actReadPrefix    = "read_"
	actInputStatusOn = "inputStatusOn_"
)

// 我方下发给前端的消息 code（与现有 chat2019 线路保持一致，前端无需区分线路）。
const (
	codePrivateMessage = 7
	codeRecall         = 9
	codeConnected      = 12
	codeMatching       = 13
	codeMatched        = 15
	codePartnerLeft    = 16
	// codeReadReceipt 是「我方消息被对方读过」的推送（晚风线路独有，chat2019 不会发）。
	codeReadReceipt = 31
	codeForbidden   = -4
	// codeSystemNotice 用于「普通系统提示」：前端遇到未知 code 且有 content 时，
	// 会把它追加到当前会话或弹 Toast，不会强制跳登录页。
	codeSystemNotice = 0
)

// ClientFrame 表示前端下发的一帧（act 协议）。
type ClientFrame struct {
	Act         string
	ID          string
	Msg         string
	Name        string
	UserSex     string
	UserAge     string
	UserAddress string
	// MatchPreference 是匹配偏好（对方 update_profile 里的字段），可省略。
	MatchPreference map[string]any
}

// ParseClientFrame 解析前端下发的 act 帧。
func ParseClientFrame(raw string) (ClientFrame, error) {
	var node map[string]any
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		return ClientFrame{}, fmt.Errorf("解析前端帧失败: %w", err)
	}
	return ClientFrame{
		Act:             PickString(node, "act"),
		ID:              PickString(node, "id"),
		Msg:             PickString(node, "msg"),
		Name:            PickString(node, "name", "nickname"),
		UserSex:         PickString(node, "userSex", "sex"),
		UserAge:         PickString(node, "userAge", "age"),
		UserAddress:     PickString(node, "useraddress", "userAddress", "address"),
		MatchPreference: PickMap(node, "matchPreference", "match_preference"),
	}, nil
}

// OutgoingCommand 表示翻译后要发给对方站点的一帧动作。
type OutgoingCommand struct {
	Action  string
	Payload map[string]any
	// Media 非空表示这是媒体消息的发送意图：客户端会先把媒体搬到对方站点，
	// 再用返回的 URL 覆盖 Payload 后发送（避免在协议翻译层做 IO）。
	Media *OutgoingMedia
}

// Profile 是我方身份在对方站点上的展示资料。
type Profile struct {
	Nickname string
	Gender   string // male/female/secret
	Age      int
	// ShowCity 对应对方站点设置页的「城市：公开 / 不公开」开关（false = 不公开）。
	//
	// 对方站点把城市按 IP 归属地挂在我们身份上，匹配成功时以 match_result.partner.city
	// 转给对面；这个开关是唯一能在协议层控制它的手段（没有它对方默认公开）。
	// nil 表示不下发，沿用对方站点上的既有设置（保持行为不变）。
	ShowCity *bool
	// MatchPreference 是匹配偏好（对方 update_profile 的字段，可省略）。
	MatchPreference map[string]any
}

// UpdateProfilePayload 组装 update_profile 动作的 payload。
//
// 对方把匹配偏好挂在用户资料上，因此改偏好必须走 update_profile（对方前端同样如此）。
func (p Protocol) UpdateProfilePayload(profile Profile) map[string]any {
	nickname := strings.TrimSpace(profile.Nickname)
	if nickname == "" {
		nickname = "匿名"
	}
	gender := strings.TrimSpace(profile.Gender)
	if gender == "" {
		gender = "secret"
	}
	age := profile.Age
	if age <= 0 {
		age = p.DefaultAge
	}
	payload := map[string]any{
		"nickname": nickname,
		"gender":   gender,
		"age":      age,
	}
	if len(profile.MatchPreference) > 0 {
		payload["matchPreference"] = profile.MatchPreference
	}
	// 「城市公开」只能通过 update_profile 下发（对方 login 帧没有该字段）。
	if profile.ShowCity != nil {
		payload["showCity"] = *profile.ShowCity
	}
	return payload
}

// isStateNotAllowed 判断对方是否在说「当前状态不允许匹配」。
//
// 对方状态机在服务端：房间/队列还在时 join_match 会被拒绝，需要先 exit_chat（离开房间）
// 或 cancel_match（退出队列）才能重新匹配。
func isStateNotAllowed(message string) bool {
	compact := strings.Join(strings.Fields(message), "")
	if compact == "" {
		return false
	}
	return strings.Contains(compact, "不允许匹配") || strings.Contains(compact, "状态不允许")
}

// TargetFromAct 从 "touser_<uid>_<nickname>" 中解析目标用户 id。
func TargetFromAct(act string) string {
	act = strings.TrimSpace(act)
	if !strings.HasPrefix(act, actToUserPrefix) {
		return ""
	}
	rest := strings.TrimPrefix(act, actToUserPrefix)
	if rest == "" {
		return ""
	}
	if idx := strings.Index(rest, "_"); idx >= 0 {
		return strings.TrimSpace(rest[:idx])
	}
	return strings.TrimSpace(rest)
}

// BuildCommand 把前端帧翻译成晚风动作；返回 ok=false 表示该帧不需要转发。
func (p Protocol) BuildCommand(frame ClientFrame, targetUID string) (OutgoingCommand, bool) {
	act := strings.TrimSpace(frame.Act)
	switch {
	case act == actRandom:
		return OutgoingCommand{Action: p.ActionJoinMatch, Payload: map[string]any{}}, true
	case act == actRandomOut:
		return OutgoingCommand{Action: p.ActionCancelMatch, Payload: map[string]any{}}, true
	case strings.HasPrefix(act, actToUserPrefix):
		target := targetUID
		if target == "" {
			target = TargetFromAct(act)
		}
		if target == "" {
			return OutgoingCommand{}, false
		}
		// 媒体消息：前端沿用 chat2019 的 "[文件名]" 编码，这里标记为待搬运
		if token, ok := ParseMediaToken(frame.Msg); ok {
			return OutgoingCommand{
				Action: p.ActionSendMessage,
				Payload: map[string]any{
					"targetUid": target,
					"localTid":  newLocalTid(),
				},
				Media: &OutgoingMedia{Kind: token.Kind, Path: token.Path},
			}, true
		}
		return OutgoingCommand{
			Action: p.ActionSendMessage,
			Payload: map[string]any{
				"targetUid": target,
				"type":      "text",
				"content":   frame.Msg,
				"localTid":  newLocalTid(),
			},
		}, true
	case act == actWarningReport:
		// 前端「拉黑该用户」：id 是被拉黑方，msg 是本地流水号（对方无用）
		target := firstNonEmpty(targetUID, frame.ID)
		if target == "" {
			return OutgoingCommand{}, false
		}
		return OutgoingCommand{Action: p.ActionBlockUser, Payload: map[string]any{"targetUid": target}}, true
	case strings.HasPrefix(act, actUnblockUserPrefix):
		target := firstNonEmpty(strings.TrimPrefix(act, actUnblockUserPrefix), frame.ID)
		if target == "" {
			return OutgoingCommand{}, false
		}
		return OutgoingCommand{Action: p.ActionUnblockUser, Payload: map[string]any{"targetUid": target}}, true
	case strings.HasPrefix(act, actReportUserPrefix):
		// reportuser_<uid>_<reason>_<remark>
		parts := strings.SplitN(strings.TrimPrefix(act, actReportUserPrefix), "_", 3)
		target := firstNonEmpty(parts[0], frame.ID)
		if target == "" {
			return OutgoingCommand{}, false
		}
		payload := map[string]any{"targetUid": target, "reason": p.DefaultReportReason}
		if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
			payload["reason"] = strings.TrimSpace(parts[1])
		}
		if len(parts) > 2 && strings.TrimSpace(parts[2]) != "" {
			payload["remark"] = strings.TrimSpace(parts[2])
		} else if strings.TrimSpace(frame.Msg) != "" {
			payload["remark"] = strings.TrimSpace(frame.Msg)
		}
		return OutgoingCommand{Action: p.ActionReportUser, Payload: payload}, true
	case strings.HasPrefix(act, actRecallPrefix):
		tid := firstNonEmpty(strings.TrimPrefix(act, actRecallPrefix), frame.Msg)
		if tid == "" {
			return OutgoingCommand{}, false
		}
		return OutgoingCommand{Action: p.ActionRecallMessage, Payload: map[string]any{"tid": tid}}, true
	case strings.HasPrefix(act, actReadPrefix):
		target := firstNonEmpty(strings.TrimPrefix(act, actReadPrefix), targetUID, frame.ID)
		if target == "" {
			return OutgoingCommand{}, false
		}
		return OutgoingCommand{Action: p.ActionReadMessages, Payload: map[string]any{"targetUid": target}}, true
	default:
		// sign / inputStatusOn_* / ShowUserLoginInfo 等由连接层自行处理或忽略
		return OutgoingCommand{}, false
	}
}

// DownstreamContext 是翻译服务端帧时需要的本地上下文。
type DownstreamContext struct {
	// SelfID 是前端使用的身份 id（我方内部 id，不是对方 uid）。
	SelfID string
	// SelfUpstreamUID 是我方在对方站点上的 uid（用于判定消息是否为自己发出）。
	SelfUpstreamUID string
	// SelfNickname 是我方昵称。
	SelfNickname string
	// SelfSex 是我方性别（男/女/未知，按前端习惯）。
	SelfSex string
	// PartnerUID / PartnerNickname 是当前聊天对象。
	PartnerUID      string
	PartnerNickname string
}

// MatchResult 是从 match_result 帧解析出的匹配结果。
type MatchResult struct {
	TargetUID string
	Nickname  string
	Sex       string // 男/女/未知
	Age       string
	Address   string
}

// ParseMatchResult 解析匹配成功帧。
func (p Protocol) ParseMatchResult(data map[string]any) (MatchResult, bool) {
	partner := PickMap(data, p.PartnerKeys...)
	targetUID := PickString(data, p.TargetUIDKeys...)
	if targetUID == "" {
		targetUID = PickString(partner, p.UIDKeys...)
	}
	if targetUID == "" {
		return MatchResult{}, false
	}

	nickname := PickString(partner, p.NicknameKeys...)
	if nickname == "" {
		nickname = "匿名用户"
	}
	age := PickString(partner, p.AgeKeys...)
	if age == "" {
		age = "0"
	}
	address := PickString(partner, p.CityKeys...)
	if address == "" {
		address = "未知"
	}

	return MatchResult{
		TargetUID: targetUID,
		Nickname:  nickname,
		Sex:       SexFromUpstream(PickString(partner, p.GenderKeys...)),
		Age:       age,
		Address:   address,
	}, true
}

// MatchedMessage 生成前端 code=15（匹配成功）消息。
func (p Protocol) MatchedMessage(m MatchResult) map[string]any {
	return map[string]any{
		"code":             codeMatched,
		"sel_userid":       m.TargetUID,
		"sel_userNikename": m.Nickname,
		"sel_userSex":      m.Sex,
		"sel_userAge":      m.Age,
		"sel_userAddress":  m.Address,
	}
}

// ConnectedMessages 生成连接成功类消息。
func (p Protocol) ConnectedMessages(data map[string]any) []map[string]any {
	out := []map[string]any{{"code": codeConnected, "content": "连接成功"}}
	state := PickString(data, p.StateKeys...)
	if state == p.StateMatching {
		out = append(out, map[string]any{"code": codeMatching, "content": "正在匹配中"})
	}
	return out
}

// MatchingMessage 生成「进入匹配队列」消息（前端 code=13 只做 Toast）。
func (p Protocol) MatchingMessage() map[string]any {
	return map[string]any{"code": codeMatching, "content": "正在匹配…"}
}

// PartnerLeftMessage 生成对方离开消息。
func (p Protocol) PartnerLeftMessage() map[string]any {
	return map[string]any{"code": codePartnerLeft, "content": "对方已离开"}
}

// RecalledMessage 生成撤回消息。
func (p Protocol) RecalledMessage(data map[string]any) map[string]any {
	tid := PickString(data, p.MessageIDKeys...)
	return map[string]any{"code": codeRecall, "tid": tid, "content": "对方撤回了一条消息"}
}

// ReceiveMessage 把 receive_message 帧翻译成前端 code=7 私信消息。
// 说明：前端用 md5(自己的 id) 判定「自己发出的消息」，这里保持与 chat2019 线路一致的编码方式。
func (p Protocol) ReceiveMessage(data map[string]any, ctx DownstreamContext) (map[string]any, bool) {
	msgType := strings.ToLower(PickString(data, p.TypeKeys...))
	// 一期只做纯文字：其它类型（图片/语音/特效等）直接丢弃，避免前端渲染异常
	if msgType != "" && msgType != "text" {
		return nil, false
	}

	fromUID := PickString(data, p.FromUIDKeys...)
	targetUID := PickString(data, p.TargetUIDKeys...)
	fromCid := PickString(data, "fromCid")
	isSelf := strings.EqualFold(fromCid, "self") ||
		(ctx.SelfUpstreamUID != "" && strings.EqualFold(fromUID, ctx.SelfUpstreamUID))

	content := PickString(data, p.ContentKeys...)
	if content == "" {
		return nil, false
	}

	tid := PickString(data, p.MessageIDKeys...)
	if tid == "" {
		tid = newLocalTid()
	}
	ts := PickInt(data, p.TimeKeys...)
	msgTime := formatMessageTime(ts)

	selfIDMD5 := md5HexLower(ctx.SelfID)

	var fromUser, toUser map[string]any
	if isSelf {
		// 自己发出的回显：fromuser.id 用 md5(自身 id)，与 chat2019 上游行为一致
		peerUID := targetUID
		if peerUID == "" {
			peerUID = ctx.PartnerUID
		}
		fromUser = map[string]any{
			"id":       selfIDMD5,
			"nickname": ctx.SelfNickname,
			"content":  content,
			"time":     msgTime,
			"Tid":      tid,
		}
		toUser = map[string]any{
			"id":       peerUID,
			"nickname": ctx.PartnerNickname,
		}
	} else {
		if fromUID == "" {
			fromUID = ctx.PartnerUID
		}
		fromUser = map[string]any{
			"id":       fromUID,
			"nickname": firstNonEmpty(PickString(data, p.NicknameKeys...), ctx.PartnerNickname),
			"content":  content,
			"time":     msgTime,
			"Tid":      tid,
		}
		toUser = map[string]any{
			"id":       selfIDMD5,
			"nickname": ctx.SelfNickname,
		}
	}

	out := map[string]any{
		"code":     codePrivateMessage,
		"tid":      tid,
		"fromuser": fromUser,
		"touser":   toUser,
	}
	// 自己发出的消息带上「对方是否已读」：对方读过后服务端会补 messages_read 推送，
	// 这里把回显里已有的状态透传，前端据此显示「已读 / 未读」。
	if isSelf {
		readByReceiver, _ := PickBool(data, "readByRecipient", "readByReceiver")
		out["readByRecipient"] = readByReceiver
	}
	return out, true
}

// ReadReceiptMessage 把 messages_read（对方读了我方消息）翻译成前端 code=31。
//
// 载荷：{tids:[...], readAt:<时间字符串>, targetUid:<对方 uid>}；
// 前端按 tid 把自己发出的消息标记为已读。
func (p Protocol) ReadReceiptMessage(data map[string]any) (map[string]any, bool) {
	tids := PickStringSlice(data, "tids", "tidsRead", "readTids")
	if len(tids) == 0 {
		return nil, false
	}

	peer := PickString(data, p.FromUIDKeys...)
	if peer == "" {
		peer = PickString(data, p.TargetUIDKeys...)
	}

	msg := map[string]any{
		"code":      codeReadReceipt,
		"tids":      tids,
		"targetUid": peer,
	}
	if readAt := PickString(data, "readAt"); readAt != "" {
		msg["readAt"] = readAt
	}
	return msg, true
}

// ErrorMessages 把对方的 error 帧翻译成前端可识别的提示/封禁帧。
// 返回 fatal=true 表示应断开并停止重连（例如身份已失效、设备重复登录）。
func (p Protocol) ErrorMessages(data map[string]any) (out []map[string]any, fatal bool) {
	code := strings.ToUpper(PickString(data, "code"))
	message := PickString(data, "message", "clientMsg", "msg")
	remainingMs := PickInt(data, "remainingMs")
	if remainingMs <= 0 {
		remainingMs = 60000
	}

	switch code {
	case "IP_BANNED", "USER_BANNED", "ACTIVATION_BANNED", "ACTIVATION_EXPIRED":
		seconds := remainingMs / 1000
		if seconds <= 0 {
			seconds = 60
		}
		content := message
		if content == "" {
			content = "当前线路暂不可用"
		}
		// 复用前端的「暂时禁止连接（带倒计时）」处理，避免用户被强制踢回登录页
		out = append(out, map[string]any{
			"code":     codeForbidden,
			"content":  fmt.Sprintf("%s，请%d秒后再试", content, seconds),
			"forceout": true,
		})
		return out, true
	default:
		if message == "" {
			return nil, false
		}
		// 普通提示（频率限制、对方已离开等）不能复用 -4：前端会把 -4+forceout 当成封禁并跳登录页
		out = append(out, map[string]any{
			"code":    codeSystemNotice,
			"content": message,
		})
		return out, false
	}
}

// SexFromUpstream 把对方的性别值映射为前端使用的取值。
func SexFromUpstream(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "male", "m", "男", "1":
		return "男"
	case "female", "f", "女", "2":
		return "女"
	default:
		return "未知"
	}
}

// SexToUpstream 把前端/身份中的性别映射为对方接受的值。
func SexToUpstream(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "male", "m", "男", "1":
		return "male"
	case "female", "f", "女", "2":
		return "female"
	default:
		return "secret"
	}
}

// LoginPayload 构造登录帧载荷。
func (p Protocol) LoginPayload(binding Binding, profile Profile, resetState bool) map[string]any {
	nickname := strings.TrimSpace(profile.Nickname)
	if nickname == "" {
		nickname = "匿名"
	}
	gender := profile.Gender
	if strings.TrimSpace(gender) == "" {
		gender = "secret"
	}
	age := profile.Age
	if age <= 0 {
		age = p.DefaultAge
	}
	payload := map[string]any{
		"uid":         binding.UpstreamUID,
		"nickname":    nickname,
		"gender":      gender,
		"age":         age,
		"resetState":  resetState,
		"deviceId":    binding.DeviceID,
		"fingerprint": binding.DeviceID,
		"hwfp":        binding.DeviceID,
	}
	if token := strings.TrimSpace(binding.IdentityToken); token != "" {
		payload["identityToken"] = token
	}
	// 匹配偏好随登录帧上送（对方把它挂在用户资料上）
	if len(profile.MatchPreference) > 0 {
		payload["matchPreference"] = profile.MatchPreference
	}
	return payload
}

func formatMessageTime(ts int) string {
	t := time.Now()
	if ts > 0 {
		if ts < 1e12 { // 秒级时间戳
			t = time.Unix(int64(ts), 0)
		} else {
			t = time.UnixMilli(int64(ts))
		}
	}
	return t.Format("2006-01-02 15:04:05.000")
}

func newLocalTid() string {
	return fmt.Sprintf("local_%d_%d", time.Now().UnixNano(), time.Now().UnixMilli()%100000)
}

func md5HexLower(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	sum := md5.Sum([]byte(input))
	return hex.EncodeToString(sum[:])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// TextCommand 构造纯文本命令，不解析媒体标记。
func (p Protocol) TextCommand(targetUID, text string) OutgoingCommand {
	return OutgoingCommand{
		Action:  p.ActionSendMessage,
		Payload: map[string]any{"targetUid": targetUID, "type": "text", "content": text, "localTid": newLocalTid()},
	}
}

// MediaCommand 构造显式媒体命令。
func (p Protocol) MediaCommand(targetUID, kind, path string) OutgoingCommand {
	return OutgoingCommand{
		Action:  p.ActionSendMessage,
		Payload: map[string]any{"targetUid": targetUID, "localTid": newLocalTid()},
		Media:   &OutgoingMedia{Kind: kind, Path: path},
	}
}
