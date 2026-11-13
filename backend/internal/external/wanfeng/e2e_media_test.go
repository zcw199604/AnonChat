package wanfeng

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeUpstream 是一个最小可用的「晚风站点」模拟实现，用于端到端验证协议适配与媒体搬运。
// 它复用本包的加密工具（服务端视角），因此同时校验了握手/派生/信封的一致性。
type fakeUpstream struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	key      []byte
	sid      string
	frames   []map[string]any // 收到的下行动作（解密后的 data）
	uploaded []fakeUpload
	mediaURL string

	connectedSent chan struct{}
	conn          *websocket.Conn

	// apiData 按路径返回固定的加密响应，供历史/关系类接口测试使用。
	apiData   map[string]map[string]any
	apiParams map[string]map[string]any // 记录每个路径收到的（解密后）参数
}

type fakeUpload struct {
	filename string
	user     string
	bucket   string
	size     int
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	f := &fakeUpstream{
		t:             t,
		connectedSent: make(chan struct{}, 4),
		apiData:       make(map[string]map[string]any),
		apiParams:     make(map[string]map[string]any),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/app/handshake", f.handleHandshake)
	mux.HandleFunc("/api/tools/util/generate", f.handleGenerate)
	mux.HandleFunc("/api/nimingChat/account/identity-cookie", f.handleOK)
	mux.HandleFunc("/api/tools/niming-file/upload", f.handleUpload)
	mux.HandleFunc("/ws", f.handleWS)
	// 历史/关系类接口统一用一个「按路径回放」的处理器
	for _, path := range []string{
		DefaultProtocol().HomeDataPath,
		DefaultProtocol().RelationsPath,
		DefaultProtocol().FavoritesPath,
		DefaultProtocol().ChatMessagesPath,
		DefaultProtocol().RelationReadPath,
		DefaultProtocol().RelationReadAllPath,
		DefaultProtocol().RelationDeletePath,
		DefaultProtocol().RelationClearPath,
		DefaultProtocol().FavoritePinPath,
	} {
		mux.HandleFunc(path, f.handleAPIData)
	}
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeUpstream) URL() string { return f.srv.URL }

func (f *fakeUpstream) handleHandshake(w http.ResponseWriter, r *http.Request) {
	var req struct {
		V   int    `json:"v"`
		Pub string `json:"pub"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		f.t.Errorf("握手解析失败: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	serverKP, err := newHandshakeKeypair()
	if err != nil {
		f.t.Errorf("服务端密钥生成失败: %v", err)
		return
	}
	sid := "sid-" + base64.RawURLEncoding.EncodeToString([]byte(time.Now().Format("150405.000000")))
	key, err := serverKP.deriveSessionKey(req.Pub, sid, DefaultProtocol().HKDFInfo)
	if err != nil {
		f.t.Errorf("服务端派生密钥失败: %v", err)
		return
	}
	f.mu.Lock()
	f.key, f.sid = key, sid
	f.mu.Unlock()

	writeFakeJSON(w, map[string]any{
		"success": true,
		"data": map[string]any{
			"sid": sid,
			"pub": serverKP.pubB64,
			"exp": time.Now().Add(time.Hour).UnixMilli(),
		},
	})
}

func (f *fakeUpstream) handleGenerate(w http.ResponseWriter, r *http.Request) {
	body, apiErr := f.readEnvelope(r)
	if apiErr != nil {
		writeFakeJSON(w, map[string]any{"success": false, "code": "BAD_ENVELOPE", "message": "请求错误"})
		return
	}
	_ = body
	f.writeEncrypted(w, map[string]any{"uid": "WFUFAKE001", "nickname": "假用户"})
}

func (f *fakeUpstream) handleOK(w http.ResponseWriter, r *http.Request) {
	f.writeEncrypted(w, map[string]any{"ok": true})
}

func (f *fakeUpstream) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		f.t.Errorf("上传表单解析失败: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile(DefaultProtocol().UploadFileField)
	if err != nil {
		f.t.Errorf("上传缺少文件字段: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, _ := io.ReadAll(file)

	f.mu.Lock()
	f.uploaded = append(f.uploaded, fakeUpload{
		filename: header.Filename,
		user:     r.FormValue(DefaultProtocol().UploadUserField),
		bucket:   r.FormValue(DefaultProtocol().UploadBucketField),
		size:     len(data),
	})
	f.mu.Unlock()

	writeFakeJSON(w, map[string]any{
		"success": true,
		"code":    200,
		"data":    map[string]any{"filePath": "/files/fake-media.jpg"},
	})
}

// handleAPIData 解密请求参数，并按路径回放预先设置的数据。
func (f *fakeUpstream) handleAPIData(w http.ResponseWriter, r *http.Request) {
	params, err := f.readEnvelope(r)
	if err != nil {
		writeFakeJSON(w, map[string]any{"success": false, "code": "BAD_ENVELOPE", "message": "请求错误"})
		return
	}
	path := r.URL.Path

	f.mu.Lock()
	if f.apiParams == nil {
		f.apiParams = make(map[string]map[string]any)
	}
	f.apiParams[path] = params
	data, has := f.apiData[path]
	f.mu.Unlock()

	if !has {
		f.writeEncrypted(w, map[string]any{})
		return
	}
	f.writeEncrypted(w, data)
}

func (f *fakeUpstream) handleWS(w http.ResponseWriter, r *http.Request) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		f.t.Errorf("WS 升级失败: %v", err)
		return
	}
	defer conn.Close()

	// 发送 connected（加密）
	f.mu.Lock()
	key, sid := f.key, f.sid
	f.mu.Unlock()
	if len(key) == 0 {
		f.t.Errorf("WS 连接前未握手成功")
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"uid": "WFUFAKE001", "state": "IDLE", "identityToken": "v1.fake-token",
	})
	env, err := sealEnvelope(key, sid, payload, DefaultProtocol().EnvelopePrefix)
	if err != nil {
		f.t.Errorf("构造 connected 帧失败: %v", err)
		return
	}
	if err := conn.WriteJSON(map[string]any{"type": "connected", "data": env, "v": 3}); err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	f.connectedSent <- struct{}{}

	for {
		var frame struct {
			Type string `json:"type"`
			Data any    `json:"data"`
		}
		if err := conn.ReadJSON(&frame); err != nil {
			return
		}
		data := f.openFrame(frame.Data, key, sid)
		if data == nil {
			continue
		}
		f.mu.Lock()
		f.frames = append(f.frames, map[string]any{"type": frame.Type, "data": data})
		f.mu.Unlock()
	}
}

func (f *fakeUpstream) openFrame(raw any, key []byte, sid string) map[string]any {
	str, _ := raw.(string)
	if str == "" {
		if m, ok := raw.(map[string]any); ok {
			return m
		}
		return nil
	}
	plain, err := openEnvelope(key, sid, str, DefaultProtocol().EnvelopePrefix)
	if err != nil {
		return nil
	}
	var out map[string]any
	if json.Unmarshal(plain, &out) != nil {
		return nil
	}
	return out
}

func (f *fakeUpstream) readEnvelope(r *http.Request) (map[string]any, error) {
	var body struct {
		V   int    `json:"v"`
		SID string `json:"sid"`
		IV  string `json:"iv"`
		CT  string `json:"ct"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, err
	}
	f.mu.Lock()
	key := f.key
	f.mu.Unlock()
	if len(key) == 0 {
		return nil, ErrBadEnvelope
	}
	plain, err := openParts(key, body.SID, body.IV, body.CT)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (f *fakeUpstream) writeEncrypted(w http.ResponseWriter, data map[string]any) {
	plain, _ := json.Marshal(data)
	f.mu.Lock()
	key, sid := f.key, f.sid
	f.mu.Unlock()
	env, err := sealEnvelope(key, sid, plain, DefaultProtocol().EnvelopePrefix)
	if err != nil {
		f.t.Errorf("加密响应失败: %v", err)
		return
	}
	writeFakeJSON(w, map[string]any{"success": true, "data": env})
}

// framesWithType 返回指定类型的下行动作。
func (f *fakeUpstream) framesWithType(name string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.frames))
	for _, fr := range f.frames {
		if fr["type"] == name {
			if data, ok := fr["data"].(map[string]any); ok {
				out = append(out, data)
			}
		}
	}
	return out
}

func (f *fakeUpstream) uploads() []fakeUpload {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeUpload(nil), f.uploaded...)
}

// pushFrame 模拟服务端推送一帧（加密）。
func (f *fakeUpstream) pushFrame(frameType string, data map[string]any) error {
	f.mu.Lock()
	key, sid, conn := f.key, f.sid, f.conn
	f.mu.Unlock()
	if conn == nil {
		return errors.New("尚未建立 WS 连接")
	}
	plain, _ := json.Marshal(data)
	env, err := sealEnvelope(key, sid, plain, DefaultProtocol().EnvelopePrefix)
	if err != nil {
		return err
	}
	return conn.WriteJSON(map[string]any{"type": frameType, "data": env, "v": 3})
}

func writeFakeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeUpstream) waitFrames(t *testing.T, name string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.framesWithType(name); len(got) >= want {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待 %s 帧超时，实际收到 %+v", name, f.framesWithType(name))
	return nil
}

// newTestClient 创建指向 fakeUpstream 的客户端。
func newTestClient(t *testing.T, f *fakeUpstream, bridge MediaBridge, emitter Emitter) *Client {
	t.Helper()
	api := NewAPIClient(APIClientConfig{
		BaseURL:  f.URL(),
		Protocol: DefaultProtocol(),
	})
	return NewClient(ClientConfig{
		IdentityID: "me-id",
		Profile:    Profile{Nickname: "我", Gender: "male"},
		API:        api,
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
		Media:      bridge,
	})
}

// TestEndToEndSendImage 验证「前端 [文件名] → 上传到对方 → 以 URL 发送」的完整链路。
func TestEndToEndSendImage(t *testing.T) {
	f := newFakeUpstream(t)
	bridge := &fakeMediaBridge{outboundURL: "https://cdn.example/files/fake-media.jpg"}
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, bridge, emitter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()

	<-f.connectedSent

	proto := DefaultProtocol()
	cmd, ok := proto.BuildCommand(ClientFrame{
		Act: "touser_WAT999_对方", ID: "me-id", Msg: "[2026/11/07/Random/pic.png]",
	}, "WAT999")
	if !ok {
		t.Fatal("媒体消息应可翻译")
	}
	client.SendCommand(cmd)

	frames := f.waitFrames(t, proto.ActionSendMessage, 1)
	payload := frames[0]
	if payload["type"] != MediaKindImage {
		t.Fatalf("应以 image 类型发送，实际 %+v", payload)
	}
	if payload["content"] != "https://cdn.example/files/fake-media.jpg" {
		t.Fatalf("content 应为对方可访问 URL，实际 %v", payload["content"])
	}
	if payload["targetUid"] != "WAT999" {
		t.Fatalf("目标用户不符: %v", payload["targetUid"])
	}
	if len(bridge.outboundCalls) != 1 || bridge.outboundCalls[0] != "image:2026/11/07/Random/pic.png" {
		t.Fatalf("应先搬运媒体，实际 %+v", bridge.outboundCalls)
	}
}

// TestEndToEndReceiveImage 验证「对方推图片 URL → 转存 → 下发 [文件名]」的完整链路。
func TestEndToEndReceiveImage(t *testing.T) {
	f := newFakeUpstream(t)
	bridge := &fakeMediaBridge{inboundName: "2026/11/07/Random/inbound.jpg"}
	emitter := &recordingEmitter{}
	client := newTestClient(t, f, bridge, emitter)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client.Start(ctx)
	defer client.Close()

	<-f.connectedSent

	// 模拟对方推送一条图片消息（走真实 WS 帧 + 加密信封）
	if err := f.pushFrame("receive_message", map[string]any{
		"type":    "image",
		"content": "https://wanfengniming.store/files/x.jpg",
		"fromUid": "WAT999",
		"tid":     "t-media",
		"ts":      float64(1700000000000),
	}); err != nil {
		t.Fatalf("推送消息失败: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && len(emitter.withCode(codePrivateMessage)) == 0 {
		time.Sleep(50 * time.Millisecond)
	}

	if len(bridge.inboundCalls) != 1 {
		t.Fatalf("应转存一次，实际 %+v", bridge.inboundCalls)
	}
	// code=12 是「连接成功」提示，这里只关心私聊消息
	msgs := emitter.withCode(codePrivateMessage)
	if len(msgs) != 1 {
		t.Fatalf("应下发一条私聊消息，实际 %+v", emitter.list())
	}
	msg := msgs[0]
	if msg["code"] != codePrivateMessage || msg["tid"] != "t-media" {
		t.Fatalf("媒体消息头部不符: %+v", msg)
	}
	fromUser := msg["fromuser"].(map[string]any)
	if fromUser["content"] != "[2026/11/07/Random/inbound.jpg]" {
		t.Fatalf("内容应为 [文件名]，实际 %v", fromUser["content"])
	}
	if fromUser["id"] != "WAT999" {
		t.Fatalf("发送者应为对方，实际 %v", fromUser["id"])
	}
}

// TestEndToEndUploadMedia 验证 UploadMedia 与对方上传接口的字段约定。
func TestEndToEndUploadMedia(t *testing.T) {
	f := newFakeUpstream(t)
	api := NewAPIClient(APIClientConfig{BaseURL: f.URL(), Protocol: DefaultProtocol()})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	result, err := api.UploadMedia(ctx, "pic.png", "image/png", []byte("fake-image-bytes"), "")
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if !strings.HasPrefix(result.URL, f.URL()) || result.FileID != "fake-media.jpg" {
		t.Fatalf("上传结果不符: %+v", result)
	}

	uploads := f.uploads()
	if len(uploads) != 1 {
		t.Fatalf("应记录一次上传，实际 %+v", uploads)
	}
	if uploads[0].filename != "pic.png" || uploads[0].size != len("fake-image-bytes") {
		t.Fatalf("上传表单不符: %+v", uploads[0])
	}
	if uploads[0].bucket != DefaultProtocol().BucketImage {
		t.Fatalf("应按桶名上传，实际 %q", uploads[0].bucket)
	}
}
