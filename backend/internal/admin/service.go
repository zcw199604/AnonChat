// Package admin 实现管理后台后端：管理员会话、举报处理、封禁管理、统计与审计。
package admin

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	// SessionCookie 是管理员会话 Cookie 名。
	SessionCookie = "__Host-admin"
	sessionTTL    = 8 * time.Hour
	bcryptCost    = 12
	minPassword   = 12
)

// 审计动作。
const (
	ActionLoginSuccess = "login_success"
	ActionLoginFailed  = "login_failed"
	ActionLogout       = "logout"
	ActionResolve      = "resolve_report"
	ActionCreateBan    = "create_ban"
	ActionRevokeBan    = "revoke_ban"
)

var (
	// zeroID 用于登录失败等无管理员身份的审计记录。
	zeroID = pgtype.UUID{Bytes: [16]byte{}, Valid: true}

	dummyHashOnce sync.Once
	dummyHash     string
)

// dummyPasswordHash 返回一个固定的假哈希，用于用户名不存在时做等时 bcrypt 比较。
func dummyPasswordHash() string {
	dummyHashOnce.Do(func() {
		h, err := bcrypt.GenerateFromPassword([]byte("anonchat-dummy-password"), bcryptCost)
		if err != nil {
			dummyHash = "$2a$12$0000000000000000000000000000000000000000000000000000"
			return
		}
		dummyHash = string(h)
	})
	return dummyHash
}

// Service 提供管理后台的领域操作。
type Service struct {
	q        *store.Queries
	pool     *pgxpool.Pool
	logger   *slog.Logger
	banSvc   *moderation.BanService
	stats    realtime.ConnStats
	online   OnlineLister
	resolver *httpx.ClientIPResolver
	userLim  moderation.RateLimiter
	ipLim    moderation.RateLimiter
	now      func() time.Time
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

// WithLoginLimiters 注入登录失败限流器（按用户名、按 IP）。
func WithLoginLimiters(user, ip moderation.RateLimiter) Option {
	return func(s *Service) {
		if user != nil {
			s.userLim = user
		}
		if ip != nil {
			s.ipLim = ip
		}
	}
}

// OnlineLister 提供在线身份快照（由 realtime.Hub 实现）。
type OnlineLister interface {
	OnlineSnapshot() []realtime.OnlineIdentity
}

// WithOnlineLister 注入在线身份快照提供方（用于「在线用户」列表）。
func WithOnlineLister(l OnlineLister) Option {
	return func(s *Service) {
		if l != nil {
			s.online = l
		}
	}
}

// New 创建管理后台服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, banSvc *moderation.BanService, stats realtime.ConnStats, opts ...Option) *Service {
	s := &Service{
		q:       store.New(pool),
		pool:    pool,
		logger:  logger,
		banSvc:  banSvc,
		stats:   stats,
		userLim: moderation.AllowAll(),
		ipLim:   moderation.AllowAll(),
		now:     time.Now,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// SetResolver 注入客户端 IP 解析器（用于登录限流按 IP 计数）。
func (s *Service) SetResolver(r *httpx.ClientIPResolver) { s.resolver = r }

// CreateUser 校验并创建管理员账号（核心逻辑，供 CLI 与测试复用）。
func CreateUser(ctx context.Context, pool *pgxpool.Pool, username, password string) (store.AdminUser, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return store.AdminUser{}, fmt.Errorf("用户名不能为空")
	}
	if len([]rune(password)) < minPassword {
		return store.AdminUser{}, fmt.Errorf("密码长度至少 %d 位", minPassword)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return store.AdminUser{}, err
	}
	user, err := store.New(pool).CreateAdminUser(ctx, store.CreateAdminUserParams{
		Username:     username,
		PasswordHash: string(hash),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return store.AdminUser{}, fmt.Errorf("用户名已存在: %s", username)
		}
		return store.AdminUser{}, err
	}
	return user, nil
}

func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique constraint")
}

// Login 校验用户名密码并创建会话，返回明文令牌。
func (s *Service) Login(ctx context.Context, username, password, ip string) (string, error) {
	user, getUserErr := s.q.GetAdminUserByUsername(ctx, username)
	hashToCheck := dummyPasswordHash()
	adminID := zeroID
	if getUserErr == nil {
		hashToCheck = user.PasswordHash
		adminID = user.ID
	} else if !errors.Is(getUserErr, pgx.ErrNoRows) {
		return "", getUserErr
	}

	cmpErr := bcrypt.CompareHashAndPassword([]byte(hashToCheck), []byte(password))
	if getUserErr != nil || cmpErr != nil {
		// 失败：计入两个限流 key 并写审计。
		okU := s.userLim.Allow(ctx, "admin_login_user:"+username)
		okIP := s.ipLim.Allow(ctx, "admin_login_ip:"+ip)
		s.audit(ctx, zeroID, ActionLoginFailed, username, map[string]any{"ip": ip})
		if !okU || !okIP {
			return "", httpx.NewError(httpx.CodeRateLimited, "登录失败次数过多，请稍后再试")
		}
		return "", httpx.NewError(httpx.CodeUnauthorized, "用户名或密码错误")
	}

	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if _, err := s.q.CreateAdminSession(ctx, store.CreateAdminSessionParams{
		TokenHash: hash,
		AdminID:   adminID,
		ExpiresAt: pgtype.Timestamptz{Time: s.now().Add(sessionTTL), Valid: true},
	}); err != nil {
		return "", err
	}
	s.audit(ctx, adminID, ActionLoginSuccess, username, map[string]any{"ip": ip})
	return token, nil
}

// Logout 删除会话并写审计。
func (s *Service) Logout(ctx context.Context, token string, adminID pgtype.UUID) error {
	if err := s.q.DeleteAdminSession(ctx, hashToken(token)); err != nil {
		return err
	}
	s.audit(ctx, adminID, ActionLogout, "", nil)
	return nil
}

// SessionAdmin 查询会话对应的管理员 ID（供 RequireAdmin 使用）。
func (s *Service) SessionAdmin(ctx context.Context, token string) (pgtype.UUID, bool) {
	sess, err := s.q.GetAdminSession(ctx, store.GetAdminSessionParams{
		TokenHash: hashToken(token),
		ExpiresAt: pgtype.Timestamptz{Time: s.now(), Valid: true},
	})
	if err != nil {
		return pgtype.UUID{}, false
	}
	return sess.AdminID, true
}

// LogAudit 供其他模块（公告、广播、激活码、作品审核）写入管理员操作审计日志。
func (s *Service) LogAudit(ctx context.Context, adminID pgtype.UUID, action, target string, detail map[string]any) {
	s.audit(ctx, adminID, action, target, detail)
}

// audit 写一条审计日志（失败只记录日志，不影响主流程）。
func (s *Service) audit(ctx context.Context, adminID pgtype.UUID, action, target string, detail map[string]any) {
	var detailJSON []byte
	if detail != nil {
		detailJSON, _ = json.Marshal(detail)
	}
	var targetPtr *string
	if target != "" {
		targetPtr = &target
	}
	if err := s.q.InsertAdminAudit(ctx, store.InsertAdminAuditParams{
		AdminID: adminID,
		Action:  action,
		Target:  targetPtr,
		Detail:  detailJSON,
	}); err != nil {
		s.logger.Warn("写审计日志失败", "action", action, "error", err)
	}
}

func newToken() (string, []byte, error) {
	var b [32]byte
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

// normalizeBanValue 校验封禁主体值并返回规范化后的值：
// 去掉首尾空白；IP 统一为 netip.ParseAddr 的字符串形式（与 httpx.ClientIPResolver.ClientIP
// 返回的形式一致，保证封禁值能与 Hub 中记录的连接 IP 精确匹配）。
func normalizeBanValue(subjectType, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("封禁主体值不能为空")
	}
	switch subjectType {
	case moderation.SubjectIdentity, moderation.SubjectAccount:
		var u pgtype.UUID
		if err := u.Scan(value); err != nil || !u.Valid {
			return "", fmt.Errorf("%s 必须是 UUID", subjectType)
		}
		return value, nil
	case moderation.SubjectIP:
		addr, err := netip.ParseAddr(value)
		if err != nil {
			return "", fmt.Errorf("ip 不合法")
		}
		return addr.String(), nil
	default:
		return "", fmt.Errorf("未知封禁类型: %s", subjectType)
	}
}
