//go:build integration

package conversation_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// asSendError 判断错误是否为 *conversation.SendError。
func asSendError(err error, target **conversation.SendError) bool {
	return errors.As(err, target)
}

// listMessagesRaw 以某身份调用历史 handler，返回原始响应体。
func listMessagesRaw(t *testing.T, svc *conversation.Service, convID pgtype.UUID, ident *store.Identity) []byte {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/conversations/"+convID.String()+"/messages", nil)
	req.SetPathValue("id", convID.String())
	req = req.WithContext(identity.WithIdentity(req.Context(), ident))
	rec := httptest.NewRecorder()
	svc.ListMessagesHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("历史接口状态 = %d, body=%s", rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// testObserver 记录会话事件，用于验证观察者通知时机。
type testObserver struct {
	mu       sync.Mutex
	messages []conversation.MessageEvent
	ended    []pgtype.UUID
}

func (o *testObserver) OnMessage(ev conversation.MessageEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.messages = append(o.messages, ev)
}

func (o *testObserver) OnConversationEnded(id pgtype.UUID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.ended = append(o.ended, id)
}

func (o *testObserver) snapshot() (int, int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.messages), len(o.ended)
}

// createMediaItem 直接插入文件夹与媒体条目，返回媒体 ID。
func createMediaItem(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var folderID, itemID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO media_folders (name) VALUES ('测试') RETURNING id").Scan(&folderID); err != nil {
		t.Fatalf("插入文件夹失败: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO media_items (folder_id, kind, mime, size_bytes, storage_key)
		VALUES ($1, 'image', 'image/png', 8, $2) RETURNING id`,
		folderID, folderID.String()+"/x.png").Scan(&itemID); err != nil {
		t.Fatalf("插入媒体失败: %v", err)
	}
	return itemID
}

func TestSendAsMediaIncludesMediaURLAndNotifiesObserver(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	broker := &fakeBroker{}
	svc := newService(pool, broker)
	obs := &testObserver{}
	svc.AddObserver(obs)

	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	mediaID := createMediaItem(t, pool)

	msg, inserted, err := svc.SendAs(ctx, conversation.SendInput{
		Identity:       &a,
		ConversationID: conv.ID,
		ClientMsgID:    newUUID(t),
		Kind:           "image",
		MediaID:        mediaID,
		SkipRateLimit:  true,
	})
	if err != nil {
		t.Fatalf("SendAs 失败: %v", err)
	}
	if !inserted || msg.ID == 0 {
		t.Fatalf("应插入成功: inserted=%v id=%d", inserted, msg.ID)
	}

	// msg.new 携带 mediaUrl。
	if len(broker.broadcasts) != 1 {
		t.Fatalf("广播数 = %d, 期望 1", len(broker.broadcasts))
	}
	payload := string(broker.broadcasts[0].env.Data)
	want := "/api/media/" + mediaID.String()
	if !strings.Contains(payload, `"mediaUrl":"`+want+`"`) {
		t.Fatalf("msg.new 应包含 mediaUrl=%s, 实际 %s", want, payload)
	}
	if strings.Contains(payload, a.ID.String()) {
		t.Error("对外载荷不应包含发送者 identity_id")
	}

	// 观察者：真正插入后通知一次。
	if n, _ := obs.snapshot(); n != 1 {
		t.Fatalf("OnMessage 次数 = %d, 期望 1", n)
	}

	// 幂等命中：不再广播、不再通知，但仍返回原消息。
	clientMsgID := newUUID(t)
	if _, _, err := svc.SendAs(ctx, conversation.SendInput{
		Identity: &a, ConversationID: conv.ID, ClientMsgID: clientMsgID,
		Kind: "text", Body: "hi", SkipRateLimit: true,
	}); err != nil {
		t.Fatalf("SendAs 失败: %v", err)
	}
	_, inserted2, err := svc.SendAs(ctx, conversation.SendInput{
		Identity: &a, ConversationID: conv.ID, ClientMsgID: clientMsgID,
		Kind: "text", Body: "hi", SkipRateLimit: true,
	})
	if err != nil {
		t.Fatalf("SendAs 失败: %v", err)
	}
	if inserted2 {
		t.Error("重复 clientMsgId 应为幂等命中")
	}
	if n, _ := obs.snapshot(); n != 2 {
		t.Fatalf("幂等命中不应通知观察者, OnMessage 次数 = %d, 期望 2", n)
	}
	if len(broker.broadcasts) != 2 {
		t.Errorf("幂等命中不应重复广播, 广播数 = %d", len(broker.broadcasts))
	}

	// 历史消息带 mediaUrl。
	var history []struct {
		ID       int64  `json:"id"`
		Kind     string `json:"kind"`
		MediaURL string `json:"mediaUrl"`
	}
	historyJSON := listMessagesRaw(t, svc, conv.ID, &a)
	if err := json.Unmarshal(historyJSON, &history); err != nil {
		t.Fatalf("解析历史失败: %v", err)
	}
	found := false
	for _, m := range history {
		if m.Kind == "image" {
			found = true
			if m.MediaURL != "/api/media/"+mediaID.String() {
				t.Errorf("历史 mediaUrl = %q", m.MediaURL)
			}
		} else if m.MediaURL != "" {
			t.Errorf("文本消息不应带 mediaUrl: %q", m.MediaURL)
		}
	}
	if !found {
		t.Fatal("历史里应有图片消息")
	}

	// 会话结束：只通知一次，重复结束不再通知。
	if err := svc.End(ctx, conv.ID, "peer_left"); err != nil {
		t.Fatalf("结束会话失败: %v", err)
	}
	if err := svc.End(ctx, conv.ID, "peer_left"); err != nil {
		t.Fatalf("重复结束会话不应报错: %v", err)
	}
	if _, ended := obs.snapshot(); ended != 1 {
		t.Errorf("OnConversationEnded 次数 = %d, 期望 1（重复结束不通知）", ended)
	}
}

func TestSendAsValidation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	svc := newService(pool, &fakeBroker{})
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)

	cases := []struct {
		name    string
		in      conversation.SendInput
		wantMsg string
	}{
		{
			name:    "图片缺少 mediaId",
			in:      conversation.SendInput{Kind: "image"},
			wantMsg: "媒体消息缺少 mediaId",
		},
		{
			name:    "文本正文为空",
			in:      conversation.SendInput{Kind: "text", Body: "   "},
			wantMsg: "消息内容为空或超过上限",
		},
		{
			name:    "不支持的消息类型",
			in:      conversation.SendInput{Kind: "system", Body: "x"},
			wantMsg: "不支持的消息类型",
		},
	}
	for _, c := range cases {
		in := c.in
		in.Identity = &a
		in.ConversationID = conv.ID
		in.ClientMsgID = newUUID(t)
		_, _, err := svc.SendAs(ctx, in)
		var se *conversation.SendError
		if err == nil || !asSendError(err, &se) {
			t.Fatalf("%s: 期望 *SendError, 实际 %v", c.name, err)
		}
		if se.Code != "invalid_request" || se.Msg != c.wantMsg {
			t.Errorf("%s: 错误 = %s/%s, 期望 invalid_request/%s", c.name, se.Code, se.Msg, c.wantMsg)
		}
	}
}
