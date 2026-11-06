//go:build integration

package conversation_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", "__Host-anon="+token)
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

func writeWS(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		t.Fatalf("WS 写入失败: %v", err)
	}
}

func readWS(t *testing.T, c *websocket.Conn) realtime.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("WS 读取失败: %v", err)
	}
	var env realtime.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("解析信封失败: %v, data=%s", err, data)
	}
	return env
}

// TestWSEndToEndSubscribeSend 验证真实 WS 上：订阅校验、msg.send 回 ack 且广播给其他在线成员。
func TestWSEndToEndSubscribeSend(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	srv := testutil.MustServer(t, pool, testutil.Config{})

	// 建身份与会话（直接操作库）。
	a, tokA := createIdentity(t, pool, "alice")
	b, tokB := createIdentity(t, pool, "bob")
	convSvc := newService(pool, nil)
	conv := createConv(t, pool, convSvc, store.ConversationKindMatch, false, a.ID, b.ID)

	wsA := dialWS(t, srv, tokA)
	if hello := readWS(t, wsA); hello.Type != realtime.TypeHello {
		t.Fatalf("A 首帧应为 hello, 实际 %q", hello.Type)
	}
	wsB := dialWS(t, srv, tokB)
	if hello := readWS(t, wsB); hello.Type != realtime.TypeHello {
		t.Fatalf("B 首帧应为 hello, 实际 %q", hello.Type)
	}

	bogus := newUUID(t).String()
	// A 订阅：成员会话 + 一个非成员会话 → 只对非成员返回 not_member error。
	writeWS(t, wsA, map[string]any{
		"t": realtime.TypeConvSubscribe, "id": "sub1",
		"d": map[string]any{"conversationIds": []string{conv.ID.String(), bogus}},
	})
	errEnv := readWS(t, wsA)
	if errEnv.Type != realtime.TypeError {
		t.Fatalf("A 应收到 error, 实际 %q", errEnv.Type)
	}
	var ep realtime.ErrorPayload
	_ = json.Unmarshal(errEnv.Data, &ep)
	if ep.Code != realtime.CodeNotMember {
		t.Errorf("错误码 = %q, 期望 not_member", ep.Code)
	}
	if !strings.Contains(ep.Message, bogus) {
		t.Errorf("错误应列出被拒的会话 %s, 实际 %q", bogus, ep.Message)
	}

	// B 订阅成员会话。
	writeWS(t, wsB, map[string]any{
		"t": realtime.TypeConvSubscribe, "id": "sub2",
		"d": map[string]any{"conversationIds": []string{conv.ID.String()}},
	})

	// B 发送消息。
	clientMsgID := newUUID(t).String()
	writeWS(t, wsB, map[string]any{
		"t": realtime.TypeMsgSend, "id": "send1",
		"d": map[string]any{
			"conversationId": conv.ID.String(),
			"clientMsgId":    clientMsgID,
			"kind":           "text",
			"body":           "你好呀",
		},
	})

	// B 收到 ack。
	ackEnv := readWS(t, wsB)
	if ackEnv.Type != realtime.TypeAck {
		t.Fatalf("B 应收到 ack, 实际 %q", ackEnv.Type)
	}
	var ack struct {
		MessageID int64 `json:"messageId"`
	}
	_ = json.Unmarshal(ackEnv.Data, &ack)
	if ack.MessageID == 0 {
		t.Error("ack.messageId 不应为 0")
	}

	// A 收到 msg.new 广播。
	newEnv := readWS(t, wsA)
	if newEnv.Type != realtime.TypeMsgNew {
		t.Fatalf("A 应收到 msg.new, 实际 %q", newEnv.Type)
	}
	if strings.Contains(string(newEnv.Data), b.ID.String()) {
		t.Error("广播载荷不应包含发送者 identity_id")
	}
	var payload struct {
		Message struct {
			Body     string `json:"body"`
			Nickname string `json:"nickname"`
		} `json:"message"`
	}
	_ = json.Unmarshal(newEnv.Data, &payload)
	if payload.Message.Body != "你好呀" || payload.Message.Nickname != "bob" {
		t.Errorf("广播内容不符: %+v", payload.Message)
	}
}
