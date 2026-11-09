//go:build integration

package bot_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

// personaUpload 模拟带管理登录态的单文件表单上传。
func personaUpload(t *testing.T, srv *httptest.Server, path, cookie, name string, data []byte, fields map[string]string) (int, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	f, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: cookie})
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

// personaPNG 与 zip 均在测试时生成，不在仓库中保存二进制样例。
func personaPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func personaKRP(t *testing.T, name string, expressionCount int) []byte {
	t.Helper()
	files := map[string][]byte{
		"fixture.krp/index.toml":       []byte("name = " + strconvQuote(name) + "\nintro = '作者备注-secret'\n[[commands]]\nname = '/ignored'\n"),
		"fixture.krp/heart.prompt":     []byte("心核-secret"),
		"fixture.krp/reason.prompt":    []byte("智核-secret"),
		"fixture.krp/tools/ignored.py": []byte("raise RuntimeError('不得执行')"),
	}
	entries := make([]map[string]string, 0, expressionCount)
	for i := 0; i < expressionCount; i++ {
		key := string(rune('a' + i))
		file := key + ".png"
		files["fixture.krp/expressions/"+file] = personaPNG(t)
		entries = append(entries, map[string]string{"id": key, "path": file, "description": "表情说明-secret"})
	}
	index, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	files["fixture.krp/expressions/index.json"] = index
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	// 固定写入顺序，避免导入失败用例依赖 map 顺序。
	names := []string{"fixture.krp/index.toml", "fixture.krp/heart.prompt", "fixture.krp/reason.prompt", "fixture.krp/tools/ignored.py", "fixture.krp/expressions/index.json"}
	for i := 0; i < expressionCount; i++ {
		names = append(names, "fixture.krp/expressions/"+string(rune('a'+i))+".png")
	}
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func strconvQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func personaObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析响应失败: %v %s", err, body)
	}
	return out
}

func personaFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func personaCount(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	// 表名来自测试内固定常量。
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPersonaImportAndExpressions(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: root})
	cookie := loginAdmin(t, pool, env.Server)
	status, body := personaUpload(t, env.Server, "/api/admin/character-cards/import", cookie, "fixture.krp", personaKRP(t, "导入角色-secret", 2), nil)
	if status != http.StatusCreated {
		t.Fatalf("导入应成功: %d %s", status, body)
	}
	result := personaObject(t, body)
	card, ok := result["card"].(map[string]any)
	if !ok {
		t.Fatalf("缺少 card: %s", body)
	}
	id, ok := card["id"].(string)
	if !ok || id == "" {
		t.Fatalf("缺少角色卡 ID: %s", body)
	}
	if card["heartPrompt"] != "心核-secret" || card["reasonPrompt"] != "智核-secret" || card["creatorNotes"] != "作者备注-secret" || card["sourceFormat"] != "krp" || card["expressionCount"] != float64(2) {
		t.Fatalf("导入字段不完整: %s", body)
	}
	warnings, ok := result["warnings"].([]any)
	if !ok || len(warnings) < 2 {
		t.Fatalf("忽略代码和命令必须给警告: %s", body)
	}
	for _, table := range []string{"character_cards", "media_folders"} {
		if n := personaCount(t, pool, table); n != 1 {
			t.Fatalf("%s = %d", table, n)
		}
	}
	if n := personaCount(t, pool, "media_items"); n != 2 {
		t.Fatalf("媒体条目数=%d", n)
	}
	if n := len(personaFiles(t, root)); n != 2 {
		t.Fatalf("图片文件数=%d", n)
	}
	var badItems int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM media_items WHERE kind <> 'image' OR cardinality(tags) <> 0 OR caption <> '表情说明-secret'").Scan(&badItems); err != nil {
		t.Fatal(err)
	}
	if badItems != 0 {
		t.Fatal("导入媒体的类型、标签或描述错误")
	}

	path := "/api/admin/character-cards/" + id
	status, body = doJSON(t, env.Server, http.MethodGet, path, nil, cookie)
	if status != http.StatusOK || personaObject(t, body)["expressionCount"] != float64(2) {
		t.Fatalf("读取角色详情失败: %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, http.MethodGet, path+"/expressions", nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取表情失败: %d %s", status, body)
	}
	var expressions []map[string]any
	if err := json.Unmarshal(body, &expressions); err != nil {
		t.Fatal(err)
	}
	if len(expressions) != 2 || expressions[0]["key"] != "a" || expressions[1]["key"] != "b" {
		t.Fatalf("表情顺序或内容错误: %s", body)
	}
	firstID := expressions[0]["id"].(string)
	firstMediaID := expressions[0]["mediaItemId"].(string)
	var storageKey string
	if err := pool.QueryRow(ctx, "SELECT storage_key FROM media_items WHERE id=$1", firstMediaID).Scan(&storageKey); err != nil {
		t.Fatal(err)
	}
	status, body = doJSON(t, env.Server, http.MethodPatch, "/api/admin/card-expressions/"+firstID, map[string]any{"key": "开心", "description": "修改说明-secret", "sort": 7}, cookie)
	if status != http.StatusOK {
		t.Fatalf("修改表情失败: %d %s", status, body)
	}
	modified := personaObject(t, body)
	if modified["key"] != "开心" || modified["description"] != "修改说明-secret" || modified["sort"] != float64(7) {
		t.Fatalf("修改未生效: %s", body)
	}
	status, body = doJSON(t, env.Server, http.MethodDelete, "/api/admin/card-expressions/"+firstID, nil, cookie)
	if status != http.StatusNoContent {
		t.Fatalf("删除表情失败: %d %s", status, body)
	}
	if _, err := os.Stat(filepath.Join(root, storageKey)); !os.IsNotExist(err) {
		t.Fatalf("表情文件未删除: %v", err)
	}
	if n := personaCount(t, pool, "media_items"); n != 1 {
		t.Fatalf("表情媒体条目未删除: %d", n)
	}
	status, body = personaUpload(t, env.Server, path+"/expressions", cookie, "new.png", personaPNG(t), map[string]string{"key": "手动", "description": "手动说明-secret"})
	if status != http.StatusCreated {
		t.Fatalf("上传表情失败: %d %s", status, body)
	}
	if n := personaCount(t, pool, "media_folders"); n != 1 {
		t.Fatalf("应复用表情文件夹: %d", n)
	}

	// 同名角色卡的表情文件夹使用后缀，卡片名称本身不应被改写。
	status, body = personaUpload(t, env.Server, "/api/admin/character-cards/import", cookie, "fixture.zip", personaKRP(t, "导入角色-secret", 1), nil)
	if status != http.StatusCreated {
		t.Fatalf("同名导入失败: %d %s", status, body)
	}
	second := personaObject(t, body)["card"].(map[string]any)
	var suffixCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM media_folders WHERE name='表情·导入角色-secret-2'").Scan(&suffixCount); err != nil {
		t.Fatal(err)
	}
	if suffixCount != 1 {
		t.Fatal("重名文件夹未追加后缀")
	}
	for _, cardID := range []string{id, second["id"].(string)} {
		status, body = doJSON(t, env.Server, http.MethodDelete, "/api/admin/character-cards/"+cardID, nil, cookie)
		if status != http.StatusNoContent {
			t.Fatalf("删除角色卡失败: %d %s", status, body)
		}
	}
	for _, table := range []string{"character_cards", "card_expressions", "media_folders", "media_items"} {
		if n := personaCount(t, pool, table); n != 0 {
			t.Fatalf("删除后 %s 残留 %d 行", table, n)
		}
	}
	if files := personaFiles(t, root); len(files) != 0 {
		t.Fatalf("删除后残留文件: %v", files)
	}
	var leaked int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit_logs WHERE action LIKE 'bot.card.%' OR action LIKE 'bot.expression.%'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked < 7 {
		t.Fatalf("管理操作缺少审计: %d", leaked)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM admin_audit_logs WHERE detail::text LIKE '%secret%'").Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("审计泄漏卡片/表情内容: %d", leaked)
	}
}

func TestPersonaImportRollback(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: root})
	cookie := loginAdmin(t, pool, env.Server)
	// 第二条表情落库时失败；此时第一张图片已经通过真实媒体服务写入磁盘。
	_, err := pool.Exec(ctx, `CREATE SEQUENCE persona_test_import_attempts;
 CREATE FUNCTION persona_test_reject_second() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM nextval('persona_test_import_attempts'); IF NEW.key = 'b' THEN RAISE EXCEPTION '测试导入中断'; END IF; RETURN NEW; END $$;
 CREATE TRIGGER persona_test_reject_second BEFORE INSERT ON card_expressions FOR EACH ROW EXECUTE FUNCTION persona_test_reject_second()`)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, "DROP TRIGGER IF EXISTS persona_test_reject_second ON card_expressions; DROP FUNCTION IF EXISTS persona_test_reject_second(); DROP SEQUENCE IF EXISTS persona_test_import_attempts"); err != nil {
			t.Error(err)
		}
	})
	status, body := personaUpload(t, env.Server, "/api/admin/character-cards/import", cookie, "fixture.krp", personaKRP(t, "回滚角色", 2), nil)
	if status >= 200 && status < 300 {
		t.Fatalf("强制落库失败不应成功: %d %s", status, body)
	}
	// 序列递增不随事务回滚；证明请求走到了第二张表情，不能因路由/解析失败而假通过。
	var attempts int
	var called bool
	if err := pool.QueryRow(ctx, "SELECT last_value, is_called FROM persona_test_import_attempts").Scan(&attempts, &called); err != nil {
		t.Fatal(err)
	}
	if !called || attempts != 2 {
		t.Fatalf("请求未走到第二张表情落库: attempts=%d called=%v status=%d body=%s", attempts, called, status, body)
	}
	for _, table := range []string{"character_cards", "card_expressions", "media_folders", "media_items"} {
		if n := personaCount(t, pool, table); n != 0 {
			t.Fatalf("失败后 %s 残留 %d 行", table, n)
		}
	}
	if files := personaFiles(t, root); len(files) != 0 {
		t.Fatalf("失败后残留文件: %v", files)
	}
}

func TestPersonaManualCardFields(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: root})
	cookie := loginAdmin(t, pool, env.Server)
	payload := map[string]any{"name": "手动角色-secret", "description": strings.Repeat("字", 5000), "heartPrompt": "心核-secret", "reasonPrompt": "智核-secret", "alternateGreetings": []string{"你好", "嗨"}, "postHistoryInstructions": "末尾-secret", "creatorNotes": "备注-secret", "tools": []string{"get_time"}, "lorebook": []map[string]any{{"keys": []string{"猫"}, "content": "猫-secret", "enabled": false, "constant": true, "case_sensitive": true, "order": 3, "comment": "注释"}}}
	status, body := doJSON(t, env.Server, http.MethodPost, "/api/admin/character-cards", payload, cookie)
	if status != http.StatusCreated {
		t.Fatalf("创建带新字段的角色卡失败: %d %s", status, body)
	}
	card := personaObject(t, body)
	id := card["id"].(string)
	if card["sourceFormat"] != "manual" || card["expressionCount"] != float64(0) {
		t.Fatalf("只读字段默认值错误: %s", body)
	}
	for key, want := range payload {
		if key == "lorebook" || key == "tools" || key == "alternateGreetings" {
			continue
		}
		if card[key] != want {
			t.Errorf("%s 未保存", key)
		}
	}
	payload["heartPrompt"] = "修改后心核-secret"
	status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/character-cards/"+id, payload, cookie)
	if status != http.StatusOK || personaObject(t, body)["heartPrompt"] != "修改后心核-secret" {
		t.Fatalf("更新新字段失败: %d %s", status, body)
	}
	status, body = doJSON(t, env.Server, http.MethodGet, "/api/admin/character-cards/"+id, nil, cookie)
	if status != http.StatusOK {
		t.Fatalf("读取卡片失败: %d %s", status, body)
	}
	card = personaObject(t, body)
	lore := card["lorebook"].([]any)
	if len(lore) != 1 || lore[0].(map[string]any)["enabled"] != false || lore[0].(map[string]any)["content"] != "猫-secret" {
		t.Fatalf("禁用世界书条目应保留: %s", body)
	}
	if len(card["alternateGreetings"].([]any)) != 2 || len(card["tools"].([]any)) != 1 {
		t.Fatalf("数组字段未保存: %s", body)
	}
	for _, field := range []string{"sourceFormat", "expressionCount"} {
		invalid := map[string]any{}
		for key, value := range payload {
			invalid[key] = value
		}
		if field == "sourceFormat" {
			invalid[field] = "krp"
		} else {
			invalid[field] = 99
		}
		status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/character-cards/"+id, invalid, cookie)
		if status != http.StatusBadRequest {
			t.Fatalf("只读字段 %s 应拒绝: %d %s", field, status, body)
		}
	}
	for _, invalid := range []map[string]any{{"heartPrompt": strings.Repeat("字", 20001)}, {"tools": []string{"run_python"}}, {"alternateGreetings": make([]string, 21)}} {
		p := map[string]any{}
		for k, v := range payload {
			p[k] = v
		}
		for k, v := range invalid {
			p[k] = v
		}
		status, body = doJSON(t, env.Server, http.MethodPut, "/api/admin/character-cards/"+id, p, cookie)
		if status != http.StatusBadRequest {
			t.Fatalf("手动卡片应复用 persona.Validate: %d %s", status, body)
		}
	}
	status, body = personaUpload(t, env.Server, "/api/admin/character-cards/"+id+"/expressions", cookie, "first.png", personaPNG(t), map[string]string{"key": "首次", "description": "自动文件夹"})
	if status != http.StatusCreated || personaCount(t, pool, "media_folders") != 1 {
		t.Fatalf("首次上传应自动建文件夹: %d %s", status, body)
	}
	for _, path := range []string{"/api/admin/character-cards/" + id, "/api/admin/character-cards/" + id + "/expressions"} {
		status, body = doJSON(t, env.Server, http.MethodGet, path, nil, "")
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("未登录访问应被拒绝: %d %s", status, body)
		}
	}
}

// personaZeroReader 用流式填充测试上传上限，避免构造一个 50 MB 的常驻字节切片。
type personaZeroReader struct{}

func (personaZeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestPersonaUploadLimitAndAuth(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	sharedPool = pool
	root := t.TempDir()
	env := newEnvWith(t, pool, testutil.Config{MediaRoot: root})
	cookie := loginAdmin(t, pool, env.Server)
	status, body := personaUpload(t, env.Server, "/api/admin/character-cards/import", "", "card.json", []byte(`{"name":"未授权"}`), nil)
	if status != http.StatusUnauthorized && status != http.StatusForbidden {
		t.Fatalf("未登录导入应被拒绝: %d %s", status, body)
	}
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodPost, "/api/admin/character-cards/00000000-0000-4000-8000-000000000001/expressions"},
		{http.MethodPatch, "/api/admin/card-expressions/00000000-0000-4000-8000-000000000001"},
		{http.MethodDelete, "/api/admin/card-expressions/00000000-0000-4000-8000-000000000001"},
	} {
		status, body = doJSON(t, env.Server, endpoint.method, endpoint.path, nil, "")
		if status != http.StatusUnauthorized && status != http.StatusForbidden {
			t.Fatalf("未登录表情管理应被拒绝: %s %d %s", endpoint.path, status, body)
		}
	}
	const boundary = "persona-upload-limit"
	prefix := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"file\"; filename=\"large.krp\"\r\nContent-Type: application/octet-stream\r\n\r\n"
	suffix := "\r\n--" + boundary + "--\r\n"
	reader := io.MultiReader(strings.NewReader(prefix), io.LimitReader(personaZeroReader{}, (50<<20)+1), strings.NewReader(suffix))
	req, err := http.NewRequest(http.MethodPost, env.Server.URL+"/api/admin/character-cards/import", reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.Header.Set("Origin", testutil.DefaultOrigin)
	req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: cookie})
	resp, err := env.Server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("超过 50 MB 上传应返回 400: %d %s", resp.StatusCode, body)
	}
	var limitError struct{ Error struct{ Message string } }
	if err := json.Unmarshal(body, &limitError); err != nil {
		t.Fatal(err)
	}
	if !strings.ContainsAny(limitError.Error.Message, "大超") && !strings.Contains(limitError.Error.Message, "50") {
		t.Fatalf("应因上传大小拒绝，而非依赖后续解析失败: %s", body)
	}
	for _, table := range []string{"character_cards", "card_expressions", "media_folders", "media_items"} {
		if n := personaCount(t, pool, table); n != 0 {
			t.Fatalf("无效上传后 %s 残留 %d 行", table, n)
		}
	}
	if files := personaFiles(t, root); len(files) != 0 {
		t.Fatalf("无效上传后残留文件: %v", files)
	}
}
