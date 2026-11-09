package persona

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
)

// safeArchivePath 在任何解压或内容读取前校验不可信路径。
func safeArchivePath(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\\x00") || strings.Contains(name, ":") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func parseKRP(fileName string, data []byte, o options) (Result, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Result{}, errors.New("人设包 zip 格式错误")
	}
	if len(reader.File) > maxArchiveEntries {
		return Result{}, errors.New("zip 条目数不能超过 300")
	}
	paths := map[string]bool{}
	for _, f := range reader.File {
		if !safeArchivePath(f.Name) || f.Mode()&fs.ModeSymlink != 0 {
			return Result{}, errors.New("zip 包含不安全路径或符号链接")
		}
		if paths[f.Name] {
			return Result{}, errors.New("zip 包含重复路径")
		}
		paths[f.Name] = true
	}
	prefix := ""
	if !paths["index.toml"] {
		for _, f := range reader.File {
			parts := strings.Split(f.Name, "/")
			if len(parts) > 1 && strings.HasSuffix(parts[0], ".krp") {
				candidate := parts[0] + "/"
				if prefix != "" && prefix != candidate {
					return Result{}, errors.New("人设包只能包含一个顶层 .krp 目录")
				}
				prefix = candidate
			}
		}
		if prefix == "" || !paths[prefix+"index.toml"] {
			return Result{}, errors.New("人设包缺少 index.toml")
		}
	}
	content := map[string][]byte{}
	var total int64
	for _, f := range reader.File {
		if prefix != "" && !strings.HasPrefix(f.Name, prefix) {
			return Result{}, errors.New("人设包顶层目录结构错误")
		}
		name := strings.TrimPrefix(f.Name, prefix)
		if strings.HasSuffix(name, "/") {
			continue
		}
		if name != "index.toml" && strings.HasSuffix(name, "/index.toml") {
			return Result{}, errors.New("人设包只能有一个 index.toml")
		}
		ext := strings.ToLower(path.Ext(name))
		text := ext == ".toml" || ext == ".prompt" || ext == ".json"
		image := strings.HasPrefix(name, "expressions/") && !text
		keep := text || image
		limit := int64(maxArchiveBytes) - total
		if text && limit > maxTextBytes {
			limit = maxTextBytes
		}
		if image && limit > o.maxImageBytes {
			limit = o.maxImageBytes
		}
		rc, err := f.Open()
		if err != nil {
			return Result{}, errors.New("无法读取 zip 条目")
		}
		var body []byte
		var n int64
		if keep {
			body, err = io.ReadAll(io.LimitReader(rc, limit+1))
			n = int64(len(body))
		} else {
			n, err = io.Copy(io.Discard, io.LimitReader(rc, limit+1))
		}
		closeErr := rc.Close()
		total += n
		if total > maxArchiveBytes {
			return Result{}, errors.New("zip 实际解压总大小不能超过 100 MB")
		}
		if text && n > maxTextBytes {
			return Result{}, errors.New("单个文本文件不能超过 1 MB")
		}
		if image && n > o.maxImageBytes {
			return Result{}, errors.New("表情图片超过媒体单文件大小上限")
		}
		if err != nil || closeErr != nil {
			return Result{}, errors.New("zip 条目读取失败或声明大小与实际不符")
		}
		if keep {
			content[name] = body
		}
	}
	var meta map[string]any
	if !utf8.Valid(content["index.toml"]) {
		return Result{}, errors.New("index.toml 必须是 UTF-8 文本")
	}
	if _, err := toml.Decode(string(content["index.toml"]), &meta); err != nil {
		return Result{}, errors.New("index.toml 格式错误")
	}
	r := Result{Card: Card{SourceFormat: "krp", Extensions: map[string]any{}}}
	if name, ok := meta["name"]; ok {
		v, valid := name.(string)
		if !valid {
			return Result{}, errors.New("人设名称必须是文本")
		}
		r.Card.Name = v
	}
	if strings.TrimSpace(r.Card.Name) == "" {
		fallback := strings.TrimSuffix(strings.TrimSuffix(prefix, "/"), ".krp")
		if fallback == "" {
			fallback = strings.TrimSuffix(path.Base(fileName), path.Ext(fileName))
		}
		r.Card.Name = fallback
	}
	if intro, ok := meta["intro"]; ok {
		v, valid := intro.(string)
		if !valid {
			return Result{}, errors.New("人设备注必须是文本")
		}
		r.Card.CreatorNotes = v
	}
	if tools, ok := meta["tools"]; ok {
		items, valid := tools.([]any)
		if !valid {
			return Result{}, errors.New("内置工具列表格式错误")
		}
		for _, item := range items {
			v, valid := item.(string)
			if !valid {
				return Result{}, errors.New("内置工具名称必须是文本")
			}
			r.Card.Tools = append(r.Card.Tools, v)
		}
	}
	for _, name := range []string{"heart.prompt", "reason.prompt"} {
		if !utf8.Valid(content[name]) {
			return Result{}, errors.New("人设提示词必须是 UTF-8 文本")
		}
	}
	r.Card.HeartPrompt = string(content["heart.prompt"])
	r.Card.ReasonPrompt = string(content["reason.prompt"])
	if commands, ok := meta["commands"].([]map[string]any); ok {
		for _, command := range commands {
			name, _ := command["name"].(string)
			r.Warnings = append(r.Warnings, "已忽略人设命令 /"+strings.TrimLeft(name, "/"))
		}
	} else if _, ok := meta["commands"]; ok {
		r.Warnings = append(r.Warnings, "已忽略人设命令")
	}
	extras := map[string]any{}
	for key, value := range meta {
		if key != "name" && key != "intro" && key != "tools" && key != "commands" {
			extras[key] = value
		}
	}
	if len(extras) > 0 {
		r.Card.Extensions["krp"] = extras
	}
	for _, f := range reader.File {
		name := strings.TrimPrefix(f.Name, prefix)
		if strings.HasPrefix(name, "tools/") && strings.HasSuffix(strings.ToLower(name), ".py") {
			r.Warnings = append(r.Warnings, "已忽略上传代码 "+name)
		}
	}
	var expressions []struct {
		ID          string `json:"id"`
		Path        string `json:"path"`
		Description string `json:"description"`
	}
	if raw, ok := content["expressions/index.json"]; ok {
		if json.Unmarshal(raw, &expressions) != nil {
			return Result{}, errors.New("表情索引 JSON 格式错误")
		}
	}
	if len(expressions) > maxExpressions {
		return Result{}, errors.New("表情数量不能超过 100")
	}
	keys := map[string]bool{}
	for _, expression := range expressions {
		if !safeArchivePath(expression.Path) || path.Clean(expression.Path) != expression.Path {
			return Result{}, errors.New("表情路径不安全")
		}
		name := "expressions/" + expression.Path
		body, exists := content[name]
		if !exists {
			return Result{}, errors.New("表情路径没有指向包内图片")
		}
		if !ValidExpressionKey(expression.ID) {
			r.Warnings = append(r.Warnings, "已跳过标识非法的表情")
			continue
		}
		if keys[expression.ID] {
			return Result{}, errors.New("表情标识不能重复")
		}
		keys[expression.ID] = true
		if utf8.RuneCountInString(expression.Description) > 200 {
			return Result{}, errors.New("表情描述最多 200 字")
		}
		switch http.DetectContentType(body) {
		case "image/png", "image/jpeg", "image/gif", "image/webp":
		default:
			r.Warnings = append(r.Warnings, "已跳过类型不支持的表情 "+expression.ID)
			continue
		}
		r.Expressions = append(r.Expressions, ExpressionFile{Key: expression.ID, Description: expression.Description, FileName: path.Base(expression.Path), Data: body})
	}
	return r, nil
}
