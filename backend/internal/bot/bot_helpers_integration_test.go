//go:build integration

package bot_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/bot"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

// pngBytes 是一个合法的 1x1 PNG。
var pngBytes = mustDecode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

// mustDecode 解码测试用 base64 素材。
func mustDecode(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// ---- 假 LLM ----

// fakeLLM 是按调用顺序记录请求、按提示词内容返回预设内容的假模型。
type fakeLLM struct {
	mu     sync.Mutex
	calls  []bot.ChatRequest
	reply  func(req bot.ChatRequest) string
	broken bool
}

func newFakeLLM(reply func(req bot.ChatRequest) string) *fakeLLM {
	return &fakeLLM{reply: reply}
}

func (f *fakeLLM) Chat(_ context.Context, req bot.ChatRequest) (bot.ChatResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	broken := f.broken
	reply := f.reply
	f.mu.Unlock()
	if broken {
		return bot.ChatResult{}, io.ErrUnexpectedEOF
	}
	content := "嗯"
	if reply != nil {
		content = reply(req)
	}
	return bot.ChatResult{Content: content, PromptTokens: 100, CompletionTokens: 20, CachedTokens: 40}, nil
}

func (f *fakeLLM) snapshot() []bot.ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bot.ChatRequest(nil), f.calls...)
}

func (f *fakeLLM) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// lastSystem 返回一次请求里最后一条 system 消息。
func lastSystem(req bot.ChatRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "system" {
			return req.Messages[i].Content
		}
	}
	return ""
}

// promptReply 是默认的假回复：开场/回瓶/正常回复分别返回不同内容。
func promptReply(req bot.ChatRequest) string {
	last := lastSystem(req)
	switch {
	case strings.Contains(last, "先打个招呼"):
		return "嗨，我是小雅"
	case strings.Contains(last, "漂流瓶"):
		return "我也有过这样的夜晚"
	default:
		return "你好呀\n[图片:自拍]"
	}
}

// ---- 通用辅助 ----

// mkIdentity 插入一个身份并返回明文令牌。
func mkIdentity(t *testing.T, pool *pgxpool.Pool, name string) (store.Identity, string) {
	t.Helper()
	token := "tok-" + name + "-" + time.Now().Format("150405.000000000")
	id, err := store.New(pool).CreateIdentity(ctx, store.CreateIdentityParams{
		TokenHash: identity.HashToken(token), Nickname: name, AvatarSeed: "seed-" + name,
	})
	if err != nil {
		t.Fatalf("创建身份失败: %v", err)
	}
	return id, token
}

// dialWS 以身份令牌建立 WS 连接。
func dialWS(t *testing.T, srv *httptest.Server, token string) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	h := http.Header{}
	h.Set("Origin", testutil.DefaultOrigin)
	h.Set("Cookie", identity.CookieName+"="+token)
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatalf("WS 连接失败: %v", err)
	}
	t.Cleanup(func() { _ = c.CloseNow() })
	return c
}

// sendWS 发送一帧。
func sendWS(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	wctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := c.Write(wctx, websocket.MessageText, data); err != nil {
		t.Fatalf("WS 写入失败: %v", err)
	}
}

// readEnvelope 读取一帧（带超时）。
func readEnvelope(t *testing.T, c *websocket.Conn, timeout time.Duration) realtime.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, data, err := c.Read(rctx)
	if err != nil {
		t.Fatalf("WS 读取失败: %v", err)
	}
	var env realtime.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("解析信封失败: %v, data=%s", err, data)
	}
	return env
}

// readUntil 跳过无关事件，直到读到指定类型（或超时失败）。
func readUntil(t *testing.T, c *websocket.Conn, want realtime.MessageType, timeout time.Duration) realtime.Envelope {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		env := readEnvelope(t, c, time.Until(deadline)+time.Second)
		if env.Type == want {
			return env
		}
		if env.Type == realtime.TypeError {
			t.Fatalf("收到 error 事件: %s", env.Data)
		}
	}
	t.Fatalf("等待 %s 超时", want)
	return realtime.Envelope{}
}

// waitFor 轮询等待条件成立（带超时，不使用固定长 sleep）。
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待超时: %s", what)
}

// seedBot 直接插入 bot 相关数据，返回 bot 关键 ID。
type seededBot struct {
	IdentityID pgtype.UUID
	BotID      pgtype.UUID
	CardID     pgtype.UUID
	FolderID   pgtype.UUID
	ItemID     pgtype.UUID
}

// seedBotOptions 是插入测试 bot 的可调参数。
type seedBotOptions struct {
	enabled         bool
	serveMatch      bool
	serveBottle     bool
	firstMessage    string
	convReplyLimit  int32
	totalReplyLimit int64
	usedReplies     int64
	mediaTags       []string
	withMedia       bool
}

// seedBot 在库里创建一个可直接参与匹配/漂流瓶的 bot（节奏参数全 0，避免测试变慢）。
func seedBot(t *testing.T, pool *pgxpool.Pool, root string, opts seedBotOptions) seededBot {
	t.Helper()
	ident, _ := mkIdentity(t, pool, "bot")
	var cardID, folderID, itemID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO character_cards (name, personality, first_message) VALUES ('小雅', '温柔', $1) RETURNING id`,
		opts.firstMessage).Scan(&cardID); err != nil {
		t.Fatalf("插入角色卡失败: %v", err)
	}
	if opts.withMedia {
		if err := pool.QueryRow(ctx, "INSERT INTO media_folders (name) VALUES ('自拍') RETURNING id").Scan(&folderID); err != nil {
			t.Fatalf("插入媒体文件夹失败: %v", err)
		}
		if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&itemID); err != nil {
			t.Fatal(err)
		}
		key := folderID.String() + "/" + itemID.String() + ".png"
		if _, err := pool.Exec(ctx,
			`INSERT INTO media_items (id, folder_id, kind, mime, size_bytes, storage_key, tags)
			 VALUES ($1,$2,'image','image/png',$3,$4,$5)`,
			itemID, folderID, len(pngBytes), key, opts.mediaTags); err != nil {
			t.Fatalf("插入媒体失败: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(root, folderID.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, key), pngBytes, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var botID pgtype.UUID
	if err := pool.QueryRow(ctx,
		`INSERT INTO bots (identity_id, label, card_id, media_folder_id, enabled, serve_match, serve_bottle,
		                   reply_delay_min_ms, reply_delay_max_ms, typing_ms_per_char,
		                   conv_reply_limit, conv_media_limit, bottle_interval_seconds,
		                   total_reply_limit, used_replies)
		 VALUES ($1,'测试 bot',$2,$3,$4,$5,$6,0,0,0,$7,3,60,$8,$9) RETURNING id`,
		ident.ID, nullUUID(cardID), nullUUID(folderID), opts.enabled, opts.serveMatch, opts.serveBottle,
		opts.convReplyLimit, opts.totalReplyLimit, opts.usedReplies).Scan(&botID); err != nil {
		t.Fatalf("插入 bot 失败: %v", err)
	}
	return seededBot{IdentityID: ident.ID, BotID: botID, CardID: cardID, FolderID: folderID, ItemID: itemID}
}

// nullUUID 把无效 UUID 转成 nil（写入可空列）。
func nullUUID(id pgtype.UUID) any {
	if !id.Valid {
		return nil
	}
	return id
}

// newEnv 构造带 bot 引擎的测试环境（假 LLM + 默认启用设置）。
func newEnv(t *testing.T, pool *pgxpool.Pool, llm bot.LLM, root string) *testutil.Env {
	t.Helper()
	return newEnvWith(t, pool, testutil.Config{BotLLM: llm, MediaRoot: root})
}

// newEnvWith 用给定配置构造环境，并补齐测试通用的节奏参数。
func newEnvWith(t *testing.T, pool *pgxpool.Pool, cfg testutil.Config) *testutil.Env {
	t.Helper()
	if cfg.BotFallbackAfter == 0 {
		cfg.BotFallbackAfter = 200 * time.Millisecond
	}
	if cfg.BotTick == 0 {
		cfg.BotTick = 10 * time.Millisecond
	}
	if cfg.BotRefresh == 0 {
		cfg.BotRefresh = 10 * time.Millisecond
	}
	if cfg.BotBottleLoop == 0 {
		cfg.BotBottleLoop = 20 * time.Millisecond
	}
	return testutil.MustEnv(t, pool, cfg)
}

// startMatch 让用户开始匹配，并等待 match.found。
func startMatch(t *testing.T, c *websocket.Conn) realtime.Envelope {
	t.Helper()
	sendWS(t, c, map[string]any{"t": realtime.TypeMatchStart, "id": "m1", "d": map[string]any{}})
	return readUntil(t, c, realtime.TypeMatchFound, 5*time.Second)
}

// foundPayload 是 match.found 的负载。
type foundPayload struct {
	ConversationID string         `json:"conversationId"`
	Peer           map[string]any `json:"peer"`
}

// parseFound 解析 match.found。
func parseFound(t *testing.T, env realtime.Envelope) foundPayload {
	t.Helper()
	var p foundPayload
	if err := json.Unmarshal(env.Data, &p); err != nil {
		t.Fatalf("解析 match.found 失败: %v, data=%s", err, env.Data)
	}
	return p
}

// peerKeys 返回 peer 字段名的排序列表（用于比较字段集合）。
func peerKeys(peer map[string]any) []string {
	keys := make([]string, 0, len(peer))
	for k := range peer {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// msgNewPayload 是 msg.new 的负载。
type msgNewPayload struct {
	ConversationID string `json:"conversationId"`
	Message        struct {
		ID       int64  `json:"id"`
		MemberID int32  `json:"memberId"`
		Kind     string `json:"kind"`
		Body     string `json:"body"`
		MediaURL string `json:"mediaUrl"`
	} `json:"message"`
}

// sendMessage 在会话里发一条文本消息。
func sendMessage(t *testing.T, c *websocket.Conn, convID, body string) int64 {
	t.Helper()
	sendWS(t, c, map[string]any{
		"t": realtime.TypeMsgSend, "id": "s1",
		"d": map[string]any{
			"conversationId": convID,
			"clientMsgId":    newUUID(t).String(),
			"kind":           "text",
			"body":           body,
		},
	})
	ack := readUntil(t, c, realtime.TypeAck, 5*time.Second)
	var payload struct {
		MessageID int64 `json:"messageId"`
	}
	_ = json.Unmarshal(ack.Data, &payload)
	return payload.MessageID
}

// newUUID 生成一个随机 UUID。
func newUUID(t *testing.T) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := poolQueryRow(t, "SELECT gen_random_uuid()").Scan(&id); err != nil {
		t.Fatalf("生成 UUID 失败: %v", err)
	}
	return id
}

// poolQueryRow 在当前测试包共享的连接上执行查询（仅用于生成 UUID）。
var sharedPool *pgxpool.Pool

func poolQueryRow(t *testing.T, sql string, args ...any) pgx.Row {
	t.Helper()
	if sharedPool == nil {
		t.Fatal("sharedPool 未初始化")
	}
	return sharedPool.QueryRow(ctx, sql, args...)
}

// doJSON 发送一次 JSON 请求（可带管理 Cookie）。
func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any, adminCookie string) (int, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if adminCookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: adminCookie})
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// errorCode 从错误响应体中取出错误码。
func errorCode(t *testing.T, body []byte) string {
	t.Helper()
	var e struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("解析错误响应失败: %v, body=%s", err, body)
	}
	return e.Error.Code
}

// loginAdmin 创建管理员并登录，返回管理 Cookie。
func loginAdmin(t *testing.T, pool *pgxpool.Pool, srv *httptest.Server) string {
	t.Helper()
	if _, err := admin.CreateUser(ctx, pool, "admin", "supersecret123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "supersecret123"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testutil.DefaultOrigin)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("登录状态 = %d, body=%s", resp.StatusCode, data)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == admin.SessionCookie {
			return ck.Value
		}
	}
	t.Fatal("登录未下发管理 Cookie")
	return ""
}
