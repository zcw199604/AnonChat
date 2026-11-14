package external

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/jackc/pgx/v5/pgtype"
)

var ErrNewIdentityLimited = errors.New("新账号注册限速")
var ErrIdentityReset = errors.New("晚风身份绑定失效")
var ErrWanfengHandshake = errors.New("晚风握手失败")

type wanfengProvider struct{ b *Bridge }

func (p *wanfengProvider) Name() string { return "wanfeng" }
func (p *wanfengProvider) available(pgtype.UUID) (bool, string) {
	if p.b.deps.Wanfeng == nil || p.b.deps.WanfengDialer == nil {
		return false, "晚风未装配"
	}
	if p.b.bindings == nil || !p.b.bindings.WanfengAvailable() {
		return false, "晚风未配置加密密钥"
	}
	return true, ""
}
func (p *wanfengProvider) open(ctx context.Context, l *link) (session, error) {
	binding, err := p.b.bindings.Get(ctx, l.userID.String())
	if err != nil {
		return nil, err
	}
	if (binding == nil || binding.UpstreamUID == "") && !p.b.limiter.Allow() {
		return nil, ErrNewIdentityLimited
	}
	api := p.b.deps.Wanfeng.Clone()
	if _, err := api.Session(ctx); err != nil {
		return nil, errors.Join(ErrWanfengHandshake, err)
	}
	nickname := ""
	if binding != nil {
		nickname = externalNickname(binding.Nickname)
	}
	if nickname == "" {
		nickname = identity.GeneratePlainNickname()
	}
	profile := wanfeng.Profile{Nickname: nickname, Gender: l.req.Context.Profile.Gender}
	if age := l.req.Context.Profile.Age; age != nil {
		profile.Age = int(*age)
	}
	store := wanfengLinkBindings{BindingStore: p.b.bindings,
		reset:   func(ctx context.Context) error { return p.b.bindings.Delete(ctx, l.userID, "wanfeng") },
		onReset: func(err error) { p.b.connectionLost(l, err) },
	}
	return connectWanfeng(ctx, wanfeng.ClientConfig{
		IdentityID: l.userID.String(), Profile: profile, API: api,
		Bindings: store, Dialer: p.b.deps.WanfengDialer, Logger: p.b.deps.Logger,
		Media: &linkMedia{b: p.b, l: l},
		Emitter: bridgeWanfengEmitter{
			emit:  func(f map[string]any) { p.b.emitFrame(l, f) },
			fatal: func(err error) { p.b.connectionLost(l, err) },
		},
		OnConnState: func(state wanfeng.ConnState, err error) {
			if state == wanfeng.ConnLost || state == wanfeng.ConnStopped {
				p.b.connectionLost(l, err)
			}
		},
		OnCommandFailed: func(cmd wanfeng.OutgoingCommand, err error) {
			p.b.commandFailed(l, err, cmd.Media != nil)
		},
	})
}

type wanfengCommandClient interface {
	SendCommand(wanfeng.OutgoingCommand)
	Close()
	IsReady() bool
}
type wanfengSession struct {
	client  wanfengCommandClient
	proto   wanfeng.Protocol
	uid     string
	localID string
	cancel  context.CancelFunc
}

func (s *wanfengSession) command(cmd wanfeng.OutgoingCommand) error {
	if !s.client.IsReady() {
		return errors.New("晚风连接未就绪")
	}
	s.client.SendCommand(cmd)
	return nil
}
func (s *wanfengSession) action(act string) error {
	raw, err := json.Marshal(map[string]any{"act": act, "id": s.uid})
	if err != nil {
		return err
	}
	frame, err := wanfeng.ParseClientFrame(string(raw))
	if err != nil {
		return err
	}
	cmd, ok := s.proto.BuildCommand(frame, "")
	if !ok {
		return errors.New("晚风不支持该动作")
	}
	return s.command(cmd)
}
func (s *wanfengSession) startMatch() error  { return s.action("random") }
func (s *wanfengSession) cancelMatch() error { return s.action("randomOut") }
func (s *wanfengSession) leaveRoom() error   { return s.action("randomOut") }
func (s *wanfengSession) sendText(peerUID, peerNick, text string) error {
	return s.command(s.proto.TextCommand(peerUID, text))
}
func (s *wanfengSession) sendMedia(peerUID, peerNick, kind, token string) error {
	return s.command(s.proto.MediaCommand(peerUID, kind, token))
}

// 晚风适配器以 ClientConfig.IdentityID 生成自回显的 fromuser.id。
func (s *wanfengSession) selfID() string { return s.localID }
func (s *wanfengSession) close() {
	s.cancel()
	s.client.Close()
}

// connectWanfeng 的 ctx 只限制打开连接；连接成功后由 session.close 管理生命周期。
// 第一次断开即关闭客户端，不使用移植包的内部自动重连。
func connectWanfeng(ctx context.Context, cfg wanfeng.ClientConfig) (*wanfengSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{}, 1)
	failed := make(chan error, 1)
	var firstFailure sync.Once
	var client *wanfeng.Client
	fail := func(err error) {
		if err == nil {
			err = errors.New("晚风连接已停止")
		}
		firstFailure.Do(func() {
			failed <- err
			cancel()
			// ConnStopped 可由 Close 同步回调；不可在此重入 Close 的 sync.Once。
			go client.Close()
		})
	}
	originalState, originalEmitter := cfg.OnConnState, cfg.Emitter
	cfg.Emitter = bridgeWanfengEmitter{
		emit: func(f map[string]any) {
			if originalEmitter != nil {
				originalEmitter.Emit(cfg.IdentityID, f)
			}
		},
		fatal: func(err error) {
			fail(err)
			if originalEmitter != nil {
				originalEmitter.EmitFatal(cfg.IdentityID, map[string]any{"content": err.Error()})
			}
		},
	}
	hidden := false
	cfg.Profile.ShowCity = &hidden
	cfg.OnConnState = func(state wanfeng.ConnState, err error) {
		if state == wanfeng.ConnReady {
			// 移植包在通知 ConnReady 之后才配置 ShowCity；先完成隐私设置再允许匹配。
			if err := client.PushShowCity(false); err != nil {
				fail(err)
				return
			}
			select {
			case ready <- struct{}{}:
			default:
			}
		} else {
			fail(err)
		}
		if originalState != nil {
			originalState(state, err)
		}
	}
	client = wanfeng.NewClient(cfg)
	client.Start(life)
	select {
	case <-ctx.Done():
		cancel()
		client.Close()
		return nil, ctx.Err()
	case err := <-failed:
		cancel()
		client.Close()
		return nil, err
	case <-ready:
	}
	// Ready 和断开可能同时排队；已经断开的连接不能交给 StartMatch。
	select {
	case err := <-failed:
		cancel()
		client.Close()
		return nil, err
	default:
	}
	binding, err := cfg.Bindings.Get(ctx, cfg.IdentityID)
	if err != nil || binding == nil || binding.UpstreamUID == "" {
		cancel()
		client.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("晚风连接就绪后没有身份绑定")
	}
	return &wanfengSession{client: client, proto: cfg.API.Protocol(), uid: binding.UpstreamUID, localID: cfg.IdentityID, cancel: cancel}, nil
}

type bridgeWanfengEmitter struct {
	emit  func(map[string]any)
	fatal func(error)
}

func (e bridgeWanfengEmitter) Emit(_ string, frame map[string]any) {
	if e.emit != nil {
		e.emit(frame)
	}
}
func (e bridgeWanfengEmitter) EmitFatal(_ string, frame map[string]any) {
	if e.fatal != nil {
		e.fatal(errors.New(wanfeng.PickString(frame, "content", "message", "msg")))
	}
}

// 移植包通过清空 uid/device 请求重置；Bridge 删除整个绑定并停止该连接，避免自动重新注册。
type wanfengLinkBindings struct {
	wanfeng.BindingStore
	reset   func(context.Context) error
	onReset func(error)
}

func (s wanfengLinkBindings) Save(ctx context.Context, binding wanfeng.Binding) error {
	if binding.UpstreamUID != "" && binding.DeviceID != "" {
		return s.BindingStore.Save(ctx, binding)
	}
	err := s.reset(ctx)
	reason := "DEVICE_DUPLICATE"
	if binding.UpstreamUID == "" {
		reason = "IDENTITY_INVALID"
	}
	s.onReset(errors.Join(fmt.Errorf("%w: %s", ErrIdentityReset, reason), err))
	return err
}
