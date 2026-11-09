//go:build integration

package match_test

import (
	"encoding/json"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"testing"
	"time"
)

func TestNextLimitedKeepsCurrentConversation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{MatchLimiter: moderation.NewFixedWindow(1, time.Hour)})
	_, a := mkIdentity(t, pool, "alice")
	_, b := mkIdentity(t, pool, "bob")
	ws, _, conv := pairTwo(t, env.Server, a, b)
	send(t, ws, map[string]any{"t": realtime.TypeMatchNext, "id": "limited-next", "d": map[string]any{}})
	e := read(t, ws, 3*time.Second)
	if e.Type != realtime.TypeError || e.ID != "limited-next" {
		t.Fatalf("应返回请求对应的限流错误: %+v", e)
	}
	var payload realtime.ErrorPayload
	if err := json.Unmarshal(e.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Code != realtime.CodeRateLimited {
		t.Fatalf("期望rate_limited，实际: %+v", payload)
	}
	var ended bool
	if err := pool.QueryRow(ctx, "SELECT ended_at IS NOT NULL FROM conversations WHERE id=$1", conv).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	if ended {
		t.Fatal("超限不能结束当前会话")
	}
	if env.Deps.Match.QueueLen() != 0 {
		t.Fatal("超限不能重新入队")
	}
}
