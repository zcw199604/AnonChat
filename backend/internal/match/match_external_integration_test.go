//go:build integration

package match_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

type externalIntegrationMatcher struct{ started chan match.ExternalRequest }

func (f *externalIntegrationMatcher) Plan() match.ExternalPlan {
	return match.ExternalPlan{Enabled: true, OnlineThreshold: 100, MatchTimeout: 10 * time.Second}
}
func (f *externalIntegrationMatcher) StartMatch(_ context.Context, r match.ExternalRequest) error {
	f.started <- r
	return nil
}
func (f *externalIntegrationMatcher) CancelMatch(pgtype.UUID, uint64) {}

type externalRecordingHub struct {
	realtime.Realtime
	mu                  sync.Mutex
	subscriptions       []pgtype.UUID
	found               []pgtype.UUID
	activated           bool
	foundBeforeActivate bool
}

func (h *externalRecordingHub) SubscribeIdentity(id, conv pgtype.UUID) {
	h.mu.Lock()
	h.subscriptions = append(h.subscriptions, id)
	h.mu.Unlock()
	h.Realtime.SubscribeIdentity(id, conv)
}
func (h *externalRecordingHub) SendToIdentity(id pgtype.UUID, e realtime.Envelope) {
	h.mu.Lock()
	if e.Type == realtime.TypeMatchFound {
		h.found = append(h.found, id)
		h.foundBeforeActivate = h.foundBeforeActivate || !h.activated
	}
	h.mu.Unlock()
	h.Realtime.SendToIdentity(id, e)
}

type externalIntegrationEnv struct {
	pool       *pgxpool.Pool
	env        *testutil.Env
	service    *match.Service
	ext        *externalIntegrationMatcher
	hub        *externalRecordingHub
	user, peer store.Identity
	ws         *websocket.Conn
	request    match.ExternalRequest
}

func externalIntegrationSetup(t *testing.T) *externalIntegrationEnv {
	t.Helper()
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	hub := &externalRecordingHub{Realtime: env.Deps.Realtime.Hub()}
	var ns atomic.Int64
	ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	service := match.New(pool, slog.Default(), env.Deps.Conversation, hub, nil, time.Hour, time.Second, match.WithClock(func() time.Time { return time.Unix(0, ns.Load()) }), match.WithTick(time.Millisecond))
	ext := &externalIntegrationMatcher{started: make(chan match.ExternalRequest, 8)}
	service.SetExternalMatcher(ext, hub.OnlineCount)
	service.RegisterWS(env.Deps.Realtime.Router(), "off")
	env.Deps.Realtime.AddObserver(service)
	runCtx, cancel := context.WithCancel(context.Background())
	go service.Run(runCtx)
	t.Cleanup(cancel)
	user, token := mkIdentity(t, pool, "external-user")
	peer, _ := mkIdentity(t, pool, "external-peer")
	ws := connectHello(t, env.Server, token)
	matchStart(t, ws)
	if e := read(t, ws, time.Second); e.Type != realtime.TypeMatchWaiting {
		t.Fatalf("expected waiting: %s", e.Type)
	}
	var req match.ExternalRequest
	select {
	case req = <-ext.started:
	case <-time.After(300 * time.Millisecond):
		t.Fatal("外站匹配未开始")
	}
	return &externalIntegrationEnv{pool, env, service, ext, hub, user, peer, ws, req}
}

func TestExternalFoundCommitsBeforeActivateAndPublishesInsideHook(t *testing.T) {
	f := externalIntegrationSetup(t)
	age := int16(25)
	province := "浙江"
	peer := match.ExternalPeer{IdentityID: f.peer.ID, Profile: match.PrefProfile{Gender: "female", Age: &age, Province: &province, ProvincePublic: true}}
	created := false
	convID, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, peer, match.ExternalHooks{
		OnCreated: func(ctx context.Context, tx pgx.Tx, id pgtype.UUID) error {
			created = true
			var n int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM conversation_members WHERE conversation_id=$1", id).Scan(&n); err != nil {
				return err
			}
			if n != 2 {
				t.Error("hook 前未创建双方成员")
			}
			return store.New(tx).InsertExternalConversation(ctx, store.InsertExternalConversationParams{ConversationID: id, Provider: "chat2019", UserIdentityID: f.user.ID, PeerIdentityID: f.peer.ID, RemotePeerUid: "remote"})
		},
		Activate: func(id pgtype.UUID, publish func()) bool {
			if !created {
				t.Error("Activate 早于 OnCreated")
			}
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM external_conversations WHERE conversation_id=$1", id).Scan(&n); err != nil || n != 1 {
				t.Errorf("Activate 前事务未提交: %d %v", n, err)
			}
			f.hub.mu.Lock()
			if len(f.hub.found) != 0 {
				t.Error("Activate 前已推送 found")
			}
			f.hub.activated = true
			f.hub.mu.Unlock()
			publish()
			f.hub.mu.Lock()
			defer f.hub.mu.Unlock()
			if len(f.hub.found) != 1 || len(f.hub.subscriptions) != 2 {
				t.Error("publish 没有同步订阅双方并推送用户")
			}
			return true
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	found := read(t, f.ws, time.Second)
	var payload struct {
		ConversationID string `json:"conversationId"`
		Peer           struct {
			MemberID int    `json:"memberId"`
			Gender   string `json:"gender"`
			Age      int    `json:"age"`
			Province string `json:"province"`
		} `json:"peer"`
	}
	if err := json.Unmarshal(found.Data, &payload); err != nil {
		t.Fatal(err)
	}
	if found.Type != realtime.TypeMatchFound || payload.ConversationID != convID.String() || payload.Peer.MemberID != 2 || payload.Peer.Gender != "female" || payload.Peer.Age != 25 || payload.Peer.Province != province {
		t.Fatalf("found 会话或对方资料不符: %s %s", found.Type, found.Data)
	}
	if f.service.QueueLen() != 0 {
		t.Fatal("成功后仍在队列")
	}
	f.hub.mu.Lock()
	if f.hub.foundBeforeActivate {
		t.Error("发现 Activate 前推送")
	}
	f.hub.mu.Unlock()
	// 实际推送证明用户已被订阅到新会话。
	event, _ := realtime.NewEnvelope(realtime.TypeMsgNew, "", map[string]any{"conversationId": convID.String()})
	f.hub.Broadcast(convID, event, nil)
	if e := read(t, f.ws, time.Second); e.Type != realtime.TypeMsgNew {
		t.Fatalf("用户未订阅会话: %s", e.Type)
	}
	if _, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, peer, match.ExternalHooks{}); !errors.Is(err, match.ErrNotQueued) {
		t.Fatalf("已认领结果不应再次生效: %v", err)
	}
}

func TestExternalFoundRejectedActivationEndsAndRequeues(t *testing.T) {
	f := externalIntegrationSetup(t)
	var created pgtype.UUID
	_, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, match.ExternalPeer{IdentityID: f.peer.ID, Nickname: f.peer.Nickname, AvatarSeed: f.peer.AvatarSeed}, match.ExternalHooks{
		Activate: func(id pgtype.UUID, _ func()) bool { created = id; return false },
	})
	if !errors.Is(err, match.ErrActivationRejected) {
		t.Fatalf("want activation rejection, got %v", err)
	}
	conv, e := store.New(f.pool).GetConversation(ctx, created)
	if e != nil || !conv.EndedAt.Valid || conv.EndReason == nil || *conv.EndReason != "peer_left" {
		t.Fatalf("拒绝激活未结束会话: %+v %v", conv, e)
	}
	if f.service.QueueLen() != 1 {
		t.Fatal("拒绝激活未回队")
	}
	f.hub.mu.Lock()
	defer f.hub.mu.Unlock()
	if len(f.hub.found) != 0 || len(f.hub.subscriptions) != 0 {
		t.Fatal("拒绝激活不应订阅或推送 found")
	}
}

func TestExternalFoundFailureRequeueRespectsIntentAtSameTime(t *testing.T) {
	for _, action := range []string{"none", "cancel", "reenter", "cancel-reenter-cancel", "success-after-cancel"} {
		t.Run(action, func(t *testing.T) {
			f := externalIntegrationSetup(t)
			entered, release := make(chan struct{}), make(chan struct{})
			released := false
			defer func() {
				if !released {
					close(release)
				}
			}()
			result := make(chan error, 1)
			injected := errors.New("creation failed")
			go func() {
				_, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, match.ExternalPeer{IdentityID: f.peer.ID, Nickname: f.peer.Nickname, AvatarSeed: f.peer.AvatarSeed}, match.ExternalHooks{
					OnCreated: func(context.Context, pgx.Tx, pgtype.UUID) error {
						close(entered)
						<-release
						if action == "success-after-cancel" {
							return nil
						}
						return injected
					},
					Activate: func(_ pgtype.UUID, publish func()) bool {
						f.hub.mu.Lock()
						f.hub.activated = true
						f.hub.mu.Unlock()
						publish()
						return true
					},
				})
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(300 * time.Millisecond):
				t.Fatal("认领后未进入事务 hook")
			}
			cancelMatch := func() {
				send(t, f.ws, map[string]any{"t": realtime.TypeMatchCancel, "id": "cancel", "d": map[string]any{}})
				if e := read(t, f.ws, time.Second); e.Type != realtime.TypeAck {
					t.Fatalf("取消未确认: %s", e.Type)
				}
			}
			if action == "cancel" || action == "cancel-reenter-cancel" || action == "success-after-cancel" {
				cancelMatch()
			}
			var next match.ExternalRequest
			if action == "reenter" || action == "cancel-reenter-cancel" {
				matchStart(t, f.ws)
				if e := read(t, f.ws, time.Second); e.Type != realtime.TypeMatchWaiting {
					t.Fatalf("重新入队未 waiting: %s", e.Type)
				}
				select {
				case next = <-f.ext.started:
				case <-time.After(300 * time.Millisecond):
					t.Fatal("重新匹配未开始")
				}
				if next.Attempt <= f.request.Attempt {
					t.Fatal("新请求复用旧代次")
				}
				if action == "cancel-reenter-cancel" {
					cancelMatch()
				}
			}
			close(release)
			released = true
			var err error
			select {
			case err = <-result:
			case <-time.After(time.Second):
				t.Fatal("ExternalFound 未完成")
			}
			if action == "success-after-cancel" {
				if err != nil {
					t.Fatal(err)
				}
				mustFound(t, f.ws)
				return
			}
			if !errors.Is(err, injected) {
				t.Fatalf("未返回建会话错误: %v", err)
			}
			var n int
			if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM conversations WHERE kind='match'").Scan(&n); err != nil || n != 0 {
				t.Fatalf("失败事务未回滚: %d %v", n, err)
			}
			want := 0
			if action == "none" || action == "reenter" {
				want = 1
			}
			if f.service.QueueLen() != want {
				t.Fatalf("队列=%d want %d", f.service.QueueLen(), want)
			}
			// 失败回队处于兜底，不会再启动外站；重入则新代次仍可认领。
			select {
			case <-f.ext.started:
				t.Fatal("失败回队不应重新进入外站")
			default:
			}
			if action == "reenter" {
				if _, err := f.service.ExternalFound(ctx, f.user.ID, next.Attempt, match.ExternalPeer{IdentityID: f.peer.ID, Nickname: f.peer.Nickname, AvatarSeed: f.peer.AvatarSeed}, match.ExternalHooks{Activate: func(_ pgtype.UUID, publish func()) bool { publish(); return true }}); err != nil {
					t.Fatalf("失败回队污染新请求: %v", err)
				}
			}
		})
	}
}

func TestExternalFoundInvalidPeerRollsBackAndRequeues(t *testing.T) {
	f := externalIntegrationSetup(t)
	_, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, match.ExternalPeer{}, match.ExternalHooks{})
	if err == nil || errors.Is(err, match.ErrNotQueued) {
		t.Fatalf("应认领后建会话失败: %v", err)
	}
	if f.service.QueueLen() != 1 {
		t.Fatal("建会话失败未回队")
	}
}

func TestExternalFoundBoundsDatabaseContext(t *testing.T) {
	f := externalIntegrationSetup(t)
	_, err := f.service.ExternalFound(ctx, f.user.ID, f.request.Attempt, match.ExternalPeer{IdentityID: f.peer.ID, Nickname: f.peer.Nickname, AvatarSeed: f.peer.AvatarSeed}, match.ExternalHooks{
		OnCreated: func(ctx context.Context, _ pgx.Tx, _ pgtype.UUID) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second {
				return errors.New("ExternalFound 数据库阶段没有 5 秒超时")
			}
			return nil
		},
		Activate: func(_ pgtype.UUID, publish func()) bool { publish(); return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	// 外站配对同样在 match.found 里透传影子身份（即外站对方）的昵称与头像种子。
	_, nick, seed := readFoundPayload(t, mustFoundEnv(t, f.ws))
	if nick != f.peer.Nickname || seed != f.peer.AvatarSeed || nick == "" {
		t.Fatalf("外站 match.found 的 peer 昵称/头像 = %q/%q, 期望 %q/%q", nick, seed, f.peer.Nickname, f.peer.AvatarSeed)
	}
}
