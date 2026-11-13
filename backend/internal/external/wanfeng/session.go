package wanfeng

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// 会话提前失效的余量：避免到期瞬间的请求失败。
const sessionExpiryMargin = 60 * time.Second

// Session 表示一次 ECDH 握手得到的加密会话。
type Session struct {
	// ID 对应对方返回的 sid（同时作为 AES-GCM 的 AAD）。
	ID string
	// Key 是派生出的 AES-256 会话密钥。
	Key []byte
	// Exp 是会话过期时间（对方返回的 exp，缺省按 23 小时）。
	Exp time.Time
}

// Valid 判断会话是否仍可用。
func (s *Session) Valid(now time.Time) bool {
	if s == nil || s.ID == "" || len(s.Key) == 0 {
		return false
	}
	if s.Exp.IsZero() {
		return true
	}
	return now.Add(sessionExpiryMargin).Before(s.Exp)
}

// sessionStore 缓存当前会话，并用 singleflight 合并并发握手。
type sessionStore struct {
	fetch func(ctx context.Context) (*Session, error)
	now   func() time.Time

	mu  sync.Mutex
	cur *Session
	sf  singleflight.Group
}

func newSessionStore(fetch func(ctx context.Context) (*Session, error), now func() time.Time) *sessionStore {
	if now == nil {
		now = time.Now
	}
	return &sessionStore{fetch: fetch, now: now}
}

// get 返回可用会话，必要时重新握手（并发请求只会触发一次握手）。
func (s *sessionStore) get(ctx context.Context) (*Session, error) {
	if sess := s.cached(); sess != nil {
		return sess, nil
	}

	v, err, _ := s.sf.Do("handshake", func() (any, error) {
		// double check：并发场景下别的协程可能已完成握手
		if sess := s.cached(); sess != nil {
			return sess, nil
		}
		sess, err := s.fetch(ctx)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.cur = sess
		s.mu.Unlock()
		return sess, nil
	})
	if err != nil {
		return nil, err
	}
	sess, _ := v.(*Session)
	if sess == nil {
		return nil, ErrBadEnvelope
	}
	return sess, nil
}

func (s *sessionStore) cached() *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur.Valid(s.now()) {
		return s.cur
	}
	return nil
}

// invalidate 主动作废会话（对方返回 401/40310 时使用）。
func (s *sessionStore) invalidate(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" || s.cur == nil || s.cur.ID == id {
		s.cur = nil
	}
}
