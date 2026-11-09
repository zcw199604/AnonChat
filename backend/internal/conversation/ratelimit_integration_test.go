//go:build integration

package conversation_test

import (
	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"testing"
)

func TestIdempotentRetryDoesNotConsumeQuota(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	settings := moderation.NewLimitSettings(pool, 1, nil)
	svc := conversation.New(pool, testLogger(), nil, conversation.WithLimitSettings(settings))
	a, _ := createIdentity(t, pool, "alice")
	b, _ := createIdentity(t, pool, "bob")
	conv := createConv(t, pool, svc, store.ConversationKindMatch, false, a.ID, b.ID)
	input := conversation.SendInput{Identity: &a, ConversationID: conv.ID, ClientMsgID: newUUID(t), Kind: "text", Body: "hi"}
	first, inserted, err := svc.SendAs(ctx, input)
	if err != nil || !inserted {
		t.Fatalf("首次发送: inserted=%v err=%v", inserted, err)
	}
	retry, inserted, err := svc.SendAs(ctx, input)
	if err != nil || inserted || retry.ID != first.ID {
		t.Fatalf("幂等重发: inserted=%v err=%v", inserted, err)
	}
	input.ClientMsgID = newUUID(t)
	if _, _, err = svc.SendAs(ctx, input); err == nil {
		t.Fatal("新消息应受秒限流")
	}
}
