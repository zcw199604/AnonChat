package wanfeng

import (
	"context"
	"encoding/base64"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLiveHandshakeAndLogin 是对真实上游的联调冒烟测试（默认跳过）。
//
// 运行方式：
//
//	WANFENG_LIVE_TEST=1 go test ./internal/upstream/wanfeng/ -run TestLiveHandshakeAndLogin -v
//
// 说明：只做「握手 → 申请身份 → 登录」，不进入匹配队列，避免打扰对方真实用户。
func TestLiveHandshakeAndLogin(t *testing.T) {
	if os.Getenv("WANFENG_LIVE_TEST") != "1" {
		t.Skip("未开启联调测试（设置 WANFENG_LIVE_TEST=1 启用）")
	}

	baseURL := os.Getenv("WANFENG_BASE_URL")
	if baseURL == "" {
		baseURL = "https://wanfengniming.store"
	}

	api := NewAPIClient(APIClientConfig{
		BaseURL:  baseURL,
		Protocol: DefaultProtocol(),
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	sess, err := api.Session(ctx)
	if err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	if sess.ID == "" || len(sess.Key) != envelopeKeyLen {
		t.Fatalf("会话不合法: sid=%q keyLen=%d", sess.ID, len(sess.Key))
	}
	t.Logf("握手成功 sid=%s", sess.ID)

	identity, err := api.GenerateIdentity(ctx)
	if err != nil {
		t.Fatalf("申请身份失败: %v", err)
	}
	t.Logf("身份 uid=%s nickname=%s", identity.UID, identity.Nickname)

	// 登录并等待 connected
	store := NewMemoryBindingStore()
	emitter := &collectingEmitter{}
	client := NewClient(ClientConfig{
		IdentityID: "live-test",
		Profile:    Profile{Nickname: "联调测试", Gender: "secret", Age: 0},
		API:        api,
		Bindings:   store,
		Emitter:    emitter,
	})

	binding := Binding{
		IdentityID:  "live-test",
		UpstreamUID: identity.UID,
		DeviceID:    "live-test-device-" + time.Now().Format("150405"),
	}
	if err := store.Save(ctx, binding); err != nil {
		t.Fatalf("保存绑定失败: %v", err)
	}

	go client.run(ctx)
	defer client.Close()

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if client.IsReady() {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !client.IsReady() {
		t.Fatalf("登录超时，已收到消息: %+v", emitter.messages())
	}
	t.Logf("登录成功，已收到消息: %+v", emitter.messages())

	// 校验 identityToken 已被持久化（重连必需）
	saved, err := store.Get(ctx, "live-test")
	if err != nil {
		t.Fatalf("读取绑定失败: %v", err)
	}
	if saved == nil || saved.IdentityToken == "" {
		t.Log("警告：未收到 identityToken（对方可能未下发），重连时身份可能重置")
	} else {
		t.Logf("已保存 identityToken=%s...", saved.IdentityToken[:min(12, len(saved.IdentityToken))])
	}
}

type collectingEmitter struct {
	mu  sync.Mutex
	all []map[string]any
}

func (e *collectingEmitter) Emit(_ string, payload map[string]any) {
	e.mu.Lock()
	e.all = append(e.all, payload)
	e.mu.Unlock()
}

func (e *collectingEmitter) EmitFatal(_ string, payload map[string]any) {
	e.Emit("", payload)
}

func (e *collectingEmitter) messages() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.all...)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestLiveMediaUploadAndDownload 校验对方媒体上传接口可用，且返回的 URL 可匿名下载（默认跳过）。
//
// 运行方式：
//
//	WANFENG_LIVE_TEST=1 go test ./internal/upstream/wanfeng/ -run TestLiveMediaUploadAndDownload -v
func TestLiveMediaUploadAndDownload(t *testing.T) {
	if os.Getenv("WANFENG_LIVE_TEST") != "1" {
		t.Skip("未开启联调测试（设置 WANFENG_LIVE_TEST=1 启用）")
	}

	baseURL := os.Getenv("WANFENG_BASE_URL")
	if baseURL == "" {
		baseURL = "https://wanfengniming.store"
	}

	api := NewAPIClient(APIClientConfig{BaseURL: baseURL, Protocol: DefaultProtocol()})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 优先复用已有身份（对方对「同 IP 短时间批量注册」有风控，反复申请会触发封禁）：
	//   WANFENG_TEST_UID=WFU... WANFENG_TEST_TOKEN=v1.xxx WANFENG_LIVE_TEST=1 go test ...
	uid := strings.TrimSpace(os.Getenv("WANFENG_TEST_UID"))
	token := strings.TrimSpace(os.Getenv("WANFENG_TEST_TOKEN"))
	if uid != "" && token != "" {
		if _, err := api.Session(ctx); err != nil {
			t.Fatalf("握手失败: %v", err)
		}
		if err := api.BindIdentityCookie(ctx, token); err != nil {
			t.Logf("绑定身份 Cookie 失败（继续尝试上传）：%v", err)
		}
	} else {
		identity, err := api.GenerateIdentity(ctx)
		if err != nil {
			if strings.Contains(err.Error(), "批量注册") || strings.Contains(err.Error(), "HANDSHAKE_FAILED") {
				t.Skipf("对方触发同 IP 注册风控，跳过（可设置 WANFENG_TEST_UID/WANFENG_TEST_TOKEN 复用已有身份）：%v", err)
			}
			t.Fatalf("申请身份失败: %v", err)
		}
		uid = identity.UID
	}
	api.SetUploadUserID(uid)
	t.Logf("使用身份 uid=%s", uid)

	// 1x1 PNG
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatalf("准备图片失败: %v", err)
	}

	result, err := api.UploadMedia(ctx, "lctest.png", "image/png", pngBytes, api.Protocol().BucketImage)
	if err != nil {
		t.Fatalf("上传媒体失败: %v", err)
	}
	if result.URL == "" {
		t.Fatal("上传成功但未返回 URL")
	}
	t.Logf("上传成功 url=%s fileId=%s", result.URL, result.FileID)

	data, contentType, err := api.DownloadMedia(ctx, result.URL)
	if err != nil {
		t.Fatalf("下载刚上传的媒体失败: %v", err)
	}
	if len(data) != len(pngBytes) {
		t.Fatalf("下载内容长度不一致: got=%d want=%d", len(data), len(pngBytes))
	}
	t.Logf("下载校验通过 contentType=%s bytes=%d", contentType, len(data))
}
