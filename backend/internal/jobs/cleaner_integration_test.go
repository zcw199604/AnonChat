//go:build integration

package jobs_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/jobs"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newCleaner(pool *pgxpool.Pool) *jobs.Cleaner {
	return jobs.New(pool, testLogger(), jobs.Options{
		MessageRetention: 168 * time.Hour,
		AuditRetention:   4320 * time.Hour,
		BottleTTL:        168 * time.Hour,
		Interval:         10 * time.Minute,
		BatchSize:        5000,
	}, jobs.WithClock(time.Now))
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

// insertIdentity 插入一个身份，revokedAt 非零时设置 revoked_at。
func insertIdentity(t *testing.T, pool *pgxpool.Pool, revokedAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	// public_code 由数据库默认逻辑生成（见 queries/identity.sql CreateIdentity 的兜底）。
	q := `INSERT INTO identities (token_hash, nickname, avatar_seed, revoked_at, public_code)
	      VALUES (uuid_send(gen_random_uuid()), 'n', 's', $1,
	              upper(substr(replace(gen_random_uuid()::text, '-', ''), 1, 10)))
	      RETURNING id`
	var rev *time.Time
	if !revokedAt.IsZero() {
		rev = &revokedAt
	}
	if err := pool.QueryRow(ctx, q, rev).Scan(&id); err != nil {
		t.Fatalf("插入身份失败: %v", err)
	}
	return id
}

// insertConversation 插入会话，endedAt 非零时设置 ended_at。
func insertConversation(t *testing.T, pool *pgxpool.Pool, endedAt time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	var ended *time.Time
	if !endedAt.IsZero() {
		ended = &endedAt
	}
	q := `INSERT INTO conversations (kind, ended_at, end_reason) VALUES ('match', $1, 'peer_left') RETURNING id`
	if err := pool.QueryRow(ctx, q, ended).Scan(&id); err != nil {
		t.Fatalf("插入会话失败: %v", err)
	}
	return id
}

func insertMessage(t *testing.T, pool *pgxpool.Pool, convID, identID pgtype.UUID, createdAt time.Time) {
	t.Helper()
	q := `INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, created_at)
	      VALUES ($1, $2, 1, gen_random_uuid(), 'text', 'hi', $3)`
	if _, err := pool.Exec(ctx, q, convID, identID, ts(createdAt)); err != nil {
		t.Fatalf("插入消息失败: %v", err)
	}
}

func count(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	return n
}

func TestCleanMessagesOldDeletedRecentKept(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	insertMessage(t, pool, conv, ident, now.Add(-8*24*time.Hour)) // 过期
	insertMessage(t, pool, conv, ident, now.Add(-1*time.Hour))    // 保留

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	got := count(t, pool, "SELECT count(*) FROM messages")
	if got != 1 {
		t.Fatalf("剩余消息数 = %d, 期望 1（仅保留近期）", got)
	}
}

func TestCleanEmptyEndedConversations(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	ident := insertIdentity(t, pool, time.Time{})
	// 已结束、早于保留期、无消息 → 应删除。
	emptyOld := insertConversation(t, pool, now.Add(-8*24*time.Hour))
	// 已结束但有消息 → 保留。
	withMsg := insertConversation(t, pool, now.Add(-8*24*time.Hour))
	insertMessage(t, pool, withMsg, ident, now.Add(-1*time.Hour))
	// 已结束但太新 → 保留。
	recentEnded := insertConversation(t, pool, now.Add(-1*time.Hour))

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1", emptyOld) != 0 {
		t.Error("已结束且空、超过保留期的会话应被删除")
	}
	if count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1", withMsg) != 1 {
		t.Error("仍有消息的会话不应删除")
	}
	if count(t, pool, "SELECT count(*) FROM conversations WHERE id=$1", recentEnded) != 1 {
		t.Error("未超过保留期的会话不应删除")
	}
}

func TestCleanExpiredBottlesAndPrivateLinks(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	ident := insertIdentity(t, pool, time.Time{})

	// 漂流瓶：过期 vs 未过期。
	if _, err := pool.Exec(ctx, `INSERT INTO bottles (identity_id, content, expires_at) VALUES ($1,'x',$2)`, ident, ts(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO bottles (identity_id, content, expires_at) VALUES ($1,'y',$2)`, ident, ts(now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}

	// 私密链接：过期 vs 未过期。
	convOld := insertConversation(t, pool, time.Time{})
	convNew := insertConversation(t, pool, time.Time{})
	if _, err := pool.Exec(ctx, `INSERT INTO private_links (token_hash, conversation_id, creator_identity_id, expires_at) VALUES (uuid_send(gen_random_uuid()),$1,$2,$3)`, convOld, ident, ts(now.Add(-time.Hour))); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO private_links (token_hash, conversation_id, creator_identity_id, expires_at) VALUES (uuid_send(gen_random_uuid()),$1,$2,$3)`, convNew, ident, ts(now.Add(24*time.Hour))); err != nil {
		t.Fatal(err)
	}

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if got := count(t, pool, "SELECT count(*) FROM bottles"); got != 1 {
		t.Errorf("剩余漂流瓶 = %d, 期望 1", got)
	}
	if got := count(t, pool, "SELECT count(*) FROM private_links"); got != 1 {
		t.Errorf("剩余私密链接 = %d, 期望 1", got)
	}
}

func TestCleanAccessLogsAndSmsCodes(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	// access_logs：200 天前应删，10 天前保留。
	pool.Exec(ctx, `INSERT INTO access_logs (event, created_at) VALUES ('x',$1)`, ts(now.Add(-200*24*time.Hour)))
	pool.Exec(ctx, `INSERT INTO access_logs (event, created_at) VALUES ('y',$1)`, ts(now.Add(-10*24*time.Hour)))
	// sms_codes：2 天前应删，1 小时前保留。
	pool.Exec(ctx, `INSERT INTO sms_codes (phone_hmac, code_hash, expires_at, created_at) VALUES (uuid_send(gen_random_uuid()),uuid_send(gen_random_uuid()),$1,$2)`, ts(now), ts(now.Add(-48*time.Hour)))
	pool.Exec(ctx, `INSERT INTO sms_codes (phone_hmac, code_hash, expires_at, created_at) VALUES (uuid_send(gen_random_uuid()),uuid_send(gen_random_uuid()),$1,$2)`, ts(now), ts(now.Add(-time.Hour)))

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if got := count(t, pool, "SELECT count(*) FROM access_logs"); got != 1 {
		t.Errorf("剩余 access_logs = %d, 期望 1", got)
	}
	if got := count(t, pool, "SELECT count(*) FROM sms_codes"); got != 1 {
		t.Errorf("剩余 sms_codes = %d, 期望 1", got)
	}
}

func TestCleanRevokedIdentities(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	// 已撤销且无引用、早于保留期 → 删除。
	orphan := insertIdentity(t, pool, now.Add(-200*24*time.Hour))
	// 已撤销但被消息引用 → 保留。
	referenced := insertIdentity(t, pool, now.Add(-200*24*time.Hour))
	conv := insertConversation(t, pool, time.Time{})
	insertMessage(t, pool, conv, referenced, now.Add(-time.Hour))
	// 未撤销 → 保留。
	active := insertIdentity(t, pool, time.Time{})

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if count(t, pool, "SELECT count(*) FROM identities WHERE id=$1", orphan) != 0 {
		t.Error("无引用的已撤销身份应被删除")
	}
	if count(t, pool, "SELECT count(*) FROM identities WHERE id=$1", referenced) != 1 {
		t.Error("被引用的身份不应删除")
	}
	if count(t, pool, "SELECT count(*) FROM identities WHERE id=$1", active) != 1 {
		t.Error("未撤销身份不应删除")
	}
}

func TestReportSnapshotSurvivesMessageCleanup(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	insertMessage(t, pool, conv, ident, now.Add(-8*24*time.Hour)) // 过期消息

	// 举报快照独立保存，会话 id 不加外键。
	if _, err := pool.Exec(ctx,
		`INSERT INTO reports (reporter_identity_id, reported_identity_id, conversation_id, reason, snapshot)
		 VALUES ($1,$2,$3,'spam','{"messages":[]}'::jsonb)`, ident, ident, conv); err != nil {
		t.Fatal(err)
	}

	if _, err := newCleaner(pool).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if got := count(t, pool, "SELECT count(*) FROM reports"); got != 1 {
		t.Errorf("举报快照不应因消息清理而丢失, 实际 %d", got)
	}
}

func TestCleanBatchesOver5000(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()

	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	q := `INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, created_at)
	      SELECT $1, $2, 1, gen_random_uuid(), 'text', 'x', $3 FROM generate_series(1, 5001)`
	if _, err := pool.Exec(ctx, q, conv, ident, ts(now.Add(-8*24*time.Hour))); err != nil {
		t.Fatalf("批量插入失败: %v", err)
	}

	stats, err := newCleaner(pool).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce 失败: %v", err)
	}
	if got := count(t, pool, "SELECT count(*) FROM messages"); got != 0 {
		t.Fatalf("剩余消息数 = %d, 期望 0", got)
	}
	if stats["messages"] != 5001 {
		t.Errorf("messages 删除行数 = %d, 期望 5001（分批循环）", stats["messages"])
	}
}

func TestCleanBotMemoriesAndCursors(t *testing.T) {
	for _, retention := range []int{0, 7} {
		t.Run(fmt.Sprintf("保留期%d天", retention), func(t *testing.T) {
			pool := testutil.MustPool(t)
			testutil.Truncate(t, pool)
			now := time.Now().UTC().Truncate(time.Second)
			if retention > 0 {
				if _, err := pool.Exec(ctx, `INSERT INTO bot_settings (id, memory_retention_days) VALUES (1, $1)`, retention); err != nil {
					t.Fatal(err)
				}
			} else {
				retention = 30
			}
			bot := insertMemoryBot(t, pool)
			expired := insertIdentity(t, pool, time.Time{})
			recent := insertIdentity(t, pool, time.Time{})
			revoked := insertIdentity(t, pool, now.Add(-time.Hour))
			for _, item := range []struct {
				id pgtype.UUID
				at time.Time
			}{{expired, now.Add(-time.Duration(retention)*24*time.Hour - time.Second)}, {recent, now.Add(-time.Duration(retention) * 24 * time.Hour)}, {revoked, now}} {
				if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_memories (bot_id, peer_identity_id, last_interaction_at) VALUES ($1,$2,$3)`, bot, item.id, ts(item.at)); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_events (bot_id, peer_identity_id, gist) VALUES ($1,$2,'保留测试')`, bot, item.id); err != nil {
					t.Fatal(err)
				}
			}
			old := insertConversation(t, pool, now.Add(-8*24*time.Hour-time.Second))
			fresh := insertConversation(t, pool, now.Add(-8*24*time.Hour))
			open := insertConversation(t, pool, time.Time{})
			for _, conv := range []pgtype.UUID{old, fresh, open} {
				// 留一条近期消息，避免旧会话清理的级联删除掩盖游标清理。
				insertMessage(t, pool, conv, recent, now)
				if _, err := pool.Exec(ctx, `INSERT INTO bot_memory_cursors (conversation_id, bot_id, extracted_until) VALUES ($1,$2,$3)`, conv, bot, ts(now)); err != nil {
					t.Fatal(err)
				}
			}
			cleaner := jobs.New(pool, testLogger(), jobs.Options{MessageRetention: 168 * time.Hour, AuditRetention: 4320 * time.Hour, BottleTTL: 168 * time.Hour}, jobs.WithClock(func() time.Time { return now }))
			stats, err := cleaner.RunOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"bot_memories_expired", "bot_memories_revoked", "bot_memory_cursors"} {
				if stats[key] != 1 {
					t.Errorf("%s 删除数 = %d，期望 1", key, stats[key])
				}
			}
			if got := count(t, pool, `SELECT count(*) FROM bot_peer_memories WHERE peer_identity_id=$1`, recent); got != 1 {
				t.Errorf("未过期画像应保留，实际 %d", got)
			}
			if got := count(t, pool, `SELECT count(*) FROM bot_peer_memories`); got != 1 {
				t.Errorf("画像剩余 %d，期望 1", got)
			}
			if got := count(t, pool, `SELECT count(*) FROM bot_peer_events`); got != 1 {
				t.Errorf("事件剩余 %d，期望级联后 1", got)
			}
			if got := count(t, pool, `SELECT count(*) FROM bot_memory_cursors WHERE conversation_id=$1`, old); got != 0 {
				t.Error("旧游标应被删除")
			}
			if got := count(t, pool, `SELECT count(*) FROM bot_memory_cursors`); got != 2 {
				t.Errorf("新游标与进行中游标应保留，实际 %d", got)
			}
		})
	}
}

func insertMemoryBot(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	identityID := insertIdentity(t, pool, time.Time{})
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO bots (identity_id, label) VALUES ($1, '清理测试') RETURNING id`, identityID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCleanBotMemoryFailureDoesNotBlockOtherTasks(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	bot := insertMemoryBot(t, pool)
	expired := insertIdentity(t, pool, time.Time{})
	revoked := insertIdentity(t, pool, now.Add(-time.Hour))
	if _, err := pool.Exec(ctx, `INSERT INTO bot_peer_memories (bot_id, peer_identity_id, last_interaction_at) VALUES ($1,$2,$3),($1,$4,$5)`, bot, expired, ts(now.Add(-31*24*time.Hour)), revoked, ts(now)); err != nil {
		t.Fatal(err)
	}
	old := insertConversation(t, pool, now.Add(-9*24*time.Hour))
	insertMessage(t, pool, old, revoked, now)
	if _, err := pool.Exec(ctx, `INSERT INTO bot_memory_cursors (conversation_id, bot_id, extracted_until) VALUES ($1,$2,$3)`, old, bot, ts(now)); err != nil {
		t.Fatal(err)
	}
	// 注入画像过期删除错误，确保身份清理与游标清理仍会执行。
	if _, err := pool.Exec(ctx, `CREATE FUNCTION fail_expired_memory_delete() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN IF OLD.last_interaction_at < now() - interval '30 days' THEN RAISE EXCEPTION '画像秘密内容'; END IF; RETURN OLD; END $$;
	CREATE TRIGGER fail_expired_memory_delete BEFORE DELETE ON bot_peer_memories FOR EACH ROW EXECUTE FUNCTION fail_expired_memory_delete()`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DROP TRIGGER fail_expired_memory_delete ON bot_peer_memories; DROP FUNCTION fail_expired_memory_delete()`); err != nil {
			t.Error(err)
		}
	})
	stats, err := newCleaner(pool).RunOnce(ctx)
	if err == nil {
		t.Error("单项失败应返回错误")
	} else if strings.Contains(err.Error(), "画像秘密内容") {
		t.Error("清理错误不得泄露记忆内容")
	}
	if stats["bot_memories_revoked"] != 1 || stats["bot_memory_cursors"] != 1 {
		t.Errorf("其他清理应成功，stats=%v", stats)
	}
	if count(t, pool, `SELECT count(*) FROM bot_peer_memories WHERE peer_identity_id=$1`, revoked) != 0 {
		t.Error("吊销身份画像应被删除")
	}
	if count(t, pool, `SELECT count(*) FROM bot_memory_cursors`) != 0 {
		t.Error("旧游标应被删除")
	}
}
