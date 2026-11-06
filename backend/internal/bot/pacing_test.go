package bot

import (
	"testing"
	"time"
)

// fixedRand 是固定返回值的随机源，便于验证区间边界。
// value 为 0 时返回下界，为 -1 时返回上界（n-1）。
type fixedRand struct{ value int64 }

// Int63n 返回固定值：0 → 0，-1 → n-1。
func (f fixedRand) Int63n(n int64) int64 {
	if n <= 0 {
		return 0
	}
	if f.value < 0 {
		return n - 1
	}
	return 0
}

// TestReadDelayInRange 验证读消息延迟落在 [min, max] 区间内。
func TestReadDelayInRange(t *testing.T) {
	min, max := 3000*time.Millisecond, 12000*time.Millisecond
	minPacer := pacer{rnd: fixedRand{value: 0}}
	maxPacer := pacer{rnd: fixedRand{value: -1}}
	rndPacer := pacer{rnd: newAsyncRand(42)}

	if got := minPacer.readDelay(min, max); got != min {
		t.Errorf("下界: got %v, 期望 %v", got, min)
	}
	if got := maxPacer.readDelay(min, max); got != max {
		t.Errorf("上界: got %v, 期望 %v", got, max)
	}
	for i := 0; i < 100; i++ {
		got := rndPacer.readDelay(min, max)
		if got < min || got > max {
			t.Fatalf("readDelay = %v, 超出 [%v, %v]", got, min, max)
		}
	}
	// max <= min 时返回 min。
	if got := rndPacer.readDelay(max, min); got != max {
		t.Errorf("max<=min 时应返回 min(%v), 实际 %v", max, got)
	}
	// 随机源为 nil 时也能工作。
	if got := (pacer{}).readDelay(min, max); got != min {
		t.Errorf("无随机源时应返回 min, 实际 %v", got)
	}
}

// TestTypingDelay 验证打字时长限制在 [800ms, 8s]，msPerChar=0 时返回 0。
func TestTypingDelay(t *testing.T) {
	if got := typingDelay(100, 0); got != 0 {
		t.Errorf("msPerChar=0 时应返回 0, 实际 %v", got)
	}
	if got := typingDelay(1, 180); got != typingDelayMin {
		t.Errorf("字数很少时应取 800ms 下限, 实际 %v", got)
	}
	if got := typingDelay(10, 180); got != 1800*time.Millisecond {
		t.Errorf("10 字 × 180ms = 1.8s, 实际 %v", got)
	}
	if got := typingDelay(1000, 180); got != typingDelayMax {
		t.Errorf("超长时应取 8s 上限, 实际 %v", got)
	}
	if got := typingDelay(-5, 180); got != typingDelayMin {
		t.Errorf("负数按 0 处理, 实际 %v", got)
	}
}

// TestBottleNextInRange 验证回瓶间隔为 interval × [0.7, 1.3]。
func TestBottleNextInRange(t *testing.T) {
	interval := 30 * time.Minute
	low := pacer{rnd: fixedRand{value: 0}}
	high := pacer{rnd: fixedRand{value: -1}}
	rnd := pacer{rnd: newAsyncRand(7)}

	if got := low.bottleNext(interval); got != time.Duration(0.7*float64(interval)) {
		t.Errorf("下界: got %v", got)
	}
	if got := high.bottleNext(interval); got < 12*interval/10 || got > 13*interval/10 {
		t.Errorf("上界: got %v", got)
	}
	for i := 0; i < 100; i++ {
		got := rnd.bottleNext(interval)
		if got < 7*interval/10 || got > 13*interval/10 {
			t.Fatalf("bottleNext = %v, 超出 ±30%% 区间", got)
		}
	}
	if got := rnd.bottleNext(0); got != 0 {
		t.Errorf("interval=0 时应返回 0, 实际 %v", got)
	}
}
