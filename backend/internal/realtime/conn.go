package realtime

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/store"
)

// 访问日志事件类型。
const (
	eventWSConnect    = "ws_connect"
	eventWSDisconnect = "ws_disconnect"
)

// outMessage 是写 goroutine 的出队消息。closeAfter 为真时写完即按指定码关闭。
type outMessage struct {
	data        []byte
	closeAfter  bool
	closeCode   websocket.StatusCode
	closeReason string
}

// Conn 表示一个 WebSocket 连接：一个读 goroutine + 一个写 goroutine。
type Conn struct {
	g         *Gateway
	ws        *websocket.Conn
	identity  *store.Identity
	ip        string
	userAgent string
	// connectedAt 是连接建立时间（供管理后台展示上线时间）。
	connectedAt time.Time

	send chan outMessage
	// done 在连接开始关闭时立即关闭（通知写 goroutine 退出、阻止再入队）。
	done chan struct{}
	// closed 在底层连接真正关闭后关闭（供 CloseAll 等待）。
	closed      chan struct{}
	closeOnce   sync.Once
	doCloseOnce sync.Once
	// closeConn 关闭底层连接（可在测试中注入）。
	closeConn func(code websocket.StatusCode, reason string)
}

// Identity 返回该连接绑定的身份（供业务模块使用）。
func (c *Conn) Identity() *store.Identity { return c.identity }

// SendEvent 发送一条服务端消息（供业务模块使用）。
func (c *Conn) SendEvent(t MessageType, id string, payload any) { c.sendEvent(t, id, payload) }

// SendError 发送一条 error 消息（供业务模块使用）。
func (c *Conn) SendError(id string, code ErrorCode, msg string) { c.sendError(id, code, msg) }

// Subscribe 让本连接订阅某会话（供 conv.subscribe 处理使用）。
func (c *Conn) Subscribe(conv pgtype.UUID) { c.g.hub.Subscribe(c, conv) }

// enqueue 把信封放入发送队列（非阻塞，满则判定为慢客户端）。
func (c *Conn) enqueue(env Envelope) {
	data, err := env.Marshal()
	if err != nil {
		return
	}
	c.trySend(outMessage{data: data})
}

// sendEvent 构造并发送一条服务端消息。
func (c *Conn) sendEvent(t MessageType, id string, payload any) {
	env, err := NewEnvelope(t, id, payload)
	if err != nil {
		return
	}
	c.enqueue(env)
}

// sendError 发送一条 error 消息。
func (c *Conn) sendError(id string, code ErrorCode, msg string) {
	c.sendEvent(TypeError, id, ErrorPayload{Code: code, Message: msg})
}

// trySend 非阻塞发送；发送缓冲已满视为慢客户端并断开。
func (c *Conn) trySend(m outMessage) {
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.send <- m:
	default:
		c.closeWith(websocket.StatusPolicyViolation, "slow client")
	}
}

// sendThenClose 先发送消息，写完后再按指定码关闭连接。
func (c *Conn) sendThenClose(env Envelope, code websocket.StatusCode, reason string) {
	data, err := env.Marshal()
	if err != nil {
		c.closeWith(code, reason)
		return
	}
	m := outMessage{data: data, closeAfter: true, closeCode: code, closeReason: reason}
	select {
	case <-c.done:
		return
	default:
	}
	select {
	case c.send <- m:
	default:
		c.closeWith(code, reason)
	}
}

// closeWith 幂等地触发关闭：只置位 done 并异步关闭底层连接，永不阻塞调用方。
func (c *Conn) closeWith(code websocket.StatusCode, reason string) {
	started := false
	c.closeOnce.Do(func() {
		close(c.done)
		started = true
	})
	if started {
		// 底层关闭（尤其是 coder/websocket 的关闭握手）放到独立 goroutine，避免卡住广播/发送方。
		go c.doClose(code, reason)
	}
}

// doClose 真正关闭底层连接并通知 closed。
func (c *Conn) doClose(code websocket.StatusCode, reason string) {
	c.doCloseOnce.Do(func() {
		if c.closeConn != nil {
			c.closeConn(code, reason)
		}
		close(c.closed)
	})
}

// writeLoop 是写 goroutine，逐条写出，写超时即断开。
func (c *Conn) writeLoop() {
	for {
		select {
		case <-c.done:
			return
		case m := <-c.send:
			ctx, cancel := context.WithTimeout(context.Background(), c.g.opts.WriteTimeout)
			err := c.ws.Write(ctx, websocket.MessageText, m.data)
			cancel()
			if err != nil {
				c.closeWith(websocket.StatusInternalError, "write failed")
				return
			}
			if m.closeAfter {
				c.closeWith(m.closeCode, m.closeReason)
				return
			}
		}
	}
}

// readLoop 是读 goroutine：每帧重置空闲超时；帧过大断开；分发到路由器。
func (c *Conn) readLoop(ctx context.Context) {
	for {
		readCtx, cancel := context.WithTimeout(ctx, c.g.opts.IdleTimeout)
		typ, data, err := c.ws.Read(readCtx)
		cancel()
		if err != nil {
			c.closeWith(websocket.StatusNormalClosure, "connection closed")
			return
		}
		if typ != websocket.MessageText {
			c.sendError("", CodeInvalidRequest, "仅支持文本帧")
			continue
		}
		if int64(len(data)) > c.g.opts.MaxFrameBytes {
			c.closeWith(websocket.StatusMessageTooBig, "frame too large")
			return
		}
		c.handleFrame(ctx, data)
	}
}

// handleFrame 解析并分发一帧；解析失败或未知类型返回 invalid_request，不断开连接。
func (c *Conn) handleFrame(ctx context.Context, data []byte) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		c.sendError("", CodeInvalidRequest, "消息格式错误")
		return
	}
	if env.Type == "" {
		c.sendError(env.ID, CodeInvalidRequest, "缺少消息类型")
		return
	}
	fn, ok := c.g.router.lookup(env.Type)
	if !ok {
		c.sendError(env.ID, CodeInvalidRequest, "未知消息类型")
		return
	}
	fn(ctx, c, env)
}

// serve 运行连接：注册到 Hub、发送 hello、启动写 goroutine、阻塞在读循环。
func (c *Conn) serve(ctx context.Context) {
	c.g.hub.addConn(c)
	c.g.accessLog.LogAccess(ctx, c.identity.ID, eventWSConnect, c.ip, c.userAgent)
	for _, o := range c.g.connObservers {
		o.OnConnOpened(c)
	}
	if c.g.hub.ConnCount(c.identity.ID) == 1 {
		for _, o := range c.g.observers {
			o.OnIdentityOnline(c.identity.ID)
		}
	}

	resumable := []string{}
	if c.g.resumable != nil {
		for _, id := range c.g.resumable(c.identity.ID) {
			resumable = append(resumable, id.String())
		}
	}
	c.sendEvent(TypeHello, "", HelloPayload{
		Nickname:   c.identity.Nickname,
		AvatarSeed: c.identity.AvatarSeed,
		Resumable:  resumable,
	})

	go c.writeLoop()
	c.readLoop(ctx)

	c.g.hub.removeConn(c)
	for _, o := range c.g.connObservers {
		o.OnConnClosed(c)
	}
	if c.g.hub.ConnCount(c.identity.ID) == 0 {
		for _, o := range c.g.observers {
			o.OnIdentityOffline(c.identity.ID)
		}
	}
	c.g.accessLog.LogAccess(context.Background(), c.identity.ID, eventWSDisconnect, c.ip, c.userAgent)
	c.closeWith(websocket.StatusNormalClosure, "disconnect")
}
