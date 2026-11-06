//go:build integration

package vip_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/anonchat/anonchat/backend/internal/vip"
)

var ctx = context.Background()

// createBatch 直接在库里创建一个批次并返回明文激活码。
func createBatch(t *testing.T, pool *pgxpool.Pool, vipDays, cards, count int32, expires time.Time) (pgtype.UUID, []string) {
	t.Helper()
	adminID := ensureAdmin(t, pool)
	svc := vip.New(pool, testLogger())
	batch, codes, err := svc.CreateBatch(ctx, vip.CreateBatchInput{
		Label:          "测试批次",
		VipDays:        vipDays,
		BroadcastCards: cards,
		Count:          count,
		ExpiresAt:      expires,
		CreatedBy:      adminID,
	})
	if err != nil {
		t.Fatalf("创建批次失败: %v", err)
	}
	return batch.ID, codes
}

func ensureAdmin(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	row := pool.QueryRow(ctx, `INSERT INTO admin_users (username, password_hash)
		VALUES ($1, 'x') ON CONFLICT (username) DO UPDATE SET username = EXCLUDED.username
		RETURNING id`, "vip-test-admin")
	if err := row.Scan(&id); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	return id
}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

func TestRedeemSuccessAndRepeat(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})

	token := createIdentity(t, pool)
	_, codes := createBatch(t, pool, 30, 3, 1, time.Now().Add(time.Hour))

	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": codes[0]}, token, "")
	if resp.Status != http.StatusOK {
		t.Fatalf("兑换状态 = %d, body=%s", resp.Status, resp.Body)
	}
	var me struct {
		Vip struct {
			Active bool    `json:"active"`
			Until  *string `json:"until"`
		} `json:"vip"`
		BroadcastCards int32 `json:"broadcastCards"`
	}
	resp.JSON(t, &me)
	if !me.Vip.Active || me.Vip.Until == nil {
		t.Fatalf("兑换后应为 VIP: %+v", me)
	}
	if me.BroadcastCards != 3 {
		t.Errorf("广播卡 = %d, 期望 3", me.BroadcastCards)
	}

	// 重复兑换同一个码：统一返回 invalid_code。
	again := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": codes[0]}, token, "")
	if again.Status != http.StatusBadRequest {
		t.Fatalf("重复兑换状态 = %d, 期望 400", again.Status)
	}
	assertCode(t, again.Body, "invalid_code")
}

func TestRedeemConcurrentOnlyOnce(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})

	_, codes := createBatch(t, pool, 30, 1, 1, time.Now().Add(time.Hour))
	code := codes[0]

	// 10 个身份同时兑换同一个码，只能成功一次。
	const n = 10
	tokens := make([]string, n)
	for i := 0; i < n; i++ {
		tokens[i] = createIdentityNamed(t, pool, i)
	}
	var success int32
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(tok string) {
			defer wg.Done()
			resp := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": code}, tok, "")
			if resp.Status == http.StatusOK {
				atomic.AddInt32(&success, 1)
			}
		}(tokens[i])
	}
	wg.Wait()
	if success != 1 {
		t.Fatalf("并发兑换成功次数 = %d, 期望 1", success)
	}
}

func TestRedeemExpiredAndRevoked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	token := createIdentity(t, pool)

	// 过期批次：直接用过去的时间点创建（服务层不允许，故直接写库）。
	adminID := ensureAdmin(t, pool)
	var batchID pgtype.UUID
	row := pool.QueryRow(ctx, `INSERT INTO activation_batches (label, vip_days, broadcast_cards, code_count, expires_at, created_by)
		VALUES ('过期', 30, 1, 1, now() - interval '1 hour', $1) RETURNING id`, adminID)
	if err := row.Scan(&batchID); err != nil {
		t.Fatalf("插入批次失败: %v", err)
	}
	code := "AAAA-BBBB-CCCC-DDDD"
	if _, err := pool.Exec(ctx, `INSERT INTO activation_codes (code_hash, batch_id) VALUES ($1, $2)`,
		vip.HashCode(vip.NormalizeCode(code)), batchID); err != nil {
		t.Fatalf("插入激活码失败: %v", err)
	}
	resp := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": code}, token, "")
	if resp.Status != http.StatusBadRequest {
		t.Fatalf("过期码状态 = %d, 期望 400", resp.Status)
	}
	assertCode(t, resp.Body, "invalid_code")

	// 撤销后的码同样 invalid_code。
	_, codes := createBatch(t, pool, 30, 1, 1, time.Now().Add(time.Hour))
	svc := vip.New(pool, testLogger())
	list, err := svc.ListBatches(ctx, 10)
	if err != nil || len(list) == 0 {
		t.Fatalf("列出批次失败: %v", err)
	}
	if _, err := svc.RevokeBatch(ctx, list[0].ID); err != nil {
		t.Fatalf("撤销批次失败: %v", err)
	}
	revoked := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": codes[0]}, token, "")
	if revoked.Status != http.StatusBadRequest {
		t.Fatalf("撤销码状态 = %d, 期望 400", revoked.Status)
	}
	assertCode(t, revoked.Body, "invalid_code")
}

func TestRedeemRateLimited(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	// 每身份只允许 1 次/10 分钟。
	env := testutil.MustEnv(t, pool, testutil.Config{
		VIPPerIDLimiter: moderation.NewFixedWindow(1, 10*time.Minute),
	})
	token := createIdentity(t, pool)

	first := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": "AAAA-BBBB-CCCC-DDDD"}, token, "")
	if first.Status != http.StatusBadRequest {
		t.Fatalf("首次状态 = %d, 期望 400（码无效）", first.Status)
	}
	second := testutil.Do(t, env.Server, http.MethodPost, "/api/vip/redeem", map[string]string{"code": "AAAA-BBBB-CCCC-DDDD"}, token, "")
	if second.Status != http.StatusTooManyRequests {
		t.Fatalf("第二次状态 = %d, 期望 429", second.Status)
	}
	assertCode(t, second.Body, "rate_limited")
}

// ---- 测试工具 ----

func createIdentity(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	return createIdentityNamed(t, pool, 0)
}

func createIdentityNamed(t *testing.T, pool *pgxpool.Pool, i int) string {
	t.Helper()
	token := "vip-tok-" + time.Now().Format("150405.000000000") + "-" + string(rune('a'+i%26))
	if _, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: "n", AvatarSeed: "s",
	}); err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return token
}

func assertCode(t *testing.T, body []byte, want string) {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := jsonUnmarshal(body, &e); err != nil {
		t.Fatalf("解析错误响应失败: %v, body=%s", err, body)
	}
	if e.Error.Code != want {
		t.Errorf("错误码 = %q, 期望 %q", e.Error.Code, want)
	}
}
