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

// TestExternalPeerNicknameFollowsRemote 验证入站昵称透传：
// 新建影子身份用外站昵称（清洗后），外站改名后影子身份昵称随之更新，且 match.ExternalPeer 带出。
func TestExternalPeerNicknameFollowsRemote(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	s := external.NewPeerStore(pool)

	first, err := s.Resolve(ctx, "chat2019", "peer-nick", external.PeerProfile{Nickname: "  外站小明\n"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Nickname != "外站小明" || first.AvatarSeed == "" {
		t.Fatalf("新建影子身份昵称 = %q/%q, 期望清洗后的外站昵称与头像种子", first.Nickname, first.AvatarSeed)
	}
	ident, err := store.New(pool).GetIdentityByID(ctx, first.IdentityID)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Nickname != "外站小明" {
		t.Fatalf("影子身份昵称 = %q, 期望 外站小明", ident.Nickname)
	}

	// 外站改名：再次 Resolve 时影子身份昵称同步更新，头像种子保持不变。
	again, err := s.Resolve(ctx, "chat2019", "peer-nick", external.PeerProfile{Nickname: "外站小红2024"})
	if err != nil {
		t.Fatal(err)
	}
	if again.IdentityID != first.IdentityID {
		t.Fatal("同一外站用户应复用影子身份")
	}
	if again.Nickname != "外站小红2024" {
		t.Fatalf("影子身份昵称未随外站昵称更新: %q", again.Nickname)
	}
	if again.AvatarSeed != first.AvatarSeed {
		t.Fatalf("头像种子不应变化: %q vs %q", again.AvatarSeed, first.AvatarSeed)
	}
	ident, err = store.New(pool).GetIdentityByID(ctx, first.IdentityID)
	if err != nil {
		t.Fatal(err)
	}
	if ident.Nickname != "外站小红2024" {
		t.Fatalf("数据库里的影子身份昵称 = %q, 期望 外站小红2024", ident.Nickname)
	}

	// 外站昵称为空时回退为随机昵称，不能写入空字符串。
	blank, err := s.Resolve(ctx, "chat2019", "peer-nick", external.PeerProfile{Nickname: "   "})
	if err != nil {
		t.Fatal(err)
	}
	if blank.Nickname == "" {
		t.Fatal("外站昵称为空时应回退为随机昵称")
	}
}
