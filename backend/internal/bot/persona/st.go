package persona

import (
	"bytes"
	"encoding/json"
	"errors"
)

// parseST 映射 ST V1、V2、V3，保留未识别字段以免往返时丢失信息。
func parseST(data []byte) (Result, error) {
	if len(data) > maxJSONBytes {
		return Result{}, errors.New("JSON 文本不能超过 5 MB")
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil || root == nil {
		return Result{}, errors.New("角色卡 JSON 格式错误")
	}
	var spec string
	if raw, ok := root["spec"]; ok {
		if json.Unmarshal(raw, &spec) != nil || (spec != "chara_card_v2" && spec != "chara_card_v3") {
			return Result{}, errors.New("不支持的角色卡版本")
		}
		// 包装层只用于识别版本；扩展字段来自 data，不能把 spec/data 合并进去。
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(root["data"], &payload); err != nil || payload == nil {
			return Result{}, errors.New("角色卡 data 格式错误")
		}
		root = payload
	}
	var v struct {
		Name                    string   `json:"name"`
		Description             string   `json:"description"`
		Personality             string   `json:"personality"`
		Scenario                string   `json:"scenario"`
		FirstMessage            string   `json:"first_mes"`
		ExampleDialogue         string   `json:"mes_example"`
		SystemPrompt            string   `json:"system_prompt"`
		PostHistoryInstructions string   `json:"post_history_instructions"`
		AlternateGreetings      []string `json:"alternate_greetings"`
		CreatorNotes            string   `json:"creator_notes"`
		Book                    struct {
			Entries []struct {
				Keys          []string `json:"keys"`
				Content       string   `json:"content"`
				Enabled       bool     `json:"enabled"`
				Constant      bool     `json:"constant"`
				CaseSensitive bool     `json:"case_sensitive"`
				Order         int      `json:"insertion_order"`
				Comment       string   `json:"comment"`
			} `json:"entries"`
		} `json:"character_book"`
	}
	normalized, err := json.Marshal(root)
	if err != nil {
		return Result{}, errors.New("角色卡 JSON 格式错误")
	}
	if json.Unmarshal(normalized, &v) != nil {
		return Result{}, errors.New("角色卡字段格式错误")
	}
	r := Result{Card: Card{Name: v.Name, Description: v.Description, Personality: v.Personality, Scenario: v.Scenario, FirstMessage: v.FirstMessage, ExampleDialogue: v.ExampleDialogue, SystemPrompt: v.SystemPrompt, PostHistoryInstructions: v.PostHistoryInstructions, AlternateGreetings: v.AlternateGreetings, CreatorNotes: v.CreatorNotes, SourceFormat: "st", Extensions: map[string]any{}}}
	for _, entry := range v.Book.Entries {
		r.Card.Lorebook = append(r.Card.Lorebook, LoreEntry{Keys: entry.Keys, Content: entry.Content, Enabled: entry.Enabled, Constant: entry.Constant, CaseSensitive: entry.CaseSensitive, Order: entry.Order, Comment: entry.Comment})
	}
	if raw := root["character_book"]; len(raw) > 0 {
		var book struct {
			Entries []map[string]json.RawMessage `json:"entries"`
		}
		_ = json.Unmarshal(raw, &book)
		for _, entry := range book.Entries {
			for _, key := range []string{"selective", "secondary_keys", "position"} {
				if _, ok := entry[key]; ok {
					r.Warnings = append(r.Warnings, "世界书的 selective、secondary_keys、position 暂不支持，只按主关键词触发")
					break
				}
			}
		}
	}
	known := map[string]bool{}
	for _, key := range []string{"name", "description", "personality", "scenario", "first_mes", "mes_example", "system_prompt", "post_history_instructions", "alternate_greetings", "creator_notes", "character_book"} {
		known[key] = true
	}
	for key, raw := range root {
		if known[key] {
			continue
		}
		var value any
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if dec.Decode(&value) != nil {
			return Result{}, errors.New("角色卡扩展字段格式错误")
		}
		r.Card.Extensions[key] = value
	}
	return r, nil
}
