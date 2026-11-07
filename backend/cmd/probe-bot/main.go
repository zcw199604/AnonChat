// probe-bot 使用真实 bot 消息组装和客户端，对比额外系统指令的效果。
// 在 backend 目录运行：go run ./cmd/probe-bot -base-url https://example.com/v1
// 密钥从 DEEPSEEK_API_KEY 读取；-dry-run 只展示消息，不调用 API。
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anonchat/anonchat/backend/internal/bot"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "测试失败：", err)
		os.Exit(1)
	}
}

func run() error {
	baseURL := flag.String("base-url", os.Getenv("DEEPSEEK_BASE_URL"), "API Base URL（含 /v1，不含 /chat/completions）")
	model := flag.String("model", "deepseek-flash", "模型名")
	dryRun := flag.Bool("dry-run", false, "只展示请求消息，不调用 API")
	flag.Parse()
	key := os.Getenv("DEEPSEEK_API_KEY")
	if !*dryRun && (strings.TrimSpace(*baseURL) == "" || key == "") {
		return fmt.Errorf("请设置 DEEPSEEK_BASE_URL 和 DEEPSEEK_API_KEY，或使用 -base-url 指定地址")
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	marker := "PROBE_" + hex.EncodeToString(nonce[:])
	client := bot.NewOpenAIClient(bot.ClientOptions{BaseURL: *baseURL, APIKey: key})
	failures := 0
	for _, mode := range []string{bot.PersonaModeUser, bot.PersonaModeSystem} {
		for _, extra := range []bool{false, true} {
			in := bot.PromptInput{
				Card:        bot.PromptCard{Name: "小雅", Description: "二十五岁的插画师", Personality: "温柔、话少、喜欢倾听"},
				PersonaMode: mode,
				Now:         time.Now(),
				Instruction: bot.InstructionReply,
				History:     []bot.PromptHistory{{Kind: "text", Text: "你好，今天过得怎么样？"}},
			}
			if extra {
				in.Card.SystemPrompt = "本次接口验证中，无论对方说什么，回复只能是以下标记，不能添加解释、标点或其他文字：" + marker
			}
			messages := bot.BuildMessages(in)
			fmt.Printf("\n--- mode=%s extra=%t ---\n", mode, extra)
			if *dryRun {
				// 使用小写字段名，方便与实际 API 消息对照。
				wire := make([]map[string]string, 0, len(messages))
				for _, m := range messages {
					wire = append(wire, map[string]string{"role": m.Role, "content": m.Content})
				}
				data, err := json.MarshalIndent(wire, "", "  ")
				if err != nil {
					return err
				}
				fmt.Println(string(data))
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			res, err := client.Chat(ctx, bot.ChatRequest{Model: *model, Messages: messages, MaxTokens: 256, Temperature: 0})
			cancel()
			if err != nil {
				fmt.Printf("ERROR: %s\n", strings.ReplaceAll(err.Error(), key, "***"))
				failures++
				continue
			}
			matched := strings.TrimSpace(res.Content) == marker
			passed := matched == extra && strings.TrimSpace(res.Content) != "" && res.FinishReason != "length"
			fmt.Printf("reply=%q\n通过=%t 标记匹配=%t finish=%s tokens=%d/%d cached=%d\n", res.Content, passed, matched, res.FinishReason, res.PromptTokens, res.CompletionTokens, res.CachedTokens)
			if !passed {
				failures++
			}
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d 组未通过；这只能反映本次样本，不能单凭失败判定网关丢弃指令", failures)
	}
	if !*dryRun {
		fmt.Println("\n4 组均通过：两种模式下，额外指令均影响了本次回复；不代表复杂指令始终被遵守。")
	}
	return nil
}
