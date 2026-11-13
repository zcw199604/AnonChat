package external

import (
	"context"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/jackc/pgx/v5/pgtype"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/store"
)

func TestMediaUnknownWanfengTokenRemainsText(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	l.mu.Lock()
	l.provider = &fakeProvider{name: "wanfeng"}
	l.state, l.gateOpen, l.peerUID = linkInRoom, true, "peer"
	var received []conversation.SendInput
	b.send = func(_ context.Context, in conversation.SendInput) (store.Message, bool, error) {
		received = append(received, in)
		return store.Message{}, true, nil
	}
	b.handleFrameLocked(l, map[string]any{"code": 7, "fromuser": map[string]any{"id": "peer"}, "content": "[anonchat-0123456789abcdef0123456789abcdef.jpg]"})
	l.mu.Unlock()
	if len(received) != 1 || received[0].Kind != "text" || received[0].MediaID.Valid {
		t.Fatalf("未登记token必须作为普通文本: %+v", received)
	}
}

func TestMediaRejectsUnregisteredAndOtherLinkTokens(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	l.mu.Lock()
	l.state = linkInRoom
	l.provider = &fakeProvider{name: "wanfeng"}
	l.mu.Unlock()
	m := &linkMedia{b: b, l: l}
	for _, identity := range []string{l.userID.String(), bridgeUUID().String()} {
		if _, err := m.MirrorOutbound(identity, "image", "anonchat-0123456789abcdef0123456789abcdef.jpg"); err == nil {
			t.Fatal("未登记/其他身份token被接受")
		}
	}
	other := &link{userID: bridgeUUID(), inboundTokens: map[string]pgtype.UUID{"anonchat-0123456789abcdef0123456789abcdef.jpg": bridgeUUID()}}
	l.mu.Lock()
	if b.receiveMediaLocked(l, wanfeng.MediaToken{Kind: "image", Path: "anonchat-0123456789abcdef0123456789abcdef.jpg"}, "tid") {
		t.Fatal("其他link的入站token被接受")
	}
	l.mu.Unlock()
	if len(other.inboundTokens) != 1 {
		t.Fatal("其他link被修改")
	}
}

func TestMediaRejectsUnlistedHostBeforeNetworkOrCache(t *testing.T) {
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	l.mu.Lock()
	l.state = linkInRoom
	l.mu.Unlock()
	b.deps.Wanfeng = wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: "https://allowed.example"})
	b.deps.WanfengMediaHosts = []string{"allowed.example"}
	m := &linkMedia{b: b, l: l, cache: map[string]mediaEcho{"x.jpg": {token: "cached", until: time.Now().Add(time.Hour)}}}
	for _, raw := range []string{"https://evil.example/x.jpg", "//evil.example/x.jpg", "file:///x.jpg", "https://user@allowed.example/x.jpg"} {
		if _, err := m.MirrorInbound(l.userID.String(), "image", raw); err == nil {
			t.Fatalf("accepted forbidden URL %s", raw)
		}
	}
}

func TestMediaQueueOverflowOnlyNotifiesForMedia(t *testing.T) {
	b, _ := testBridge(t)
	h := &correctionHub{}
	b.deps.Hub = h
	id, user := bridgeUUID(), bridgeUUID()
	l := &link{provider: &fakeProvider{name: "wanfeng"}, userID: user, done: make(chan struct{}), outbox: make(chan outboundItem, 1)}
	b.convs[id] = l
	l.outbox <- outboundItem{}
	for _, kind := range []string{"text", "image"} {
		b.OnMessage(conversation.MessageEvent{ConversationID: id, SenderIdentityID: user, Kind: kind})
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.events) != 1 {
		t.Fatalf("queue overflow events=%d", len(h.events))
	}
}

func TestMediaCloseCancelsInFlightDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	defer server.Close()
	b, _ := testBridge(t)
	l := startBridge(t, b, bridgeRequest(1))
	l.mu.Lock()
	l.state = linkInRoom
	l.mu.Unlock()
	host, _ := url.Parse(server.URL)
	b.deps.Wanfeng = wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: server.URL, HTTPClient: server.Client()})
	b.deps.WanfengMediaHosts = []string{host.Hostname()}
	m := &linkMedia{b: b, l: l, api: b.deps.Wanfeng}
	done := make(chan error, 1)
	go func() { _, err := m.MirrorInbound(l.userID.String(), "image", "/slow.png"); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("download did not start")
	}
	b.closeLink(l, false)
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("closed link completed download")
		}
	case <-time.After(time.Second):
		t.Fatal("closed link did not cancel download")
	}
}
