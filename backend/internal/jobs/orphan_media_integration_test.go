//go:build integration

package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/jobs"
	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertOrphanMedia(t *testing.T, pool *pgxpool.Pool, root, source string, at time.Time) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	var folder pgtype.UUID
	if source == "library" {
		if err := pool.QueryRow(ctx, "INSERT INTO media_folders(name) VALUES(gen_random_uuid()::text) RETURNING id").Scan(&folder); err != nil {
			t.Fatal(err)
		}
	}
	key := fmt.Sprintf("%s/%d.png", source, time.Now().UnixNano())
	if err := pool.QueryRow(ctx, `INSERT INTO media_items(id,folder_id,kind,mime,size_bytes,storage_key,original_name,source,created_at)
    VALUES(gen_random_uuid(),$1,'image','image/png',4,$2,'x.png',$3,$4) RETURNING id`, folder, key, source, ts(at)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if root != "" {
		path := filepath.Join(root, key)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return id
}
func orphanCleaner(pool *pgxpool.Pool, now time.Time, storage interface{ Remove(string) error }) *jobs.Cleaner {
	return jobs.New(pool, testLogger(), jobs.Options{MessageRetention: 7 * 24 * time.Hour, AuditRetention: 180 * 24 * time.Hour, BottleTTL: 7 * 24 * time.Hour}, jobs.WithClock(func() time.Time { return now }), jobs.WithMediaStorage(storage))
}

func TestOrphanMediaDeletesOnlyOldUnreferencedFiles(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	root := t.TempDir()
	storage, err := media.NewStorage(root)
	if err != nil {
		t.Fatal(err)
	}
	old := insertOrphanMedia(t, pool, root, "user", now.Add(-25*time.Hour))
	burned := insertOrphanMedia(t, pool, root, "external", now.Add(-25*time.Hour))
	recent := insertOrphanMedia(t, pool, root, "user", now.Add(-23*time.Hour))
	library := insertOrphanMedia(t, pool, root, "library", now.Add(-25*time.Hour))
	referenced := insertOrphanMedia(t, pool, root, "external", now.Add(-25*time.Hour))
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	for _, id := range []pgtype.UUID{burned, referenced} {
		if _, err := pool.Exec(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id,burned_at) VALUES($1,$2,1,gen_random_uuid(),'image','',$3,CASE WHEN $4 THEN now() ELSE NULL END)`, conv, ident, id, id == burned); err != nil {
			t.Fatal(err)
		}
	}
	stats, err := orphanCleaner(pool, now, storage).RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["orphan_media"] != 2 {
		t.Fatalf("orphan_media=%d want 2", stats["orphan_media"])
	}
	for _, id := range []pgtype.UUID{old, burned} {
		if n := count(t, pool, "SELECT count(*) FROM media_items WHERE id=$1", id); n != 0 {
			t.Errorf("孤儿行未清理: %s", id)
		}
	}
	for _, id := range []pgtype.UUID{recent, library, referenced} {
		if n := count(t, pool, "SELECT count(*) FROM media_items WHERE id=$1", id); n != 1 {
			t.Errorf("有效媒体误删除: %s", id)
		}
	}
	files := 0
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files++
		}
		return err
	})
	if files != 3 {
		t.Fatalf("剩余文件=%d want 3", files)
	}
}

func TestOrphanMediaPassesMoreThan500ReferencedRows(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	_, err := pool.Exec(ctx, `WITH media AS (
      INSERT INTO media_items(id,kind,mime,size_bytes,storage_key,original_name,source,created_at)
      SELECT gen_random_uuid(),'image','image/png',1,'ref/'||g,'x','user',$1 FROM generate_series(1,501) g RETURNING id)
      INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id)
      SELECT $2,$3,1,gen_random_uuid(),'image','',id FROM media`, ts(now.Add(-48*time.Hour)), conv, ident)
	if err != nil {
		t.Fatal(err)
	}
	orphan := insertOrphanMedia(t, pool, "", "user", now.Add(-25*time.Hour))
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	stats, err := orphanCleaner(pool, now, nil).RunOnce(deadline)
	if err != nil {
		t.Fatal(err)
	}
	if stats["orphan_media"] != 1 || count(t, pool, "SELECT count(*) FROM media_items WHERE id=$1", orphan) != 0 {
		t.Fatalf("超过500条引用后未找到孤儿: %v", stats)
	}
	if count(t, pool, "SELECT count(*) FROM media_items") != 501 {
		t.Fatal("引用媒体误删")
	}
}

type checkingStorage struct {
	t     *testing.T
	pool  *pgxpool.Pool
	calls int
}

func (s *checkingStorage) Remove(key string) error {
	s.calls++
	if count(s.t, s.pool, "SELECT count(*) FROM media_items WHERE storage_key=$1", key) != 0 {
		s.t.Error("数据库提交前删除文件")
	}
	return fmt.Errorf("fake remove failure")
}
func TestOrphanMediaCommitsBeforeRemovingFiles(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	insertOrphanMedia(t, pool, "", "external", now.Add(-25*time.Hour))
	storage := &checkingStorage{t: t, pool: pool}
	stats, err := orphanCleaner(pool, now, storage).RunOnce(ctx)
	if err != nil || stats["orphan_media"] != 1 || storage.calls != 1 {
		t.Fatalf("删除失败应只告警: stats=%v calls=%d err=%v", stats, storage.calls, err)
	}
}

func TestOrphanMediaStopsAfter20BatchesAndUsesCursor(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	_, err := pool.Exec(ctx, `INSERT INTO media_items(id,kind,mime,size_bytes,storage_key,original_name,source,created_at)
    SELECT gen_random_uuid(),'image','image/png',1,'batch/'||g,'x','user',$1 FROM generate_series(1,10001) g`, ts(now.Add(-25*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}
	stats, err := orphanCleaner(pool, now, nil).RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats["orphan_media"] != 10000 || count(t, pool, "SELECT count(*) FROM media_items") != 1 {
		t.Fatalf("批次上限不符: %v", stats)
	}
	stats, err = orphanCleaner(pool, now, nil).RunOnce(ctx)
	if err != nil || stats["orphan_media"] != 1 {
		t.Fatalf("下一轮未继续清理: %v %v", stats, err)
	}
}

func TestOrphanMediaSkipsMediaLockedBySending(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	id := insertOrphanMedia(t, pool, "", "user", now.Add(-25*time.Hour))
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id) VALUES($1,$2,1,gen_random_uuid(),'image','',$3)`, conv, ident, id); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	stats, err := orphanCleaner(pool, now, nil).RunOnce(deadline)
	if err != nil {
		t.Fatal(err)
	}
	if stats["orphan_media"] != 0 {
		t.Fatalf("清理未跳过发送持有的锁: %v", stats)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "SELECT count(*) FROM messages WHERE media_id=$1", id) != 1 {
		t.Fatal("消息媒体引用丢失")
	}
}

// 锁内第二条语句必须重新检查引用，不能依赖候选列表直接删除。
func TestOrphanMediaSecondStatementRechecksReference(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	id := insertOrphanMedia(t, pool, "", "external", now.Add(-25*time.Hour))
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	// 建立第一个语句快照，再在同一事务内写入引用验证DELETE重新检查。
	q := store.New(tx)
	rows, err := q.ListOrphanNonLibraryMedia(ctx, store.ListOrphanNonLibraryMediaParams{Cutoff: ts(now.Add(-24 * time.Hour)), AfterCreatedAt: pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true}, AfterID: pgtype.UUID{Valid: true}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("候选=%v err=%v", rows, err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id) VALUES($1,$2,1,gen_random_uuid(),'image','',$3)`, conv, ident, id); err != nil {
		t.Fatal(err)
	}
	deleted, err := q.DeleteOrphanNonLibraryMedia(ctx, []pgtype.UUID{id})
	if err != nil || len(deleted) != 0 {
		t.Fatalf("第二语句未重查引用: %v %v", deleted, err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "SELECT count(*) FROM messages WHERE media_id=$1", id) != 1 {
		t.Fatal("消息引用被置空")
	}
}

func TestOrphanMediaCleanupLockMakesConcurrentSendFailWithoutNullReference(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	id := insertOrphanMedia(t, pool, "", "user", now.Add(-25*time.Hour))
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	cleanTx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanTx.Rollback(ctx)
	q := store.New(cleanTx)
	candidates, err := q.ListOrphanNonLibraryMedia(ctx, store.ListOrphanNonLibraryMediaParams{Cutoff: ts(now.Add(-24 * time.Hour)), AfterCreatedAt: pgtype.Timestamptz{InfinityModifier: pgtype.NegativeInfinity, Valid: true}, AfterID: pgtype.UUID{Valid: true}})
	if err != nil || len(candidates) != 1 {
		t.Fatalf("候选=%v err=%v", candidates, err)
	}
	sending, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer sending.Release()
	pid := sending.Conn().PgConn().PID()
	sendResult := make(chan error, 1)
	go func() {
		_, err := sending.Exec(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id) VALUES($1,$2,1,gen_random_uuid(),'image','',$3)`, conv, ident, id)
		sendResult <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, "SELECT COALESCE(wait_event_type='Lock',false) FROM pg_stat_activity WHERE pid=$1", pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("并发发送没有等待媒体锁")
		}
		time.Sleep(time.Millisecond)
	}
	keys, err := q.DeleteOrphanNonLibraryMedia(ctx, []pgtype.UUID{id})
	if err != nil || len(keys) != 1 {
		t.Fatalf("删除=%v err=%v", keys, err)
	}
	if err := cleanTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-sendResult:
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" || pgErr.ConstraintName != "messages_media_id_fkey" {
			t.Fatalf("应是媒体外键错误: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("发送未结束")
	}
	if count(t, pool, "SELECT count(*) FROM messages WHERE conversation_id=$1", conv) != 0 {
		t.Fatal("清理竞争产生空媒体引用的消息")
	}
}

func TestOrphanMediaReleasedByRetentionIsDeletedNextRun(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	now := time.Now()
	id := insertOrphanMedia(t, pool, "", "external", now.Add(-9*24*time.Hour))
	ident := insertIdentity(t, pool, time.Time{})
	conv := insertConversation(t, pool, time.Time{})
	if _, err := pool.Exec(ctx, `INSERT INTO messages(conversation_id,sender_identity_id,sender_member_id,client_msg_id,kind,body,media_id,created_at) VALUES($1,$2,1,gen_random_uuid(),'image','',$3,$4)`, conv, ident, id, ts(now.Add(-8*24*time.Hour))); err != nil {
		t.Fatal(err)
	}
	cleaner := orphanCleaner(pool, now, nil)
	first, err := cleaner.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first["orphan_media"] != 0 || first["messages"] != 1 || count(t, pool, "SELECT count(*) FROM media_items WHERE id=$1", id) != 1 {
		t.Fatalf("首轮先保护引用再清消息: %v", first)
	}
	second, err := cleaner.RunOnce(ctx)
	if err != nil || second["orphan_media"] != 1 {
		t.Fatalf("次轮应清理释放媒体: %v %v", second, err)
	}
}
