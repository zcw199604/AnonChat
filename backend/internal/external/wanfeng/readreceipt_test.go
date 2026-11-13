package wanfeng

import "testing"

func TestReadReceiptMessage(t *testing.T) {
	p := DefaultProtocol()
	msg, ok := p.ReadReceiptMessage(map[string]any{"tids": []any{"t1", "t2"}, "readAt": "2026-11-07 20:00:00", "fromUid": "peer"})
	if !ok {
		t.Fatal("应解析出已读回执")
	}
	if msg["code"] != 31 {
		t.Fatalf("code 应为 31，实际 %+v", msg["code"])
	}
	tids, _ := msg["tids"].([]string)
	if len(tids) != 2 || tids[0] != "t1" {
		t.Fatalf("tids 解析错误: %+v", msg["tids"])
	}
	if msg["readAt"] != "2026-11-07 20:00:00" {
		t.Fatalf("readAt 未透传: %+v", msg["readAt"])
	}

	if _, ok := p.ReadReceiptMessage(map[string]any{"readAt": "x"}); ok {
		t.Fatal("没有 tids 时不应下发")
	}
}

// TestClientEmitsReadReceipt 校验对方已读回执会翻译成 code=31 下发。
func TestClientEmitsReadReceipt(t *testing.T) {
	emitter := &recordingEmitter{}
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
	})

	client.markPartnerRead(map[string]any{
		"tids":    []any{"t1", "t2"},
		"readAt":  "2026-11-07 20:00:00",
		"fromUid": "WA1",
	})

	msgs := emitter.list()
	if len(msgs) != 1 {
		t.Fatalf("应下发一条已读回执，实际 %+v", msgs)
	}
	if msgs[0]["code"] != codeReadReceipt {
		t.Fatalf("code 应为 %d，实际 %+v", codeReadReceipt, msgs[0]["code"])
	}
	tids, _ := msgs[0]["tids"].([]string)
	if len(tids) != 2 {
		t.Fatalf("tids 应透传，实际 %+v", msgs[0]["tids"])
	}
	if msgs[0]["targetUid"] != "WA1" {
		t.Fatalf("应带上对端 uid，实际 %+v", msgs[0]["targetUid"])
	}
}

// TestClientPendingReadTids 校验对方消息的 tid 会被记下，并在回执时带上并清空。
func TestClientPendingReadTids(t *testing.T) {
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    &recordingEmitter{},
	})

	client.rememberUnreadTid("WA1", "t1")
	client.rememberUnreadTid("WA1", "t2")
	client.rememberUnreadTid("WA1", "t2")      // 重复不累加
	client.rememberUnreadTid("WA1", "local_1") // 本地乐观 tid 不上报
	client.rememberUnreadTid("WA2", "other")

	if got := client.takePendingReadTids("WA1"); len(got) != 2 || got[0] != "t1" || got[1] != "t2" {
		t.Fatalf("待回执 tid 记录错误，实际 %+v", got)
	}
	if got := client.takePendingReadTids("WA1"); len(got) != 0 {
		t.Fatalf("取出后应清空，实际 %+v", got)
	}
	if got := client.takePendingReadTids("WA2"); len(got) != 1 {
		t.Fatalf("不同对端应分开记录，实际 %+v", got)
	}
}
