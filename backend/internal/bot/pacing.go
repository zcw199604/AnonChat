package bot

import (
	"math/rand"
	"sync"
	"time"
)

// 节奏参数（how.md §7.1）。
const (
	// typingDelayMin / typingDelayMax 是"正在输入"时长的上下限。
	typingDelayMin = 800 * time.Millisecond
	typingDelayMax = 8 * time.Second
	// bottleNextRatioMin / Max 是回瓶间隔的随机比例区间。
	bottleNextRatioMin = 0.7
	bottleNextRatioMax = 1.3
)

// RandSource 是可注入的随机源（默认用 math/rand）。
type RandSource interface {
	// Int63n 返回 [0, n) 的随机数。
	Int63n(n int64) int64
}

// asyncRand 把 *rand.Rand 适配成 RandSource，并加锁保证并发安全。
type asyncRand struct {
	mu  sync.Mutex
	rnd *rand.Rand
}

// newAsyncRand 用给定种子创建并发安全的随机源。
func newAsyncRand(seed int64) *asyncRand {
	return &asyncRand{rnd: rand.New(rand.NewSource(seed))}
}

// Int63n 返回 [0, n) 的随机数。
func (a *asyncRand) Int63n(n int64) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rnd.Int63n(n)
}

// pacer 提供读消息延迟、打字时长与回瓶间隔的随机计算。
type pacer struct {
	rnd RandSource
}

// readDelay 返回 [min, max] 之间的读消息延迟；max <= min 时返回 min。
func (p pacer) readDelay(min, max time.Duration) time.Duration {
	if max <= min || p.rnd == nil {
		if min < 0 {
			return 0
		}
		return min
	}
	span := int64(max - min)
	if span <= 0 {
		return min
	}
	return min + time.Duration(p.rnd.Int63n(span+1))
}

// typingDelay 按字数与每字时长计算"正在输入"时长，限制在 [800ms, 8s]。
// msPerChar 为 0 时返回 0（不模拟打字）。
func typingDelay(runes, msPerChar int) time.Duration {
	if msPerChar <= 0 {
		return 0
	}
	if runes < 0 {
		runes = 0
	}
	d := time.Duration(runes*msPerChar) * time.Millisecond
	if d < typingDelayMin {
		return typingDelayMin
	}
	if d > typingDelayMax {
		return typingDelayMax
	}
	return d
}

// randIntn 返回 [0, n) 的随机数（无随机源时返回 0）。
func (p pacer) randIntn(n int) int {
	if n <= 1 || p.rnd == nil {
		return 0
	}
	return int(p.rnd.Int63n(int64(n)))
}

// bottleNext 返回下一次回瓶的时间间隔 = interval × [0.7, 1.3]。
func (p pacer) bottleNext(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	if p.rnd == nil {
		return interval
	}
	const steps = 1000
	ratio := bottleNextRatioMin + (bottleNextRatioMax-bottleNextRatioMin)*float64(p.rnd.Int63n(steps))/float64(steps)
	return time.Duration(float64(interval) * ratio)
}
