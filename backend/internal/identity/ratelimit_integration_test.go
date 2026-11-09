//go:build integration

package identity_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestConcurrentCreateCountsSuccessfulIdentities(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	bans := moderation.NewBanService(pool, nil)
	settings := moderation.NewLimitSettings(pool, 1, bans)
	rule := settings.Current().Rules[moderation.RuleIdentityCreate]
	rule.Limit = 3
	if err := settings.Update(ctx, []moderation.Rule{rule}, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	s := identity.NewService(pool, slog.Default(), moderation.AllowAll(), identity.WithLimitSettings(settings))
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create(ctx, "192.0.2.10", "")
			if err == nil {
				success.Add(1)
				return
			}
			var e *httpx.APIError
			if !errors.As(err, &e) || e.Code != httpx.CodeRateLimited {
				t.Errorf("非限流错误: %v", err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 3 {
		t.Fatalf("成功创建 %d 次，期望3", success.Load())
	}
	if !bans.IsIPBanned(ctx, "192.0.2.10") {
		t.Fatal("超限应自动封禁IP")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM identities WHERE created_ip = '192.0.2.10'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("数据库身份数=%d", count)
	}
}

func TestConcurrentResetLocksOldIdentity(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	settings := moderation.NewLimitSettings(pool, 1, nil)
	rule := settings.Current().Rules[moderation.RuleIdentityCreate]
	rule.Limit = 100
	rule.BanSeconds = 0
	if err := settings.Update(ctx, []moderation.Rule{rule}, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	s := identity.NewService(pool, slog.Default(), moderation.AllowAll(), identity.WithLimitSettings(settings))
	first, err := s.Create(ctx, "192.0.2.11", "")
	if err != nil {
		t.Fatal(err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Reset(ctx, &first.Identity, "192.0.2.11", "")
			if err == nil {
				success.Add(1)
			} else if !errors.Is(err, identity.ErrUnauthorized) {
				t.Errorf("期望旧身份失效，实际: %v", err)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatalf("换身份成功%d次，期望1", success.Load())
	}
}

func TestResetOverLimitPreservesIdentity(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	settings := moderation.NewLimitSettings(pool, 1, nil)
	rule := settings.Current().Rules[moderation.RuleIdentityCreate]
	rule.Limit = 1
	rule.BanSeconds = 0
	if err := settings.Update(ctx, []moderation.Rule{rule}, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	s := identity.NewService(pool, slog.Default(), moderation.AllowAll(), identity.WithLimitSettings(settings))
	first, err := s.Create(ctx, "192.0.2.12", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reset(ctx, &first.Identity, "192.0.2.12", ""); err == nil {
		t.Fatal("应拒绝超限重置")
	}
	if _, err := s.Resolve(ctx, first.Token); err != nil {
		t.Fatalf("旧身份应保持有效: %v", err)
	}
}
