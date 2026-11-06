// Package privatechat 实现私密链接聊天（D 形态）：创建、加入、销毁、过期扫描。
package privatechat

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	defaultTTLHours = 24
	maxTTLHours     = 168
	minPasswordLen  = 4
	maxPasswordLen  = 64
	bcryptCost      = 10
)

// Service 提供私密链接操作。
type Service struct {
	q       *store.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	convSvc *conversation.Service
	hub     realtime.Realtime

	createLimiter   moderation.RateLimiter
	passwordLimiter moderation.RateLimiter

	now          func() time.Time
	scanInterval time.Duration
	resolver     *httpx.ClientIPResolver
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithScanInterval 设置过期扫描间隔（测试用短值）。
func WithScanInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.scanInterval = d
		}
	}
}

// WithPasswordLimiter 注入密码错误限流器（每个链接/身份/IP 各 5 次/10 分钟）。
func WithPasswordLimiter(l moderation.RateLimiter) Option {
	return func(s *Service) {
		if l != nil {
			s.passwordLimiter = l
		}
	}
}

// New 创建私密链接服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, convSvc *conversation.Service, hub realtime.Realtime,
	createLimiter, passwordLimiter moderation.RateLimiter, opts ...Option) *Service {
	if createLimiter == nil {
		createLimiter = moderation.AllowAll()
	}
	if passwordLimiter == nil {
		passwordLimiter = moderation.AllowAll()
	}
	s := &Service{
		q:               store.New(pool),
		pool:            pool,
		logger:          logger,
		convSvc:         convSvc,
		hub:             hub,
		createLimiter:   createLimiter,
		passwordLimiter: passwordLimiter,
		now:             time.Now,
		scanInterval:    time.Minute,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// CreatedLink 是创建链接的结果。
type CreatedLink struct {
	Token          string
	ConversationID pgtype.UUID
	ExpiresAt      time.Time
}

// SetResolver 注入客户端 IP 解析器（用于密码错误按 IP 限流）。
func (s *Service) SetResolver(r *httpx.ClientIPResolver) { s.resolver = r }

// Create 创建私密链接。
func (s *Service) Create(ctx context.Context, creator pgtype.UUID, password string, ttlHours int, burnAfterRead bool) (*CreatedLink, error) {
	if ttlHours == 0 {
		ttlHours = defaultTTLHours
	}
	if ttlHours < 1 || ttlHours > maxTTLHours {
		return nil, httpx.NewError(httpx.CodeInvalidRequest, "有效期须为 1-168 小时")
	}
	var passwordHash *string
	if password != "" {
		n := utf8.RuneCountInString(password)
		if n < minPasswordLen || n > maxPasswordLen {
			return nil, httpx.NewError(httpx.CodeInvalidRequest, "密码长度须为 4-64")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
		if err != nil {
			return nil, err
		}
		hs := string(h)
		passwordHash = &hs
	}
	if !s.createLimiter.Allow(ctx, "private_create:"+creator.String()) {
		return nil, httpx.NewError(httpx.CodeRateLimited, "今日创建次数已达上限")
	}

	token, hash, err := newToken()
	if err != nil {
		return nil, err
	}
	expires := s.now().Add(time.Duration(ttlHours) * time.Hour)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	conv, _, err := s.convSvc.Create(ctx, tx, store.ConversationKindPrivate, burnAfterRead, creator)
	if err != nil {
		return nil, err
	}
	if _, err := q.InsertPrivateLink(ctx, store.InsertPrivateLinkParams{
		TokenHash:         hash,
		ConversationID:    conv.ID,
		CreatorIdentityID: creator,
		PasswordHash:      passwordHash,
		ExpiresAt:         pgtype.Timestamptz{Time: expires, Valid: true},
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &CreatedLink{Token: token, ConversationID: conv.ID, ExpiresAt: expires}, nil
}

// Status 是链接状态信息。
type Status struct {
	NeedPassword  bool
	BurnAfterRead bool
	Status        string
}

// Lookup 查询链接信息。
func (s *Service) Lookup(ctx context.Context, token string) (*Status, error) {
	link, conv, err := s.load(ctx, token)
	if err != nil {
		return nil, err
	}
	st, err := s.status(ctx, link, conv)
	if err != nil {
		return nil, err
	}
	return &Status{NeedPassword: link.PasswordHash != nil, BurnAfterRead: conv.BurnAfterRead, Status: st}, nil
}

// Join 加入私密链接，返回会话 ID。
func (s *Service) Join(ctx context.Context, requester *store.Identity, token, password, ip string) (pgtype.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return pgtype.UUID{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	link, err := q.LockPrivateLinkByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, notFoundErr()
		}
		return pgtype.UUID{}, err
	}
	conv, err := q.GetConversation(ctx, link.ConversationID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if link.DestroyedAt.Valid {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeConversationEnded, "链接已销毁")
	}
	if !s.now().Before(link.ExpiresAt.Time) {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeLinkExpired, "链接已过期")
	}
	// 创建者本人或已加入者：幂等返回。
	if _, err := q.GetMember(ctx, store.GetMemberParams{ConversationID: link.ConversationID, IdentityID: requester.ID}); err == nil {
		return link.ConversationID, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, err
	}
	// 已满。
	count, err := q.CountActiveMembers(ctx, link.ConversationID)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if count >= 2 {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeLinkFull, "链接已满")
	}
	// 屏蔽关系（不泄露原因）。
	if blocked, err := q.ExistsBlockBetween(ctx, store.ExistsBlockBetweenParams{
		BlockerIdentityID: link.CreatorIdentityID, BlockedIdentityID: requester.ID,
	}); err != nil {
		return pgtype.UUID{}, err
	} else if blocked {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeLinkFull, "无法加入该链接")
	}
	// 密码校验。
	if link.PasswordHash != nil {
		if password == "" || bcrypt.CompareHashAndPassword([]byte(*link.PasswordHash), []byte(password)) != nil {
			okLink := s.passwordLimiter.Allow(ctx, "pw_link:"+link.ID.String())
			okID := s.passwordLimiter.Allow(ctx, "pw_id:"+requester.ID.String())
			okIP := s.passwordLimiter.Allow(ctx, "pw_ip:"+ip)
			if !okLink || !okID || !okIP {
				return pgtype.UUID{}, httpx.NewError(httpx.CodeRateLimited, "密码错误次数过多，请稍后再试")
			}
			return pgtype.UUID{}, httpx.NewError(httpx.CodeBadPassword, "密码错误")
		}
	}

	member, err := s.convSvc.AddMember(ctx, tx, link.ConversationID, requester.ID)
	if err != nil {
		return pgtype.UUID{}, err
	}

	// 系统消息：对方已加入。
	clientMsgID, err := genUUID()
	if err != nil {
		return pgtype.UUID{}, err
	}
	sysMsg, err := q.InsertMessage(ctx, store.InsertMessageParams{
		ConversationID:   link.ConversationID,
		SenderIdentityID: requester.ID,
		SenderMemberID:   member.MemberID,
		ClientMsgID:      clientMsgID,
		Kind:             "system",
		Body:             "对方已加入",
		MediaID:          pgtype.UUID{}, // 系统消息没有媒体
	})
	if err != nil {
		return pgtype.UUID{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return pgtype.UUID{}, err
	}

	s.hub.SubscribeIdentity(link.CreatorIdentityID, link.ConversationID)
	s.hub.SubscribeIdentity(requester.ID, link.ConversationID)
	if env, e := realtime.NewEnvelope(realtime.TypeMsgNew, "", map[string]any{
		"conversationId": link.ConversationID.String(),
		"message": map[string]any{
			"id":            sysMsg.ID,
			"memberId":      member.MemberID,
			"nickname":      requester.Nickname,
			"avatarSeed":    requester.AvatarSeed,
			"kind":          "system",
			"body":          "对方已加入",
			"createdAt":     formatTS(sysMsg.CreatedAt),
			"burnAfterRead": conv.BurnAfterRead,
		},
	}); e == nil {
		s.hub.Broadcast(link.ConversationID, env, nil)
	}
	return link.ConversationID, nil
}

// Destroy 销毁链接（仅成员）。
func (s *Service) Destroy(ctx context.Context, requester pgtype.UUID, token string) error {
	link, err := s.q.GetPrivateLinkByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return notFoundErr()
		}
		return err
	}
	if _, err := s.q.GetMember(ctx, store.GetMemberParams{ConversationID: link.ConversationID, IdentityID: requester}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return httpx.NewError(httpx.CodeNotMember, "你不是该会话成员")
		}
		return err
	}
	if _, err := s.q.SetPrivateLinkDestroyed(ctx, link.ID); err != nil {
		return err
	}
	return s.convSvc.End(ctx, link.ConversationID, "destroyed")
}

// StartExpiry 启动过期扫描，直到 ctx 取消。
func (s *Service) StartExpiry(ctx context.Context) {
	ticker := time.NewTicker(s.scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ScanExpired(ctx); err != nil {
				s.logger.Warn("私密链接过期扫描失败", "error", err)
			}
		}
	}
}

// ScanExpired 结束所有已过期但未结束的链接会话。
func (s *Service) ScanExpired(ctx context.Context) error {
	rows, err := s.q.ListExpiredPrivateLinks(ctx, pgtype.Timestamptz{Time: s.now(), Valid: true})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.convSvc.End(ctx, row.ConversationID, "expired"); err != nil {
			s.logger.Warn("结束过期私密链接失败", "error", err)
		}
	}
	return nil
}

// ---- 辅助 ----

func (s *Service) load(ctx context.Context, token string) (store.PrivateLink, store.Conversation, error) {
	link, err := s.q.GetPrivateLinkByTokenHash(ctx, hashToken(token))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.PrivateLink{}, store.Conversation{}, notFoundErr()
		}
		return store.PrivateLink{}, store.Conversation{}, err
	}
	conv, err := s.q.GetConversation(ctx, link.ConversationID)
	if err != nil {
		return store.PrivateLink{}, store.Conversation{}, err
	}
	return link, conv, nil
}

func (s *Service) status(ctx context.Context, link store.PrivateLink, _ store.Conversation) (string, error) {
	if link.DestroyedAt.Valid {
		return "destroyed", nil
	}
	if !s.now().Before(link.ExpiresAt.Time) {
		return "expired", nil
	}
	count, err := s.q.CountActiveMembers(ctx, link.ConversationID)
	if err != nil {
		return "", err
	}
	if count >= 2 {
		return "full", nil
	}
	return "active", nil
}

func newToken() (string, []byte, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	return token, hashToken(token), nil
}

func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

func genUUID() (pgtype.UUID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: b, Valid: true}, nil
}

func notFoundErr() error {
	e := httpx.NewError(httpx.CodeInvalidRequest, "链接不存在")
	e.Status = 404
	return e
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
