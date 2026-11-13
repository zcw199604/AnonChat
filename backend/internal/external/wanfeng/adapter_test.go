package wanfeng

import (
	"encoding/json"
	"testing"
)

func TestParseClientFrame(t *testing.T) {
	raw := `{"act":"sign","id":"u1","name":"小明","userSex":"男","userAge":"18","useraddress":"北京"}`
	frame, err := ParseClientFrame(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if frame.Act != "sign" || frame.ID != "u1" || frame.Name != "小明" || frame.UserSex != "男" || frame.UserAge != "18" {
		t.Fatalf("字段解析不符: %+v", frame)
	}
}

func TestTargetFromAct(t *testing.T) {
	cases := map[string]string{
		"touser_WFU123_昵称": "WFU123",
		"touser_WFU123_":   "WFU123",
		"touser_WFU123":    "WFU123",
		"random":           "",
		"":                 "",
	}
	for act, want := range cases {
		if got := TargetFromAct(act); got != want {
			t.Fatalf("TargetFromAct(%q)=%q，期望 %q", act, got, want)
		}
	}
}

func TestBuildCommand(t *testing.T) {
	p := DefaultProtocol()

	cmd, ok := p.BuildCommand(ClientFrame{Act: "random", ID: "u1"}, "")
	if !ok || cmd.Action != p.ActionJoinMatch {
		t.Fatalf("random 应翻译为 join_match，实际 %+v ok=%v", cmd, ok)
	}

	cmd, ok = p.BuildCommand(ClientFrame{Act: "randomOut", ID: "u1"}, "")
	if !ok || cmd.Action != p.ActionCancelMatch {
		t.Fatalf("randomOut 应翻译为 cancel_match，实际 %+v", cmd)
	}

	cmd, ok = p.BuildCommand(ClientFrame{Act: "touser_WFU9_小明", ID: "u1", Msg: "你好"}, "")
	if !ok || cmd.Action != p.ActionSendMessage {
		t.Fatalf("touser_ 应翻译为 send_message，实际 %+v", cmd)
	}
	if cmd.Payload["targetUid"] != "WFU9" || cmd.Payload["content"] != "你好" || cmd.Payload["type"] != "text" {
		t.Fatalf("消息载荷不符: %+v", cmd.Payload)
	}

	if _, ok := p.BuildCommand(ClientFrame{Act: "sign", ID: "u1"}, ""); ok {
		t.Fatal("sign 不应作为业务动作转发")
	}
	if _, ok := p.BuildCommand(ClientFrame{Act: "ShowUserLoginInfo", ID: "u1"}, ""); ok {
		t.Fatal("未知 act 不应转发")
	}
}

func TestParseMatchResultAndMatchedMessage(t *testing.T) {
	p := DefaultProtocol()
	data := map[string]any{
		"type":      "chat",
		"targetUid": "WAT123",
		"partner": map[string]any{
			"uid":      "WAT123",
			"nickname": "看看13",
			"gender":   "female",
			"age":      float64(23),
			"city":     "湖北省武汉市",
		},
	}

	result, ok := p.ParseMatchResult(data)
	if !ok {
		t.Fatal("应解析出匹配结果")
	}
	if result.TargetUID != "WAT123" || result.Nickname != "看看13" || result.Sex != "女" || result.Age != "23" || result.Address != "湖北省武汉市" {
		t.Fatalf("匹配结果不符: %+v", result)
	}

	msg := p.MatchedMessage(result)
	if msg["code"] != codeMatched || msg["sel_userid"] != "WAT123" || msg["sel_userNikename"] != "看看13" {
		t.Fatalf("code=15 消息不符: %+v", msg)
	}

	if _, ok := p.ParseMatchResult(map[string]any{"type": "chat"}); ok {
		t.Fatal("缺少对象 uid 时不应解析成功")
	}
}

func TestReceiveMessagePartner(t *testing.T) {
	p := DefaultProtocol()
	ctx := DownstreamContext{SelfID: "me-id", SelfUpstreamUID: "WFUME", SelfNickname: "我", PartnerUID: "WA1", PartnerNickname: "对方"}

	data := map[string]any{
		"tid":       "t1",
		"fromUid":   "WA1",
		"fromCid":   "C_xxx",
		"targetUid": "WFUME",
		"type":      "text",
		"payload":   "在吗",
		"ts":        float64(1700000000000),
	}
	msg, ok := p.ReceiveMessage(data, ctx)
	if !ok {
		t.Fatal("应翻译为 code=7 消息")
	}
	if msg["code"] != codePrivateMessage || msg["tid"] != "t1" {
		t.Fatalf("消息基础字段不符: %+v", msg)
	}
	fromUser := msg["fromuser"].(map[string]any)
	if fromUser["id"] != "WA1" || fromUser["content"] != "在吗" {
		t.Fatalf("fromuser 不符: %+v", fromUser)
	}
	if fromUser["time"] == "" {
		t.Fatal("时间字段不应为空")
	}
}

func TestReceiveMessageSelfEcho(t *testing.T) {
	p := DefaultProtocol()
	ctx := DownstreamContext{SelfID: "me-id", SelfUpstreamUID: "WFUME", SelfNickname: "我", PartnerUID: "WA1", PartnerNickname: "对方"}

	data := map[string]any{
		"tid":       "t2",
		"fromUid":   "WFUME",
		"fromCid":   "self",
		"targetUid": "WA1",
		"type":      "text",
		"content":   "我发的",
	}
	msg, ok := p.ReceiveMessage(data, ctx)
	if !ok {
		t.Fatal("应翻译为自己的回显消息")
	}
	fromUser := msg["fromuser"].(map[string]any)
	// 前端用 md5(自己 id) 判定自己发出的消息
	if fromUser["id"] != md5HexLower("me-id") {
		t.Fatalf("自己回显的 fromuser.id 应为 md5(自身 id)，实际 %v", fromUser["id"])
	}
	toUser := msg["touser"].(map[string]any)
	if toUser["id"] != "WA1" {
		t.Fatalf("touser.id 应为对方 uid，实际 %v", toUser["id"])
	}
}

func TestReceiveMessageIgnoresNonText(t *testing.T) {
	p := DefaultProtocol()
	if _, ok := p.ReceiveMessage(map[string]any{"type": "image", "content": "x"}, DownstreamContext{}); ok {
		t.Fatal("一期只支持纯文字，图片等应忽略")
	}
	if _, ok := p.ReceiveMessage(map[string]any{"type": "text", "content": ""}, DownstreamContext{}); ok {
		t.Fatal("空内容应忽略")
	}
}

func TestErrorMessages(t *testing.T) {
	p := DefaultProtocol()

	out, fatal := p.ErrorMessages(map[string]any{"code": "IP_BANNED", "message": "IP 已被封禁", "remainingMs": float64(120000)})
	if !fatal || len(out) != 1 {
		t.Fatalf("封禁类错误应为致命并输出一条，实际 fatal=%v out=%+v", fatal, out)
	}
	if out[0]["code"] != codeForbidden || out[0]["forceout"] != true {
		t.Fatalf("封禁帧应复用 code=-4 forceout，实际 %+v", out[0])
	}
	if content := out[0]["content"].(string); content == "" {
		t.Fatal("封禁提示不应为空")
	}

	// 普通提示必须是非致命、且不能复用 -4（前端会把 -4+forceout 当封禁跳登录页）
	out, fatal = p.ErrorMessages(map[string]any{"code": "SOME_NOTICE", "message": "提示一下"})
	if fatal || len(out) != 1 {
		t.Fatalf("普通错误应为非致命提示，实际 fatal=%v out=%+v", fatal, out)
	}
	if out[0]["code"] != codeSystemNotice || out[0]["forceout"] != nil || out[0]["content"] != "提示一下" {
		t.Fatalf("普通错误应作为系统提示下发，实际 %+v", out[0])
	}

	// 身份/设备类错误交给客户端自愈，不应判死
	for _, code := range []string{"IDENTITY_INVALID", "DEVICE_DUPLICATE"} {
		if _, fatal := p.ErrorMessages(map[string]any{"code": code, "message": "x"}); fatal {
			t.Fatalf("%s 应由客户端自愈，不应致命", code)
		}
	}
}

func TestRecalledMessageHasContent(t *testing.T) {
	p := DefaultProtocol()
	msg := p.RecalledMessage(map[string]any{"tid": "t9"})
	if msg["code"] != codeRecall || msg["tid"] != "t9" || msg["content"] == "" {
		t.Fatalf("撤回消息应带 content 以便前端展示，实际 %+v", msg)
	}
}

func TestSexMapping(t *testing.T) {
	cases := []struct {
		in     string
		fromUp string
		toUp   string
	}{
		{"male", "男", "male"},
		{"female", "女", "female"},
		{"secret", "未知", "secret"},
		{"男", "男", "male"},
		{"", "未知", "secret"},
	}
	for _, c := range cases {
		if got := SexFromUpstream(c.in); got != c.fromUp {
			t.Fatalf("SexFromUpstream(%q)=%q，期望 %q", c.in, got, c.fromUp)
		}
		if got := SexToUpstream(c.in); got != c.toUp {
			t.Fatalf("SexToUpstream(%q)=%q，期望 %q", c.in, got, c.toUp)
		}
	}
}

func TestLoginPayload(t *testing.T) {
	p := DefaultProtocol()
	binding := Binding{IdentityID: "id1", UpstreamUID: "WFU1", IdentityToken: "v1.abc", DeviceID: "dev-1"}
	payload := p.LoginPayload(binding, Profile{Nickname: "小明", Gender: "male", Age: 20}, true)

	if payload["uid"] != "WFU1" || payload["nickname"] != "小明" || payload["gender"] != "male" {
		t.Fatalf("登录载荷不符: %+v", payload)
	}
	if payload["identityToken"] != "v1.abc" {
		t.Fatalf("应携带 identityToken: %+v", payload)
	}
	if payload["deviceId"] != "dev-1" || payload["resetState"] != true {
		t.Fatalf("设备/重置标记不符: %+v", payload)
	}

	// 无 token 时不应出现 identityToken 字段（对方对空值敏感）
	payload2 := p.LoginPayload(Binding{UpstreamUID: "WFU2", DeviceID: "d"}, Profile{}, false)
	if _, ok := payload2["identityToken"]; ok {
		t.Fatal("无 token 时不应携带 identityToken")
	}
	if payload2["gender"] != "secret" {
		t.Fatalf("缺省性别应为 secret，实际 %v", payload2["gender"])
	}
}

func TestProtocolOverrides(t *testing.T) {
	p := DefaultProtocol()
	out, err := p.ApplyOverrides(`{"actionJoinMatch":"joinMatch","hkdfInfo":"wf-enc-v4","envelopePrefix":"v4."}`)
	if err != nil {
		t.Fatalf("覆盖失败: %v", err)
	}
	if out.ActionJoinMatch != "joinMatch" || out.HKDFInfo != "wf-enc-v4" || out.EnvelopePrefix != "v4." {
		t.Fatalf("覆盖未生效: %+v", out)
	}
	// 未覆盖字段应保持默认值
	if out.ActionSendMessage != "send_message" || out.WSPath != "/ws" {
		t.Fatalf("未覆盖字段被清空: %+v", out)
	}

	if _, err := p.ApplyOverrides(`{bad json}`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}

	same, err := p.ApplyOverrides("")
	if err != nil || same.ActionLogin != p.ActionLogin {
		t.Fatalf("空覆盖应返回默认值: %+v err=%v", same, err)
	}
}

func TestPickHelpers(t *testing.T) {
	m := map[string]any{
		"targetUid": "a",
		"age":       float64(18),
		"partner":   map[string]any{"uid": "p"},
	}
	if got := PickString(m, "missing", "targetUid"); got != "a" {
		t.Fatalf("PickString 应回退到候选键，实际 %q", got)
	}
	if got := PickInt(m, "missing", "age"); got != 18 {
		t.Fatalf("PickInt 取值错误: %d", got)
	}
	if got := PickMap(m, "partner"); got["uid"] != "p" {
		t.Fatalf("PickMap 取值错误: %+v", got)
	}
	if got := PickString(m, "missing"); got != "" {
		t.Fatalf("缺省应为空串，实际 %q", got)
	}
}

// 确保下游消息可被 JSON 序列化（前端按 JSON 解析）。
func TestDownstreamMessagesAreMarshalable(t *testing.T) {
	p := DefaultProtocol()
	msgs := []map[string]any{
		p.MatchingMessage(),
		p.PartnerLeftMessage(),
		p.RecalledMessage(map[string]any{"tid": "t1"}),
		p.MatchedMessage(MatchResult{TargetUID: "u", Nickname: "n", Sex: "未知", Age: "0", Address: "未知"}),
	}
	for _, m := range msgs {
		if _, err := json.Marshal(m); err != nil {
			t.Fatalf("下游消息不可序列化: %v (%+v)", err, m)
		}
	}
}
