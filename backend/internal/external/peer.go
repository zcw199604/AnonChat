package external

import (
	"context"
	"crypto/rand"
	"errors"
	"strconv"
	"strings"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/profile"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PeerProfile 是外站对方的公开资料，保留原始字符串供统一映射。
type PeerProfile struct{ Nickname, Sex, Age, Address string }
type PeerStore struct{ pool *pgxpool.Pool }

func NewPeerStore(pool *pgxpool.Pool) *PeerStore { return &PeerStore{pool: pool} }

// Resolve 在同一事务内取得影子身份并刷新资料。
// 事务锁以站点和远端 UID 为键，避免并发首见时产生无归属的影子身份。
func (s *PeerStore) Resolve(ctx context.Context, provider, remoteUID string, p PeerProfile) (match.ExternalPeer, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return match.ExternalPeer{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", "external_peer:"+provider+":"+remoteUID); err != nil {
		return match.ExternalPeer{}, err
	}
	q := store.New(tx)
	// 外站昵称在我方作为影子身份的昵称显示，统一清洗后落库。
	nickname := sanitizeExternalPeerNickname(p.Nickname)
	row, err := q.GetExternalPeer(ctx, store.GetExternalPeerParams{Provider: provider, RemoteUid: remoteUID})
	if errors.Is(err, pgx.ErrNoRows) {
		tokenHash := make([]byte, 32)
		if _, err := rand.Read(tokenHash); err != nil {
			return match.ExternalPeer{}, err
		}
		var ident store.Identity
		// 与身份服务一样，对数据库随机公开编号的唯一冲突作有限重试。
		for range 3 {
			ident, err = q.CreateIdentity(ctx, store.CreateIdentityParams{TokenHash: tokenHash, Nickname: nickname, AvatarSeed: identity.GenerateAvatarSeed()})
			if !errors.Is(err, pgx.ErrNoRows) {
				break
			}
		}
		if err != nil {
			return match.ExternalPeer{}, err
		}
		row.IdentityID = ident.ID
	} else if err != nil {
		return match.ExternalPeer{}, err
	}
	row, err = q.UpsertExternalPeer(ctx, store.UpsertExternalPeerParams{Provider: provider, RemoteUid: remoteUID, IdentityID: row.IdentityID, Nickname: p.Nickname})
	if err != nil {
		return match.ExternalPeer{}, err
	}
	mapped := mapPeerProfile(p)
	if _, err := q.UpdateIdentityProfile(ctx, store.UpdateIdentityProfileParams{ID: row.IdentityID, Gender: mapped.Gender, Age: mapped.Age, Province: mapped.Province, ProvincePublic: mapped.ProvincePublic, Tags: []string{}, MatchGender: "any"}); err != nil {
		return match.ExternalPeer{}, err
	}
	// 已存在的影子身份同样每次按外站最新昵称刷新，保证我方显示对外站改名可见。
	ident, err := q.UpdateIdentityNickname(ctx, store.UpdateIdentityNicknameParams{ID: row.IdentityID, Nickname: nickname})
	if err != nil {
		return match.ExternalPeer{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return match.ExternalPeer{}, err
	}
	return match.ExternalPeer{IdentityID: row.IdentityID, Profile: mapped, Nickname: ident.Nickname, AvatarSeed: ident.AvatarSeed}, nil
}

func mapPeerProfile(p PeerProfile) match.PrefProfile {
	result := match.PrefProfile{Gender: "secret", MatchGender: "any"}
	switch strings.TrimSpace(p.Sex) {
	case "男":
		result.Gender = "male"
	case "女":
		result.Gender = "female"
	}
	if age, err := strconv.Atoi(strings.TrimSpace(p.Age)); err == nil && age >= 18 && age <= 80 {
		value := int16(age)
		result.Age = &value
	}
	address := strings.TrimSpace(p.Address)
	for _, province := range profile.Provinces() {
		prefix := province
		for _, suffix := range []string{"特别行政区", "壮族自治区", "回族自治区", "维吾尔自治区", "自治区", "省", "市"} {
			prefix = strings.TrimSuffix(prefix, suffix)
		}
		if prefix != "" && strings.HasPrefix(address, prefix) {
			value := province
			result.Province = &value
			result.ProvincePublic = true
			break
		}
	}
	return result
}
