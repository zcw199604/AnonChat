//go:build integration

package external_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"regexp"
	"sync"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/external"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func createExternalTestIdentity(t *testing.T, pool *pgxpool.Pool) pgtype.UUID {
	t.Helper()
	token := make([]byte, 32)
	_, _ = rand.Read(token)
	ident, err := store.New(pool).CreateIdentity(context.Background(), store.CreateIdentityParams{TokenHash: token, Nickname: "本站昵称不要外传", AvatarSeed: "avatar"})
	if err != nil {
		t.Fatal(err)
	}
	return ident.ID
}

func TestExternalAccountBindingPersistence(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	ctx := context.Background()
	id := createExternalTestIdentity(t, pool)
	key := bytes.Repeat([]byte{9}, 32)
	s := external.NewBindingStore(pool, key)
	first, err := s.Chat2019(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(first.RemoteUid) {
		t.Fatalf("chat2019 UID 格式错误: %q", first.RemoteUid)
	}
	if !regexp.MustCompile(`^[\p{Han}]+的[\p{Han}]+#\d{4}$`).MatchString(first.Nickname) {
		t.Fatalf("chat2019 未使用随机昵称: %q", first.Nickname)
	}
	if first.IdentityTokenEnc != nil {
		t.Fatal("chat2019 不应保存token")
	}
	reloaded, err := external.NewBindingStore(pool, key).Chat2019(ctx, id)
	if err != nil || reloaded.RemoteUid != first.RemoteUid || reloaded.Nickname != first.Nickname {
		t.Fatalf("chat2019绑定未复用: %v", err)
	}
	binding := wanfeng.Binding{IdentityID: id.String(), UpstreamUID: "WF-uid", IdentityToken: "fake-test-token", DeviceID: "device", Nickname: "随机昵称"}
	if err := s.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, "SELECT identity_token_enc FROM external_bindings WHERE identity_id=$1 AND provider='wanfeng'", id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || bytes.Contains(raw, []byte(binding.IdentityToken)) {
		t.Fatal("数据库token没有加密")
	}
	got, err := external.NewBindingStore(pool, key).Get(ctx, id.String())
	if err != nil || got == nil {
		t.Fatalf("读取晚风绑定: %v", err)
	}
	if got.UpstreamUID != binding.UpstreamUID || got.IdentityToken != binding.IdentityToken || got.DeviceID != binding.DeviceID || got.Nickname != binding.Nickname {
		t.Fatal("晚风绑定往返字段不一致")
	}
	if _, err := external.NewBindingStore(pool, bytes.Repeat([]byte{8}, 32)).Get(ctx, id.String()); err == nil {
		t.Fatal("错误密钥不应解密")
	}
	if err := s.Save(ctx, binding); err != nil {
		t.Fatal(err)
	}
	var second []byte
	_ = pool.QueryRow(ctx, "SELECT identity_token_enc FROM external_bindings WHERE identity_id=$1 AND provider='wanfeng'", id).Scan(&second)
	if bytes.Equal(raw, second) {
		t.Fatal("token加密没有使用随机nonce")
	}
	// 移植客户端的身份重置通过 Save 清空 uid/token/device，空 token 存 NULL。
	if err := s.Save(ctx, wanfeng.Binding{IdentityID: id.String(), Nickname: binding.Nickname}); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.Get(ctx, id.String())
	if err != nil || cleared == nil || cleared.UpstreamUID != "" || cleared.IdentityToken != "" || cleared.DeviceID != "" {
		t.Fatalf("重置绑定未保留清空语义: %v", err)
	}
	if err := s.Delete(ctx, id, "wanfeng"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ctx, id.String()); err != nil || got != nil {
		t.Fatalf("删除后仍有绑定: %v", err)
	}
	if got, err := s.Chat2019(ctx, id); err != nil || got.RemoteUid != first.RemoteUid {
		t.Fatalf("删除晚风影响chat2019: %v", err)
	}
}

func TestExternalChatBindingConcurrentCreation(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	id := createExternalTestIdentity(t, pool)
	s := external.NewBindingStore(pool, nil)
	const count = 12
	got := make(chan string, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, err := s.Chat2019(context.Background(), id)
			if err != nil {
				errs <- err
				return
			}
			got <- b.RemoteUid
		}()
	}
	wg.Wait()
	close(got)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var canonical string
	for uid := range got {
		if uid == "" {
			t.Fatal("空UID")
		}
		if canonical == "" {
			canonical = uid
		}
		if uid != canonical {
			t.Fatal("并发创建返回不同绑定")
		}
	}
}
