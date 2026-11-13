package wanfeng

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newHistoryTestClient 构造一个指向 fakeUpstream、已完成握手的客户端。
func newHistoryTestClient(t *testing.T, data map[string]map[string]any) (*APIClient, *fakeUpstream) {
	t.Helper()
	f := newFakeUpstream(t)
	for path, payload := range data {
		f.apiData[path] = payload
	}
	api := NewAPIClient(APIClientConfig{BaseURL: f.URL(), Protocol: DefaultProtocol()})
	// 握手一次，后续请求复用会话
	if _, err := api.Session(context.Background()); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	return api, f
}

// TestParseRelationsFromHomeData 校验首页聚合数据能解析出会话列表。
func TestParseRelationsFromHomeData(t *testing.T) {
	api, _ := newHistoryTestClient(t, map[string]map[string]any{
		DefaultProtocol().HomeDataPath: {
			"relations": []any{
				map[string]any{
					"uid":            "rel1",
					"targetUid":      "WFU2",
					"targetNickname": "小晚",
					"targetGender":   "female",
					"targetAge":      float64(22),
					"lastMessageAt":  "2026-11-07T10:00:00Z",
					"unreadCount":    float64(3),
				},
				// 没有 targetUid 的脏数据应被丢弃
				map[string]any{"targetNickname": "噪声"},
			},
			"relationsTotal": float64(2),
			"favorites": []any{
				map[string]any{"uid": "WFU9", "nickname": "收藏甲", "gender": "male", "age": float64(30)},
			},
			"favoritesTotal": float64(1),
		},
	})

	data, err := api.HomeData(context.Background(), "WFU1", 20)
	if err != nil {
		t.Fatalf("HomeData 失败: %v", err)
	}
	if len(data.Relations) != 1 {
		t.Fatalf("会话条数应为 1（脏数据被丢弃），实际 %d", len(data.Relations))
	}
	rel := data.Relations[0]
	if rel.TargetUID != "WFU2" || rel.TargetNickname != "小晚" {
		t.Fatalf("会话解析错误: %+v", rel)
	}
	if rel.TargetGender != "female" || rel.TargetAge != 22 || rel.UnreadCount != 3 {
		t.Fatalf("会话字段解析错误: %+v", rel)
	}
	if len(data.Favorites) != 1 || data.Favorites[0].UID != "WFU9" {
		t.Fatalf("收藏解析错误: %+v", data.Favorites)
	}
	if data.Favorites[0].Gender != "male" || data.Favorites[0].Age != 30 {
		t.Fatalf("收藏字段解析错误: %+v", data.Favorites[0])
	}
}

// TestChatMessagesParsing 校验聊天记录解析与翻页游标传递。
func TestChatMessagesParsing(t *testing.T) {
	proto := DefaultProtocol()
	api, fake := newHistoryTestClient(t, map[string]map[string]any{
		proto.ChatMessagesPath: {
			"messages": []any{
				map[string]any{
					"tid":       "t1",
					"fromCid":   "self",
					"fromUid":   "WFU1",
					"targetUid": "WFU2",
					"type":      "text",
					"payload":   "你好",
					"ts":        float64(1699000000000),
				},
				map[string]any{
					"tid":     "t2",
					"fromCid": "peer",
					"fromUid": "WFU2",
					"type":    "image",
					"payload": "https://cdn.example.com/a.jpg",
					"ts":      float64(1699000001000),
				},
			},
			"hasMore": true,
		},
	})

	page, err := api.ChatMessages(context.Background(), "WFU1", "WFU2", 20, "cursor-1")
	if err != nil {
		t.Fatalf("ChatMessages 失败: %v", err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("消息条数应为 2，实际 %d", len(page.Messages))
	}
	if !page.HasMore {
		t.Fatal("应解析出 hasMore=true")
	}
	if page.Messages[0].Type != MediaKindText || page.Messages[0].Payload != "你好" {
		t.Fatalf("文本消息解析错误: %+v", page.Messages[0])
	}
	if page.Messages[1].Type != MediaKindImage {
		t.Fatalf("图片消息类型解析错误: %+v", page.Messages[1])
	}

	// 参数应完整送达对方（翻页游标 before 不能丢）
	fake.mu.Lock()
	params := fake.apiParams[proto.ChatMessagesPath]
	fake.mu.Unlock()
	if params["uid"] != "WFU1" || params["targetUid"] != "WFU2" {
		t.Fatalf("uid/targetUid 传递错误: %+v", params)
	}
	if params["before"] != "cursor-1" {
		t.Fatalf("翻页游标丢失: %+v", params)
	}
}

// TestHistoryRelationMaintenancePaths 校验已读/删除/置顶走的是协议里配置的路径。
func TestHistoryRelationMaintenancePaths(t *testing.T) {
	api, fake := newHistoryTestClient(t, nil)
	proto := DefaultProtocol()

	ctx := context.Background()
	if err := api.MarkRelationRead(ctx, "WFU1", "WFU2"); err != nil {
		t.Fatalf("标记已读失败: %v", err)
	}
	if err := api.MarkAllRelationsRead(ctx, "WFU1"); err != nil {
		t.Fatalf("一键已读失败: %v", err)
	}
	if err := api.DeleteRelation(ctx, "WFU1", "WFU2"); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if err := api.PinFavorite(ctx, "WFU1", "WFU2", true); err != nil {
		t.Fatalf("置顶失败: %v", err)
	}
	// 空列表应直接返回，不发请求
	if err := api.ClearRelations(ctx, "WFU1", nil); err != nil {
		t.Fatalf("空列表清空应直接成功: %v", err)
	}

	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, path := range []string{
		proto.RelationReadPath,
		proto.RelationReadAllPath,
		proto.RelationDeletePath,
		proto.FavoritePinPath,
	} {
		if _, ok := fake.apiParams[path]; !ok {
			t.Fatalf("未请求路径 %s（已请求: %v）", path, keysOf(fake.apiParams))
		}
	}
	// 空列表不应触发 clear-all
	if _, ok := fake.apiParams[proto.RelationClearPath]; ok {
		t.Fatal("空列表清空不应发请求")
	}
	if fake.apiParams[proto.FavoritePinPath]["pinned"] != true {
		t.Fatalf("置顶参数错误: %+v", fake.apiParams[proto.FavoritePinPath])
	}
}

// keysOf 返回 map 的键，便于失败时打印。
func keysOf(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBuildCommandBlockReportRecallRead 校验新增的动作翻译。
func TestBuildCommandBlockReportRecallRead(t *testing.T) {
	proto := DefaultProtocol()

	tests := []struct {
		name       string
		frame      ClientFrame
		wantAction string
		wantKeys   map[string]any
	}{
		{
			name:       "拉黑（前端 warningreport）",
			frame:      ClientFrame{Act: actWarningReport, ID: "WFU2", Msg: "uuid"},
			wantAction: proto.ActionBlockUser,
			wantKeys:   map[string]any{"targetUid": "WFU2"},
		},
		{
			name:       "解除拉黑",
			frame:      ClientFrame{Act: actUnblockUserPrefix + "WFU2"},
			wantAction: proto.ActionUnblockUser,
			wantKeys:   map[string]any{"targetUid": "WFU2"},
		},
		{
			name:       "举报带原因与备注",
			frame:      ClientFrame{Act: actReportUserPrefix + "WFU2_骚扰_一直发广告"},
			wantAction: proto.ActionReportUser,
			wantKeys: map[string]any{
				"targetUid": "WFU2",
				"reason":    "骚扰",
				"remark":    "一直发广告",
			},
		},
		{
			name:       "举报只有目标时用默认原因",
			frame:      ClientFrame{Act: actReportUserPrefix + "WFU2"},
			wantAction: proto.ActionReportUser,
			wantKeys: map[string]any{
				"targetUid": "WFU2",
				"reason":    proto.DefaultReportReason,
			},
		},
		{
			name:       "撤回",
			frame:      ClientFrame{Act: actRecallPrefix + "t9"},
			wantAction: proto.ActionRecallMessage,
			wantKeys:   map[string]any{"tid": "t9"},
		},
		{
			name:       "已读",
			frame:      ClientFrame{Act: actReadPrefix + "WFU2"},
			wantAction: proto.ActionReadMessages,
			wantKeys:   map[string]any{"targetUid": "WFU2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd, ok := proto.BuildCommand(tc.frame, TargetFromAct(tc.frame.Act))
			if !ok {
				t.Fatal("应翻译成动作")
			}
			if cmd.Action != tc.wantAction {
				t.Fatalf("动作名错误: got=%s want=%s", cmd.Action, tc.wantAction)
			}
			for k, want := range tc.wantKeys {
				if got := cmd.Payload[k]; got != want {
					t.Fatalf("payload[%s] 错误: got=%v want=%v", k, got, want)
				}
			}
		})
	}
}

// TestBuildCommandStillIgnoresTyping 校验输入状态帧仍被忽略（对方站点无此能力）。
func TestBuildCommandStillIgnoresTyping(t *testing.T) {
	proto := DefaultProtocol()
	for _, act := range []string{"inputStatusOn_u1_nick", "inputStatusOff_u1_nick", "ShowUserLoginInfo"} {
		if _, ok := proto.BuildCommand(ClientFrame{Act: act}, ""); ok {
			t.Fatalf("%s 不应被转发", act)
		}
	}
}

// TestUpdateProfilePayloadMatchPreference 校验匹配偏好走 update_profile。
func TestUpdateProfilePayloadMatchPreference(t *testing.T) {
	proto := DefaultProtocol()
	payload := proto.UpdateProfilePayload(Profile{
		Nickname: "阿离",
		Gender:   "female",
		Age:      24,
		MatchPreference: map[string]any{
			"preferGender":       "male",
			"preferAgeMin":       20,
			"preferSameProvince": true,
		},
	})
	if payload["nickname"] != "阿离" || payload["gender"] != "female" || payload["age"] != 24 {
		t.Fatalf("基础资料错误: %+v", payload)
	}
	pref, ok := payload["matchPreference"].(map[string]any)
	if !ok || pref["preferGender"] != "male" {
		t.Fatalf("匹配偏好未带上: %+v", payload)
	}

	// 无偏好时不应出现该字段（避免把对方已有偏好清空）
	bare := proto.UpdateProfilePayload(Profile{Nickname: "x"})
	if _, exists := bare["matchPreference"]; exists {
		t.Fatal("无偏好时不应携带 matchPreference")
	}
}

// TestPickBoolAndStringSlice 校验新增容错取值。
func TestPickBoolAndStringSlice(t *testing.T) {
	if v, ok := PickBool(map[string]any{"success": "true"}, "success"); !ok || !v {
		t.Fatal("字符串 true 应解析为 true")
	}
	if v, ok := PickBool(map[string]any{"success": float64(0)}, "success"); !ok || v {
		t.Fatal("数字 0 应解析为 false")
	}
	if _, ok := PickBool(map[string]any{}, "success"); ok {
		t.Fatal("缺字段应返回未找到")
	}

	tids := PickStringSlice(map[string]any{"tids": []any{"a", "", "b"}}, "tids")
	if len(tids) != 2 || tids[0] != "a" || tids[1] != "b" {
		t.Fatalf("字符串切片解析错误: %v", tids)
	}
}

// TestProtocolOverridesHistoryPaths 校验对方改版时路径/动作名可通过覆盖热修。
func TestProtocolOverridesHistoryPaths(t *testing.T) {
	proto := DefaultProtocol()

	effective, err := proto.ApplyOverrides(`{"homeDataPath":"/api/v2/home","actionBlockUser":"block_v2"}`)
	if err != nil {
		t.Fatalf("应用覆盖失败: %v", err)
	}
	if effective.HomeDataPath != "/api/v2/home" {
		t.Fatalf("覆盖值被默认值冲掉了: %s", effective.HomeDataPath)
	}
	if effective.ActionBlockUser != "block_v2" {
		t.Fatalf("动作名覆盖失效: %s", effective.ActionBlockUser)
	}

	// 未覆盖的字段仍应拿到默认值
	if effective.RelationsPath != DefaultProtocol().RelationsPath {
		t.Fatalf("未覆盖字段应回落到默认值: %s", effective.RelationsPath)
	}
}

// waitFrames 等待 fakeUpstream 收到至少 n 帧（或超时）。
func waitFrames(f *fakeUpstream, n int, timeout time.Duration) []map[string]any {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		frames := append([]map[string]any(nil), f.frames...)
		f.mu.Unlock()
		if len(frames) >= n {
			return frames
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.frames...)
}

// findFrame 按动作名找一帧。
func findFrame(frames []map[string]any, action string) map[string]any {
	for _, frame := range frames {
		if frame["type"] == action {
			if data, ok := frame["data"].(map[string]any); ok {
				return data
			}
		}
	}
	return nil
}

// TestClientSendsAutoReadOnIncomingMessage 校验收到对方消息后自动回 read_messages。
//
// 我方前端没有已读 UI，但对方站点有；这条回执让晚风用户看到「已读」，
// 不影响我们自己界面的任何行为。
func TestClientSendsAutoReadOnIncomingMessage(t *testing.T) {
	f := newFakeUpstream(t)
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, nil, emitter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()

	<-f.connectedSent

	if err := f.pushFrame("receive_message", map[string]any{
		"type":      "text",
		"content":   "在吗",
		"fromUid":   "WAT999",
		"targetUid": "WFUFAKE001",
		"tid":       "t-auto-read",
		"ts":        float64(1700000000000),
	}); err != nil {
		t.Fatalf("推送消息失败: %v", err)
	}

	frames := waitFrames(f, 2, 5*time.Second)
	read := findFrame(frames, DefaultProtocol().ActionReadMessages)
	if read == nil {
		t.Fatalf("应自动发送 %s，实际帧: %v", DefaultProtocol().ActionReadMessages, frames)
	}
	if read["targetUid"] != "WAT999" {
		t.Fatalf("已读目标错误: %+v", read)
	}
}

// TestClientSkipsAutoReadForSelfMessage 校验自己发出的消息不会触发已读回执。
func TestClientSkipsAutoReadForSelfMessage(t *testing.T) {
	f := newFakeUpstream(t)
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, nil, emitter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()

	<-f.connectedSent

	if err := f.pushFrame("receive_message", map[string]any{
		"type":      "text",
		"content":   "我发的",
		"fromCid":   "self",
		"fromUid":   "WFUFAKE001",
		"targetUid": "WAT999",
		"tid":       "t-self",
		"ts":        float64(1700000000000),
	}); err != nil {
		t.Fatalf("推送消息失败: %v", err)
	}

	frames := waitFrames(f, 1, 1500*time.Millisecond)
	if read := findFrame(frames, DefaultProtocol().ActionReadMessages); read != nil {
		t.Fatalf("自己发的消息不应回已读: %+v", read)
	}
}

// TestClientHandlesReportResultAndReadEvent 校验 report_user / messages_read 事件不 panic。
func TestClientHandlesReportResultAndReadEvent(t *testing.T) {
	f := newFakeUpstream(t)
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, nil, emitter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()

	<-f.connectedSent

	// 举报成功：不打扰用户
	if err := f.pushFrame("report_user", map[string]any{"targetUid": "WAT999", "success": true}); err != nil {
		t.Fatalf("推送举报成功失败: %v", err)
	}
	// 已读回执：前端无 UI，只落日志
	if err := f.pushFrame("messages_read", map[string]any{"targetUid": "WAT999", "tids": []any{"t1"}}); err != nil {
		t.Fatalf("推送已读回执失败: %v", err)
	}
	// 举报失败：应有一条系统提示
	if err := f.pushFrame("report_user", map[string]any{"targetUid": "WAT999", "success": false}); err != nil {
		t.Fatalf("推送举报失败失败: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && len(emitter.withCode(codeSystemNotice)) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	notices := emitter.withCode(codeSystemNotice)
	if len(notices) != 1 {
		t.Fatalf("举报失败应提示一次，实际 %+v", emitter.list())
	}
	if !strings.Contains(toString(notices[0]["content"]), "举报") {
		t.Fatalf("提示文案不符: %+v", notices[0])
	}
}

// toString 安全取字符串（测试辅助）。
func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
