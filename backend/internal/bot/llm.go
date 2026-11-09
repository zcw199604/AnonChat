package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ChatMessage 是一条对话消息，role 取值 system | user | assistant。
type ChatMessage struct {
	Role    string
	Content string
}

// ChatRequest 是一次模型调用请求。
type ChatRequest struct {
	Model       string
	Messages    []ChatMessage
	MaxTokens   int
	Temperature float64
	// CacheKey 是提示词前缀缓存的键；为空表示不参与缓存。
	CacheKey string
}

// ChatResult 是一次模型调用的结果与用量。
type ChatResult struct {
	Content          string
	PromptTokens     int
	CompletionTokens int
	// CachedTokens 是 prompt 中命中前缀缓存的部分。
	CachedTokens int
	// FinishReason 是结束原因（stop / length 等）；length 表示输出额度用完。
	FinishReason string
}

// LLM 是模型客户端接口（集成测试可注入假实现）。
type LLM interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResult, error)
}

// 重试与日志参数。
const (
	// llmMaxAttempts 是单次调用的最大尝试次数（首次 + 重试 1 次）。
	llmMaxAttempts = 2
	// llmRetryWait 是默认重试间隔。
	llmRetryWait = time.Second
	// llmMaxRetryAfter 是 Retry-After 的等待上限。
	llmMaxRetryAfter = 5 * time.Second
)

// sharedHTTPClient 是整个进程共用的 HTTP 客户端，复用连接（how.md §5）。
var sharedHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 16,
		IdleConnTimeout:     90 * time.Second,
	},
}

// ClientOptions 是 OpenAI 兼容客户端的配置。
type ClientOptions struct {
	// BaseURL 是接口地址，不带 /chat/completions。
	BaseURL string
	APIKey  string
	// SendCacheKey 为 false 时不发送 prompt_cache_key（兼容拒绝未知参数的服务商）。
	SendCacheKey bool
	Logger       *slog.Logger
}

// OpenAIClient 是 OpenAI 兼容协议（DeepSeek、OpenRouter、通义等）的实现。
type OpenAIClient struct {
	endpoint     string
	apiKey       string
	sendCacheKey bool
	logger       *slog.Logger
	client       *http.Client
	// sleep 用于重试间隔，测试可替换。
	sleep func(ctx context.Context, d time.Duration) error
}

// NewOpenAIClient 创建客户端。
func NewOpenAIClient(opts ClientOptions) *OpenAIClient {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &OpenAIClient{
		endpoint:     strings.TrimRight(opts.BaseURL, "/") + "/chat/completions",
		apiKey:       opts.APIKey,
		sendCacheKey: opts.SendCacheKey,
		logger:       logger,
		client:       sharedHTTPClient,
		sleep:        sleepCtx,
	}
}

// chatRequestPayload 是 OpenAI 兼容接口的请求体。
type chatRequestPayload struct {
	Model          string        `json:"model"`
	Messages       []wireMessage `json:"messages"`
	MaxTokens      int           `json:"max_tokens"`
	Temperature    float64       `json:"temperature"`
	Stream         bool          `json:"stream"`
	PromptCacheKey string        `json:"prompt_cache_key,omitempty"`
}

type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatResponsePayload 是 OpenAI 兼容接口的响应体（只取用到的字段）。
type chatResponsePayload struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		// PromptTokensDetails 是 OpenAI / OpenRouter / 通义等的缓存命中字段。
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		// PromptCacheHitTokens 是 DeepSeek 的缓存命中字段。
		PromptCacheHitTokens int `json:"prompt_cache_hit_tokens"`
	} `json:"usage"`
}

// Chat 执行一次模型调用；429 与 5xx 只重试 1 次。
func (c *OpenAIClient) Chat(ctx context.Context, req ChatRequest) (ChatResult, error) {
	payload := chatRequestPayload{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		Stream:      false,
		Messages:    make([]wireMessage, 0, len(req.Messages)),
	}
	for _, m := range req.Messages {
		payload.Messages = append(payload.Messages, wireMessage(m))
	}
	// 只有显式开启且 CacheKey 非空时才带上该字段（关掉时请求体里不能出现）。
	if c.sendCacheKey && req.CacheKey != "" {
		payload.PromptCacheKey = req.CacheKey
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return ChatResult{}, fmt.Errorf("序列化模型请求失败: %w", err)
	}

	var lastErr error
	for attempt := 1; attempt <= llmMaxAttempts; attempt++ {
		res, retryAfter, err := c.do(ctx, body)
		if err == nil {
			return res, nil
		}
		lastErr = err
		if retryAfter == nil || attempt == llmMaxAttempts {
			break
		}
		// 429 / 5xx：等待后重试一次。
		if serr := c.sleep(ctx, *retryAfter); serr != nil {
			return ChatResult{}, fmt.Errorf("重试等待被取消: %w", serr)
		}
	}
	return ChatResult{}, lastErr
}

// do 发送一次请求。返回非空 retryAfter 表示应当重试。
func (c *OpenAIClient) do(ctx context.Context, body []byte) (ChatResult, *time.Duration, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return ChatResult{}, nil, fmt.Errorf("构造模型请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		// 网络错误不重试。
		return ChatResult{}, nil, fmt.Errorf("调用模型失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 上游可能回显请求或智核内容，错误响应正文一律不进入日志。
		c.logger.Warn("模型接口返回错误", "status", resp.StatusCode)

		err := fmt.Errorf("模型接口返回 %d", resp.StatusCode)
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError {
			wait := c.retryAfter(resp)
			return ChatResult{}, &wait, err
		}
		return ChatResult{}, nil, err
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return ChatResult{}, nil, fmt.Errorf("读取模型响应失败: %w", err)
	}
	var out chatResponsePayload
	if err := json.Unmarshal(data, &out); err != nil {
		return ChatResult{}, nil, fmt.Errorf("解析模型响应失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return ChatResult{}, nil, errors.New("模型响应没有 choices")
	}

	cached := out.Usage.PromptCacheHitTokens
	if out.Usage.PromptTokensDetails != nil && out.Usage.PromptTokensDetails.CachedTokens > cached {
		cached = out.Usage.PromptTokensDetails.CachedTokens
	}
	return ChatResult{
		Content:          out.Choices[0].Message.Content,
		FinishReason:     out.Choices[0].FinishReason,
		PromptTokens:     out.Usage.PromptTokens,
		CompletionTokens: out.Usage.CompletionTokens,
		CachedTokens:     cached,
	}, nil, nil
}

// retryAfter 解析 Retry-After（秒），缺省 1 秒，上限 5 秒。
func (c *OpenAIClient) retryAfter(resp *http.Response) time.Duration {
	wait := llmRetryWait
	if raw := strings.TrimSpace(resp.Header.Get("Retry-After")); raw != "" {
		if secs, err := strconv.Atoi(raw); err == nil && secs >= 0 {
			wait = time.Duration(secs) * time.Second
		}
	}
	if wait > llmMaxRetryAfter {
		wait = llmMaxRetryAfter
	}
	return wait
}

// sleepCtx 是可被 ctx 取消的等待。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
