package moderation

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewIdentityAndMatchDefaults(t *testing.T) {
	defaults := DefaultLimits()
	if defaults.IdentityCreate.Limit != 5 || defaults.IdentityCreate.Window.Hours() != 24 {
		t.Fatalf("身份默认额度应为 5/24h，得到 %+v", defaults.IdentityCreate)
	}
	if defaults.MatchStart.Limit != 15 {
		t.Fatalf("匹配默认额度应为15，得到%d", defaults.MatchStart.Limit)
	}
}

func TestRuleSettingsValidation(t *testing.T) {
	s := NewLimitSettings(nil, 1, nil)
	base := s.Current().Rules[RuleMsgPerMinute]
	cases := []Rule{base, base, base, base, base, base}
	cases[0].Key = "unknown"
	cases[1].Limit = 0
	cases[2].WindowSeconds = 0
	cases[3].BanSeconds = -2
	cases[4] = s.Current().Rules[RuleRoomMsgInterval]
	cases[4].BanSeconds = 1
	cases[5] = s.Current().Rules[RuleWSConnPerIP]
	cases[5].WindowSeconds = 1
	for _, rule := range cases {
		if err := s.validate([]Rule{rule}); err == nil {
			t.Errorf("应拒绝 %+v", rule)
		}
	}
	if err := s.validate([]Rule{base, base}); err == nil {
		t.Fatal("应拒绝重复规则")
	}
}

func TestCombinedRuleChecksDoNotPartiallyConsume(t *testing.T) {
	s := NewLimitSettings(nil, 1, nil)
	ctx := context.Background()
	if !s.Limiter(RuleMsgPerSecond).Allow(ctx, "msg:a") {
		t.Fatal("首次应放行")
	}
	if ok, hit := s.AllowAll(ctx, RuleCheck{RuleMsgPerMinute, "msg:a"}, RuleCheck{RuleMsgPerSecond, "msg:a"}); ok || hit != RuleMsgPerSecond {
		t.Fatal("秒规则应拒绝")
	}
	for i := 0; i < 10; i++ {
		if !s.Limiter(RuleMsgPerMinute).Allow(ctx, "msg:a") {
			t.Fatalf("分钟额度不应被部分消耗，i=%d", i)
		}
	}
}

func TestRuleLimiterConfigurationChanges(t *testing.T) {
	s := NewLimitSettings(nil, 1, nil)
	s.now = func() time.Time { return time.Unix(100, 0) }
	ctx := context.Background()
	lim := s.Limiter(RuleMsgPerSecond)
	if !lim.Allow(ctx, "msg:a") || lim.Allow(ctx, "msg:a") {
		t.Fatal("秒额度应拒绝第二次")
	}
	publish := func(change func(*Rule)) {
		old := s.Current()
		rules := cloneRules(old.Rules)
		r := rules[RuleMsgPerSecond]
		change(&r)
		rules[r.Key] = r
		s.snapshot.Store(&RuleSnapshot{old.Version + 1, rules})
	}
	publish(func(r *Rule) { r.BanSeconds = 60 })
	if lim.Allow(ctx, "msg:a") {
		t.Fatal("修改封禁时长不得清空计数")
	}
	publish(func(r *Rule) { r.Enabled = false })
	if !lim.Allow(ctx, "msg:a") {
		t.Fatal("禁用应放行")
	}
	publish(func(r *Rule) { r.Enabled = true })
	if lim.Allow(ctx, "msg:a") {
		t.Fatal("重新启用不得清空计数")
	}
	publish(func(r *Rule) { r.Limit = 2 })
	for i := 0; i < 2; i++ {
		if !lim.Allow(ctx, "msg:a") {
			t.Fatalf("修改次数应重建额度，第 %d 次应放行", i+1)
		}
	}
	if lim.Allow(ctx, "msg:a") {
		t.Fatal("修改次数后第三次应拒绝")
	}
	publish(func(r *Rule) { r.WindowSeconds = 2 })
	if !lim.Allow(ctx, "msg:a") {
		t.Fatal("修改窗口应重建额度")
	}
}

type fakeAutoBanner struct {
	subjects []BanSubject
	seconds  []int
	reasons  []string
}

func (f *fakeAutoBanner) EnsureAutoBan(_ context.Context, s BanSubject, seconds int, reason string) error {
	f.subjects = append(f.subjects, s)
	f.seconds = append(f.seconds, seconds)
	f.reasons = append(f.reasons, reason)
	return nil
}
func TestRuleLimiterAutomaticBan(t *testing.T) {
	for _, seconds := range []int{-1, 0, 3600} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			b := &fakeAutoBanner{}
			s := NewLimitSettings(nil, 1, b)
			s.now = func() time.Time { return time.Unix(100, 0) }
			rules := cloneRules(s.Current().Rules)
			r := rules[RuleMsgPerSecond]
			r.BanSeconds = seconds
			rules[r.Key] = r
			s.snapshot.Store(&RuleSnapshot{1, rules})
			lim := s.Limiter(r.Key)
			lim.Allow(context.Background(), "msg:identity")
			if lim.Allow(context.Background(), "msg:identity") {
				t.Fatal("超限应拒绝")
			}
			if seconds == 0 {
				if len(b.seconds) != 0 {
					t.Fatal("0不得调用封禁")
				}
			} else if len(b.seconds) != 1 || b.seconds[0] != seconds || b.subjects[0].Value != "identity" || b.reasons[0] != "自动封禁：每秒发送消息超限" {
				t.Fatalf("错误自动封禁 %+v", b)
			}
		})
	}
	b := &fakeAutoBanner{}
	s := NewLimitSettings(nil, 1, b)
	room := s.Current().Rules[RuleRoomMsgInterval]
	room.BanSeconds = -1
	s.EnsureRuleBan(context.Background(), room, "room_msg:conv|identity")
	msg := s.Current().Rules[RuleMsgPerSecond]
	msg.BanSeconds = -1
	s.EnsureRuleBan(context.Background(), msg, "wrong:identity")
	if len(b.seconds) != 0 {
		t.Fatal("不支持主体或前缀不匹配不得封禁")
	}
}
func TestRuntimeMultiplier(t *testing.T) {
	s := NewLimitSettings(nil, 3, nil)
	for key, want := range map[RuleKey]int{RuleMsgPerSecond: 3, RuleRoomMsgInterval: 3, RuleWSConnPerIP: 9, RuleWSConnPerIPIdentity: 1} {
		if got := s.EffectiveLimit(s.Current().Rules[key]); got != want {
			t.Errorf("%s=%d want %d", key, got, want)
		}
	}
}

func TestRuleSettingsConcurrentChecksNeverOverspend(t *testing.T) {
	s := NewLimitSettings(nil, 1, nil)
	s.now = func() time.Time { return time.Unix(100, 0) }
	var wg sync.WaitGroup
	var allowed atomic.Int32
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Limiter(RuleMsgPerMinute).Allow(context.Background(), "msg:a") {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()
	if allowed.Load() != 10 {
		t.Fatalf("并发仅允许10次，得到%d", allowed.Load())
	}
}

func TestCounterResetEvenWhenLimitChangesBackBeforeNextRequest(t *testing.T) {
	s := NewLimitSettings(nil, 1, nil)
	s.now = func() time.Time { return time.Unix(100, 0) }
	lim := s.Limiter(RuleMsgPerSecond)
	ctx := context.Background()
	lim.Allow(ctx, "msg:a")
	if lim.Allow(ctx, "msg:a") {
		t.Fatal("应耗尽")
	}
	rules := cloneRules(s.Current().Rules)
	r := rules[RuleMsgPerSecond]
	r.Limit = 2
	rules[r.Key] = r
	s.publish(rules)
	rules = cloneRules(s.Current().Rules)
	r.Limit = 1
	rules[r.Key] = r
	s.publish(rules)
	if !lim.Allow(ctx, "msg:a") {
		t.Fatal("规则变更应立即清空额度，即使两次判定之间恢复旧值")
	}
}
