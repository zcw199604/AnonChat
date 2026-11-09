//go:build integration

package moderation_test

import (
	"sync"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestLimitSettingsUpdatePublishesCommittedSnapshot(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	s := moderation.NewLimitSettings(pool, 1, nil)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	before := s.Current()
	r := before.Rules[moderation.RuleMsgPerMinute]
	r.Limit = 2
	t.Cleanup(func() { r.Limit = 10; _ = s.Update(ctx, []moderation.Rule{r}, pgtype.UUID{}) })
	if err := s.Update(ctx, []moderation.Rule{r}, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	after := s.Current()
	if after.Version != before.Version+1 || after.Rules[r.Key].Limit != 2 || before.Rules[r.Key].Limit != 10 {
		t.Fatal("快照未原子发布或修改旧快照")
	}
	reload := moderation.NewLimitSettings(pool, 1, nil)
	if err := reload.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if reload.Current().Rules[r.Key].Limit != 2 {
		t.Fatal("变更未持久化")
	}
	lim := s.Limiter(r.Key)
	if !lim.Allow(ctx, "msg:a") || !lim.Allow(ctx, "msg:a") || lim.Allow(ctx, "msg:a") {
		t.Fatal("保存后应即时生效")
	}
}

func TestEnsureAutoBanConcurrentAndRevoked(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	s := moderation.NewBanService(pool, nil)
	subject := moderation.BanSubject{Type: moderation.SubjectIP, Value: "192.0.2.10"}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.EnsureAutoBan(ctx, subject, -1, "自动封禁：测试超限") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, pool, "SELECT count(*) FROM bans WHERE subject_value=$1", subject.Value); n != 1 {
		t.Fatalf("并发封禁应仅插入一行，得到%d", n)
	}
	var id pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM bans WHERE subject_value=$1", subject.Value).Scan(&id); err != nil {
		t.Fatal(err)
	}
	ban, err := store.New(pool).GetBan(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if ban.ExpiresAt.Valid || ban.CreatedBy.Valid {
		t.Fatal("永久自动封禁应无到期时间与创建管理员")
	}
	if err := s.Revoke(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureAutoBan(ctx, subject, 60, "自动封禁：测试超限"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, pool, "SELECT count(*) FROM bans WHERE subject_value=$1", subject.Value); n != 2 {
		t.Fatal("撤销后应可再次自动封禁")
	}
}

func TestLimitSettingsMissingDefaultsUnknownIgnoredAndFailedUpdateUnpublished(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	if _, err := pool.Exec(ctx, "DELETE FROM rate_limit_rules WHERE key='msg_per_second'"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "INSERT INTO rate_limit_rules(key,limit_count,window_seconds) VALUES('future_unknown',1,60)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM rate_limit_rules WHERE key='future_unknown'")
		_, _ = pool.Exec(ctx, "INSERT INTO rate_limit_rules(key,limit_count,window_seconds) VALUES('msg_per_second',1,1) ON CONFLICT(key) DO NOTHING")
	})
	s := moderation.NewLimitSettings(pool, 1, nil)
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if s.Current().Rules[moderation.RuleMsgPerSecond].Limit != 1 {
		t.Fatal("缺失规则应补齐代码默认值")
	}
	if _, ok := s.Current().Rules["future_unknown"]; ok {
		t.Fatal("未知规则应忽略")
	}
	before := s.Current()
	r := before.Rules[moderation.RuleMsgPerMinute]
	r.Limit = 123
	var missingAdmin pgtype.UUID
	_ = missingAdmin.Scan("00000000-0000-0000-0000-000000000001")
	if err := s.Update(ctx, []moderation.Rule{r}, missingAdmin); err == nil {
		t.Fatal("不存在的管理员外键应写入失败")
	}
	if s.Current() != before {
		t.Fatal("失败事务不得发布新快照")
	}
	var limit int
	if err := pool.QueryRow(ctx, "SELECT limit_count FROM rate_limit_rules WHERE key='msg_per_minute'").Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit != 10 {
		t.Fatal("失败事务不得写入部分配置")
	}
}
