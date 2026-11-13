package external

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/config"
)

func TestConfiguredBridgeBuildsGuardedClientsWithoutNetwork(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); _, _ = io.WriteString(w, "must not reach") }))
	defer target.Close()
	cfg := &config.Config{ExternalTimeout: time.Second, ExternalChat2019RandServerURL: target.URL, ExternalChat2019ImgServerURL: target.URL,
		ExternalWanfengBaseURL: "https://wanfeng.test", ExternalWanfengWSURL: "wss://socket.test/ws", ExternalWanfengMediaHosts: []string{"media.test"}, ExternalWanfengProtocolOverrides: `{"actionSendMessage":"test-send"}`, SettingsEncKey: []byte(strings.Repeat("k", 32)), ExternalWanfengNewIdentityPerHour: 7}
	b, err := NewConfiguredBridge(BridgeDeps{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if hits.Load() != 0 {
		t.Fatal("构造时访问了外站")
	}
	if b.deps.Chat2019.HTTPClient == nil || b.deps.Chat2019.WSDialer == nil || b.deps.Wanfeng == nil || b.deps.WanfengDialer == nil {
		t.Fatal("守卫客户端未装配")
	}
	if _, err := b.deps.Chat2019.HTTPClient.Get(target.URL); err == nil {
		t.Fatal("chat2019装配绕过内网守卫")
	}
	if _, _, err := b.deps.Wanfeng.DownloadMedia(context.Background(), "https://forbidden.test/x.jpg"); err == nil {
		t.Fatal("晚风装配未限制白名单")
	}
	if b.deps.Wanfeng.Protocol().ActionSendMessage != "test-send" {
		t.Fatal("协议覆盖未装配")
	}
	if hits.Load() != 0 {
		t.Fatal("受限请求仍到达假服务")
	}
	if b.Plan().Enabled {
		t.Fatal("缺少设置时默认不得启用外站")
	}
}
func TestConfiguredBridgeRejectsInvalidProtocolOverride(t *testing.T) {
	if _, err := NewConfiguredBridge(BridgeDeps{}, &config.Config{ExternalWanfengBaseURL: "https://wanfeng.test", ExternalWanfengProtocolOverrides: `{"actionSendMessage":42}`}); err == nil {
		t.Fatal("类型非法的协议覆盖被接受")
	}
}
