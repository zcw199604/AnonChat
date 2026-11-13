package wanfeng

import (
	"strings"
	"testing"
)

// 2026-11-07 实测抓到的上传响应 data（旧格式 RSA 分段），解密后应得到 view 路径。
const legacyUploadSample = "V0I0MVVJVnpwM3hkbDVhd2JJYzRaUHFMOWtncGY2NS8rZWo2OFArbnU3ZllLV3UrRmR0THg3MDZKZGsyUkZ5UHNsZitOb2xpMkFIN0lFb1FPS3NZVXc9PXxvZFV1bFI2ZGg1WUZXOGJiMXBPdGQ3VXBOZEMvTXp0ZjR4dSt5ckhZMVFtWlQ4dlYrMnlMbEtCTG1hN2V2WndCeTloKzJGZ2h1Nk1XL0lEV3RlSjd6dz09fGk4OXJaQlRqbzU5WElwUTZSYU51Q0Y4VXU3Z3plVGlDYThENytibkZpU09Yam5tayt6Q0daRktvbWI0WjNmTVVKWHB4bFZ2Q2E0Vm92UzFyUDdmd0R3PT0="

// TestOpenLegacyPayload 校验旧格式（RSA 分段）回参能被解开。
func TestOpenLegacyPayload(t *testing.T) {
	decoded, err := openLegacyPayload(legacyUploadSample)
	if err != nil {
		t.Fatalf("旧格式解密失败: %v", err)
	}
	fields, ok := decoded.(map[string]any)
	if !ok {
		t.Fatalf("解密结果应为对象，实际 %T", decoded)
	}
	filePath := PickString(fields, "filePath")
	if !strings.HasPrefix(filePath, "/api/tools/niming-file/view/") {
		t.Fatalf("filePath 解析错误: %q（整段密文被当成路径就是这里挂了）", filePath)
	}
	if PickInt(fields, "expireAt") <= 0 {
		t.Fatalf("expireAt 丢失: %+v", fields)
	}
}

// TestOpenLegacyPayloadInvalid 校验坏数据不会 panic，只返回错误。
func TestOpenLegacyPayloadInvalid(t *testing.T) {
	if _, err := openLegacyPayload("not-base64!!"); err == nil {
		t.Fatal("非法回参应报错")
	}
	if _, err := openLegacyPayload(""); err == nil {
		t.Fatal("空回参应报错")
	}
	// 合法 base64 但分段长度不对（密文长度不符）
	if _, err := openLegacyPayload("YWJjfGRlZg=="); err == nil {
		t.Fatal("密文长度不符应报错")
	}
}

// TestDecodeDataField 校验统一入口能区分明文 / 旧格式。
func TestDecodeDataField(t *testing.T) {
	api := NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()})

	plain, err := api.decodeDataField(nil, map[string]any{"filePath": "/x.jpg"})
	if err != nil {
		t.Fatalf("明文对象应原样返回: %v", err)
	}
	if PickString(plain.(map[string]any), "filePath") != "/x.jpg" {
		t.Fatalf("明文对象被改坏: %+v", plain)
	}

	legacy, err := api.decodeDataField(nil, legacyUploadSample)
	if err != nil {
		t.Fatalf("旧格式应可解析: %v", err)
	}
	if !strings.HasPrefix(PickString(legacy.(map[string]any), "filePath"), "/api/tools/niming-file/view/") {
		t.Fatalf("旧格式解析结果异常: %+v", legacy)
	}

	if got, err := api.decodeDataField(nil, ""); got != nil || err != nil {
		t.Fatalf("空 data 应返回 nil,nil 实际 %v,%v", got, err)
	}
}
