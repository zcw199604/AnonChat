package realtime

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestTryAddLimitConcurrent 验证并发加入不会超过 max_members（M3 修复）。
func TestTryAddLimitConcurrent(t *testing.T) {
	h := NewHub()
	const limit = 5
	const n = 20

	var success int32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			c := mkTestConn(uuidN(byte(i+1)), 1)
			ok, _ := h.TryAdd("room:1", c, limit)
			if ok {
				atomic.AddInt32(&success, 1)
			}
		}(i)
	}
	wg.Wait()

	if success != limit {
		t.Fatalf("成功加入数 = %d, 期望 %d", success, limit)
	}
	if got := h.Count("room:1"); got != limit {
		t.Fatalf("在线数 = %d, 期望 %d", got, limit)
	}
}

// TestTryAddSameIdentityNotLimited 验证同一身份已在房间时不受上限约束。
func TestTryAddSameIdentityNotLimited(t *testing.T) {
	h := NewHub()
	id := uuidN(9)
	c1 := mkTestConn(id, 1)
	if ok, _ := h.TryAdd("room:1", c1, 1); !ok {
		t.Fatal("首个连接应可加入")
	}
	c2 := mkTestConn(id, 1)
	if ok, _ := h.TryAdd("room:1", c2, 1); !ok {
		t.Fatal("同一身份第二个连接不应受上限约束")
	}
	if got := h.Count("room:1"); got != 1 {
		t.Fatalf("同一身份在线数应计 1, 实际 %d", got)
	}
}
