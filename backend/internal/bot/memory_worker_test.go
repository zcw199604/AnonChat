package bot

import (
	"context"
	"testing"
	"time"
)

func TestMemoryWorkerNotifyAndExit(t *testing.T) {
	e := NewEngine(nil, nil, nil, nil, nil)
	if cap(e.memory.wake) != 1 {
		t.Fatal("提前触发通道应缓冲1次")
	}
	convID := genTestUUID(t)
	e.convs[convID] = &convState{}
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			e.OnConversationEnded(convID)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("会话结束通知不应阻塞")
	}
	if len(e.memory.wake) != 1 {
		t.Fatal("会话结束应合并为一次提前触发")
	}
	if _, ok := e.convs[convID]; ok {
		t.Fatal("会话结束应清理状态")
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { e.memory.Run(ctx); close(stopped) }()
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("worker应跟随ctx退出")
	}
}
