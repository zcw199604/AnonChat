package external

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/secretbox"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrWanfengKeyRequired = errors.New("晚风需要配置 SETTINGS_ENC_KEY")

// BindingStore 持久化每个本站身份的外站账号；晚风 token 仅以密文保存。
type BindingStore struct {
	pool *pgxpool.Pool
	key  []byte
}

var _ wanfeng.BindingStore = (*BindingStore)(nil)

func NewBindingStore(pool *pgxpool.Pool, key []byte) *BindingStore {
	return &BindingStore{pool: pool, key: append([]byte(nil), key...)}
}
func (s *BindingStore) WanfengAvailable() bool { return len(s.key) == 32 }

// Get 实现 wanfeng.BindingStore；缺少加密密钥时不读取晚风账号。
func (s *BindingStore) Get(ctx context.Context, id string) (*wanfeng.Binding, error) {
	if !s.WanfengAvailable() {
		return nil, ErrWanfengKeyRequired
	}
	var userID pgtype.UUID
	if err := userID.Scan(id); err != nil {
		return nil, err
	}
	row, err := store.New(s.pool).GetExternalBinding(ctx, store.GetExternalBindingParams{IdentityID: userID, Provider: "wanfeng"})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var token []byte
	if len(row.IdentityTokenEnc) > 0 {
		token, err = secretbox.Decrypt(s.key, row.IdentityTokenEnc)
		if err != nil {
			return nil, err
		}
	}
	return &wanfeng.Binding{IdentityID: id, UpstreamUID: row.RemoteUid, IdentityToken: string(token), DeviceID: row.DeviceID, Nickname: row.Nickname}, nil
}

func (s *BindingStore) Save(ctx context.Context, b wanfeng.Binding) error {
	if !s.WanfengAvailable() {
		return ErrWanfengKeyRequired
	}
	var userID pgtype.UUID
	if err := userID.Scan(b.IdentityID); err != nil {
		return err
	}
	var encrypted []byte
	if b.IdentityToken != "" {
		var err error
		encrypted, err = secretbox.Encrypt(s.key, []byte(b.IdentityToken))
		if err != nil {
			return err
		}
	}
	_, err := store.New(s.pool).UpsertExternalBinding(ctx, store.UpsertExternalBindingParams{IdentityID: userID, Provider: "wanfeng", RemoteUid: b.UpstreamUID, IdentityTokenEnc: encrypted, DeviceID: b.DeviceID, Nickname: b.Nickname})
	return err
}

// Chat2019 只在本地生成账号；同一身份并发调用也返回同一绑定。
func (s *BindingStore) Chat2019(ctx context.Context, id pgtype.UUID) (store.ExternalBinding, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return store.ExternalBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "external_chat_binding:"+id.String()); err != nil {
		return store.ExternalBinding{}, err
	}
	q := store.New(tx)
	row, err := q.GetExternalBinding(ctx, store.GetExternalBindingParams{IdentityID: id, Provider: "chat2019"})
	if errors.Is(err, pgx.ErrNoRows) {
		uid := make([]byte, 16)
		if _, err = rand.Read(uid); err != nil {
			return store.ExternalBinding{}, err
		}
		row, err = q.UpsertExternalBinding(ctx, store.UpsertExternalBindingParams{IdentityID: id, Provider: "chat2019", RemoteUid: hex.EncodeToString(uid), Nickname: identity.GeneratePlainNickname()})
	}
	if err != nil {
		return store.ExternalBinding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return store.ExternalBinding{}, err
	}
	return row, nil
}

func (s *BindingStore) Delete(ctx context.Context, id pgtype.UUID, provider string) error {
	return store.New(s.pool).DeleteExternalBinding(ctx, store.DeleteExternalBindingParams{IdentityID: id, Provider: provider})
}

// IdentityLimiter 是进程共享的注册令牌桶，容量为每小时上限，随时间平滑补充。
// Bridge 只在首次打开一个没有晚风绑定的身份时消耗令牌；复用绑定不消耗。
type IdentityLimiter struct {
	mu      sync.Mutex
	perHour int
	tokens  float64
	last    time.Time
	now     func() time.Time
}

func NewIdentityLimiter(perHour int) *IdentityLimiter {
	return &IdentityLimiter{perHour: perHour, tokens: float64(max(0, perHour)), last: time.Now(), now: time.Now}
}
func (l *IdentityLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.perHour <= 0 {
		return false
	}
	now := l.now()
	if now.After(l.last) {
		l.tokens = min(float64(l.perHour), l.tokens+now.Sub(l.last).Hours()*float64(l.perHour))
		l.last = now
	}
	if l.tokens < 1 {
		return false
	}
	l.tokens--
	return true
}
