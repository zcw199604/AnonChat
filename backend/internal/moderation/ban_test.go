package moderation

import (
	"fmt"
	"testing"
	"time"
)

// TestBanCachePurge 验证大量不同 key 查询后缓存大小有上界（B1 修复）。
func TestBanCachePurge(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := &BanService{
		cache:     make(map[string]banCacheEntry),
		now:       func() time.Time { return now },
		lastSweep: now,
	}
	for i := 0; i < 5000; i++ {
		b.cache[fmt.Sprintf("ip|10.0.%d.%d", i/256, i%256)] = banCacheEntry{
			banned: true,
			until:  now.Add(-time.Second), // 已过期
		}
	}
	b.cache["ip|keep"] = banCacheEntry{banned: true, until: now.Add(time.Minute)}

	b.purgeLocked(now)

	if len(b.cache) != 1 {
		t.Fatalf("清理后缓存条目 = %d, 期望 1（仅保留未过期）", len(b.cache))
	}
	if _, ok := b.cache["ip|keep"]; !ok {
		t.Error("未过期条目应保留")
	}
}
