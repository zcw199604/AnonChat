package moderation

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AutoBanner interface {
	EnsureAutoBan(context.Context, BanSubject, int, string) error
}
type RuleSnapshot struct {
	Version int64
	Rules   map[RuleKey]Rule
}
type RuleCheck struct {
	Rule RuleKey
	Key  string
}
type ruleCounter struct {
	limit, window int
	fixed         map[string]bucket
	token         map[string]tbBucket
	calls         int
}

// LimitSettings 通过不可变快照发布配置；计数锁与数据库更新锁分离。
type LimitSettings struct {
	pool              *pgxpool.Pool
	multiplier        int
	banner            AutoBanner
	snapshot          atomic.Pointer[RuleSnapshot]
	updateMu          sync.Mutex
	countMu           sync.Mutex
	counters          map[RuleKey]*ruleCounter
	identityWindowMax int
	now               func() time.Time
}
type LimitSettingsOption func(*LimitSettings)

func WithIdentityWindowMax(d time.Duration) LimitSettingsOption {
	return func(s *LimitSettings) { s.identityWindowMax = int(d / time.Second) }
}
func NewLimitSettings(pool *pgxpool.Pool, multiplier int, banner AutoBanner, opts ...LimitSettingsOption) *LimitSettings {
	if multiplier < 1 {
		multiplier = 1
	}
	s := &LimitSettings{pool: pool, multiplier: multiplier, banner: banner, counters: make(map[RuleKey]*ruleCounter), identityWindowMax: 15552000, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	rules := make(map[RuleKey]Rule, len(ruleRegistry))
	for _, m := range ruleRegistry {
		rules[m.Key] = Rule{Key: m.Key, Limit: m.DefaultLimit, WindowSeconds: m.DefaultWindowSeconds, BanSeconds: m.DefaultBanSeconds, Enabled: true}
	}
	s.snapshot.Store(&RuleSnapshot{Version: 1, Rules: rules})
	return s
}
func (s *LimitSettings) Current() *RuleSnapshot { return s.snapshot.Load() }
func (s *LimitSettings) Multiplier() int        { return s.multiplier }
func (s *LimitSettings) Registry() []RuleMeta {
	ms := Registry()
	for i := range ms {
		if ms[i].Key == RuleIdentityCreate {
			ms[i].MaxWindowSeconds = s.identityWindowMax
		}
	}
	return ms
}
func (s *LimitSettings) EffectiveLimit(r Rule) int {
	m, _ := Meta(r.Key)
	if m.Kind == "frequency" || r.Key == RuleWSConnPerIP {
		return r.Limit * s.multiplier
	}
	return r.Limit
}
func (s *LimitSettings) Load(ctx context.Context) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	rows, err := store.New(s.pool).ListRateLimitRules(ctx)
	if err != nil {
		return err
	}
	rules := cloneRules(s.Current().Rules)
	for _, r := range rows {
		key := RuleKey(r.Key)
		if _, ok := Meta(key); ok {
			rules[key] = Rule{key, int(r.LimitCount), int(r.WindowSeconds), int(r.BanSeconds), r.Enabled, r.UpdatedAt.Time}
		}
	}
	all := make([]Rule, 0, len(rules))
	for _, r := range rules {
		all = append(all, r)
	}
	if err := s.validate(all); err != nil {
		return err
	}
	s.publish(rules)
	return nil
}
func cloneRules(in map[RuleKey]Rule) map[RuleKey]Rule {
	out := make(map[RuleKey]Rule, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
func (s *LimitSettings) validate(rules []Rule) error {
	seen := make(map[RuleKey]bool)
	for _, r := range rules {
		m, ok := Meta(r.Key)
		if !ok {
			return fmt.Errorf("未知限流规则：%s", r.Key)
		}
		if seen[r.Key] {
			return fmt.Errorf("%s：重复规则", m.Name)
		}
		seen[r.Key] = true
		maxWindow := m.MaxWindowSeconds
		if r.Key == RuleIdentityCreate {
			maxWindow = s.identityWindowMax
		}
		if m.Kind == "concurrent" {
			if r.Limit < 1 || r.Limit > 100 || r.WindowSeconds != 0 {
				return fmt.Errorf("%s：连接数须为1至100且窗口须为0", m.Name)
			}
		} else if r.Limit < 1 || r.Limit > 100000 || r.WindowSeconds < 1 || r.WindowSeconds > maxWindow {
			return fmt.Errorf("%s：次数须为1至100000，窗口须为1至%d秒", m.Name, maxWindow)
		}
		if r.BanSeconds < -1 || r.BanSeconds > math.MaxInt32 {
			return fmt.Errorf("%s：封禁秒数须为-1、0或合法正数", m.Name)
		}
		if m.BanSubject == "" && r.BanSeconds != 0 {
			return fmt.Errorf("%s：不支持自动封禁", m.Name)
		}
	}
	return nil
}
func (s *LimitSettings) Update(ctx context.Context, rules []Rule, adminID pgtype.UUID) error {
	s.updateMu.Lock()
	defer s.updateMu.Unlock()
	if err := s.validate(rules); err != nil {
		return &ValidationError{Message: err.Error()}
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	next := cloneRules(s.Current().Rules)
	q := store.New(tx)
	for _, r := range rules {
		row, err := q.UpsertRateLimitRule(ctx, store.UpsertRateLimitRuleParams{Key: string(r.Key), LimitCount: int32(r.Limit), WindowSeconds: int32(r.WindowSeconds), BanSeconds: int32(r.BanSeconds), Enabled: r.Enabled, UpdatedBy: adminID})
		if err != nil {
			return err
		}
		r.UpdatedAt = row.UpdatedAt.Time
		next[r.Key] = r
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	s.publish(next)
	return nil
}

type RuleLimiter struct {
	s   *LimitSettings
	key RuleKey
}

func (s *LimitSettings) Limiter(key RuleKey) RateLimiter { return &RuleLimiter{s, key} }
func (l *RuleLimiter) Allow(ctx context.Context, key string) bool {
	ok, _ := l.s.AllowAll(ctx, RuleCheck{l.key, key})
	return ok
}

// AllowAll 在同一快照下先检查全部余量，再统一扣减；拒绝不会部分消耗额度。
func (s *LimitSettings) AllowAll(ctx context.Context, checks ...RuleCheck) (bool, RuleKey) {
	s.countMu.Lock()
	snap := s.Current()
	now := s.now()
	for _, check := range checks {
		r, ok := snap.Rules[check.Rule]
		if !ok {
			s.countMu.Unlock()
			return false, check.Rule
		}
		if !r.Enabled {
			continue
		}
		m, _ := Meta(r.Key)
		if m.Kind != "frequency" {
			continue
		}
		c := s.counter(r)
		if !s.available(c, m, check.Key, now) {
			s.countMu.Unlock()
			s.EnsureRuleBan(ctx, r, check.Key)
			return false, check.Rule
		}
	}
	for _, check := range checks {
		r := snap.Rules[check.Rule]
		if !r.Enabled {
			continue
		}
		m, _ := Meta(r.Key)
		if m.Kind != "frequency" {
			continue
		}
		c := s.counter(r)
		if m.Algorithm == "token_bucket" {
			b := c.token[check.Key]
			b.tokens--
			c.token[check.Key] = b
		} else {
			b := c.fixed[check.Key]
			b.count++
			c.fixed[check.Key] = b
		}
	}
	s.countMu.Unlock()
	return true, ""
}
func (s *LimitSettings) counter(r Rule) *ruleCounter {
	limit := s.EffectiveLimit(r)
	c := s.counters[r.Key]
	if c == nil || c.limit != limit || c.window != r.WindowSeconds {
		c = &ruleCounter{limit: limit, window: r.WindowSeconds, fixed: make(map[string]bucket), token: make(map[string]tbBucket)}
		s.counters[r.Key] = c
	}
	return c
}
func (s *LimitSettings) available(c *ruleCounter, m RuleMeta, key string, now time.Time) bool {
	c.calls++
	window := time.Duration(c.window) * time.Second
	if c.calls%cleanEvery == 0 {
		for k, b := range c.fixed {
			if now.Sub(b.windowStart) >= window {
				delete(c.fixed, k)
			}
		}
		for k, b := range c.token {
			if now.Sub(b.last) >= window {
				delete(c.token, k)
			}
		}
	}
	if m.Algorithm == "token_bucket" {
		b, ok := c.token[key]
		if !ok {
			b = tbBucket{tokens: float64(c.limit), last: now}
		} else if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
			b.tokens = math.Min(float64(c.limit), b.tokens+elapsed*float64(c.limit)/float64(c.window))
			b.last = now
		}
		c.token[key] = b
		return b.tokens >= 1
	}
	b, ok := c.fixed[key]
	if !ok || now.Sub(b.windowStart) >= window {
		b = bucket{windowStart: now}
	}
	c.fixed[key] = b
	return b.count < c.limit
}
func (s *LimitSettings) EnsureBan(ctx context.Context, key RuleKey, counterKey string) {
	s.EnsureRuleBan(ctx, s.Current().Rules[key], counterKey)
}
func (s *LimitSettings) EnsureRuleBan(ctx context.Context, r Rule, key string) {
	m, ok := Meta(r.Key)
	if !ok || !r.Enabled || r.BanSeconds == 0 || m.BanSubject == "" || s.banner == nil || !strings.HasPrefix(key, m.KeyPrefix) {
		return
	}
	value := strings.TrimPrefix(key, m.KeyPrefix)
	if value == "" {
		return
	}
	if err := s.banner.EnsureAutoBan(ctx, BanSubject{Type: m.BanSubject, Value: value}, r.BanSeconds, "自动封禁："+m.Name+"超限"); err != nil {
		slog.Error("自动封禁失败", "rule", r.Key, "error", err)
	}
}

// SetIdentityWindowMax 在启动加载前限制身份计数窗口不得超出审计保留期。
func (s *LimitSettings) SetIdentityWindowMax(seconds int) { s.identityWindowMax = seconds }
func (s *LimitSettings) MaxWindowSeconds(key RuleKey) int {
	if key == RuleIdentityCreate {
		return s.identityWindowMax
	}
	m, _ := Meta(key)
	return m.MaxWindowSeconds
}
func (s *LimitSettings) AutoBan(ctx context.Context, r Rule, key string) {
	s.EnsureRuleBan(ctx, r, key)
}

type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// publish 与组合判定使用同一计数锁，避免旧快照在新配置发布后重建旧额度。
func (s *LimitSettings) publish(rules map[RuleKey]Rule) {
	s.countMu.Lock()
	defer s.countMu.Unlock()
	old := s.Current()
	for key, r := range rules {
		before := old.Rules[key]
		if before.Limit != r.Limit || before.WindowSeconds != r.WindowSeconds {
			delete(s.counters, key)
		}
	}
	s.snapshot.Store(&RuleSnapshot{Version: old.Version + 1, Rules: rules})
}
