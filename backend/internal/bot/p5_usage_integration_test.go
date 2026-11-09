//go:build integration

package bot

import (
	"context"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// p5TimeoutLLM 模拟调用结束时会话上下文已超时，但上游仍返回了消耗量。
type p5TimeoutLLM struct{}

func (p5TimeoutLLM) Chat(ctx context.Context, _ ChatRequest) (ChatResult, error) {
	<-ctx.Done()
	return ChatResult{PromptTokens: 12, CompletionTokens: 3}, ctx.Err()
}

func TestP5NewInputMarksPendingBeforeLookup(t *testing.T) {
	f := newP5Fixture(t, "single")
	f.e.convs[f.convID].running = true
	tx, err := f.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = tx.Rollback(context.Background())
		f.e.wg.Wait()
	}()
	// 暂时挡住异步 bot 查询，验证收消息时立即标记打断，不依赖查库完成。
	if _, err := tx.Exec(context.Background(), "LOCK TABLE bots IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	f.e.OnMessage(conversation.MessageEvent{
		ConversationID: f.convID, ConversationKind: store.ConversationKindMatch,
		SenderIdentityID: f.peerID,
	})
	if !f.e.hasPendingInput(f.convID) {
		t.Fatal("已收到的新输入必须在异步查询前标记打断")
	}
}

func TestP5TimedOutCallsStillCountUsage(t *testing.T) {
	for _, mode := range []string{"single", "dual"} {
		t.Run(mode, func(t *testing.T) {
			f := newP5Fixture(t, mode)
			f.e.turnTimeout = 50 * time.Millisecond
			f.e.llmOverride = p5TimeoutLLM{}
			f.e.runTurn(f.convID, false)
			var tokens int
			f.row(t, `SELECT COALESCE(sum(prompt_tokens+completion_tokens),0) FROM bot_usage WHERE bot_id=$1`, f.botID).Scan(&tokens)
			if tokens != 15 {
				t.Fatalf("超时调用返回的token仍必须计量，实际%d", tokens)
			}
		})
	}
}

func TestP5RepeatedExpressionsUseMediaLimit(t *testing.T) {
	f := newP5Fixture(t, "single")
	f.exec(t, `UPDATE bots SET conv_media_limit=2 WHERE id=$1`, f.botID)
	f.model.reply = func(int, ChatRequest) (ChatResult, error) {
		return ChatResult{Content: "你好\n[表情:开心]"}, nil
	}
	for i := 0; i < 3; i++ {
		f.e.runTurn(f.convID, false)
	}
	var count int
	f.row(t, `SELECT count(*) FROM messages WHERE conversation_id=$1 AND kind='image'`, f.convID).Scan(&count)
	if count != 2 {
		t.Fatalf("同一表情可重复发送，但必须按实际条数执行限额，实际%d", count)
	}
}
