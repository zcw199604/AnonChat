package wanfeng

import (
	"context"
	"sync"
)

// Binding 记录「我方身份」与「对方站点身份」的绑定关系。
// 说明：对方会下发 uid 与 identityToken，必须持久化，否则每次重连都会变成新用户。
type Binding struct {
	// IdentityID 是我方身份 id（前端 id）。
	IdentityID string
	// UpstreamUID 是对方分配的 uid。
	UpstreamUID string
	// IdentityToken 是对方下发的身份令牌，重连时必须携带。
	IdentityToken string
	// DeviceID 是设备标识；对方限制「同一设备只能登录一个账号」，因此必须按身份独立并持久化。
	DeviceID string
	// 以下为最近一次上送/回执的资料，便于排查。
	Nickname string
	Gender   string
	Age      int
}

// BindingStore 负责持久化绑定关系（由 app 层用数据库实现）。
type BindingStore interface {
	Get(ctx context.Context, identityID string) (*Binding, error)
	Save(ctx context.Context, binding Binding) error
}

// MemoryBindingStore 是内存实现，便于测试与无数据库场景。
type MemoryBindingStore struct {
	mu   sync.RWMutex
	data map[string]Binding
}

// NewMemoryBindingStore 创建内存绑定存储。
func NewMemoryBindingStore() *MemoryBindingStore {
	return &MemoryBindingStore{data: make(map[string]Binding)}
}

// Get 读取绑定。
func (s *MemoryBindingStore) Get(_ context.Context, identityID string) (*Binding, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.RLock()
	b, ok := s.data[identityID]
	s.mu.RUnlock()
	if !ok {
		return nil, nil
	}
	cp := b
	return &cp, nil
}

// Save 写入绑定。
func (s *MemoryBindingStore) Save(_ context.Context, binding Binding) error {
	if s == nil || binding.IdentityID == "" {
		return nil
	}
	s.mu.Lock()
	s.data[binding.IdentityID] = binding
	s.mu.Unlock()
	return nil
}
