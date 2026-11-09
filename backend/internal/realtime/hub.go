package realtime

import (
	"sort"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgtype"
)

// closeAllCap 是 CloseAll 等待全部底层连接关闭的总时限。
const closeAllCap = 5 * time.Second

// Hub 是进程内的连接注册表，同时实现 Broker 与 Presence。
// 维护：identity → 连接集合、conversation → 订阅连接集合、presence key → 身份引用计数。
type Hub struct {
	mu         sync.RWMutex
	byIdentity map[pgtype.UUID]map[*Conn]struct{}
	byIP       map[string]map[*Conn]struct{}
	connSubs   map[*Conn]map[pgtype.UUID]struct{}
	byConv     map[pgtype.UUID]map[*Conn]struct{}
	// presence[key][identityID] = 该身份在 key 下的连接数（引用计数）。
	presence map[string]map[pgtype.UUID]int
	// connPresence[c] = 该连接登记过的 presence key 集合，供断开时自动清理。
	connPresence map[*Conn]map[string]struct{}
}

// NewHub 创建一个空的内存 Hub。
func NewHub() *Hub {
	return &Hub{
		byIdentity:   make(map[pgtype.UUID]map[*Conn]struct{}),
		byIP:         make(map[string]map[*Conn]struct{}),
		connSubs:     make(map[*Conn]map[pgtype.UUID]struct{}),
		byConv:       make(map[pgtype.UUID]map[*Conn]struct{}),
		presence:     make(map[string]map[pgtype.UUID]int),
		connPresence: make(map[*Conn]map[string]struct{}),
	}
}

// addConn 注册一个连接。
func (h *Hub) addConn(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set := h.byIdentity[c.identity.ID]
	if set == nil {
		set = make(map[*Conn]struct{})
		h.byIdentity[c.identity.ID] = set
	}
	set[c] = struct{}{}
	if c.ip != "" {
		ipset := h.byIP[c.ip]
		if ipset == nil {
			ipset = make(map[*Conn]struct{})
			h.byIP[c.ip] = ipset
		}
		ipset[c] = struct{}{}
	}
	if _, ok := h.connSubs[c]; !ok {
		h.connSubs[c] = make(map[pgtype.UUID]struct{})
	}
	if _, ok := h.connPresence[c]; !ok {
		h.connPresence[c] = make(map[string]struct{})
	}
}

// removeConn 注销连接并清理其订阅与 presence 登记。
func (h *Hub) removeConn(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if set, ok := h.byIdentity[c.identity.ID]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.byIdentity, c.identity.ID)
		}
	}
	if c.ip != "" {
		if set, ok := h.byIP[c.ip]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.byIP, c.ip)
			}
		}
	}
	for conv := range h.connSubs[c] {
		if set, ok := h.byConv[conv]; ok {
			delete(set, c)
			if len(set) == 0 {
				delete(h.byConv, conv)
			}
		}
	}
	delete(h.connSubs, c)
	// 清理该连接登记的全部 presence key。
	for key := range h.connPresence[c] {
		h.decPresenceLocked(key, c.identity.ID)
	}
	delete(h.connPresence, c)
}

// Subscribe 让连接订阅某会话（供 conv.subscribe 处理使用）。
func (h *Hub) Subscribe(c *Conn, conv pgtype.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subscribeLocked(c, conv)
}

// subscribeLocked 在持锁情况下登记订阅。
func (h *Hub) subscribeLocked(c *Conn, conv pgtype.UUID) {
	subs, ok := h.connSubs[c]
	if !ok {
		subs = make(map[pgtype.UUID]struct{})
		h.connSubs[c] = subs
	}
	if _, exists := subs[conv]; exists {
		return
	}
	subs[conv] = struct{}{}
	set := h.byConv[conv]
	if set == nil {
		set = make(map[*Conn]struct{})
		h.byConv[conv] = set
	}
	set[c] = struct{}{}
}

// SubscribeIdentity 把某身份的全部连接订阅到某会话（供匹配成功使用）。
func (h *Hub) SubscribeIdentity(identityID, conv pgtype.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.byIdentity[identityID] {
		h.subscribeLocked(c, conv)
	}
}

// Unsubscribe 取消连接对某会话的订阅。
func (h *Hub) Unsubscribe(c *Conn, conv pgtype.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if subs, ok := h.connSubs[c]; ok {
		delete(subs, conv)
	}
	if set, ok := h.byConv[conv]; ok {
		delete(set, c)
		if len(set) == 0 {
			delete(h.byConv, conv)
		}
	}
}

// PresenceHas 报告某身份是否在某个 presence key 下在线。
func (h *Hub) PresenceHas(key string, identityID pgtype.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.presence[key][identityID]
	return ok
}

// ConnCount 返回某身份当前的连接数（测试与运维用）。
func (h *Hub) ConnCount(identityID pgtype.UUID) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byIdentity[identityID])
}

// Broadcast 实现 Broker：向会话的全部订阅连接广播，排除 exclude 中的身份。
func (h *Hub) Broadcast(conversationID pgtype.UUID, env Envelope, exclude map[pgtype.UUID]struct{}) {
	h.mu.RLock()
	targets := make([]*Conn, 0, len(h.byConv[conversationID]))
	for c := range h.byConv[conversationID] {
		if _, skip := exclude[c.identity.ID]; skip {
			continue
		}
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(env)
	}
}

// SendToIdentity 实现 Broker：向某身份的全部连接发送。
func (h *Hub) SendToIdentity(identityID pgtype.UUID, env Envelope) {
	h.mu.RLock()
	targets := make([]*Conn, 0, len(h.byIdentity[identityID]))
	for c := range h.byIdentity[identityID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(env)
	}
}

// Add 实现 Presence：登记连接在某 key 的在线状态（按身份引用计数）。
func (h *Hub) Add(key string, c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := h.presence[key]
	if m == nil {
		m = make(map[pgtype.UUID]int)
		h.presence[key] = m
	}
	m[c.identity.ID]++
	keys := h.connPresence[c]
	if keys == nil {
		keys = make(map[string]struct{})
		h.connPresence[c] = keys
	}
	keys[key] = struct{}{}
}

// Remove 实现 Presence：注销连接在某 key 的在线状态。
func (h *Hub) Remove(key string, c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.decPresenceLocked(key, c.identity.ID)
	if keys, ok := h.connPresence[c]; ok {
		delete(keys, key)
	}
}

// decPresenceLocked 递减引用计数，为 0 时删除身份登记。
func (h *Hub) decPresenceLocked(key string, identityID pgtype.UUID) {
	m := h.presence[key]
	if m == nil {
		return
	}
	if n := m[identityID]; n > 1 {
		m[identityID] = n - 1
	} else {
		delete(m, identityID)
	}
	if len(m) == 0 {
		delete(h.presence, key)
	}
}

// Count 实现 Presence：返回某 key 的在线身份数。
func (h *Hub) Count(key string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.presence[key])
}

// Kick 向该身份的全部连接发送 kicked 消息后关闭连接（非阻塞）。
func (h *Hub) Kick(identityID pgtype.UUID, reason string) {
	h.KickIdentity(identityID, reason)
}

// KickIdentity 向某身份的全部连接发送 kicked 后关闭。
func (h *Hub) KickIdentity(identityID pgtype.UUID, reason string) {
	h.mu.RLock()
	targets := make([]*Conn, 0, len(h.byIdentity[identityID]))
	for c := range h.byIdentity[identityID] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	h.kickConns(targets, reason)
}

// KickIP 向来自某 IP 的全部连接发送 kicked 后关闭。
func (h *Hub) KickIP(ip string, reason string) {
	h.mu.RLock()
	targets := make([]*Conn, 0, len(h.byIP[ip]))
	for c := range h.byIP[ip] {
		targets = append(targets, c)
	}
	h.mu.RUnlock()
	h.kickConns(targets, reason)
}

// KickAccount 向某实名账号绑定的全部身份的连接发送 kicked 后关闭。
func (h *Hub) KickAccount(accountID pgtype.UUID, reason string) {
	h.mu.RLock()
	var targets []*Conn
	for _, set := range h.byIdentity {
		for c := range set {
			if c.identity.AccountID.Valid && c.identity.AccountID == accountID {
				targets = append(targets, c)
			}
		}
	}
	h.mu.RUnlock()
	h.kickConns(targets, reason)
}

// kickConns 向给定连接发送 kicked 并关闭。
func (h *Hub) kickConns(targets []*Conn, reason string) {
	env, err := NewEnvelope(TypeKicked, "", KickedPayload{Reason: reason})
	if err != nil {
		return
	}
	for _, c := range targets {
		c.sendThenClose(env, websocket.StatusPolicyViolation, reason)
	}
}

// CloseAll 关闭全部连接（用于优雅关闭）：并发关闭，整体最多等待 closeAllCap。
func (h *Hub) CloseAll(code websocket.StatusCode, reason string) {
	h.mu.RLock()
	var targets []*Conn
	for _, set := range h.byIdentity {
		for c := range set {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	// 触发全部关闭（各自异步关闭底层连接）。
	for _, c := range targets {
		c.closeWith(code, reason)
	}
	// 并发等待底层连接真正关闭，整体设置上限。
	var wg sync.WaitGroup
	for _, c := range targets {
		wg.Add(1)
		go func(c *Conn) {
			defer wg.Done()
			select {
			case <-c.closed:
			case <-time.After(closeAllCap):
			}
		}(c)
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeAllCap):
	}
}

// TryAdd 原子地把连接登记到 presence key：若该身份已在房间则不受上限约束；
// 否则在达到 limit 时拒绝。返回是否成功与当前在线身份数。
func (h *Hub) TryAdd(key string, c *Conn, limit int) (bool, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := h.presence[key]
	if m == nil {
		m = make(map[pgtype.UUID]int)
		h.presence[key] = m
	}
	if _, exists := m[c.identity.ID]; !exists && limit > 0 && len(m) >= limit {
		return false, len(m)
	}
	m[c.identity.ID]++
	keys := h.connPresence[c]
	if keys == nil {
		keys = make(map[string]struct{})
		h.connPresence[c] = keys
	}
	keys[key] = struct{}{}
	return true, len(m)
}

// OnlineCount 返回当前在线的身份数（按身份去重）。
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.byIdentity)
}

// OnlineIdentity 是某在线身份的聚合快照（供管理后台展示）。
type OnlineIdentity struct {
	IdentityID  pgtype.UUID
	PublicCode  string
	Nickname    string
	AccountID   pgtype.UUID
	IPs         []string // 去重、排序
	ConnCount   int
	ConnectedAt time.Time // 该身份最早一条在线连接的建立时间
	UserAgent   string    // 最近建立的那条连接的 UA
}

// OnlineSnapshot 聚合当前全部在线身份（每个身份一条），按上线时间降序返回。
// 只拷贝值，不暴露 Hub 内部的连接指针或 map。
func (h *Hub) OnlineSnapshot() []OnlineIdentity {
	h.mu.RLock()
	defer h.mu.RUnlock()

	out := make([]OnlineIdentity, 0, len(h.byIdentity))
	for identityID, set := range h.byIdentity {
		if len(set) == 0 {
			continue
		}
		// 用集合去重 IP，同时保留最早/最新连接的信息。
		ipSet := make(map[string]struct{}, len(set))
		var earliest time.Time
		var latest time.Time
		userAgent := ""
		for c := range set {
			if c.ip != "" {
				ipSet[c.ip] = struct{}{}
			}
			at := c.connectedAt
			if at.IsZero() {
				continue
			}
			if earliest.IsZero() || at.Before(earliest) {
				earliest = at
			}
			if latest.IsZero() || at.After(latest) {
				latest = at
				userAgent = c.userAgent
			}
		}
		ips := make([]string, 0, len(ipSet))
		for ip := range ipSet {
			ips = append(ips, ip)
		}
		sort.Strings(ips)

		item := OnlineIdentity{
			IdentityID:  identityID,
			IPs:         ips,
			ConnCount:   len(set),
			ConnectedAt: earliest,
			UserAgent:   userAgent,
		}
		// 任取一条连接取身份字段（同一身份的各连接共享同一身份记录）。
		for c := range set {
			if c.identity != nil {
				item.PublicCode = c.identity.PublicCode
				item.Nickname = c.identity.Nickname
				item.AccountID = c.identity.AccountID
			}
			break
		}
		out = append(out, item)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ConnectedAt.Equal(out[j].ConnectedAt) {
			return out[i].IdentityID.String() < out[j].IdentityID.String()
		}
		return out[i].ConnectedAt.After(out[j].ConnectedAt)
	})
	return out
}
