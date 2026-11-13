//go:build integration

package external_test

import (
	"context"
	"sync"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestExternalPeerReuseAndProfileRefresh(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	s := external.NewPeerStore(pool)
	p := external.PeerProfile{Nickname: "外站昵称", Sex: "女", Age: "22", Address: "浙江省杭州市"}
	first, err := s.Resolve(ctx, "wanfeng", "peer-1", p)
	if err != nil {
		t.Fatal(err)
	}
	if !first.IdentityID.Valid {
		t.Fatal("没有创建影子身份")
	}
	ident, err := store.New(pool).GetIdentityByID(ctx, first.IdentityID)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Gender != "female" || ident.Age == nil || *ident.Age != 22 || ident.Province == nil || *ident.Province != "浙江" || !ident.ProvincePublic {
		t.Fatalf("数据库资料未映射: %+v", ident)
	}
	p = external.PeerProfile{Nickname: "新昵称", Sex: "unknown", Age: "invalid", Address: "unknown"}
	again, err := s.Resolve(ctx, "wanfeng", "peer-1", p)
	if err != nil || again.IdentityID != first.IdentityID {
		t.Fatalf("未复用影子身份: %v", err)
	}
	ident, err = store.New(pool).GetIdentityByID(ctx, first.IdentityID)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Gender != "secret" || ident.Age != nil || ident.Province != nil || ident.ProvincePublic {
		t.Fatalf("资料未刷新: %+v", ident)
	}
	row, err := store.New(pool).GetExternalPeer(ctx, store.GetExternalPeerParams{Provider: "wanfeng", RemoteUid: "peer-1"})
	if err != nil || row.Nickname != "新昵称" || row.LastSeenAt.Time.Before(row.FirstSeenAt.Time) {
		t.Fatalf("peer资料未刷新: %v", err)
	}
	other, err := s.Resolve(ctx, "chat2019", "peer-1", p)
	if err != nil || other.IdentityID == first.IdentityID {
		t.Fatalf("不同站点不应共用身份: %v", err)
	}
}

func TestExternalPeerConcurrentCreationDoesNotLeakIdentities(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	s := external.NewPeerStore(pool)
	const count = 12
	ids := make(chan pgtype.UUID, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := s.Resolve(context.Background(), "chat2019", "peer-concurrent", external.PeerProfile{Sex: "男", Age: "25"})
			if err != nil {
				errs <- err
				return
			}
			ids <- p.IdentityID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var canonical pgtype.UUID
	for id := range ids {
		if !id.Valid {
			t.Fatal("空影子身份")
		}
		if !canonical.Valid {
			canonical = id
		}
		if id != canonical {
			t.Fatal("同一外站用户返回不同影子身份")
		}
	}
	var total int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM identities").Scan(&total); err != nil || total != 1 {
		t.Fatalf("并发创建遗留身份: count=%d err=%v", total, err)
	}
}
