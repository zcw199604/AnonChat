package realtime

import (
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

func uuidN(n byte) pgtype.UUID {
	var b [16]byte
	b[15] = n
	return pgtype.UUID{Bytes: b, Valid: true}
}

// mkTestConn 构造一个不依赖网络的测试连接（未启动读/写 goroutine）。
func mkTestConn(id pgtype.UUID, buf int) *Conn {
	return &Conn{
		identity: &store.Identity{ID: id},
		send:     make(chan outMessage, buf),
		done:     make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func waitClosed(t *testing.T, c *Conn) {
	t.Helper()
	select {
	case <-c.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("连接未在预期时间内关闭")
	}
}

// TestSlowClientClosed 验证发送缓冲写满时判定为慢客户端并断开。
func TestSlowClientClosed(t *testing.T) {
	var code websocket.StatusCode
	c := mkTestConn(uuidN(1), 1)
	c.closeConn = func(cc websocket.StatusCode, _ string) { code = cc }

	c.trySend(outMessage{data: []byte("1")}) // 填满缓冲
	c.trySend(outMessage{data: []byte("2")}) // 触发慢客户端
	waitClosed(t, c)
	if code != websocket.StatusPolicyViolation {
		t.Errorf("关闭码 = %d, 期望 %d", code, websocket.StatusPolicyViolation)
	}
	// 关闭后再次入队应安全返回。
	c.trySend(outMessage{data: []byte("3")})
}

// TestCloseWithIdempotent 验证重复关闭只生效一次。
func TestCloseWithIdempotent(t *testing.T) {
	calls := 0
	c := mkTestConn(uuidN(2), 1)
	c.closeConn = func(websocket.StatusCode, string) { calls++ }

	c.closeWith(websocket.StatusNormalClosure, "a")
	c.closeWith(websocket.StatusGoingAway, "b")
	waitClosed(t, c)
	if calls != 1 {
		t.Errorf("closeConn 调用次数 = %d, 期望 1", calls)
	}
}

// TestBroadcastNotBlockedBySlowClient 验证慢客户端的断开不会阻塞向其他连接的广播（W1 修复）。
func TestBroadcastNotBlockedBySlowClient(t *testing.T) {
	h := NewHub()
	fast := mkTestConn(uuidN(10), 16)
	slow := mkTestConn(uuidN(11), 1)
	h.admit(fast, 0, 0)
	h.admit(slow, 0, 0)

	// 模拟 coder/websocket.Close 的关闭握手耗时：closeConn 阻塞直到测试结束。
	block := make(chan struct{})
	slow.closeConn = func(websocket.StatusCode, string) { <-block }

	conv := uuidN(99)
	h.Subscribe(fast, conv)
	h.Subscribe(slow, conv)

	// 先填满慢客户端发送缓冲，使下一次广播触发慢客户端断开。
	slow.trySend(outMessage{data: []byte("fill")})

	env, err := NewEnvelope(TypeMsgNew, "", map[string]string{"x": "y"})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	for i := 0; i < 5; i++ {
		h.Broadcast(conv, env, nil)
	}
	elapsed := time.Since(start)
	close(block) // 释放慢连接关闭 goroutine

	if elapsed > 100*time.Millisecond {
		t.Fatalf("广播被慢客户端阻塞，耗时 %v（应 < 100ms）", elapsed)
	}

	// 快客户端应收到全部 5 条广播。
	got := 0
	for {
		select {
		case <-fast.send:
			got++
		default:
			if got != 5 {
				t.Fatalf("快客户端收到 %d 条，期望 5", got)
			}
			return
		}
	}
}

// TestPresenceRefCounting 验证按连接登记、按身份去重计数（W2 修复）。
func TestPresenceRefCounting(t *testing.T) {
	h := NewHub()
	idA := uuidN(20)
	idB := uuidN(21)
	a1 := mkTestConn(idA, 1)
	a2 := mkTestConn(idA, 1)
	b := mkTestConn(idB, 1)
	h.admit(a1, 0, 0)
	h.admit(a2, 0, 0)
	h.admit(b, 0, 0)

	h.Add("room:1", a1)
	if got := h.Count("room:1"); got != 1 {
		t.Fatalf("加入一个连接后计数 = %d, 期望 1", got)
	}
	h.Add("room:1", a2) // 同一身份另一个标签页
	if got := h.Count("room:1"); got != 1 {
		t.Fatalf("同一身份两个连接计数 = %d, 期望 1", got)
	}
	h.Add("room:1", b)
	if got := h.Count("room:1"); got != 2 {
		t.Fatalf("两个身份计数 = %d, 期望 2", got)
	}

	h.Remove("room:1", a1) // 关掉 A 的一个标签页
	if got := h.Count("room:1"); got != 2 {
		t.Fatalf("A 仍有一个连接时计数 = %d, 期望 2", got)
	}
	h.Remove("room:1", a2) // 关掉 A 的另一个标签页
	if got := h.Count("room:1"); got != 1 {
		t.Fatalf("A 全部离开后计数 = %d, 期望 1", got)
	}
}

// TestPresenceDisconnectCleanup 验证连接断开自动清理其登记的全部 presence key。
func TestPresenceDisconnectCleanup(t *testing.T) {
	h := NewHub()
	c := mkTestConn(uuidN(30), 1)
	h.admit(c, 0, 0)
	h.Add("room:1", c)
	h.Add("room:2", c)
	if h.Count("room:1") != 1 || h.Count("room:2") != 1 {
		t.Fatal("登记后计数应为 1")
	}

	h.removeConn(c) // 模拟连接断开

	if h.Count("room:1") != 0 || h.Count("room:2") != 0 {
		t.Errorf("断开后应自动清理 presence: room1=%d room2=%d", h.Count("room:1"), h.Count("room:2"))
	}
}
