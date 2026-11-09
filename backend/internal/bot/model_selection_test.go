package bot

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

// TestModelRequest 覆盖三条模型回退链及每类调用的生成参数。
func TestModelRequest(t *testing.T) {
	var id pgtype.UUID
	if err := id.Scan("00000000-0000-4000-8000-000000000001"); err != nil {
		t.Fatal(err)
	}
	base := store.Bot{ID: id, Model: " heart ", ReasonModel: " bot-reason ", MaxOutputTokens: 400, Temperature: 0.7}
	settings := Settings{Model: "default", ReasonModel: "global-reason", MemoryModel: "memory"}
	cases := []struct {
		name, kind, botModel, botReason, globalReason, memory, want string
		max                                                         int
		temp                                                        float64
		key                                                         string
	}{
		{"心核覆盖", "reply", "heart", "bot-reason", "global-reason", "memory", "heart", 400, 0.7, CacheKey(id)},
		{"重写沿用心核", "retry", "heart", "", "", "", "heart", 400, 0.7, CacheKey(id)},
		{"心核回退", "reply", " ", "", "", "", "default", 400, 0.7, CacheKey(id)},
		{"智核覆盖", "reason", "heart", " bot-reason ", "global-reason", "", "bot-reason", 800, 0.5, CacheKey(id) + "-reason"},
		{"智核全局", "reason", "heart", " ", "global-reason", "", "global-reason", 800, 0.5, CacheKey(id) + "-reason"},
		{"智核心核", "reason", "heart", "", " ", "", "heart", 800, 0.5, CacheKey(id) + "-reason"},
		{"智核最终回退", "reason", " ", "", "", "", "default", 800, 0.5, CacheKey(id) + "-reason"},
		{"记忆覆盖", "memory", "heart", "bot-reason", "global-reason", " memory ", "memory", 1200, 0.2, "anonchat-memory"},
		{"记忆直接回退全局心核", "memory", "heart", "bot-reason", "global-reason", " ", "default", 1200, 0.2, "anonchat-memory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, s := base, settings
			b.Model, b.ReasonModel = tc.botModel, tc.botReason
			s.ReasonModel, s.MemoryModel = tc.globalReason, tc.memory
			e := &Engine{settings: &SettingsCache{current: s}}
			got := e.modelRequest(b, tc.kind)
			if got.Model != tc.want || got.MaxTokens != tc.max || math.Abs(got.Temperature-tc.temp) > 1e-6 || got.CacheKey != tc.key {
				t.Fatalf("请求参数 = %+v，期望模型 %s tokens=%d temperature=%v key=%s", got, tc.want, tc.max, tc.temp, tc.key)
			}
		})
	}
	base.MaxOutputTokens = 1600
	e := &Engine{settings: &SettingsCache{current: settings}}
	if got := e.modelRequest(base, "reason"); got.MaxTokens != 1600 {
		t.Fatalf("智核不能降低 bot 的额度: %+v", got)
	}
}

// TestPersonaMessages 验证共用助手保留顺序、不修改输入，并将设定合并到首条 user。
func TestPersonaMessages(t *testing.T) {
	history := []ChatMessage{{Role: "user", Content: "你好"}, {Role: "assistant", Content: "嗨"}}
	before := append([]ChatMessage(nil), history...)
	got := personaMessages(PersonaModeUser, "设定", history)
	if !reflect.DeepEqual(history, before) {
		t.Fatal("不应修改原始历史")
	}
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "设定\n\n你好" || got[1] != history[1] {
		t.Fatalf("user 注入错误: %+v", got)
	}
	for _, mode := range []string{PersonaModeUser, PersonaModeSystem} {
		got := personaMessages(mode, "设定", history[1:])
		role := "system"
		if mode == PersonaModeUser {
			role = "user"
		}
		if len(got) != 2 || got[0].Role != role || !strings.Contains(got[0].Content, "设定") || got[1] != history[1] {
			t.Fatalf("%s 注入错误: %+v", mode, got)
		}
	}
	if got := personaMessages(PersonaModeUser, "设定", nil); len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("空历史: %+v", got)
	}
}
