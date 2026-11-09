package persona

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"io/fs"
	"strings"
	"testing"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func chunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(kind)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	return b.Bytes()
}
func cardPNG(t *testing.T, kind, keyword, card string) []byte {
	b := pngBytes(t)
	payload := []byte(keyword + "\x00")
	if kind == "iTXt" {
		payload = append(payload, 0, 0, 0, 0)
	}
	payload = append(payload, []byte(base64.StdEncoding.EncodeToString([]byte(card)))...)
	out := append([]byte{}, b[:len(b)-12]...)
	out = append(out, chunk(kind, payload)...)
	return append(out, b[len(b)-12:]...)
}
func zipBytes(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, data := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func krpFiles(t *testing.T) map[string][]byte {
	return map[string][]byte{"index.toml": []byte("name = '小林'\nintro = '备注'\ntools = ['recall_memory','unknown']\ncustom = '保留'\n[[commands]]\nname = 'hello'\n"), "heart.prompt": []byte("心核 {{char}}"), "reason.prompt": []byte("智核"), "tools/a.py": []byte("raise Exception('不可执行')"), "expressions/index.json": []byte(`[{"id":"开心","path":"a.png","description":"微笑"}]`), "expressions/a.png": pngBytes(t)}
}
func mustReject(t *testing.T, name string, data []byte, opts ...Option) {
	t.Helper()
	if _, err := Parse(name, data, opts...); err == nil {
		t.Fatal("应拒绝不安全或非法输入")
	}
}

func TestSTJSON(t *testing.T) {
	card := `{"name":"小林","description":"外貌","personality":"开朗","scenario":"校园","first_mes":"你好 {{user}}","mes_example":"对话","system_prompt":"要求","post_history_instructions":"末尾","alternate_greetings":["嗨"],"creator_notes":"作者","tags":["测试"],"extensions":{"custom":1},"character_book":{"entries":[{"keys":["考试"],"content":"设定","enabled":false,"constant":true,"case_sensitive":true,"insertion_order":3,"comment":"注释","secondary_keys":["其他"]}]}}`
	for _, spec := range []string{"", "chara_card_v2", "chara_card_v3"} {
		t.Run(spec, func(t *testing.T) {
			input := card
			if spec != "" {
				input = `{"spec":"` + spec + `","data":` + card + `}`
			}
			got, err := Parse("card.json", []byte(input))
			if err != nil {
				t.Fatal(err)
			}
			c := got.Card
			if c.Name != "小林" || c.SourceFormat != "st" || c.FirstMessage != "你好 {{user}}" || c.PostHistoryInstructions != "末尾" || c.CreatorNotes != "作者" || len(c.AlternateGreetings) != 1 || len(c.Lorebook) != 1 || c.Lorebook[0].Enabled || !c.Lorebook[0].Constant || c.Lorebook[0].Order != 3 || c.Extensions["tags"] == nil || c.Extensions["extensions"] == nil || len(got.Warnings) == 0 {
				t.Fatalf("映射不完整: %+v", got)
			}
		})
	}
	got, err := Parse("long.json", []byte(`{"name":"`+strings.Repeat("名", 41)+`"}`))
	if err != nil || len([]rune(got.Card.Name)) != 40 || len(got.Warnings) == 0 {
		t.Fatalf("名字截断: %+v %v", got, err)
	}
	for _, bad := range []string{`{`, `[]`, `null`, `{"name":""}`, `{"spec":"unknown","data":{"name":"a"}}`, `{"name":"a","alternate_greetings":42}`} {
		mustReject(t, "card.json", []byte(bad))
	}
}

func TestPNGMetadata(t *testing.T) {
	for _, kind := range []string{"tEXt", "iTXt"} {
		for _, key := range []string{"chara", "ccv3"} {
			t.Run(kind+key, func(t *testing.T) {
				got, err := Parse("wrong.zip", cardPNG(t, kind, key, `{"name":"PNG卡"}`))
				if err != nil || got.Card.Name != "PNG卡" {
					t.Fatalf("PNG解析: %+v %v", got, err)
				}
			})
		}
	}
	b := cardPNG(t, "tEXt", "chara", `{"name":"旧卡"}`)
	pos := len(b) - 12
	newer := chunk("tEXt", []byte("ccv3\x00"+base64.StdEncoding.EncodeToString([]byte(`{"spec":"chara_card_v3","data":{"name":"新卡"}}`))))
	b = append(append(append([]byte{}, b[:pos]...), newer...), b[pos:]...)
	got, err := Parse("card.png", b)
	if err != nil || got.Card.Name != "新卡" {
		t.Fatalf("ccv3优先: %+v %v", got, err)
	}
	mustReject(t, "card.png", pngBytes(t))
	for _, b := range [][]byte{[]byte("\x89PNG\r\n\x1a\n"), append([]byte("\x89PNG\r\n\x1a\n"), 0xff, 0xff, 0xff, 0xff, 't', 'E', 'X', 't'), cardPNG(t, "tEXt", "chara", "不是JSON")} {
		mustReject(t, "card.png", b)
	}
	malformed := pngBytes(t)
	malformed[8] = 0xff
	mustReject(t, "card.png", malformed)
}

func TestKRP(t *testing.T) {
	for _, prefix := range []string{"", "sample.krp/"} {
		files := krpFiles(t)
		if prefix != "" {
			prefixed := map[string][]byte{}
			for k, v := range files {
				prefixed[prefix+k] = v
			}
			files = prefixed
		}
		got, err := Parse("sample.zip", zipBytes(t, files))
		if err != nil {
			t.Fatal(err)
		}
		if got.Card.Name != "小林" || got.Card.HeartPrompt != "心核 {{char}}" || got.Card.ReasonPrompt != "智核" || got.Card.SourceFormat != "krp" || len(got.Card.Tools) != 1 || got.Card.Tools[0] != "recall_memory" || len(got.Expressions) != 1 || got.Expressions[0].Key != "开心" || len(got.Warnings) < 3 || got.Card.Extensions["krp"] == nil {
			t.Fatalf("KRP映射: %+v", got)
		}
	}
	got, err := Parse("fallback.krp", zipBytes(t, map[string][]byte{"fallback.krp/index.toml": []byte("intro='备注'")}))
	if err != nil || got.Card.Name != "fallback" {
		t.Fatalf("目录名字回退: %+v %v", got, err)
	}
	files := krpFiles(t)
	files["expressions/a.png"] = []byte("伪装成图片的文本")
	got, err = Parse("card.krp", zipBytes(t, files))
	if err != nil || len(got.Expressions) != 0 || len(got.Warnings) == 0 {
		t.Fatalf("伪造图片应跳过: %+v %v", got, err)
	}
	files = krpFiles(t)
	files["expressions/index.json"] = []byte(`[{"id":"bad/key","path":"a.png"}]`)
	got, err = Parse("card.krp", zipBytes(t, files))
	if err != nil || len(got.Expressions) != 0 || len(got.Warnings) == 0 {
		t.Fatalf("非法key应跳过: %+v %v", got, err)
	}
	mustReject(t, "missing.krp", zipBytes(t, map[string][]byte{"heart.prompt": []byte("没有元数据")}))
}

func TestArchiveSecurity(t *testing.T) {
	for _, name := range []string{"../escape", "/absolute", "a/../escape", "a\\evil", "C:/evil"} {
		files := krpFiles(t)
		files[name] = []byte("x")
		t.Run(name, func(t *testing.T) { mustReject(t, "bad.zip", zipBytes(t, files)) })
	}
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	h := &zip.FileHeader{Name: "index.toml", Method: zip.Store}
	h.SetMode(fs.ModeSymlink | 0777)
	f, _ := w.CreateHeader(h)
	_, _ = f.Write([]byte("target"))
	_ = w.Close()
	mustReject(t, "symlink.zip", b.Bytes())
	files := krpFiles(t)
	for i := 0; i < 301; i++ {
		files["unknown/"+strings.Repeat("x", i+1)] = nil
	}
	mustReject(t, "many.zip", zipBytes(t, files))
	files = krpFiles(t)
	files["index.toml"] = []byte(strings.Repeat("#", (1<<20)+1))
	mustReject(t, "large.zip", zipBytes(t, files))
	files = krpFiles(t)
	mustReject(t, "image.zip", zipBytes(t, files), WithMaxImageBytes(1))
	files = krpFiles(t)
	entries := make([]map[string]string, 101)
	for i := range entries {
		entries[i] = map[string]string{"id": "a", "path": "a.png"}
	}
	files["expressions/index.json"], _ = json.Marshal(entries)
	mustReject(t, "many-images.zip", zipBytes(t, files))
	files = krpFiles(t)
	files["expressions/index.json"] = []byte(`[{"id":"开心","path":"../heart.prompt"}]`)
	mustReject(t, "escape.zip", zipBytes(t, files))
	files = krpFiles(t)
	files["other.krp/index.toml"] = []byte("name='另一个'")
	mustReject(t, "roots.zip", zipBytes(t, files))
}

func TestSizeLimitsAndBomb(t *testing.T) {
	mustReject(t, "large.json", bytes.Repeat([]byte(" "), (5<<20)+1))
	mustReject(t, "upload", make([]byte, (50<<20)+1))
	b := pngBytes(t)
	pos := len(b) - 12
	b = append(append(append([]byte{}, b[:pos]...), chunk("tEXt", bytes.Repeat([]byte("x"), (5<<20)+1))...), b[pos:]...)
	mustReject(t, "large.png", b)
	// 实际解压体积超过总上限；所有未知文件也必须计入实际读取总量。
	files := map[string][]byte{"index.toml": []byte("name='大包'")}
	large := bytes.Repeat([]byte("x"), 1<<20)
	for i := 0; i < 101; i++ {
		files["data/"+strings.Repeat("a", i+1)+".bin"] = large
	}
	mustReject(t, "total.zip", zipBytes(t, files))
	// 篡改中央目录的解压大小为 1：不能因声明小而接受超出限制的流。
	bomb := zipBytes(t, map[string][]byte{"index.toml": []byte("name='炸弹'"), "data.bin": bytes.Repeat([]byte("x"), 2<<20)})
	for start := 0; start < len(bomb)-46; start++ {
		if string(bomb[start:start+4]) == "PK\x01\x02" && string(bomb[start+46:start+54]) == "data.bin" {
			binary.LittleEndian.PutUint32(bomb[start+24:start+28], 1)
			break
		}
	}
	mustReject(t, "bomb.zip", bomb)
}

func TestValidate(t *testing.T) {
	if err := Validate(Card{Name: "有效", Lorebook: []LoreEntry{{Enabled: false, Content: "保留"}}}); err != nil {
		t.Fatal(err)
	}
	cases := []Card{{Name: ""}, {Name: strings.Repeat("名", 41)}, {Name: "名", HeartPrompt: strings.Repeat("字", 20001)}, {Name: "名", AlternateGreetings: make([]string, 21)}, {Name: "名", AlternateGreetings: []string{strings.Repeat("字", 2001)}}, {Name: "名", Lorebook: make([]LoreEntry, 101)}, {Name: "名", Lorebook: []LoreEntry{{Content: strings.Repeat("字", 2001)}}}, {Name: "名", Lorebook: []LoreEntry{{Keys: make([]string, 21)}}}, {Name: "名", Tools: []string{"exec"}}}
	for i, c := range cases {
		if err := Validate(c); err == nil {
			t.Errorf("用例%d应拒绝", i)
		}
	}
	for _, key := range []string{"abc_1-开心", strings.Repeat("中", 32)} {
		if !ValidExpressionKey(key) {
			t.Errorf("合法key=%q", key)
		}
	}
	for _, key := range []string{"", strings.Repeat("中", 33), "a.b", "🙂", "a/b"} {
		if ValidExpressionKey(key) {
			t.Errorf("非法key=%q", key)
		}
	}
	if got := ReplaceMacros("{{CHAR}} <bot> {{User}} <USER> {{unknown}}", "小林"); got != "小林 小林 对方 对方 {{unknown}}" {
		t.Fatalf("宏替换=%q", got)
	}
}

// TestArchiveMalformed 拒绝损坏的条目和表情索引，同时保证错误路径不 panic。
func TestArchiveMalformed(t *testing.T) {
	for _, raw := range [][]byte{[]byte("PK\x03\x04"), []byte("PK\x05\x06")} {
		mustReject(t, "broken.krp", raw)
	}
	for _, index := range []string{`{`, `[{"id":"开心","path":"missing.png"}]`, `[{"id":"开心","path":"a.png"},{"id":"开心","path":"a.png"}]`, `[{"id":"开心","path":"a.png","description":"` + strings.Repeat("字", 201) + `"}]`} {
		files := krpFiles(t)
		files["expressions/index.json"] = []byte(index)
		mustReject(t, "bad-index.krp", zipBytes(t, files))
	}
	for _, name := range []string{"heart.prompt", "expressions/index.json"} {
		files := krpFiles(t)
		files[name] = bytes.Repeat([]byte(" "), (1<<20)+1)
		mustReject(t, "large-text.krp", zipBytes(t, files))
	}
	files := krpFiles(t)
	files["index.toml"] = []byte("name=[")
	mustReject(t, "toml.krp", zipBytes(t, files))
	// 缺省名称来自根布局的上传名，不依赖磁盘路径。
	got, err := Parse("root.krp", zipBytes(t, map[string][]byte{"index.toml": []byte("intro='注释'")}))
	if err != nil || got.Card.Name != "root" {
		t.Fatalf("根布局名字: %+v %v", got, err)
	}
}

func TestPNGCorruption(t *testing.T) {
	b := cardPNG(t, "tEXt", "chara", `{"name":"卡"}`)
	b[len(b)-1] ^= 1
	mustReject(t, "crc.png", b)
	b = pngBytes(t)
	pos := len(b) - 12
	for _, payload := range [][]byte{[]byte("chara\x00%%%"), []byte("chara\x00\x01\x00\x00\x00xxx"), []byte("chara\x00\x00\x00no-separators")} {
		out := append(append(append([]byte{}, b[:pos]...), chunk("iTXt", payload)...), b[pos:]...)
		mustReject(t, "itxt.png", out)
	}
}

func TestExactTextLimits(t *testing.T) {
	data := []byte(`{"name":"边界"}`)
	data = append(data, bytes.Repeat([]byte(" "), maxJSONBytes-len(data))...)
	if _, err := Parse("max.json", data); err != nil {
		t.Fatalf("恰好 5 MB 应允许: %v", err)
	}
	meta := []byte("name='边界'\n#")
	meta = append(meta, bytes.Repeat([]byte("x"), maxTextBytes-len(meta))...)
	if _, err := Parse("max.krp", zipBytes(t, map[string][]byte{"index.toml": meta})); err != nil {
		t.Fatalf("恰好 1 MB 文本应允许: %v", err)
	}
	c := Card{Name: strings.Repeat("名", 40), HeartPrompt: strings.Repeat("字", 20000), AlternateGreetings: []string{strings.Repeat("字", 2000)}, Lorebook: []LoreEntry{{Content: strings.Repeat("字", 2000), Keys: make([]string, 20)}}, Tools: []string{"recall_memory", "get_time", "lookup_lore"}}
	if err := Validate(c); err != nil {
		t.Fatalf("恰好字段上限应允许: %v", err)
	}
}

func TestPNGPrefersCCV3OverInvalidLegacy(t *testing.T) {
	b := cardPNG(t, "tEXt", "ccv3", `{"spec":"chara_card_v3","data":{"name":"新卡"}}`)
	pos := len(b) - 12
	b = append(append(append([]byte{}, b[:pos]...), chunk("tEXt", []byte("chara\x00%%%"))...), b[pos:]...)
	got, err := Parse("card.png", b)
	if err != nil || got.Card.Name != "新卡" {
		t.Fatalf("优先 ccv3，不应解析旧元数据: %+v %v", got, err)
	}
}

func TestSTWrapperDoesNotLeakIntoExtensions(t *testing.T) {
	got, err := Parse("card.json", []byte(`{"spec":"chara_card_v2","spec_version":"2.0","data":{"name":"小林","custom":"保留"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Card.Extensions) != 1 || got.Card.Extensions["custom"] != "保留" {
		t.Fatalf("扩展只应保存 payload 未识别字段: %+v", got.Card.Extensions)
	}
}

func TestKRPCommandWarningHasSingleSlash(t *testing.T) {
	files := krpFiles(t)
	files["index.toml"] = []byte("name='小林'\n[[commands]]\nname='/hello'\n")
	got, err := Parse("card.krp", zipBytes(t, files))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range got.Warnings {
		if strings.Contains(warning, "人设命令") {
			found = true
			if warning != "已忽略人设命令 /hello" {
				t.Fatalf("命令警告应只带单个斜杠: %q", warning)
			}
		}
	}
	if !found {
		t.Fatal("缺少忽略命令的警告")
	}
}
