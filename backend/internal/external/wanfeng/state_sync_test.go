package wanfeng

import (
	"context"
	"testing"
	"time"
)

// waitClientRoom 等待客户端进入/离开房间（本地状态）。
func waitClientRoom(t *testing.T, c *Client, want bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.inRoom() == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待房间状态超时 want=%v", want)
}

// actionSequence 返回对方收到的动作序列（保序）。
func actionSequence(f *fakeUpstream) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.frames))
	for _, fr := range f.frames {
		name, _ := fr["type"].(string)
		out = append(out, name)
	}
	return out
}

// countAction 统计某动作出现的次数。
func countAction(seq []string, action string) int {
	n := 0
	for _, s := range seq {
		if s == action {
			n++
		}
	}
	return n
}

// indexAction 返回动作首次出现的下标（-1 表示没有）。
func indexAction(seq []string, action string) int {
	for i, s := range seq {
		if s == action {
			return i
		}
	}
	return -1
}

// enterRoom 让客户端进入房间里（推送 match_result 帧）。
func enterRoom(t *testing.T, f *fakeUpstream, client *Client) {
	t.Helper()
	proto := DefaultProtocol()
	if err := f.pushFrame(proto.EventMatchResult, map[string]any{
		"targetUid": "WAT999",
		"partner": map[string]any{
			"uid": "WAT999", "nickname": "对方", "gender": "female", "age": "22", "city": "杭州",
		},
	}); err != nil {
		t.Fatalf("推送匹配帧失败: %v", err)
	}
	waitClientRoom(t, client, true)
}

// TestJoinMatchLeavesRoomFirst 校验「房间内再次匹配」会先 exit_chat 再 join_match。
//
// 对方状态机在服务端：房间还在时 join_match 会被拒绝（error「当前状态不允许匹配」），
// cancel_match 只作用于匹配队列，所以必须先 exit_chat。
func TestJoinMatchLeavesRoomFirst(t *testing.T) {
	f := newFakeUpstream(t)
	client := newTestClient(t, f, nil, &recordingEmitter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	proto := DefaultProtocol()
	enterRoom(t, f, client)

	cmd, ok := proto.BuildCommand(ClientFrame{Act: "random", ID: "me-id"}, "")
	if !ok {
		t.Fatal("random 应翻译成 join_match")
	}
	client.SendCommand(cmd)

	f.waitFrames(t, proto.ActionJoinMatch, 1)
	seq := actionSequence(f)
	exitIdx := indexAction(seq, proto.ActionExitChat)
	joinIdx := indexAction(seq, proto.ActionJoinMatch)
	if exitIdx < 0 {
		t.Fatalf("房间内重新匹配应先发 exit_chat，实际序列=%v", seq)
	}
	if joinIdx < 0 || exitIdx > joinIdx {
		t.Fatalf("exit_chat 必须在 join_match 之前，实际序列=%v", seq)
	}
	// 本地状态应已离开房间前的旧对象，避免继续把消息发给已离开的人
	if got := client.PartnerUID(); got != "" {
		t.Fatalf("退出房间后应清空 partnerUID，实际 %q", got)
	}
}

// TestCancelMatchInRoomBecomesExitChat 校验房间内的「取消/退出」映射成 exit_chat。
func TestCancelMatchInRoomBecomesExitChat(t *testing.T) {
	f := newFakeUpstream(t)
	client := newTestClient(t, f, nil, &recordingEmitter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	proto := DefaultProtocol()
	enterRoom(t, f, client)

	cmd, ok := proto.BuildCommand(ClientFrame{Act: "randomOut", ID: "me-id"}, "WAT999")
	if !ok {
		t.Fatal("randomOut 应翻译成 cancel_match")
	}
	client.SendCommand(cmd)

	f.waitFrames(t, proto.ActionExitChat, 1)
	seq := actionSequence(f)
	if countAction(seq, proto.ActionCancelMatch) != 0 {
		t.Fatalf("房间内不应发 cancel_match（那只作用于匹配队列），实际序列=%v", seq)
	}
	waitClientRoom(t, client, false)
}

// TestCancelMatchKeepsCancelWhenNotInRoom 校验不在房间时仍是 cancel_match（原来行为不变）。
func TestCancelMatchKeepsCancelWhenNotInRoom(t *testing.T) {
	f := newFakeUpstream(t)
	client := newTestClient(t, f, nil, &recordingEmitter{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	proto := DefaultProtocol()
	cmd, ok := proto.BuildCommand(ClientFrame{Act: "randomOut", ID: "me-id"}, "")
	if !ok {
		t.Fatal("randomOut 应翻译成 cancel_match")
	}
	client.SendCommand(cmd)

	f.waitFrames(t, proto.ActionCancelMatch, 1)
	if seq := actionSequence(f); countAction(seq, proto.ActionExitChat) != 0 {
		t.Fatalf("空闲态取消匹配不应发 exit_chat，实际序列=%v", seq)
	}
}

// TestStateNotAllowedErrorSelfHeals 校验对方回「当前状态不允许匹配」时会自愈重试。
func TestStateNotAllowedErrorSelfHeals(t *testing.T) {
	f := newFakeUpstream(t)
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, nil, emitter)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()
	<-f.connectedSent

	proto := DefaultProtocol()
	// 模拟「我方以为空闲、对方却认为在房间里」：先发 join_match，再让服务端拒绝
	cmd, _ := proto.BuildCommand(ClientFrame{Act: "random", ID: "me-id"}, "")
	client.SendCommand(cmd)
	f.waitFrames(t, proto.ActionJoinMatch, 1)

	if err := f.pushFrame(proto.EventError, map[string]any{
		"code": float64(0), "message": "当前状态不允许匹配",
	}); err != nil {
		t.Fatalf("推送错误帧失败: %v", err)
	}

	// 自愈：exit_chat + 重发 join_match（共 2 次 join）
	f.waitFrames(t, proto.ActionJoinMatch, 2)
	seq := actionSequence(f)
	if indexAction(seq, proto.ActionExitChat) < 0 {
		t.Fatalf("自愈时应先 exit_chat，实际序列=%v", seq)
	}
	waitClientRoom(t, client, false)

	// 自愈错误不应把「当前状态不允许匹配」当成用户可见提示刷屏
	for _, msg := range emitter.list() {
		if content, _ := msg["content"].(string); content == "当前状态不允许匹配" {
			t.Fatalf("自愈时不应把内部错误下发给前端: %v", msg)
		}
	}
}
