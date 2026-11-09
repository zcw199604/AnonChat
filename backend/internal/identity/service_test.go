package identity

import (
	"context"
	"errors"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func uuidN(n byte) pgtype.UUID {
	var b [16]byte
	b[0] = n
	return pgtype.UUID{Bytes: b, Valid: true}
}

// newSweepService 构造一个只用于清理逻辑测试的 Service（不连数据库）。
func newSweepService(now time.Time) *Service {
	return &Service{
		lastSeen:      make(map[pgtype.UUID]time.Time),
		touchInterval: time.Minute,
		lastSweep:     now,
	}
}

// TestLastSeenCleanupByCalls 验证达到调用次数阈值时会清理过期条目。
func TestLastSeenCleanupByCalls(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newSweepService(now)
	old := uuidN(1)
	recent := uuidN(2)
	s.lastSeen[old] = now.Add(-2 * time.Minute) // 超过 touchInterval
	s.lastSeen[recent] = now.Add(-30 * time.Second)
	s.touchCalls = lastSeenSweepEveryCalls - 1 // 下一次调用触发清理

	s.maybeSweepLocked(now)

	if _, ok := s.lastSeen[old]; ok {
		t.Error("过期条目应被清理")
	}
	if _, ok := s.lastSeen[recent]; !ok {
		t.Error("未过期条目不应被清理")
	}
}

// TestLastSeenCleanupByPeriod 验证超过清理周期也会触发清理。
func TestLastSeenCleanupByPeriod(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newSweepService(now.Add(-2 * time.Minute)) // lastSweep 在 2 分钟前
	old := uuidN(3)
	s.lastSeen[old] = now.Add(-90 * time.Second)

	s.maybeSweepLocked(now)

	if _, ok := s.lastSeen[old]; ok {
		t.Error("超过周期后应清理过期条目")
	}
}

// TestLastSeenNotCleanedBeforeThreshold 验证未达阈值时不会清理。
func TestLastSeenNotCleanedBeforeThreshold(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := newSweepService(now)
	id := uuidN(4)
	s.lastSeen[id] = now.Add(-2 * time.Minute)
	s.touchCalls = 0

	s.maybeSweepLocked(now)

	if _, ok := s.lastSeen[id]; !ok {
		t.Error("未达清理阈值时不应清理")
	}
}

// TestInvalidIPFallsBackToMemory 在无法解析 IP 时先拒绝，不会接触数据库。
func TestInvalidIPFallsBackToMemory(t *testing.T) {
	settings := moderation.NewLimitSettings(nil, 1, nil)
	s := NewService(nil, slog.Default(), moderation.AllowAll(), WithLimitSettings(settings))
	for _, ip := range []string{"", "not-an-ip"} {
		limiter := settings.Limiter(moderation.RuleIdentityCreate)
		for i := 0; i < 5; i++ {
			if !limiter.Allow(context.Background(), createLimitKey+ip) {
				t.Fatal("初始额度不足")
			}
		}
		_, err := s.Create(context.Background(), ip, "")
		if err == nil {
			t.Fatal("空或非法IP必须内存限流")
		}
		var appErr *httpx.APIError
		if !errors.As(err, &appErr) || appErr.Code != httpx.CodeRateLimited {
			t.Fatalf("期望限流错误，实际 %v", err)
		}
	}
}
