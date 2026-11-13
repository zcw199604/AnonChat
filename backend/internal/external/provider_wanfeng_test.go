package external

import (
	"context"
	"errors"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"testing"
	"time"
)

type captureWanfengClient struct {
	commands []wanfeng.OutgoingCommand
	closed   bool
	ready    bool
}

func (c *captureWanfengClient) SendCommand(cmd wanfeng.OutgoingCommand) {
	c.commands = append(c.commands, cmd)
}
func (c *captureWanfengClient) Close()        { c.closed = true }
func (c *captureWanfengClient) IsReady() bool { return c.ready }
func TestProviderWanfengCommandsKeepBracketTextAsText(t *testing.T) {
	c := &captureWanfengClient{ready: true}
	proto := wanfeng.DefaultProtocol()
	s := &wanfengSession{client: c, proto: proto, uid: "remote-self", localID: "local-self", cancel: func() {}}
	for _, fn := range []func() error{s.startMatch, s.cancelMatch, s.leaveRoom, func() error { return s.sendText("peer", "昵称", "[x.jpg]") }, func() error { return s.sendMedia("peer", "昵称", "image", "anonchat-token") }} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.commands) != 5 {
		t.Fatalf("命令数=%d,需要5", len(c.commands))
	}
	if c.commands[0].Action != proto.ActionJoinMatch || c.commands[1].Action != proto.ActionCancelMatch || c.commands[2].Action != proto.ActionCancelMatch {
		t.Fatalf("匹配命令=%+v", c.commands[:3])
	}
	text := c.commands[3]
	if text.Media != nil || text.Action != proto.ActionSendMessage || text.Payload["type"] != "text" || text.Payload["content"] != "[x.jpg]" || text.Payload["targetUid"] != "peer" {
		t.Fatalf("方括号文本错误转成媒体: %+v", text)
	}
	media := c.commands[4]
	if media.Media == nil || media.Media.Kind != "image" || media.Media.Path != "anonchat-token" || media.Payload["targetUid"] != "peer" {
		t.Fatalf("媒体命令=%+v", media)
	}
	if s.selfID() != "local-self" {
		t.Fatalf("selfID=%q", s.selfID())
	}
	s.close()
	if !c.closed {
		t.Fatal("未关闭客户端")
	}
	c.ready = false
	if err := s.startMatch(); err == nil {
		t.Fatal("未就绪时匹配不应伪成功")
	}
}
func TestProviderWanfengConnectHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	api := wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: "http://unused.test"})
	started := time.Now()
	_, err := connectWanfeng(ctx, wanfeng.ClientConfig{API: api, Bindings: wanfeng.NewMemoryBindingStore()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("等待就绪未尊重ctx: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("取消未及时返回")
	}
}
