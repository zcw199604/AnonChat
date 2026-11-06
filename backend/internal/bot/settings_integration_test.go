//go:build integration

package bot_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// settingsPayload 返回一份合法的全局设置请求体（可覆盖个别字段）。
func settingsPayload(overrides map[string]any) map[string]any {
	p := map[string]any{
		"enabled":              true,
		"baseUrl":              "http://llm.test/v1",
		"model":                "test-model",
		"timeoutSeconds":       30,
		"sendCacheKey":         true,
		"matchFallbackSeconds": 3,
		"bottleMinAgeSeconds":  60,
		"timezone":             "Asia/Shanghai",
	}
	for k, v := range overrides {
		p[k] = v
	}
	return p
}

// getSettings 读取全局设置（返回解析后的对象与原始响应体）。
func getSettings(t *testing.T, env *testutil.Env, cookie string) (map[string]any, []byte) {
	t.Helper()
	status, body := doJSON(t, env.Server, http.MethodGet, "/api/admin/bot-settings", nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取设置失败: %d %s", status, body)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析设置失败: %v, body=%s", err, body)
	}
	return out, body
}

// putSettings 保存全局设置。
func putSettings(t *testing.T, env *testutil.Env, cookie string, payload map[string]any) (int, []byte) {
	t.Helper()
	return doJSON(t, env.Server, http.MethodPut, "/api/admin/bot-settings", payload, cookie)
}

// TestBotSettingsKeyStorage 覆盖 §15.5 第 1 条：
// 响应里没有 key、数据库只存密文、不传 apiKey 保持不变、clearApiKey 能清除。
func TestBotSettingsKeyStorage(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir(), NoSettingsEncKey: false})
	cookie := loginAdmin(t, pool, env.Server)

	const plain = "sk-live-abcd1234"
	status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{"apiKey": plain}))
	if status != http.StatusOK {
		t.Fatalf("保存设置失败: %d %s", status, body)
	}
	if bytes.Contains(body, []byte(plain)) {
		t.Fatalf("保存响应里出现了 API Key 明文: %s", body)
	}
	var saved map[string]any
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["apiKeySet"] != true || saved["apiKeyHint"] != "••••1234" || saved["apiKeySource"] != "db" {
		t.Fatalf("key 状态不符: %s", body)
	}
	if saved["secretStorageAvailable"] != true || saved["ready"] != true {
		t.Fatalf("存储/可用状态不符: %s", body)
	}

	// 数据库里只有密文，且密文不含明文。
	var enc []byte
	var hint string
	if err := pool.QueryRow(ctx, "SELECT llm_api_key_enc, llm_api_key_hint FROM bot_settings WHERE id = 1").
		Scan(&enc, &hint); err != nil {
		t.Fatal(err)
	}
	if len(enc) == 0 {
		t.Fatal("数据库中应保存密文")
	}
	if bytes.Contains(enc, []byte(plain)) || bytes.Contains(enc, []byte("abcd1234")) {
		t.Fatal("数据库密文中不应包含 key 明文")
	}
	if hint != "••••1234" {
		t.Fatalf("hint = %q", hint)
	}
	// 审计日志不含 key（明文或密文的 base64）。
	var auditHits int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM admin_audit_logs WHERE detail::text LIKE '%abcd1234%' OR target LIKE '%abcd1234%'").
		Scan(&auditHits); err != nil {
		t.Fatal(err)
	}
	if auditHits != 0 {
		t.Fatalf("审计日志里出现了 key 信息: %d 条", auditHits)
	}

	// GET 也不返回 key。
	got, raw := getSettings(t, env, cookie)
	if bytes.Contains(raw, []byte(plain)) {
		t.Fatalf("GET 响应里出现了 API Key 明文: %s", raw)
	}
	if got["apiKeySet"] != true {
		t.Fatalf("GET apiKeySet 应为 true: %s", raw)
	}

	// 不传 apiKey = 保持不变（密文逐字节相同）。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{"model": "test-model-2"}))
	if status != http.StatusOK {
		t.Fatalf("再次保存失败: %d %s", status, body)
	}
	var enc2 []byte
	if err := pool.QueryRow(ctx, "SELECT llm_api_key_enc FROM bot_settings WHERE id = 1").Scan(&enc2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(enc, enc2) {
		t.Fatal("不传 apiKey 时 key 应保持不变")
	}
	got, _ = getSettings(t, env, cookie)
	if got["apiKeySet"] != true || got["model"] != "test-model-2" {
		t.Fatalf("设置未按预期更新: %v", got)
	}

	// clearApiKey 清除数据库中的 key。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{"clearApiKey": true}))
	if status != http.StatusOK {
		t.Fatalf("清除 key 失败: %d %s", status, body)
	}
	got, raw = getSettings(t, env, cookie)
	if got["apiKeySet"] != false || got["apiKeySource"] != "none" {
		t.Fatalf("清除后应无 key: %s", raw)
	}
	var enc3 []byte
	if err := pool.QueryRow(ctx, "SELECT llm_api_key_enc FROM bot_settings WHERE id = 1").Scan(&enc3); err != nil {
		t.Fatal(err)
	}
	if len(enc3) != 0 {
		t.Fatal("清除后密文应为 NULL")
	}
}

// TestBotSettingsWithoutEncKey 覆盖 §15.5 第 4 条：
// 没有 SETTINGS_ENC_KEY 时拒绝保存 key，但其他字段可以保存。
func TestBotSettingsWithoutEncKey(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir(), NoSettingsEncKey: true})
	cookie := loginAdmin(t, pool, env.Server)

	status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{"apiKey": "sk-should-fail"}))
	if status != http.StatusBadRequest {
		t.Fatalf("未配置 SETTINGS_ENC_KEY 时保存 key 应 400, 实际 %d %s", status, body)
	}
	if code := errorCode(t, body); code != "invalid_request" {
		t.Errorf("错误码 = %q, 期望 invalid_request", code)
	}
	if !bytes.Contains(body, []byte("SETTINGS_ENC_KEY")) {
		t.Errorf("错误文案应提示 SETTINGS_ENC_KEY: %s", body)
	}

	// 其他字段照常可保存。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{"model": "no-key-model"}))
	if status != http.StatusOK {
		t.Fatalf("不带 key 的设置应可保存: %d %s", status, body)
	}
	got, raw := getSettings(t, env, cookie)
	if got["secretStorageAvailable"] != false {
		t.Fatalf("secretStorageAvailable 应为 false: %s", raw)
	}
	if got["model"] != "no-key-model" {
		t.Fatalf("模型未保存: %s", raw)
	}
	if got["ready"] != false || got["notReadyReason"] == "" {
		t.Fatalf("缺 key 时应不可用: %s", raw)
	}
}

// TestBotSettingsHotSwitch 覆盖 §15.5 第 2 条：不重启热切换全局开关。
func TestBotSettingsHotSwitch(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	seedBot(t, pool, root, seedBotOptions{enabled: true, serveMatch: true})
	llm := newFakeLLM(promptReply)
	// 服务启动时全局关闭。
	env := newEnvWith(t, pool, testutil.Config{
		BotLLM: llm, MediaRoot: root, BotSettingsDisabled: true,
	})
	cookie := loginAdmin(t, pool, env.Server)

	// 第一次排队（此时全局关闭）：兜底时间 200ms 也不应被 bot 接走。
	// 注意：等待超时会关闭这条测试连接（coder/websocket 语义），下面换一个用户验证热切换。
	_, tokenA := mkIdentity(t, pool, "ivy")
	wsA := dialWS(t, env.Server, tokenA)
	_ = readEnvelope(t, wsA, 3*time.Second)
	sendWS(t, wsA, map[string]any{"t": realtime.TypeMatchStart, "id": "a", "d": map[string]any{}})
	_ = readUntil(t, wsA, realtime.TypeMatchWaiting, 5*time.Second)
	expectNoFound(t, wsA, 700*time.Millisecond)

	// 通过后台打开开关（不重启）。
	if status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{"enabled": true})); status != http.StatusOK {
		t.Fatalf("打开开关失败: %d %s", status, body)
	}
	// 新用户在下一次排队时被 bot 兜底。
	_, tokenB := mkIdentity(t, pool, "kate")
	wsB := dialWS(t, env.Server, tokenB)
	_ = readEnvelope(t, wsB, 3*time.Second)
	botFound := parseFound(t, startMatch(t, wsB))
	if botFound.ConversationID == "" {
		t.Fatal("打开开关后应被 bot 兜底")
	}
}

// TestBotSettingsModelHotSwitch 覆盖 §15.5 第 3 条：
// 修改模型后下一次请求使用新模型；bot 自己的模型优先级更高。
func TestBotSettingsModelHotSwitch(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	seeded := seedBot(t, pool, root, seedBotOptions{enabled: true, serveMatch: true})
	llm := newFakeLLM(promptReply)
	env := newEnvWith(t, pool, testutil.Config{BotLLM: llm, MediaRoot: root})
	cookie := loginAdmin(t, pool, env.Server)

	_, token := mkIdentity(t, pool, "jane")
	ws := dialWS(t, env.Server, token)
	_ = readEnvelope(t, ws, 3*time.Second)
	found := parseFound(t, startMatch(t, ws))

	// 改全局默认模型，下一次请求应使用它。
	if status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{"model": "model-global-2"})); status != http.StatusOK {
		t.Fatalf("保存模型失败: %d %s", status, body)
	}
	sendMessage(t, ws, found.ConversationID, "你好")
	readUntil(t, ws, realtime.TypeMsgNew, 8*time.Second)
	waitFor(t, "使用新的全局模型", 5*time.Second, func() bool {
		calls := llm.snapshot()
		return len(calls) > 0 && calls[len(calls)-1].Model == "model-global-2"
	})

	// bot 自己设置的模型优先级更高（走后台 PUT，保存后引擎立即刷新）。
	updateBotModel(t, env, cookie, seeded.BotID, "model-bot-own")
	sendMessage(t, ws, found.ConversationID, "再聊聊")
	readUntil(t, ws, realtime.TypeMsgNew, 8*time.Second)
	waitFor(t, "使用 bot 自己的模型", 5*time.Second, func() bool {
		calls := llm.snapshot()
		return len(calls) > 0 && calls[len(calls)-1].Model == "model-bot-own"
	})
}

// TestBotSettingsTestConnection 覆盖 §15.5 第 5 条：测试连接成功与失败，且错误里不含 key。
func TestBotSettingsTestConnection(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir()})
	cookie := loginAdmin(t, pool, env.Server)

	const plain = "sk-testkey-abcd"
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	defer okSrv.Close()
	unauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 故意把 Authorization 回显到错误体里：接口返回的错误文案仍不得包含 key。
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key ` + r.Header.Get("Authorization") + `"}}`))
	}))
	defer unauthSrv.Close()

	// 先保存好 key（baseUrl 指向假服务）。
	if status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{
		"baseUrl": okSrv.URL, "apiKey": plain,
	})); status != http.StatusOK {
		t.Fatalf("保存设置失败: %d %s", status, body)
	}

	// 成功。
	status, body := doJSON(t, env.Server, http.MethodPost, "/api/admin/bot-settings/test", map[string]any{}, cookie)
	if status != http.StatusOK {
		t.Fatalf("测试连接失败: %d %s", status, body)
	}
	var okResp struct {
		OK      bool  `json:"ok"`
		Latency int64 `json:"latencyMs"`
		Model   string
		Error   string
	}
	mustJSON(t, body, &okResp)
	if !okResp.OK || okResp.Model != "test-model" {
		t.Fatalf("测试连接应成功: %s", body)
	}

	// 失败（401）：换地址时必须带 key 才会真正发请求，返回错误且不含 key。
	status, body = doJSON(t, env.Server, http.MethodPost, "/api/admin/bot-settings/test",
		map[string]any{"baseUrl": unauthSrv.URL, "apiKey": plain}, cookie)
	if status != http.StatusOK {
		t.Fatalf("测试连接接口应返回 200（ok=false）: %d %s", status, body)
	}
	var failResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	mustJSON(t, body, &failResp)
	if failResp.OK || failResp.Error == "" {
		t.Fatalf("401 时应返回失败与错误文案: %s", body)
	}
	if strings.Contains(failResp.Error, plain) || strings.Contains(string(body), plain) {
		t.Fatalf("错误信息不应包含 API Key: %s", body)
	}

	// 未登录 → 401。
	if status, _ := doJSON(t, env.Server, http.MethodPost, "/api/admin/bot-settings/test", map[string]any{}, ""); status != http.StatusUnauthorized {
		t.Fatalf("未登录应 401, 实际 %d", status)
	}
}

// TestBotSettingsBaseURLBinding 覆盖"key 绑定 Base URL"（how.md §15.1/§15.3）：
// 换地址时既不把已保存的 key 用于测试连接，也不允许在没给新 key 的情况下保存。
func TestBotSettingsBaseURLBinding(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: t.TempDir()})
	cookie := loginAdmin(t, pool, env.Server)

	var hitsA, hitsB int
	newLLM := func(counter *int) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*counter++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
		}))
	}
	srvA := newLLM(&hitsA)
	defer srvA.Close()
	srvB := newLLM(&hitsB)
	defer srvB.Close()

	const key1 = "sk-bind-abcd"
	// 1) 在地址 A 上保存 key，绑定关系写入数据库。
	if status, body := putSettings(t, env, cookie, settingsPayload(map[string]any{
		"baseUrl": srvA.URL, "apiKey": key1,
	})); status != http.StatusOK {
		t.Fatalf("保存设置失败: %d %s", status, body)
	}
	var bound string
	if err := pool.QueryRow(ctx, "SELECT llm_api_key_base_url FROM bot_settings WHERE id = 1").Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != srvA.URL {
		t.Fatalf("key 绑定地址 = %q, 期望 %q", bound, srvA.URL)
	}

	// 2) 用不同的 baseUrl 测试连接且不带 key：返回 ok=false，且假服务没有收到任何请求。
	status, body := doJSON(t, env.Server, http.MethodPost, "/api/admin/bot-settings/test",
		map[string]any{"baseUrl": srvB.URL}, cookie)
	if status != http.StatusOK {
		t.Fatalf("测试连接接口应返回 200（ok=false）: %d %s", status, body)
	}
	var noKeyResp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	mustJSON(t, body, &noKeyResp)
	if noKeyResp.OK || !strings.Contains(noKeyResp.Error, "更换 Base URL") {
		t.Fatalf("换地址且不带 key 时应返回失败提示: %s", body)
	}
	if hitsB != 0 {
		t.Fatalf("换地址且不带 key 时不应发出任何请求, 实际 %d 次", hitsB)
	}

	// 3) 只改 Base URL、不带新 key：拒绝保存。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{"baseUrl": srvB.URL}))
	if status != http.StatusBadRequest {
		t.Fatalf("改地址不带 key 应 400, 实际 %d %s", status, body)
	}
	if code := errorCode(t, body); code != "invalid_request" {
		t.Errorf("错误码 = %q, 期望 invalid_request", code)
	}
	if !bytes.Contains(body, []byte("修改 Base URL 时需要重新填写 API Key")) {
		t.Errorf("错误文案不符: %s", body)
	}

	// 4) 同时带新 key：保存成功并立即可用，绑定地址跟随新地址。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{
		"baseUrl": srvB.URL, "apiKey": "sk-new-1234",
	}))
	if status != http.StatusOK {
		t.Fatalf("带新 key 改地址应成功: %d %s", status, body)
	}
	got, raw := getSettings(t, env, cookie)
	if got["ready"] != true || got["apiKeySource"] != "db" || got["apiKeyHint"] != "••••1234" {
		t.Fatalf("保存后应可用: %s", raw)
	}
	if err := pool.QueryRow(ctx, "SELECT llm_api_key_base_url FROM bot_settings WHERE id = 1").Scan(&bound); err != nil {
		t.Fatal(err)
	}
	if bound != srvB.URL {
		t.Fatalf("新 key 的绑定地址 = %q, 期望 %q", bound, srvB.URL)
	}

	// 5) 绑定地址与当前 Base URL 不一致（模拟被改过）→ 不可用并提示重填。
	if _, err := pool.Exec(ctx,
		"UPDATE bot_settings SET llm_api_key_base_url = 'https://old.example/v1' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	env.Deps.BotEngine.Invalidate()
	got, raw = getSettings(t, env, cookie)
	if got["ready"] != false || got["apiKeySource"] != "none" || got["apiKeyStale"] != true {
		t.Fatalf("绑定不一致时应不可用: %s", raw)
	}
	if reason, _ := got["notReadyReason"].(string); !bytes.Contains([]byte(reason), []byte("Base URL 已变更")) {
		t.Fatalf("不可用原因不符: %s", raw)
	}
	if hint, _ := got["apiKeyHint"].(string); hint == "" {
		t.Errorf("绑定不一致时仍应保留 hint 供提示: %s", raw)
	}

	// 6) 改地址同时 clearApiKey：允许保存，结果是没有 key。
	status, body = putSettings(t, env, cookie, settingsPayload(map[string]any{
		"baseUrl": srvA.URL, "clearApiKey": true,
	}))
	if status != http.StatusOK {
		t.Fatalf("改地址并清除 key 应允许: %d %s", status, body)
	}
	got, raw = getSettings(t, env, cookie)
	if got["apiKeySet"] != false {
		t.Fatalf("清除后应无 key: %s", raw)
	}
}

// updateBotModel 通过后台接口修改某个 bot 的模型（保存后引擎会立即刷新缓存）。
func updateBotModel(t *testing.T, env *testutil.Env, cookie string, botID pgtype.UUID, model string) {
	t.Helper()
	status, body := doJSON(t, env.Server, http.MethodGet, "/api/admin/bots", nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取 bot 列表失败: %d %s", status, body)
	}
	var bots []map[string]any
	if err := json.Unmarshal(body, &bots); err != nil {
		t.Fatalf("解析 bot 列表失败: %v", err)
	}
	var target map[string]any
	for _, b := range bots {
		if b["id"] == botID.String() {
			target = b
			break
		}
	}
	if target == nil {
		t.Fatalf("未找到 bot %s", botID.String())
	}
	profile, _ := target["profile"].(map[string]any)
	payload := map[string]any{
		"label":                  target["label"],
		"cardId":                 target["cardId"],
		"mediaFolderId":          target["mediaFolderId"],
		"enabled":                target["enabled"],
		"model":                  model,
		"temperature":            target["temperature"],
		"maxOutputTokens":        target["maxOutputTokens"],
		"serveMatch":             target["serveMatch"],
		"serveBottle":            target["serveBottle"],
		"maxActiveMatches":       target["maxActiveMatches"],
		"totalTokenLimit":        target["totalTokenLimit"],
		"totalReplyLimit":        target["totalReplyLimit"],
		"windowSeconds":          target["windowSeconds"],
		"windowTokenLimit":       target["windowTokenLimit"],
		"windowReplyLimit":       target["windowReplyLimit"],
		"convReplyLimit":         target["convReplyLimit"],
		"convMinIntervalSeconds": target["convMinIntervalSeconds"],
		"convMediaLimit":         target["convMediaLimit"],
		"replyDelayMinMs":        target["replyDelayMinMs"],
		"replyDelayMaxMs":        target["replyDelayMaxMs"],
		"typingMsPerChar":        target["typingMsPerChar"],
		"bottleIntervalSeconds":  target["bottleIntervalSeconds"],
		"profile": map[string]any{
			"nickname":       profile["nickname"],
			"gender":         profile["gender"],
			"age":            profile["age"],
			"province":       profile["province"],
			"provincePublic": profile["provincePublic"],
			"tags":           profile["tags"],
			"cleanMode":      profile["cleanMode"],
		},
	}
	if status, body := doJSON(t, env.Server, http.MethodPut, "/api/admin/bots/"+botID.String(), payload, cookie); status != http.StatusOK {
		t.Fatalf("更新 bot 模型失败: %d %s", status, body)
	}
}
