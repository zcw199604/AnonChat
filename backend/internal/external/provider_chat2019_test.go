package external

import (
	"errors"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"regexp"
	"testing"
)

type captureChatSender struct {
	frames []map[string]any
	closed bool
	err    error
}

func (c *captureChatSender) Send(f map[string]any) error {
	c.frames = append(c.frames, f)
	return c.err
}
func (c *captureChatSender) Close() { c.closed = true }
func TestProviderChat2019Commands(t *testing.T) {
	c := &captureChatSender{}
	s := &chat2019Session{conn: c, self: chat2019.Self{UID: "self"}}
	for _, fn := range []func() error{s.startMatch, s.cancelMatch, s.leaveRoom, func() error { return s.sendText("peer", "昵称", "[x.jpg]") }, func() error { return s.sendMedia("peer", "昵称", "image", "uploaded.jpg") }} {
		if err := fn(); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.frames) != 5 {
		t.Fatalf("发送帧数=%d,需要5", len(c.frames))
	}
	if f := c.frames[0]; f["act"] != "random" || f["id"] != "self" || f["userAge"] != "0" {
		t.Fatalf("匹配帧=%v", f)
	}
	for _, f := range c.frames[1:3] {
		msg, _ := f["msg"].(string)
		if f["act"] != "randomOut" || f["id"] != "self" || !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(msg) {
			t.Fatalf("退出帧=%v", f)
		}
	}
	if c.frames[1]["msg"] == c.frames[2]["msg"] {
		t.Fatal("退出流水号被复用")
	}
	for i, want := range []string{"[x.jpg]", "[uploaded.jpg]"} {
		f := c.frames[i+3]
		if f["act"] != "touser_peer_昵称" || f["id"] != "self" || f["msg"] != want {
			t.Fatalf("消息帧=%v", f)
		}
	}
	s.close()
	if !c.closed {
		t.Fatal("未关闭连接")
	}
	c.err = errors.New("write failed")
	if !errors.Is(s.sendText("peer", "昵称", "text"), c.err) {
		t.Fatal("写入错误未返回")
	}
}
