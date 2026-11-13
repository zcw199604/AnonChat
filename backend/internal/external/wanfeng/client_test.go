package wanfeng

import (
	"context"
	"testing"
)

// TestClientResetBinding 校验身份/设备自愈时能正确清空绑定字段。
func TestClientResetBinding(t *testing.T) {
	store := NewMemoryBindingStore()
	ctx := context.Background()
	if err := store.Save(ctx, Binding{IdentityID: "id1", UpstreamUID: "WFU1", IdentityToken: "v1", DeviceID: "dev"}); err != nil {
		t.Fatalf("准备绑定失败: %v", err)
	}

	client := NewClient(ClientConfig{
		IdentityID: "id1",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   store,
	})
	client.binding = &Binding{IdentityID: "id1", UpstreamUID: "WFU1", IdentityToken: "v1", DeviceID: "dev"}

	// 身份失效：uid/token/device 全部清空，下次重连会申请新身份
	client.resetBinding(true, true)
	got, err := store.Get(ctx, "id1")
	if err != nil || got == nil {
		t.Fatalf("读取绑定失败: %v", err)
	}
	if got.UpstreamUID != "" || got.IdentityToken != "" || got.DeviceID != "" {
		t.Fatalf("身份失效后应清空绑定，实际 %+v", got)
	}

	// 设备重复：只换设备号，保留身份
	if err := store.Save(ctx, Binding{IdentityID: "id1", UpstreamUID: "WFU1", IdentityToken: "v1", DeviceID: "dev"}); err != nil {
		t.Fatalf("准备绑定失败: %v", err)
	}
	client.binding = &Binding{IdentityID: "id1", UpstreamUID: "WFU1", IdentityToken: "v1", DeviceID: "dev"}
	client.resetBinding(false, true)
	got, _ = store.Get(ctx, "id1")
	if got.UpstreamUID != "WFU1" || got.DeviceID != "" {
		t.Fatalf("设备重复只应更换设备号，实际 %+v", got)
	}
}

// TestClientParseFrameDataPlaintext 校验对方改回明文帧时也能解析（降级容错）。
func TestClientParseFrameDataPlaintext(t *testing.T) {
	client := NewClient(ClientConfig{
		IdentityID: "id1",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
	})

	data, ok := client.parseFrameData(map[string]any{"data": map[string]any{"state": "idle"}})
	if !ok || data["state"] != "idle" {
		t.Fatalf("明文帧应可解析，实际 ok=%v data=%+v", ok, data)
	}

	// 缺 data 时返回空 map 且视为可处理
	if data, ok := client.parseFrameData(map[string]any{"type": "x"}); !ok || len(data) != 0 {
		t.Fatalf("缺 data 应返回空 map，实际 ok=%v data=%+v", ok, data)
	}
}
