package external

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
)

func TestBindingWithoutKeyRejectsWanfeng(t *testing.T) {
	for _, key := range [][]byte{nil, make([]byte, 16)} {
		s := NewBindingStore(nil, key)
		if s.WanfengAvailable() {
			t.Error("缺少32字节密钥仍允许晚风")
		}
		if _, err := s.Get(context.Background(), "test"); !errors.Is(err, ErrWanfengKeyRequired) {
			t.Errorf("Get missing key: %v", err)
		}
		if err := s.Save(context.Background(), wanfeng.Binding{}); !errors.Is(err, ErrWanfengKeyRequired) {
			t.Errorf("Save missing key: %v", err)
		}
	}
	if !NewBindingStore(nil, make([]byte, 32)).WanfengAvailable() {
		t.Error("有效密钥仍不可用")
	}
}

func TestIdentityLimiterGlobalBucketAndRefill(t *testing.T) {
	now := time.Unix(1700000000, 0)
	l := NewIdentityLimiter(2)
	l.now = func() time.Time { return now }
	l.last = now
	for range 2 {
		if !l.Allow() {
			t.Fatal("初始令牌不足")
		}
	}
	if l.Allow() {
		t.Fatal("超过全局注册限额仍允许")
	}
	now = now.Add(29 * time.Minute)
	if l.Allow() {
		t.Fatal("未补满一个令牌仍允许")
	}
	now = now.Add(time.Minute)
	if !l.Allow() || l.Allow() {
		t.Fatal("半小时应只补一个令牌")
	}
	now = now.Add(24 * time.Hour)
	for range 2 {
		if !l.Allow() {
			t.Fatal("空闲后令牌不足")
		}
	}
	if l.Allow() {
		t.Fatal("空闲补充不能超过桶容量")
	}
	if NewIdentityLimiter(0).Allow() {
		t.Fatal("非正限额应拒绝注册")
	}
}

func TestIdentityLimiterConcurrent(t *testing.T) {
	l := NewIdentityLimiter(10)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.Allow() {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 10 {
		t.Fatalf("accepted=%d want=10", accepted.Load())
	}
}
