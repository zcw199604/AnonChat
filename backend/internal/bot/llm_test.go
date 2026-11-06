package bot

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// quietLogger 返回丢弃输出的日志器。
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// testRequest 返回一个最小的模型调用请求。
func testRequest(cacheKey string) ChatRequest {
	return ChatRequest{
		Model: "test-model",
		Messages: []ChatMessage{
			{Role: "system", Content: "你是小雅"},
			{Role: "user", Content: "你好"},
		},
		MaxTokens:   300,
		Temperature: 0.9,
		CacheKey:    cacheKey,
	}
}

// okResponse 返回一个成功响应体。
func okResponse(content string) string {
	return `{"choices":[{"message":{"content":"` + content + `"}}],` +
		`"usage":{"prompt_tokens":100,"completion_tokens":20}}`
}

// TestChatSendsCacheKey 验证 LLM_SEND_CACHE_KEY 开关控制 prompt_cache_key 字段。
func TestChatSendsCacheKey(t *testing.T) {
	t.Run("开启时带上 prompt_cache_key", func(t *testing.T) {
		var raw string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			raw = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(okResponse("嗨")))
		}))
		defer srv.Close()

		client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "sk-test", SendCacheKey: true, Logger: quietLogger()})
		if _, err := client.Chat(context.Background(), testRequest("anonchat-bot-1")); err != nil {
			t.Fatalf("Chat 失败: %v", err)
		}
		if !strings.Contains(raw, `"prompt_cache_key":"anonchat-bot-1"`) {
			t.Fatalf("请求体应包含 prompt_cache_key: %s", raw)
		}
		// 请求体里不能出现 base_url 之外的额外字段。
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("请求体不是合法 JSON: %v", err)
		}
		for _, key := range []string{"model", "messages", "max_tokens", "temperature", "stream"} {
			if _, ok := payload[key]; !ok {
				t.Errorf("请求体缺少字段 %q", key)
			}
		}
		if payload["stream"] != false {
			t.Errorf("stream 应为 false, 实际 %v", payload["stream"])
		}
	})

	t.Run("关闭时不出现 prompt_cache_key", func(t *testing.T) {
		var raw string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			raw = string(body)
			_, _ = w.Write([]byte(okResponse("嗨")))
		}))
		defer srv.Close()

		client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "sk-test", SendCacheKey: false, Logger: quietLogger()})
		if _, err := client.Chat(context.Background(), testRequest("anonchat-bot-1")); err != nil {
			t.Fatalf("Chat 失败: %v", err)
		}
		if strings.Contains(raw, "prompt_cache_key") {
			t.Fatalf("关闭开关后请求体不应出现 prompt_cache_key: %s", raw)
		}
	})
}

// TestChatParsesCachedTokens 验证两种缓存命中字段都能解析（取较大值）。
func TestChatParsesCachedTokens(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{
			name: "openai 风格 prompt_tokens_details.cached_tokens",
			body: `{"choices":[{"message":{"content":"嗨"}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":64}}}`,
			want: 64,
		},
		{
			name: "deepseek 风格 prompt_cache_hit_tokens",
			body: `{"choices":[{"message":{"content":"嗨"}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20,"prompt_cache_hit_tokens":80}}`,
			want: 80,
		},
		{
			name: "两种字段同时出现取较大值",
			body: `{"choices":[{"message":{"content":"嗨"}}],
				"usage":{"prompt_tokens":100,"completion_tokens":20,
					"prompt_tokens_details":{"cached_tokens":90},"prompt_cache_hit_tokens":40}}`,
			want: 90,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(c.body))
			}))
			defer srv.Close()
			client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
			res, err := client.Chat(context.Background(), testRequest(""))
			if err != nil {
				t.Fatalf("Chat 失败: %v", err)
			}
			if res.CachedTokens != c.want {
				t.Errorf("CachedTokens = %d, 期望 %d", res.CachedTokens, c.want)
			}
			if res.PromptTokens != 100 || res.CompletionTokens != 20 || res.Content != "嗨" {
				t.Errorf("结果不符: %+v", res)
			}
		})
	}
}

// TestChatAuthorizationHeader 验证 Authorization 头正确。
func TestChatAuthorizationHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("路径 = %q, 期望 /chat/completions", r.URL.Path)
		}
		_, _ = w.Write([]byte(okResponse("嗨")))
	}))
	defer srv.Close()

	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL + "/", APIKey: "sk-secret", Logger: quietLogger()})
	if _, err := client.Chat(context.Background(), testRequest("")); err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if got != "Bearer sk-secret" {
		t.Errorf("Authorization = %q", got)
	}
}

// TestChatRetriesOnceOn5xx 验证 5xx 只重试 1 次（共 2 次请求）。
func TestChatRetriesOnceOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		_, _ = w.Write([]byte(okResponse("好吧")))
	}))
	defer srv.Close()

	var slept []time.Duration
	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
	client.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}
	res, err := client.Chat(context.Background(), testRequest(""))
	if err != nil {
		t.Fatalf("Chat 失败: %v", err)
	}
	if res.Content != "好吧" {
		t.Errorf("Content = %q", res.Content)
	}
	if calls != 2 {
		t.Errorf("5xx 应只重试 1 次（共 2 次请求），实际 %d 次", calls)
	}
	if len(slept) != 1 || slept[0] != time.Second {
		t.Errorf("重试间隔 = %v, 期望 1s", slept)
	}
}

// TestChat429RetryAfter 验证 429 与 Retry-After 的处理（上限 5 秒）。
func TestChat429RetryAfter(t *testing.T) {
	t.Run("按 Retry-After 等待", func(t *testing.T) {
		var calls int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if atomic.AddInt32(&calls, 1) == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(okResponse("好")))
		}))
		defer srv.Close()

		var slept []time.Duration
		client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
		client.sleep = func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
		if _, err := client.Chat(context.Background(), testRequest("")); err != nil {
			t.Fatalf("Chat 失败: %v", err)
		}
		if len(slept) != 1 || slept[0] != 2*time.Second {
			t.Errorf("等待时长 = %v, 期望 2s", slept)
		}
	})

	t.Run("Retry-After 超过 5 秒时截断", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		var slept []time.Duration
		client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
		client.sleep = func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		}
		if _, err := client.Chat(context.Background(), testRequest("")); err == nil {
			t.Fatal("持续 5xx 应返回错误")
		}
		if len(slept) != 1 || slept[0] != 5*time.Second {
			t.Errorf("等待时长 = %v, 期望被截断为 5s", slept)
		}
	})
}

// TestChatNoRetryOn4xx 验证非 429 的 4xx 不重试。
func TestChatNoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad"}}`))
	}))
	defer srv.Close()

	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
	client.sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := client.Chat(context.Background(), testRequest("")); err == nil {
		t.Fatal("4xx 应返回错误")
	}
	if calls != 1 {
		t.Errorf("4xx 不应重试, 实际请求 %d 次", calls)
	}
}

// TestChatLogsDoNotLeakSecrets 验证错误日志里没有 API Key 与消息正文。
func TestChatLogsDoNotLeakSecrets(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// 错误响应体很长：日志里最多记录前 200 字节。
		_, _ = w.Write([]byte(strings.Repeat("E", 1000)))
	}))
	defer srv.Close()

	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "sk-super-secret", Logger: logger})
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, _ = client.Chat(context.Background(), testRequest(""))

	logged := buf.String()
	if strings.Contains(logged, "sk-super-secret") {
		t.Error("日志中出现了 API Key")
	}
	if strings.Contains(logged, "你好") {
		t.Error("日志中出现了消息正文")
	}
	if n := strings.Count(logged, "E"); n > llmErrorBodyLogMax {
		t.Errorf("错误响应体记录了 %d 字节, 超过上限 %d", n, llmErrorBodyLogMax)
	}
}

// TestChatEmptyChoices 验证空 choices 返回错误。
func TestChatEmptyChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer srv.Close()
	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "k", Logger: quietLogger()})
	if _, err := client.Chat(context.Background(), testRequest("")); err == nil {
		t.Fatal("空 choices 应返回错误")
	}
}

// TestChatLogsDoNotEchoKey 验证即使服务商在错误体里回显了密钥，日志里也会被擦掉。
func TestChatLogsDoNotEchoKey(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid key ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer srv.Close()

	client := NewOpenAIClient(ClientOptions{BaseURL: srv.URL, APIKey: "sk-echo-secret", Logger: logger})
	client.sleep = func(context.Context, time.Duration) error { return nil }
	_, _ = client.Chat(context.Background(), testRequest(""))

	if strings.Contains(buf.String(), "sk-echo-secret") {
		t.Fatalf("日志里出现了被回显的 API Key: %s", buf.String())
	}
}
