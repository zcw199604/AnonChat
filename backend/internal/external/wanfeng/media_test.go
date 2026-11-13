package wanfeng

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

var errFakeMirror = errors.New("模拟搬运失败")

func testContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func TestMediaKindFromName(t *testing.T) {
	cases := map[string]string{
		"a.jpg":          MediaKindImage,
		"a.JPEG":         MediaKindImage,
		"dir/a.png?x=1":  MediaKindImage,
		"/images/b.webp": MediaKindImage,
		"c.mp4":          MediaKindVideo,
		"voice_1.m4a":    MediaKindAudio,
		"d.mp3":          MediaKindAudio,
		"e.txt":          "",
		"noext":          "",
		"":               "",
	}
	for name, want := range cases {
		if got := MediaKindFromName(name); got != want {
			t.Fatalf("MediaKindFromName(%q)=%q，期望 %q", name, got, want)
		}
	}
}

func TestParseMediaToken(t *testing.T) {
	// 前端现有格式：[文件名]
	token, ok := ParseMediaToken("[2026/11/a.jpg]")
	if !ok || token.Kind != MediaKindImage || token.Path != "2026/11/a.jpg" || token.Legacy {
		t.Fatalf("解析单标签媒体失败: %+v ok=%v", token, ok)
	}

	// 旧版双标签
	token, ok = ParseMediaToken("[img]/upload/images/a.png[/img]")
	if !ok || token.Kind != MediaKindImage || token.Path != "/upload/images/a.png" || !token.Legacy {
		t.Fatalf("解析旧版图片标签失败: %+v ok=%v", token, ok)
	}
	token, ok = ParseMediaToken("[video]/upload/videos/a.mp4[/video]")
	if !ok || token.Kind != MediaKindVideo {
		t.Fatalf("解析旧版视频标签失败: %+v ok=%v", token, ok)
	}

	// 表情/普通文本不能被当作媒体
	for _, raw := range []string{"[doge]", "你好", "", "[]", "[a[b].jpg]"} {
		if _, ok := ParseMediaToken(raw); ok {
			t.Fatalf("%q 不应被识别为媒体", raw)
		}
	}
}

func TestMediaContent(t *testing.T) {
	if got := MediaContent("2026/11/a.jpg"); got != "[2026/11/a.jpg]" {
		t.Fatalf("MediaContent 结果不符: %q", got)
	}
	if got := MediaContent("  "); got != "" {
		t.Fatalf("空文件名应返回空串，实际 %q", got)
	}
}

func TestBuildCommandForMedia(t *testing.T) {
	p := DefaultProtocol()

	cmd, ok := p.BuildCommand(ClientFrame{Act: "touser_WFU1_x", ID: "u1", Msg: "[2026/11/a.jpg]"}, "WFU1")
	if !ok {
		t.Fatal("媒体消息应可翻译")
	}
	if cmd.Media == nil || cmd.Media.Kind != MediaKindImage || cmd.Media.Path != "2026/11/a.jpg" {
		t.Fatalf("应标记为待搬运的图片，实际 %+v", cmd.Media)
	}
	if cmd.Payload["targetUid"] != "WFU1" {
		t.Fatalf("应保留目标用户，实际 %+v", cmd.Payload)
	}
	// 媒体消息不能在翻译阶段就带 type/content（要等搬运完成后填）
	if _, exists := cmd.Payload["content"]; exists {
		t.Fatalf("媒体消息不应预先写入 content: %+v", cmd.Payload)
	}

	// 纯文本仍走原路径
	cmd, ok = p.BuildCommand(ClientFrame{Act: "touser_WFU1_x", ID: "u1", Msg: "你好"}, "WFU1")
	if !ok || cmd.Media != nil || cmd.Payload["content"] != "你好" || cmd.Payload["type"] != "text" {
		t.Fatalf("纯文本翻译异常: %+v", cmd)
	}
}

func TestParseReceiveMedia(t *testing.T) {
	p := DefaultProtocol()

	info, ok := p.ParseReceiveMedia(map[string]any{
		"type":    "image",
		"content": "https://wanfengniming.store/files/a.jpg",
		"tid":     "t1",
		"ts":      float64(1700000000000),
	})
	if !ok || info.Kind != MediaKindImage || info.Tid != "t1" || info.URL == "" {
		t.Fatalf("解析媒体消息失败: %+v ok=%v", info, ok)
	}

	// 媒体地址放在 mediaUrl 时也应识别
	info, ok = p.ParseReceiveMedia(map[string]any{"type": "video", "mediaUrl": "https://x/v.mp4"})
	if !ok || info.Kind != MediaKindVideo || info.URL != "https://x/v.mp4" {
		t.Fatalf("应按 mediaUrl 取地址: %+v", info)
	}

	// 文本消息不算媒体
	if _, ok := p.ParseReceiveMedia(map[string]any{"type": "text", "content": "hi"}); ok {
		t.Fatal("文本消息不应识别为媒体")
	}
	// 缺地址不算媒体
	if _, ok := p.ParseReceiveMedia(map[string]any{"type": "image"}); ok {
		t.Fatal("缺地址不应识别为媒体")
	}
}

func TestMediaMessageShapes(t *testing.T) {
	p := DefaultProtocol()
	ctx := DownstreamContext{
		SelfID: "me-id", SelfUpstreamUID: "WFUME", SelfNickname: "我",
		PartnerUID: "WA1", PartnerNickname: "对方",
	}
	info := ReceiveMediaInfo{Kind: MediaKindImage, URL: "https://x/a.jpg", Tid: "t9", TS: 1700000000000}

	// 对方发来的图片
	msg := p.MediaMessage(info, "2026/11/a.jpg", ctx, false)
	if msg["code"] != codePrivateMessage || msg["tid"] != "t9" || msg["type"] != MediaKindImage {
		t.Fatalf("媒体消息基础字段不符: %+v", msg)
	}
	fromUser := msg["fromuser"].(map[string]any)
	if fromUser["id"] != "WA1" || fromUser["content"] != "[2026/11/a.jpg]" {
		t.Fatalf("fromuser 不符: %+v", fromUser)
	}

	// 自己发出去的图片回显：fromuser.id 必须是 md5(自身 id)，前端才认得出是自己发的
	selfMsg := p.MediaMessage(info, "2026/11/a.jpg", ctx, true)
	selfFrom := selfMsg["fromuser"].(map[string]any)
	if selfFrom["id"] != md5HexLower("me-id") {
		t.Fatalf("自己回显的 fromuser.id 应为 md5(自身 id)，实际 %v", selfFrom["id"])
	}
	selfTo := selfMsg["touser"].(map[string]any)
	if selfTo["id"] != "WA1" {
		t.Fatalf("自己回显的 touser.id 应为对方 uid，实际 %v", selfTo["id"])
	}
}

func TestSendMediaPayload(t *testing.T) {
	p := DefaultProtocol()
	payload := p.SendMediaPayload("WA1", MediaKindVideo, "https://x/v.mp4")
	if payload["targetUid"] != "WA1" || payload["type"] != MediaKindVideo || payload["content"] != "https://x/v.mp4" {
		t.Fatalf("媒体发送载荷不符: %+v", payload)
	}
	if payload["localTid"] == "" {
		t.Fatal("应带 localTid（对方用它对账回显）")
	}
	// 类型缺省时按图片
	payload = p.SendMediaPayload("WA1", "", "https://x/a.jpg")
	if payload["type"] != MediaKindImage {
		t.Fatalf("类型缺省应为 image，实际 %v", payload["type"])
	}
}

func TestMediaURLKey(t *testing.T) {
	cases := map[string]string{
		"https://wanfengniming.store/files/abc.jpg?token=1": "abc.jpg",
		"/files/abc.jpg": "abc.jpg",
		"abc.jpg":        "abc.jpg",
		"":               "",
	}
	for in, want := range cases {
		if got := MediaURLKey(in); got != want {
			t.Fatalf("MediaURLKey(%q)=%q，期望 %q", in, got, want)
		}
	}
	if !strings.HasPrefix(MediaURLKey("https://x/y/z.png"), "z.png") {
		t.Fatal("应只保留最后一段")
	}
}

// fakeMediaBridge 用于验证客户端媒体搬运的接线（不触网）。
type fakeMediaBridge struct {
	outboundCalls []string
	inboundCalls  []string
	outboundURL   string
	inboundName   string
	outboundErr   error
	inboundErr    error
}

func (f *fakeMediaBridge) MirrorOutbound(_ string, kind, remoteFilename string) (string, error) {
	f.outboundCalls = append(f.outboundCalls, kind+":"+remoteFilename)
	return f.outboundURL, f.outboundErr
}

func (f *fakeMediaBridge) MirrorInbound(_ string, kind, remoteURL string) (string, error) {
	f.inboundCalls = append(f.inboundCalls, kind+":"+remoteURL)
	return f.inboundName, f.inboundErr
}

func TestClientReceiveMediaUsesBridge(t *testing.T) {
	emitter := &recordingEmitter{}
	bridge := &fakeMediaBridge{inboundName: "2026/11/mirrored.jpg"}
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
		Media:      bridge,
	})
	client.partnerUID = "WA1"
	client.partnerNickname = "对方"

	client.handleReceiveMedia(ReceiveMediaInfo{
		Kind: MediaKindImage, URL: "https://wanfengniming.store/files/a.jpg", Tid: "t1",
	}, DownstreamContext{
		SelfID: "me-id", SelfUpstreamUID: "WFUME", PartnerUID: "WA1", PartnerNickname: "对方",
	}, map[string]any{"type": "image", "fromUid": "WA1"})

	if len(bridge.inboundCalls) != 1 {
		t.Fatalf("应调用一次媒体转存，实际 %+v", bridge.inboundCalls)
	}
	msgs := emitter.list()
	if len(msgs) != 1 || msgs[0]["code"] != codePrivateMessage {
		t.Fatalf("应下发一条 code=7 媒体消息，实际 %+v", msgs)
	}
	fromUser := msgs[0]["fromuser"].(map[string]any)
	if fromUser["content"] != "[2026/11/mirrored.jpg]" {
		t.Fatalf("媒体内容应使用转存后的文件名，实际 %v", fromUser["content"])
	}
}

func TestClientReceiveMediaSelfEchoReusesPath(t *testing.T) {
	emitter := &recordingEmitter{}
	bridge := &fakeMediaBridge{inboundName: "should-not-be-used.jpg"}
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
		Media:      bridge,
	})
	// 发送时已记录映射
	client.rememberOutboundMedia("https://wanfengniming.store/files/sent.jpg?x=1", "2026/11/sent.jpg")

	client.handleReceiveMedia(ReceiveMediaInfo{
		Kind: MediaKindImage, URL: "https://wanfengniming.store/files/sent.jpg", Tid: "t1",
	}, DownstreamContext{
		SelfID: "me-id", SelfUpstreamUID: "WFUME", PartnerUID: "WA1", PartnerNickname: "对方",
	}, map[string]any{"type": "image", "fromCid": "self", "fromUid": "WFUME"})

	if len(bridge.inboundCalls) != 0 {
		t.Fatalf("自己回显不应重复转存，实际 %+v", bridge.inboundCalls)
	}
	msgs := emitter.list()
	if len(msgs) != 1 {
		t.Fatalf("应下发一条消息，实际 %+v", msgs)
	}
	fromUser := msgs[0]["fromuser"].(map[string]any)
	if fromUser["id"] != md5HexLower("me-id") {
		t.Fatalf("自己回显应标记为自己，实际 %v", fromUser["id"])
	}
	if fromUser["content"] != "[2026/11/sent.jpg]" {
		t.Fatalf("应复用发送时的文件名，实际 %v", fromUser["content"])
	}
}

func TestClientReceiveMediaWithoutBridgeNotifies(t *testing.T) {
	emitter := &recordingEmitter{}
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
	})
	client.handleReceiveMedia(ReceiveMediaInfo{Kind: MediaKindImage, URL: "https://x/a.jpg"}, DownstreamContext{}, map[string]any{"type": "image"})

	msgs := emitter.list()
	if len(msgs) != 1 || msgs[0]["code"] != codeSystemNotice {
		t.Fatalf("未启用媒体转发时应提示，实际 %+v", msgs)
	}
}

// recordingEmitter 收集下发的消息（并发安全）。
type recordingEmitter struct {
	mu  sync.Mutex
	all []map[string]any
}

func (e *recordingEmitter) Emit(_ string, payload map[string]any) {
	e.mu.Lock()
	e.all = append(e.all, payload)
	e.mu.Unlock()
}

func (e *recordingEmitter) EmitFatal(_ string, payload map[string]any) { e.Emit("", payload) }

func (e *recordingEmitter) list() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.all...)
}

// TestClientDeliverMirrorsMedia 校验媒体动作在投递时先搬运、再用对方 URL 发送。
func TestClientDeliverMirrorsMedia(t *testing.T) {
	emitter := &recordingEmitter{}
	bridge := &fakeMediaBridge{outboundURL: "https://wanfengniming.store/files/up.jpg"}
	api := NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()})
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        api,
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
		Media:      bridge,
	})

	cmd := OutgoingCommand{
		Action:  DefaultProtocol().ActionSendMessage,
		Payload: map[string]any{"targetUid": "WA1"},
		Media:   &OutgoingMedia{Kind: MediaKindImage, Path: "2026/11/local.jpg"},
	}
	payload, err := client.resolvePayload(testContext(t), cmd)
	if err != nil {
		t.Fatalf("载荷解析失败: %v", err)
	}

	if len(bridge.outboundCalls) != 1 || bridge.outboundCalls[0] != "image:2026/11/local.jpg" {
		t.Fatalf("应先搬运媒体，实际 %+v", bridge.outboundCalls)
	}
	if payload["content"] != "https://wanfengniming.store/files/up.jpg" || payload["type"] != MediaKindImage {
		t.Fatalf("发送载荷应带对方 URL，实际 %+v", payload)
	}
	if payload["targetUid"] != "WA1" {
		t.Fatalf("应保留目标用户，实际 %+v", payload)
	}
	// 发送后应记录映射，供回显复用
	if client.lookupOutboundMedia("https://wanfengniming.store/files/up.jpg") != "2026/11/local.jpg" {
		t.Fatal("应记录媒体映射用于回显")
	}
}

// TestClientDeliverMediaErrorNotifies 校验搬运失败时给出可读提示而非静默丢消息。
func TestClientDeliverMediaErrorNotifies(t *testing.T) {
	emitter := &recordingEmitter{}
	bridge := &fakeMediaBridge{outboundErr: errFakeMirror}
	client := NewClient(ClientConfig{
		IdentityID: "me-id",
		API:        NewAPIClient(APIClientConfig{BaseURL: "https://example.com", Protocol: DefaultProtocol()}),
		Bindings:   NewMemoryBindingStore(),
		Emitter:    emitter,
		Media:      bridge,
	})
	_, err := client.resolvePayload(testContext(t), OutgoingCommand{
		Action:  DefaultProtocol().ActionSendMessage,
		Payload: map[string]any{"targetUid": "WA1"},
		Media:   &OutgoingMedia{Kind: MediaKindImage, Path: "a.jpg"},
	})
	if err == nil {
		t.Fatal("搬运失败应返回错误")
	}
	msgs := emitter.list()
	if len(msgs) != 1 || msgs[0]["code"] != codeSystemNotice {
		t.Fatalf("应下发系统提示，实际 %+v", msgs)
	}
}

// withCode 过滤指定 code 的下发消息（忽略连接提示等噪音）。
func (e *recordingEmitter) withCode(code int) []map[string]any {
	var out []map[string]any
	for _, msg := range e.list() {
		if v, ok := msg["code"].(int); ok && v == code {
			out = append(out, msg)
		}
	}
	return out
}

// TestUploadMediaContentTypePart 校验上传分段的 Content-Type 与文件名字段正确。
func TestUploadMediaContentTypePart(t *testing.T) {
	var gotPartCT, gotFileName, gotFormCT string
	var gotBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFormCT = r.Header.Get("Content-Type")
		mr, err := r.MultipartReader()
		if err != nil {
			t.Errorf("不是 multipart 请求: %v", err)
			return
		}
		part, err := mr.NextPart()
		if err != nil {
			t.Errorf("读取分段失败: %v", err)
			return
		}
		gotPartCT = part.Header.Get("Content-Type")
		gotFileName = part.FileName()
		gotBytes, _ = io.ReadAll(part)
		writeFakeJSON(w, map[string]any{"code": 200, "data": map[string]any{"filePath": "/files/x.jpg"}})
	}))
	defer srv.Close()

	api := NewAPIClient(APIClientConfig{BaseURL: srv.URL, Protocol: DefaultProtocol()})
	api.SetUploadUserID("WFU1")
	if _, err := api.UploadMedia(context.Background(), "照片.jpg", "image/jpeg", []byte("abc"), ""); err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	if !strings.HasPrefix(gotFormCT, "multipart/form-data") {
		t.Fatalf("表单类型不符: %q", gotFormCT)
	}
	if gotPartCT != "image/jpeg" {
		t.Fatalf("分段 Content-Type 应为图片类型，实际 %q", gotPartCT)
	}
	if gotFileName != "照片.jpg" {
		t.Fatalf("文件名不符: %q", gotFileName)
	}
	if string(gotBytes) != "abc" {
		t.Fatalf("内容不符: %q", gotBytes)
	}
}

// TestUploadMediaRetriesOnSessionError 校验 40300（会话/Cookie 失效）会自动重连重试一次。
func TestUploadMediaRetriesOnSessionError(t *testing.T) {
	var uploadCalls, handshakeCalls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/app/handshake", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&handshakeCalls, 1)
		kp, err := newHandshakeKeypair()
		if err != nil {
			t.Errorf("密钥生成失败: %v", err)
			return
		}
		writeFakeJSON(w, map[string]any{"success": true, "data": map[string]any{
			"sid": "sid-x", "pub": kp.pubB64, "exp": 4 << 40,
		}})
	})
	mux.HandleFunc("/api/tools/niming-file/upload", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&uploadCalls, 1) == 1 {
			writeFakeJSON(w, map[string]any{"code": 40300, "msg": "请求错误，请刷新页面"})
			return
		}
		writeFakeJSON(w, map[string]any{"code": 200, "data": map[string]any{"filePath": "/files/ok.jpg"}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	api := NewAPIClient(APIClientConfig{BaseURL: srv.URL, Protocol: DefaultProtocol()})
	api.SetUploadUserID("WFU1")
	// 先建好会话，验证失败后确实作废并重新握手
	if _, err := api.Session(context.Background()); err != nil {
		t.Fatalf("预热会话失败: %v", err)
	}

	result, err := api.UploadMedia(context.Background(), "a.jpg", "image/jpeg", []byte("abc"), "")
	if err != nil {
		t.Fatalf("重试后仍失败: %v", err)
	}
	if result.URL != srv.URL+"/files/ok.jpg" {
		t.Fatalf("结果不符: %+v", result)
	}
	if atomic.LoadInt32(&uploadCalls) != 2 {
		t.Fatalf("应重试一次，实际上传 %d 次", uploadCalls)
	}
	if got := atomic.LoadInt32(&handshakeCalls); got != 2 {
		t.Fatalf("重试前应重新握手（预热 1 次 + 重试 1 次），实际 %d 次", got)
	}
}
