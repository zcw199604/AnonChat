// Package room 实现官方聊天室（B 形态）：房间列表、加入/退出、在线人数与房间消息历史。
package room

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// presencePushInterval 是同一房间在线人数推送的最小间隔。
const presencePushInterval = time.Second

// presenceKey 返回房间 presence key。
func presenceKey(roomID pgtype.UUID) string { return "room:" + roomID.String() }

type presenceState struct {
	lastAt time.Time
	timer  *time.Timer
}

// Service 提供聊天室操作。
type Service struct {
	q       *store.Queries
	pool    *pgxpool.Pool
	logger  *slog.Logger
	convSvc *conversation.Service
	hub     realtime.Realtime
	now     func() time.Time

	messageRetention time.Duration

	mu           sync.Mutex
	connRooms    map[*realtime.Conn]map[pgtype.UUID]pgtype.UUID // conn → (roomID → conversationID)
	presencePush map[pgtype.UUID]*presenceState                 // roomID → 推送状态
}

// WithMessageRetention 设置房间历史可回溯的时长（默认 7 天）。
func WithMessageRetention(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.messageRetention = d
		}
	}
}

// Option 配置 Service。
type Option func(*Service)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// New 创建聊天室服务。
func New(pool *pgxpool.Pool, logger *slog.Logger, convSvc *conversation.Service, hub realtime.Realtime, opts ...Option) *Service {
	s := &Service{
		q:                store.New(pool),
		pool:             pool,
		logger:           logger,
		convSvc:          convSvc,
		hub:              hub,
		now:              time.Now,
		messageRetention: 168 * time.Hour,
		connRooms:        make(map[*realtime.Conn]map[pgtype.UUID]pgtype.UUID),
		presencePush:     make(map[pgtype.UUID]*presenceState),
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// RegisterWS 注册 room.join / room.leave（均挂 RequireVerified）。
func (s *Service) RegisterWS(router *realtime.Router, verificationMode string) {
	v := realtime.RequireVerified(verificationMode)
	router.Handle(realtime.TypeRoomJoin, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleJoin(ctx, c, env)
	}))
	router.Handle(realtime.TypeRoomLeave, v(func(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
		s.handleLeave(ctx, c, env)
	}))
}

// joinPayload 是 room.join / room.leave 的载荷。
type joinPayload struct {
	RoomID string `json:"roomId"`
}

// joinAck 是房间加入的 ack 负载。
type joinAck struct {
	RoomID         string `json:"roomId"`
	ConversationID string `json:"conversationId"`
	MemberID       int32  `json:"memberId"`
	Online         int    `json:"online"`
}

func (s *Service) handleJoin(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	var p joinPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "载荷格式错误")
		return
	}
	roomID, err := parseUUID(p.RoomID)
	if err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "房间 ID 非法")
		return
	}
	room, err := s.q.GetRoomByID(ctx, roomID)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.SendError(env.ID, realtime.CodeInvalidRequest, "房间不存在")
			return
		}
		s.internalError(c, env.ID, err)
		return
	}
	if room.ArchivedAt.Valid {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "房间已归档")
		return
	}

	key := presenceKey(roomID)
	ident := c.Identity()
	ok, online := s.hub.TryAdd(key, c, int(room.MaxMembers))
	if !ok {
		c.SendError(env.ID, realtime.CodeRoomFull, "房间已满")
		return
	}

	member, err := s.addMember(ctx, room.ConversationID, ident.ID)
	if err != nil {
		s.hub.Remove(key, c) // 回滚 presence
		s.internalError(c, env.ID, err)
		return
	}

	s.hub.Subscribe(c, room.ConversationID)
	s.track(c, roomID, room.ConversationID)

	c.SendEvent(realtime.TypeAck, env.ID, joinAck{
		RoomID:         roomID.String(),
		ConversationID: room.ConversationID.String(),
		MemberID:       member,
		Online:         online,
	})
	s.schedulePresence(roomID, room.ConversationID)
}

func (s *Service) handleLeave(ctx context.Context, c *realtime.Conn, env realtime.Envelope) {
	var p joinPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "载荷格式错误")
		return
	}
	roomID, err := parseUUID(p.RoomID)
	if err != nil {
		c.SendError(env.ID, realtime.CodeInvalidRequest, "房间 ID 非法")
		return
	}
	room, err := s.q.GetRoomByID(ctx, roomID)
	if err != nil {
		if err == pgx.ErrNoRows {
			c.SendError(env.ID, realtime.CodeInvalidRequest, "房间不存在")
			return
		}
		s.internalError(c, env.ID, err)
		return
	}
	key := presenceKey(roomID)
	ident := c.Identity()
	s.hub.Remove(key, c)
	s.hub.Unsubscribe(c, room.ConversationID)
	s.untrack(c, roomID)
	if !s.hub.PresenceHas(key, ident.ID) {
		// 该身份在此房间已无任何连接 → 标记离开，之后不能再通过 msg.send 发言。
		_ = s.convSvc.Leave(ctx, room.ConversationID, ident.ID)
	}
	online := s.hub.Count(key)
	c.SendEvent(realtime.TypeAck, env.ID, map[string]any{"roomId": roomID.String(), "online": online})
	s.schedulePresence(roomID, room.ConversationID)
}

// OnConnOpened 实现 realtime.ConnObserver（房间登记在 join 时完成，此处无需处理）。
func (s *Service) OnConnOpened(*realtime.Conn) {}

// OnConnClosed 实现 realtime.ConnObserver：清理该连接加入过的房间。
func (s *Service) OnConnClosed(c *realtime.Conn) {
	s.mu.Lock()
	rooms := s.connRooms[c]
	delete(s.connRooms, c)
	s.mu.Unlock()
	if len(rooms) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ident := c.Identity()
	for roomID, convID := range rooms {
		key := presenceKey(roomID)
		if !s.hub.PresenceHas(key, ident.ID) {
			_ = s.convSvc.Leave(ctx, convID, ident.ID)
		}
		s.schedulePresence(roomID, convID)
	}
}

// addMember 在事务内加入房间会话成员，返回 member_id。
func (s *Service) addMember(ctx context.Context, convID, identityID pgtype.UUID) (int32, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	member, err := s.convSvc.AddMember(ctx, tx, convID, identityID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return member.MemberID, nil
}

func (s *Service) track(c *realtime.Conn, roomID, convID pgtype.UUID) {
	s.mu.Lock()
	m := s.connRooms[c]
	if m == nil {
		m = make(map[pgtype.UUID]pgtype.UUID)
		s.connRooms[c] = m
	}
	m[roomID] = convID
	s.mu.Unlock()
}

func (s *Service) untrack(c *realtime.Conn, roomID pgtype.UUID) {
	s.mu.Lock()
	if m := s.connRooms[c]; m != nil {
		delete(m, roomID)
		if len(m) == 0 {
			delete(s.connRooms, c)
		}
	}
	s.mu.Unlock()
}

// schedulePresence 合并推送房间在线人数，同一房间每秒最多一次。
func (s *Service) schedulePresence(roomID, convID pgtype.UUID) {
	s.mu.Lock()
	st := s.presencePush[roomID]
	if st == nil {
		st = &presenceState{}
		s.presencePush[roomID] = st
	}
	now := s.now()
	if now.Sub(st.lastAt) >= presencePushInterval {
		st.lastAt = now
		s.mu.Unlock()
		s.pushPresence(roomID, convID)
		return
	}
	if st.timer == nil {
		delay := presencePushInterval - now.Sub(st.lastAt)
		st.timer = time.AfterFunc(delay, func() {
			s.mu.Lock()
			st.timer = nil
			st.lastAt = s.now()
			s.mu.Unlock()
			s.pushPresence(roomID, convID)
		})
	}
	s.mu.Unlock()
}

// pushPresence 立即广播当前在线人数。
func (s *Service) pushPresence(roomID, convID pgtype.UUID) {
	online := s.hub.Count(presenceKey(roomID))
	env, err := realtime.NewEnvelope(realtime.TypeRoomPresence, "", map[string]any{
		"roomId": roomID.String(),
		"online": online,
	})
	if err != nil {
		return
	}
	s.hub.Broadcast(convID, env, nil)
}

func (s *Service) internalError(c *realtime.Conn, reqID string, err error) {
	s.logger.Error("聊天室内部错误", "error", err)
	c.SendError(reqID, realtime.CodeInternal, "服务器内部错误")
}

// parseUUID 解析 UUID 字符串。
func parseUUID(str string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(str); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	return u, nil
}
