// Package external 管理外站接入设置和连接。
package external

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// Settings 是生效设置的内存快照，所有开关默认关闭。
type Settings struct {
	Enabled              bool
	Chat2019Enabled      bool
	WanfengEnabled       bool
	OnlineThreshold      int
	LocalWait            time.Duration
	ExternalMatchTimeout time.Duration
	Chat2019MaxSessions  int
	WanfengMaxSessions   int
}

// SettingsCache 启动时加载单行设置，后台保存后立即更新快照。
type SettingsCache struct {
	q        *store.Queries
	updateMu sync.Mutex // 串行化数据库写入、快照更新和通知，保持顺序一致。
	mu       sync.RWMutex
	current  Settings
	onChange func(Settings)
}

func NewSettingsCache(ctx context.Context, pool *pgxpool.Pool) (*SettingsCache, error) {
	c := &SettingsCache{q: store.New(pool)}
	if err := c.q.EnsureExternalSettings(ctx); err != nil {
		return nil, err
	}
	row, err := c.q.GetExternalSettings(ctx)
	if err != nil {
		return nil, err
	}
	c.current = settingsFromRow(row)
	return c, nil
}

// Plan 只读内存，可安全地用于匹配器的阶段判断。
func (c *SettingsCache) Plan() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.current
}

// OnChange 注册保存后的同步通知，供 Bridge.ApplySettings 使用。
// 通知与保存串行，回调可以读取 Plan，但不能重入 Update 或 OnChange。
func (c *SettingsCache) OnChange(fn func(Settings)) {
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	c.onChange = fn
}

// UpdateInput 的时长字段以整数秒表示，与后台 API 和 SQL CHECK 保持一致。
type UpdateInput struct {
	Enabled              bool `json:"enabled"`
	Chat2019Enabled      bool `json:"chat2019Enabled"`
	WanfengEnabled       bool `json:"wanfengEnabled"`
	OnlineThreshold      int  `json:"onlineThreshold"`
	LocalWaitSeconds     int  `json:"localWaitSeconds"`
	ExternalMatchSeconds int  `json:"externalMatchSeconds"`
	Chat2019MaxSessions  int  `json:"chat2019MaxSessions"`
	WanfengMaxSessions   int  `json:"wanfengMaxSessions"`
}

func (in UpdateInput) Validate() error {
	for _, v := range []struct {
		name            string
		value, min, max int
	}{
		{"onlineThreshold", in.OnlineThreshold, 0, 100000},
		{"localWaitSeconds", in.LocalWaitSeconds, 0, 600},
		{"externalMatchSeconds", in.ExternalMatchSeconds, 3, 120},
		{"chat2019MaxSessions", in.Chat2019MaxSessions, 1, 1000},
		{"wanfengMaxSessions", in.WanfengMaxSessions, 1, 1000},
	} {
		if v.value < v.min || v.value > v.max {
			return httpx.NewError(httpx.CodeInvalidRequest, v.name+" 超出允许范围")
		}
	}
	return nil
}

func (c *SettingsCache) Update(ctx context.Context, in UpdateInput, adminID pgtype.UUID) (Settings, error) {
	if err := in.Validate(); err != nil {
		return Settings{}, err
	}
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	// 请求已解码并校验后，保存必须完成到缓存和通知；不随 HTTP 断开而取消。
	// 单独设定数据库超时，避免请求取消发生在提交之后、返回结果之前造成快照滞后。
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	row, err := c.q.UpdateExternalSettings(writeCtx, store.UpdateExternalSettingsParams{
		Enabled: in.Enabled, Chat2019Enabled: in.Chat2019Enabled, WanfengEnabled: in.WanfengEnabled,
		OnlineThreshold: int32(in.OnlineThreshold), LocalWaitSeconds: int32(in.LocalWaitSeconds), ExternalMatchSeconds: int32(in.ExternalMatchSeconds),
		Chat2019MaxSessions: int32(in.Chat2019MaxSessions), WanfengMaxSessions: int32(in.WanfengMaxSessions), UpdatedBy: adminID,
	})
	if err != nil {
		return Settings{}, err
	}
	s := settingsFromRow(row)
	c.mu.Lock()
	c.current = s
	c.mu.Unlock()
	if c.onChange != nil {
		c.onChange(s)
	}
	return s, nil
}

func settingsFromRow(row store.ExternalSetting) Settings {
	return Settings{Enabled: row.Enabled, Chat2019Enabled: row.Chat2019Enabled, WanfengEnabled: row.WanfengEnabled,
		OnlineThreshold: int(row.OnlineThreshold), LocalWait: time.Duration(row.LocalWaitSeconds) * time.Second,
		ExternalMatchTimeout: time.Duration(row.ExternalMatchSeconds) * time.Second,
		Chat2019MaxSessions:  int(row.Chat2019MaxSessions), WanfengMaxSessions: int(row.WanfengMaxSessions)}
}
