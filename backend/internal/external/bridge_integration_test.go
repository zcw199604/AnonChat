//go:build integration

package external

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/match"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/migrations"
	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// This integration fixture creates its own database to avoid importing testutil
// (which assembles api and therefore imports external).
func bridgeIntegrationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	raw := os.Getenv("TEST_DATABASE_URL")
	if raw == "" {
		t.Skip("TEST_DATABASE_URL 未设置")
	}
	cfg, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ConnConfig.Host != "127.0.0.1" && cfg.ConnConfig.Host != "localhost" {
		t.Fatal("Bridge集成测试仅允许本机数据库")
	}
	ctx := context.Background()
	adminPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	name := "anonchat_bridge_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = adminPool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := adminPool.Exec(context.Background(), "DROP DATABASE "+quoted+" WITH (FORCE)"); err != nil {
			t.Error(err)
		}
		adminPool.Close()
	})
	cfg = cfg.Copy()
	cfg.ConnConfig.Database = name
	migrationDB := stdlib.OpenDB(*cfg.ConnConfig)
	if _, err = migrations.Up(ctx, migrationDB); err != nil {
		migrationDB.Close()
		t.Fatal(err)
	}
	migrationDB.Close()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type bridgeE2EProvider struct {
	opened chan *link
	sent   chan string
	left   chan struct{}
}

func (p *bridgeE2EProvider) Name() string                         { return "wanfeng" }
func (p *bridgeE2EProvider) available(pgtype.UUID) (bool, string) { return true, "" }
func (p *bridgeE2EProvider) open(_ context.Context, l *link) (session, error) {
	p.opened <- l
	return &bridgeE2ESession{p: p}, nil
}

type bridgeE2ESession struct{ p *bridgeE2EProvider }

func (s *bridgeE2ESession) startMatch() error  { return nil }
func (s *bridgeE2ESession) cancelMatch() error { return nil }
func (s *bridgeE2ESession) leaveRoom() error {
	select {
	case s.p.left <- struct{}{}:
	default:
	}
	return nil
}
func (s *bridgeE2ESession) sendText(_, _, text string) error  { s.p.sent <- text; return nil }
func (s *bridgeE2ESession) sendMedia(_, _, _, _ string) error { return fmt.Errorf("媒体留待P5") }
func (s *bridgeE2ESession) selfID() string                    { return "e2e-self" }
func (s *bridgeE2ESession) close()                            {}

func bridgeE2ERead(t *testing.T, c *websocket.Conn) realtime.Envelope {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, raw, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var e realtime.Envelope
	if err = json.Unmarshal(raw, &e); err != nil {
		t.Fatal(err)
	}
	return e
}
func bridgeE2ESend(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	raw, _ := json.Marshal(v)
	if err := c.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
}
func bridgeE2EWaitType(t *testing.T, c *websocket.Conn, typ realtime.MessageType) realtime.Envelope {
	t.Helper()
	for range 8 {
		e := bridgeE2ERead(t, c)
		if e.Type == typ {
			return e
		}
		if e.Type == realtime.TypeError {
			t.Fatalf("unexpected error: %s", e.Data)
		}
	}
	t.Fatalf("未收到 %s", typ)
	return realtime.Envelope{}
}

func TestBridgeEndToEndWithFakeSession(t *testing.T) {
	pool := bridgeIntegrationPool(t)
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	idSvc := identity.NewService(pool, logger, nil)
	user, err := idSvc.Create(ctx, "127.0.0.1", "bridge-e2e")
	if err != nil {
		t.Fatal(err)
	}
	gateway := realtime.NewGateway(realtime.Options{AllowedOrigins: []string{"http://bridge.test"}}, logger, idSvc, nil, idSvc, nil)
	conv := conversation.New(pool, logger, gateway.Hub())
	svc := match.New(pool, logger, conv, gateway.Hub(), nil, time.Hour, time.Second, match.WithTick(time.Millisecond))
	settings, err := NewSettingsCache(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	_, err = settings.Update(ctx, UpdateInput{Enabled: true, WanfengEnabled: true, OnlineThreshold: 100, ExternalMatchSeconds: 10, Chat2019MaxSessions: 2, WanfengMaxSessions: 2}, pgtype.UUID{})
	if err != nil {
		t.Fatal(err)
	}
	b := NewBridge(BridgeDeps{Pool: pool, Settings: settings, Match: svc, Conv: conv, Hub: gateway.Hub(), Logger: logger})
	fake := &bridgeE2EProvider{opened: make(chan *link, 1), sent: make(chan string, 8), left: make(chan struct{}, 8)}
	b.providers = []provider{fake}
	svc.SetExternalMatcher(b, gateway.Hub().OnlineCount)
	conv.AddObserver(b)
	settings.OnChange(b.ApplySettings)
	svc.RegisterWS(gateway.Router(), "off")
	conv.RegisterWS(gateway.Router(), "off")
	gateway.AddObserver(svc)
	runCtx, cancel := context.WithCancel(ctx)
	go svc.Run(runCtx)
	t.Cleanup(cancel)
	t.Cleanup(b.Close)
	server := httptest.NewServer(http.HandlerFunc(gateway.ServeWS))
	t.Cleanup(server.Close)
	t.Cleanup(gateway.CloseAll)
	headers := http.Header{"Origin": {"http://bridge.test"}, "Cookie": {identity.CookieName + "=" + user.Token}}
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.CloseNow() })
	if e := bridgeE2ERead(t, ws); e.Type != realtime.TypeHello {
		t.Fatal(e.Type)
	}
	bridgeE2ESend(t, ws, map[string]any{"t": realtime.TypeMatchStart, "id": "start", "d": map[string]any{}})
	var l *link
	select {
	case l = <-fake.opened:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Bridge未打开假session")
	}
	b.emitFrame(l, map[string]any{"code": 15, "sel_userid": "remote-peer", "sel_userNikename": "外站昵称", "sel_userSex": "女", "sel_userAge": "25", "sel_userAddress": "浙江省杭州市"})
	found := bridgeE2EWaitType(t, ws, realtime.TypeMatchFound)
	var payload struct {
		ConversationID string `json:"conversationId"`
	}
	if err = json.Unmarshal(found.Data, &payload); err != nil {
		t.Fatal(err)
	}
	var convID pgtype.UUID
	if err = convID.Scan(payload.ConversationID); err != nil {
		t.Fatal(err)
	}
	var storedProvider string
	if err = pool.QueryRow(ctx, "SELECT provider FROM external_conversations WHERE conversation_id=$1", convID).Scan(&storedProvider); err != nil || storedProvider != "wanfeng" {
		t.Fatalf("外站会话未同事务落库: %q %v", storedProvider, err)
	}
	// User text which resembles media must remain text all the way to the session.
	bridgeE2ESend(t, ws, map[string]any{"t": realtime.TypeMsgSend, "id": "send", "d": map[string]any{"conversationId": payload.ConversationID, "clientMsgId": uuid.NewString(), "kind": "text", "body": "[x.jpg]"}})
	bridgeE2EWaitType(t, ws, realtime.TypeAck)
	select {
	case body := <-fake.sent:
		if body != "[x.jpg]" {
			t.Fatal(body)
		}
	case <-time.After(time.Second):
		t.Fatal("本站文本未转发假session")
	}
	b.emitFrame(l, map[string]any{"code": 7, "tid": "remote-tid", "fromuser": map[string]any{"id": "remote-peer", "content": "外站文本", "Tid": "remote-tid"}})
	received := false
	for range 8 {
		e := bridgeE2ERead(t, ws)
		if e.Type == realtime.TypeMsgNew && strings.Contains(string(e.Data), "外站文本") {
			received = true
			break
		}
	}
	if !received {
		t.Fatal("外站文本未广播到用户")
	}
	var sender pgtype.UUID
	if err = pool.QueryRow(ctx, "SELECT sender_identity_id FROM messages WHERE conversation_id=$1 AND body='外站文本'", convID).Scan(&sender); err != nil {
		t.Fatal(err)
	}
	if sender == user.Identity.ID {
		t.Fatal("入站文本错误使用本站用户身份")
	}
	peer, err := store.New(pool).GetExternalPeer(ctx, store.GetExternalPeerParams{Provider: "wanfeng", RemoteUid: "remote-peer"})
	if err != nil || sender != peer.IdentityID {
		t.Fatalf("入站未使用影子身份: %v", err)
	}
	b.emitFrame(l, map[string]any{"code": 16})
	ended := bridgeE2EWaitType(t, ws, realtime.TypeMatchEnded)
	if !strings.Contains(string(ended.Data), "peer_left") {
		t.Fatalf("结束原因=%s", ended.Data)
	}
	conversation, err := store.New(pool).GetConversation(ctx, convID)
	if err != nil || !conversation.EndedAt.Valid {
		t.Fatalf("会话未结束: %v", err)
	}
	select {
	case <-fake.left:
	case <-time.After(time.Second):
		t.Fatal("会话结束未leaveRoom")
	}
}
