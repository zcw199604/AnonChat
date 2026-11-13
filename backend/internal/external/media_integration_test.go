//go:build integration

package external

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anonchat/anonchat/backend/internal/conversation"
	"github.com/anonchat/anonchat/backend/internal/external/chat2019"
	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
	"github.com/anonchat/anonchat/backend/internal/media"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bridgeMediaService(t *testing.T) *media.Service {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL required")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	storage, err := media.NewStorage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return media.New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)), storage, 1)
}

var bridgePNG = []byte{'\x89', 'P', 'N', 'G', '\r', '\n', '\x1a', '\n', 0, 0, 0, 13, 'I', 'H', 'D', 'R', 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0}

type mediaUpstream struct {
	t            *testing.T
	srv          *httptest.Server
	api          *wanfeng.APIClient
	mu           sync.Mutex
	conn         *websocket.Conn
	key          []byte
	frames       chan providerWFFrame
	downloads    atomic.Int32
	uploads      atomic.Int32
	rejectUpload atomic.Bool
	uploadData   []byte
}

func newMediaUpstream(t *testing.T) *mediaUpstream {
	f := &mediaUpstream{t: t, frames: make(chan providerWFFrame, 100)}
	p := wanfeng.DefaultProtocol()
	mux := http.NewServeMux()
	mux.HandleFunc(p.HandshakePath, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Pub string `json:"pub"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		kp, err := ecdh.P256().GenerateKey(rand.Reader)
		if err != nil {
			t.Error(err)
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(req.Pub)
		pub, err := ecdh.P256().NewPublicKey(raw)
		if err != nil {
			t.Error(err)
			return
		}
		shared, err := kp.ECDH(pub)
		if err != nil {
			t.Error(err)
			return
		}
		key, err := hkdf.Key(sha256.New, shared, []byte("media-session"), p.HKDFInfo, 32)
		if err != nil {
			t.Error(err)
			return
		}
		f.mu.Lock()
		f.key = key
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"sid": "media-session", "pub": base64.StdEncoding.EncodeToString(kp.PublicKey().Bytes()), "exp": time.Now().Add(time.Hour).UnixMilli()}})
	})
	mux.HandleFunc(p.RefreshExpiryPath, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{}})
	})
	mux.HandleFunc(p.UploadPath, func(w http.ResponseWriter, r *http.Request) {
		if f.rejectUpload.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		file, header, err := r.FormFile(p.UploadFileField)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(file)
		if err != nil {
			t.Error(err)
			return
		}
		if header.Filename == "" {
			t.Error("empty filename")
		}
		if r.FormValue(p.UploadBucketField) != p.BucketImage || r.FormValue(p.UploadUserField) != "remote" {
			t.Error("wrong upload identity or bucket")
		}
		f.mu.Lock()
		f.uploadData = data
		f.mu.Unlock()
		f.uploads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"filePath": "/files/out.png"}})
	})
	mux.HandleFunc("/files/in.png", func(w http.ResponseWriter, r *http.Request) {
		f.downloads.Add(1)
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(bridgePNG)
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		f.mu.Lock()
		f.conn = conn
		f.mu.Unlock()
		first := true
		for {
			var frame struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			f.mu.Lock()
			key := append([]byte(nil), f.key...)
			f.mu.Unlock()
			plain, err := f.api.OpenFrameData(&wanfeng.Session{ID: "media-session", Key: key}, frame.Data)
			if err != nil {
				t.Error(err)
				return
			}
			var data map[string]any
			if err = json.Unmarshal(plain, &data); err != nil {
				t.Error(err)
				return
			}
			f.frames <- providerWFFrame{frame.Type, data}
			if first {
				first = false
				f.push(p.EventConnected, map[string]any{"uid": "remote", "state": p.StateIdle})
			}
		}
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	guard, err := NewNetGuard(Policy{AllowPrivate: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	f.api = wanfeng.NewAPIClient(wanfeng.APIClientConfig{BaseURL: f.srv.URL, WSURL: "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/ws", Protocol: p, HTTPClient: guard.HTTPClient(time.Second)})
	return f
}
func (f *mediaUpstream) push(kind string, data map[string]any) {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.conn.WriteJSON(map[string]any{"type": kind, "data": data}); err != nil {
		f.t.Error(err)
	}
}
func (f *mediaUpstream) wait(kind string) map[string]any {
	f.t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case fr := <-f.frames:
			if fr.action == kind {
				return fr.payload
			}
		case <-timer.C:
			f.t.Fatalf("missing upstream frame %s", kind)
			return nil
		}
	}
}

func TestBridgeRealWanfengMediaRoundTrip(t *testing.T) {
	service := bridgeMediaService(t)
	upstream := newMediaUpstream(t)
	b, p := testBridge(t)
	hub := &correctionHub{}
	b.deps.Hub = hub
	p.name = "wanfeng"
	b.deps.Media = service
	b.deps.Wanfeng = upstream.api
	host, _ := url.Parse(upstream.srv.URL)
	b.deps.WanfengMediaHosts = []string{host.Hostname()}
	bindings := wanfeng.NewMemoryBindingStore()
	req := bridgeRequest(1)
	if err := bindings.Save(context.Background(), wanfeng.Binding{IdentityID: req.Identity.ID.String(), UpstreamUID: "remote", DeviceID: "device", Nickname: "me"}); err != nil {
		t.Fatal(err)
	}
	var adapter *linkMedia
	p.openFn = func(l *link) (session, error) {
		adapter = &linkMedia{b: b, l: l, api: upstream.api.Clone()}
		adapter.api.SetUploadUserID("remote")
		guard, err := NewNetGuard(Policy{AllowPrivate: true}, "")
		if err != nil {
			return nil, err
		}
		return connectWanfeng(context.Background(), wanfeng.ClientConfig{IdentityID: l.userID.String(), API: upstream.api, Bindings: bindings, Dialer: guard.GorillaDialer(), Profile: wanfeng.Profile{Nickname: "me"}, Media: adapter, Emitter: bridgeWanfengEmitter{emit: func(f map[string]any) { b.emitFrame(l, f) }}, OnCommandFailed: func(cmd wanfeng.OutgoingCommand, err error) { b.commandFailed(l, err, cmd.Media != nil) }, OnConnState: func(s wanfeng.ConnState, err error) {
			if s == wanfeng.ConnLost || s == wanfeng.ConnStopped {
				b.connectionLost(l, err)
			}
		}})
	}
	var mu sync.Mutex
	var received []conversation.SendInput
	b.send = func(_ context.Context, in conversation.SendInput) (store.Message, bool, error) {
		mu.Lock()
		received = append(received, in)
		mu.Unlock()
		return store.Message{}, true, nil
	}
	var ended atomic.Int32
	b.end = func(context.Context, pgtype.UUID, string) error { ended.Add(1); return nil }
	l := startBridge(t, b, req)
	upstream.wait(wanfeng.DefaultProtocol().ActionJoinMatch)
	upstream.push("match_result", map[string]any{"targetUid": "peer", "partner": map[string]any{"uid": "peer", "nickname": "friend"}})
	waitBridge(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return l.gateOpen })
	upstream.push("receive_message", map[string]any{"fromUid": "peer", "type": "text", "content": "hello", "tid": "text-1"})
	waitBridge(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(received) == 1 })
	item, err := service.SaveExternalItem(context.Background(), "out.png", bytes.NewReader(bridgePNG))
	if err != nil {
		t.Fatal(err)
	}
	b.loadRelay = func(_ context.Context, id int64) (store.GetMessageForRelayRow, error) {
		row := store.GetMessageForRelayRow{ConversationID: l.convID, SenderIdentityID: l.userID, Kind: "text", Body: "[private.jpg]"}
		if id >= 2 {
			row.Kind = "image"
			row.MediaID = item.ID
			row.Body = "caption"
			if id == 3 {
				row.Body = ""
			}
		}
		return row, nil
	}
	b.OnMessage(conversation.MessageEvent{ConversationID: l.convID, SenderIdentityID: l.userID, MessageID: 1, Kind: "text"})
	if frame := upstream.wait("send_message"); frame["type"] != "text" || frame["content"] != "[private.jpg]" {
		t.Fatalf("text interpreted as media: %v", frame)
	}
	upstream.push("receive_message", map[string]any{"fromUid": "peer", "type": "image", "content": "/files/in.png", "tid": "media-1"})
	waitBridge(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(received) == 2 })
	mu.Lock()
	got := received[1]
	mu.Unlock()
	if got.Kind != "image" || !got.MediaID.Valid || got.Identity.ID != l.peerIdent || !got.SkipRateLimit {
		t.Fatalf("inbound media: %+v", got)
	}
	token, err := adapter.MirrorInbound(l.userID.String(), "image", "/files/in.png?duplicate=1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, "anonchat-") || len(strings.TrimSuffix(strings.TrimPrefix(token, "anonchat-"), ".png")) != 32 || upstream.downloads.Load() != 1 {
		t.Fatalf("token/cache: %q downloads=%d", token, upstream.downloads.Load())
	}
	b.emitFrame(l, textFrame("peer", "media-2", "["+token+"]"))
	waitBridge(t, func() bool { mu.Lock(); defer mu.Unlock(); return len(received) == 3 })
	mu.Lock()
	repeated := received[2]
	mu.Unlock()
	if repeated.Kind != "image" || repeated.MediaID != got.MediaID {
		t.Fatalf("cached inbound media became text: %+v", repeated)
	}
	b.OnMessage(conversation.MessageEvent{ConversationID: l.convID, SenderIdentityID: l.userID, MessageID: 2, Kind: "image"})
	if frame := upstream.wait("send_message"); frame["type"] != "image" || frame["content"] != upstream.srv.URL+"/files/out.png" {
		t.Fatalf("outbound media: %v", frame)
	}
	if frame := upstream.wait("send_message"); frame["type"] != "text" || frame["content"] != "caption" {
		t.Fatalf("caption ordering: %v", frame)
	}
	if upstream.uploads.Load() != 1 {
		t.Fatal("missing upload")
	}
	upstream.mu.Lock()
	same := bytes.Equal(upstream.uploadData, bridgePNG)
	upstream.mu.Unlock()
	if !same {
		t.Fatal("upload bytes changed")
	}
	if _, err := adapter.MirrorInbound(l.userID.String(), "image", upstream.srv.URL+"/files/out.png"); err != nil || upstream.downloads.Load() != 1 {
		t.Fatalf("outbound echo downloaded again: %v", err)
	}
	upstream.rejectUpload.Store(true)
	b.OnMessage(conversation.MessageEvent{ConversationID: l.convID, SenderIdentityID: l.userID, MessageID: 3, Kind: "image"})
	waitBridge(t, func() bool { hub.mu.Lock(); defer hub.mu.Unlock(); return len(hub.events) == 1 })
	hub.mu.Lock()
	var failure map[string]string
	err = json.Unmarshal(hub.events[0].Data, &failure)
	hub.mu.Unlock()
	if err != nil || failure["code"] != "media_rejected" || failure["message"] != "对方站点暂不支持该媒体，发送失败" {
		t.Fatalf("media failure: %v %v", failure, err)
	}
	upstream.mu.Lock()
	_ = upstream.conn.Close()
	upstream.mu.Unlock()
	waitBridge(t, func() bool { return ended.Load() == 1 })
}

type mediaRoundTripper func(*http.Request) (*http.Response, error)

func (f mediaRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type mediaFrameSender struct{ frames []map[string]any }

func (s *mediaFrameSender) Send(f map[string]any) error { s.frames = append(s.frames, f); return nil }
func (s *mediaFrameSender) Close()                      {}

func TestBridgeChatMediaUsesSniffedKindAndUpload(t *testing.T) {
	svc := bridgeMediaService(t)
	webm := []byte{0x1a, 0x45, 0xdf, 0xa3, 0x93, 0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}
	var downloads, uploads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/server":
			_ = json.NewEncoder(w).Encode(map[string]any{"State": "OK", "Msg": map[string]any{"server": "media.invalid"}})
		case "/asmx/upload.asmx/ProcessRequest":
			f, _, err := r.FormFile("upload_file")
			if err != nil {
				t.Error(err)
				return
			}
			defer f.Close()
			data, _ := io.ReadAll(f)
			if !bytes.Equal(data, bridgePNG) {
				t.Error("wrong upload bytes")
			}
			uploads.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"State": "OK", "Msg": "remote.png"})
		default:
			downloads.Add(1)
			w.Header().Set("Content-Type", "video/mp4")
			if strings.HasSuffix(r.URL.Path, "video.jpg") {
				_, _ = w.Write(webm)
				return
			}
			_, _ = w.Write(bridgePNG)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	client := &http.Client{Transport: mediaRoundTripper(func(r *http.Request) (*http.Response, error) {
		clone := r.Clone(r.Context())
		u := *r.URL
		u.Host = base.Host
		u.Scheme = base.Scheme
		clone.URL = &u
		return http.DefaultTransport.RoundTrip(clone)
	})}
	b, _ := testBridge(t)
	b.deps.Media = svc
	b.chatMedia = chat2019.NewMedia(chat2019.Config{HTTPClient: client, ImgServerURL: server.URL + "/server"}, 1<<20)
	l := startBridge(t, b, bridgeRequest(1))
	sender := &mediaFrameSender{}
	l.mu.Lock()
	l.sess = &chat2019Session{conn: sender, self: chat2019.Self{UID: "self"}}
	l.state = linkInRoom
	l.gateOpen = true
	l.peerUID = "peer"
	l.convID = bridgeUUID()
	var received conversation.SendInput
	b.send = func(_ context.Context, in conversation.SendInput) (store.Message, bool, error) {
		received = in
		return store.Message{}, true, nil
	}
	b.handleFrameLocked(l, textFrame("peer", "image-1", "[wrong-extension.mp4]"))
	l.mu.Unlock()
	if received.Kind != "image" || !received.MediaID.Valid || !received.SkipRateLimit || downloads.Load() != 1 {
		t.Fatalf("sniffed inbound: %+v downloads=%d", received, downloads.Load())
	}
	l.mu.Lock()
	err := b.sendMediaLocked(context.Background(), l, store.GetMessageForRelayRow{Kind: "image", MediaID: received.MediaID})
	l.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if uploads.Load() != 1 || len(sender.frames) != 1 || sender.frames[0]["msg"] != "[remote.png]" {
		t.Fatalf("chat upload: %v uploads=%d", sender.frames, uploads.Load())
	}
	l.mu.Lock()
	b.handleFrameLocked(l, textFrame("peer", "video-1", "[video.jpg]"))
	l.mu.Unlock()
	if received.Kind != "video" || !received.MediaID.Valid {
		t.Fatalf("video sniffed as image: %+v", received)
	}
}

func TestBridgeMediaTokenOneUseEvenWhenFileReadFails(t *testing.T) {
	b, _ := testBridge(t)
	b.deps.Media = bridgeMediaService(t)
	l := startBridge(t, b, bridgeRequest(1))
	token := "anonchat-0123456789abcdef0123456789abcdef.jpg"
	l.mu.Lock()
	l.state = linkInRoom
	l.outboundTokens[token] = bridgeUUID()
	l.mu.Unlock()
	m := &linkMedia{b: b, l: l}
	if _, err := m.MirrorOutbound(l.userID.String(), "image", token); err == nil {
		t.Fatal("missing file accepted")
	}
	l.mu.Lock()
	_, exists := l.outboundTokens[token]
	l.mu.Unlock()
	if exists {
		t.Fatal("failed token can be reused")
	}
	if _, err := m.MirrorOutbound(l.userID.String(), "image", token); err == nil {
		t.Fatal("token replay accepted")
	}
}
