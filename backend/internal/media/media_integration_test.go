//go:build integration

package media_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
	"github.com/anonchat/anonchat/backend/internal/testutil"
)

var ctx = context.Background()

// pngBytes 是一个合法的 1x1 PNG（嗅探结果为 image/png）。
var pngBytes = mustDecodeBase64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==")

// mustDecodeBase64 解码测试用二进制素材。
func mustDecodeBase64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// resp 是一次 JSON 调用的结果。
type resp struct {
	status int
	body   []byte
}

// JSON 把响应体解析到 v。
func (r resp) JSON(t *testing.T, v any) {
	t.Helper()
	if err := json.Unmarshal(r.body, v); err != nil {
		t.Fatalf("解析响应失败: %v, body=%s", err, r.body)
	}
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

// doJSON 发送一次 JSON 请求（可带管理 Cookie）。
func doJSON(t *testing.T, srv *httptest.Server, method, path string, body any, adminCookie string) resp {
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
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer out.Body.Close()
	data, _ := io.ReadAll(out.Body)
	return resp{status: out.StatusCode, body: data}
}

// loginAdmin 创建管理员并登录，返回 __Host-admin Cookie。
func loginAdmin(t *testing.T, pool *pgxpool.Pool, srv *httptest.Server) string {
	t.Helper()
	if _, err := admin.CreateUser(ctx, pool, "admin", "supersecret123"); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "supersecret123"})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/login", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", testutil.DefaultOrigin)
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("登录请求失败: %v", err)
	}
	defer out.Body.Close()
	if out.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(out.Body)
		t.Fatalf("登录失败: %d %s", out.StatusCode, data)
	}
	// testutil.Do 只提取身份 Cookie，这里直接读管理会话 Cookie。
	for _, ck := range out.Cookies() {
		if ck.Name == admin.SessionCookie {
			return ck.Value
		}
	}
	t.Fatal("登录未下发管理 Cookie")
	return ""
}

// uploadedFile 是一次 multipart 上传里的文件。
type uploadedFile struct {
	name    string
	content []byte
}

// uploadItems 以 multipart 上传文件（tags/caption 应用到这批所有文件）。
func uploadItems(t *testing.T, srv *httptest.Server, folderID, adminCookie string, files []uploadedFile, tags, caption string) resp {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, f := range files {
		w, err := mw.CreateFormFile("files", f.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(f.content); err != nil {
			t.Fatal(err)
		}
	}
	if tags != "" {
		_ = mw.WriteField("tags", tags)
	}
	if caption != "" {
		_ = mw.WriteField("caption", caption)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/admin/media-folders/"+folderID+"/items", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if adminCookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: adminCookie})
	}
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("上传失败: %v", err)
	}
	defer out.Body.Close()
	data, _ := io.ReadAll(out.Body)
	return resp{status: out.StatusCode, body: data}
}

// itemDTO 是媒体条目的响应结构。
type itemDTO struct {
	ID           string   `json:"id"`
	FolderID     string   `json:"folderId"`
	Kind         string   `json:"kind"`
	Mime         string   `json:"mime"`
	SizeBytes    int64    `json:"sizeBytes"`
	OriginalName string   `json:"originalName"`
	Tags         []string `json:"tags"`
	Caption      string   `json:"caption"`
	FileURL      string   `json:"fileUrl"`
}

// folderDTO 是文件夹的响应结构。
type folderDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ItemCount int64  `json:"itemCount"`
}

// mkIdentity 插入一个身份，返回身份与明文令牌。
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

// doFile 以身份 Cookie 请求媒体文件，rangeHeader 非空时带上 Range 头。
func doFile(t *testing.T, srv *httptest.Server, path, token, rangeHeader string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: token})
	}
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求媒体失败: %v", err)
	}
	return out
}

// doAdminFile 以管理 Cookie 请求后台媒体文件接口。
func doAdminFile(t *testing.T, srv *httptest.Server, path, adminCookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if adminCookie != "" {
		req.AddCookie(&http.Cookie{Name: admin.SessionCookie, Value: adminCookie})
	}
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("请求媒体失败: %v", err)
	}
	return out
}

// countFiles 统计媒体根目录下的文件数（不含目录）。
func countFiles(t *testing.T, root string) int {
	t.Helper()
	var n int
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历媒体目录失败: %v", err)
	}
	return n
}

// ---- 管理后台：文件夹与上传 ----

// TestAdminMediaUploadLifecycle 覆盖：未登录 401、文件夹 CRUD、合法 png 上传、
// 非法 txt 被拒（media_rejected）、改标签、删除条目后磁盘文件消失、删除文件夹级联。
func TestAdminMediaUploadLifecycle(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	root := t.TempDir()
	env := testutil.MustEnv(t, pool, testutil.Config{MediaRoot: root})

	// 未登录访问管理接口 → 401。
	if r := doJSON(t, env.Server, http.MethodGet, "/api/admin/media-folders", nil, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("未登录列表应 401, 实际 %d", r.status)
	}
	if r := doJSON(t, env.Server, http.MethodPost, "/api/admin/media-folders", map[string]string{"name": "x"}, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("未登录新建应 401, 实际 %d", r.status)
	}

	cookie := loginAdmin(t, pool, env.Server)

	// 新建文件夹。
	created := doJSON(t, env.Server, http.MethodPost, "/api/admin/media-folders", map[string]string{"name": "自拍"}, cookie)
	if created.status != http.StatusCreated {
		t.Fatalf("新建文件夹失败: %d %s", created.status, created.body)
	}
	var folder folderDTO
	created.JSON(t, &folder)
	if folder.ID == "" || folder.Name != "自拍" {
		t.Fatalf("文件夹响应不符: %+v", folder)
	}
	// 重名文件夹返回 invalid_request。
	if dup := doJSON(t, env.Server, http.MethodPost, "/api/admin/media-folders", map[string]string{"name": "自拍"}, cookie); dup.status != http.StatusBadRequest {
		t.Errorf("重名文件夹应 400, 实际 %d %s", dup.status, dup.body)
	}

	// 上传合法 png（带标签与描述）。
	up := uploadItems(t, env.Server, folder.ID, cookie,
		[]uploadedFile{{name: "a.png", content: pngBytes}}, "自拍, 微笑,自拍", "笑脸")
	if up.status != http.StatusCreated {
		t.Fatalf("上传 png 失败: %d %s", up.status, up.body)
	}
	var items []itemDTO
	up.JSON(t, &items)
	if len(items) != 1 {
		t.Fatalf("上传返回 %d 个条目, 期望 1", len(items))
	}
	item := items[0]
	if item.Kind != "image" || item.Mime != "image/png" {
		t.Errorf("嗅探结果不符: kind=%q mime=%q", item.Kind, item.Mime)
	}
	if item.SizeBytes != int64(len(pngBytes)) {
		t.Errorf("sizeBytes = %d, 期望 %d", item.SizeBytes, len(pngBytes))
	}
	// 标签去重、去空白。
	if len(item.Tags) != 2 || item.Tags[0] != "自拍" || item.Tags[1] != "微笑" {
		t.Errorf("标签规范化不符: %v", item.Tags)
	}
	if item.Caption != "笑脸" {
		t.Errorf("caption = %q", item.Caption)
	}
	// 文件落在 <root>/<folder_id>/<item_id>.png。
	itemPath := filepath.Join(root, folder.ID, item.ID+".png")
	if _, err := os.Stat(itemPath); err != nil {
		t.Fatalf("磁盘文件不存在: %v", err)
	}

	// 上传伪装成 png 的文本：只认内容嗅探，返回 media_rejected，且不留孤儿/临时文件。
	rejected := uploadItems(t, env.Server, folder.ID, cookie,
		[]uploadedFile{{name: "evil.png", content: []byte("这不是图片，只是伪装成 png 的文本内容")}}, "", "")
	if rejected.status != http.StatusUnsupportedMediaType {
		t.Fatalf("上传 txt 应 415(media_rejected), 实际 %d %s", rejected.status, rejected.body)
	}
	if code := errorCode(t, rejected.body); code != "media_rejected" {
		t.Errorf("错误码 = %q, 期望 media_rejected", code)
	}
	if got := countFiles(t, root); got != 1 {
		t.Errorf("被拒上传后磁盘文件数 = %d, 期望 1（不留临时文件）", got)
	}

	// 列表带条目数。
	list := doJSON(t, env.Server, http.MethodGet, "/api/admin/media-folders", nil, cookie)
	var folders []folderDTO
	list.JSON(t, &folders)
	if len(folders) != 1 || folders[0].ItemCount != 1 {
		t.Fatalf("文件夹列表不符: %+v", folders)
	}

	// 修改标签与描述。
	patched := doJSON(t, env.Server, http.MethodPatch, "/api/admin/media-items/"+item.ID,
		map[string]any{"tags": []string{"风景", "风景", " 夜景 "}, "caption": "新描述"}, cookie)
	if patched.status != http.StatusOK {
		t.Fatalf("修改标签失败: %d %s", patched.status, patched.body)
	}
	var patchedItem itemDTO
	patched.JSON(t, &patchedItem)
	if len(patchedItem.Tags) != 2 || patchedItem.Tags[0] != "风景" || patchedItem.Tags[1] != "夜景" {
		t.Errorf("标签修改不符: %v", patchedItem.Tags)
	}
	if patchedItem.Caption != "新描述" {
		t.Errorf("描述修改不符: %q", patchedItem.Caption)
	}

	// 后台预览接口可读出原文件。
	preview := doAdminFile(t, env.Server, "/api/admin/media-items/"+item.ID+"/file", cookie)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("后台预览状态 = %d", preview.StatusCode)
	}
	previewBody, _ := io.ReadAll(preview.Body)
	_ = preview.Body.Close()
	if !bytes.Equal(previewBody, pngBytes) {
		t.Errorf("后台预览内容不符: %d 字节", len(previewBody))
	}

	// 删除条目：磁盘文件消失。
	if del := doJSON(t, env.Server, http.MethodDelete, "/api/admin/media-items/"+item.ID, nil, cookie); del.status != http.StatusNoContent {
		t.Fatalf("删除条目失败: %d %s", del.status, del.body)
	}
	if _, err := os.Stat(itemPath); !os.IsNotExist(err) {
		t.Errorf("删除条目后磁盘文件应消失, err=%v", err)
	}

	// 重命名文件夹。
	renamed := doJSON(t, env.Server, http.MethodPatch, "/api/admin/media-folders/"+folder.ID, map[string]string{"name": "自拍合集"}, cookie)
	if renamed.status != http.StatusOK {
		t.Fatalf("重命名失败: %d %s", renamed.status, renamed.body)
	}
	var renamedFolder folderDTO
	renamed.JSON(t, &renamedFolder)
	if renamedFolder.Name != "自拍合集" {
		t.Errorf("文件夹名 = %q", renamedFolder.Name)
	}

	// 再上传两个文件后删除文件夹：级联删除条目与磁盘文件。
	up2 := uploadItems(t, env.Server, folder.ID, cookie, []uploadedFile{
		{name: "b.png", content: pngBytes},
		{name: "c.png", content: pngBytes},
	}, "批量", "")
	if up2.status != http.StatusCreated {
		t.Fatalf("批量上传失败: %d %s", up2.status, up2.body)
	}
	if got := countFiles(t, root); got != 2 {
		t.Fatalf("批量上传后磁盘文件数 = %d, 期望 2", got)
	}
	if del := doJSON(t, env.Server, http.MethodDelete, "/api/admin/media-folders/"+folder.ID, nil, cookie); del.status != http.StatusNoContent {
		t.Fatalf("删除文件夹失败: %d %s", del.status, del.body)
	}
	if got := countFiles(t, root); got != 0 {
		t.Errorf("删除文件夹后磁盘文件数 = %d, 期望 0", got)
	}
	var left int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM media_items").Scan(&left); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Errorf("删除文件夹后媒体行数 = %d, 期望 0", left)
	}
	if r := doJSON(t, env.Server, http.MethodGet, "/api/admin/media-folders/"+folder.ID+"/items", nil, cookie); r.status != http.StatusNotFound {
		t.Errorf("删除后的文件夹应 404, 实际 %d", r.status)
	}
}

// ---- 用户侧访问控制 ----

// TestMediaAccessControl 覆盖：会话成员 200 且支持 Range（206），非成员 404，不存在的媒体 404。
// 媒体通过直接插入 media_items 行与带 media_id 的消息构造，不依赖上传流程。
func TestMediaAccessControl(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	root := t.TempDir()
	env := testutil.MustEnv(t, pool, testutil.Config{MediaRoot: root})

	member, memberToken := mkIdentity(t, pool, "member")
	_, otherToken := mkIdentity(t, pool, "other")

	// 文件夹 + 媒体行（显式指定条目 ID，便于拼出 storage_key）+ 磁盘文件。
	var folderID, itemID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO media_folders (name) VALUES ('测试') RETURNING id").Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	key := folderID.String() + "/" + itemID.String() + ".png"
	if _, err := pool.Exec(ctx,
		`INSERT INTO media_items (id, folder_id, kind, mime, size_bytes, storage_key, tags)
		 VALUES ($1, $2, 'image', 'image/png', $3, $4, '{自拍}')`,
		itemID, folderID, len(pngBytes), key); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, folderID.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, key), pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// 会话：member 是成员，另一个身份不是；消息带 media_id。
	var convID pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO conversations (kind) VALUES ('match') RETURNING id").Scan(&convID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		"INSERT INTO conversation_members (conversation_id, member_id, identity_id) VALUES ($1, 1, $2)",
		convID, member.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO messages (conversation_id, sender_identity_id, sender_member_id, client_msg_id, kind, body, media_id)
		 VALUES ($1, $2, 1, gen_random_uuid(), 'image', '', $3)`,
		convID, member.ID, itemID); err != nil {
		t.Fatal(err)
	}

	path := "/api/media/" + itemID.String()

	// 会话成员：200，内容与类型正确。
	full := doFile(t, env.Server, path, memberToken, "")
	if full.StatusCode != http.StatusOK {
		t.Fatalf("成员访问媒体状态 = %d", full.StatusCode)
	}
	if ct := full.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, 期望 image/png", ct)
	}
	if cc := full.Header.Get("Cache-Control"); cc != "private, max-age=86400" {
		t.Errorf("Cache-Control = %q", cc)
	}
	body, _ := io.ReadAll(full.Body)
	_ = full.Body.Close()
	if !bytes.Equal(body, pngBytes) {
		t.Errorf("返回内容与文件不一致: %d vs %d 字节", len(body), len(pngBytes))
	}

	// 带 Range：206 且只返回 4 字节。
	ranged := doFile(t, env.Server, path, memberToken, "bytes=0-3")
	if ranged.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range 请求状态 = %d, 期望 206", ranged.StatusCode)
	}
	part, _ := io.ReadAll(ranged.Body)
	_ = ranged.Body.Close()
	if len(part) != 4 || !bytes.Equal(part, pngBytes[:4]) {
		t.Errorf("Range 内容不符: %d 字节", len(part))
	}

	// 非成员：404（与不存在不区分）。
	notMember := doFile(t, env.Server, path, otherToken, "")
	if notMember.StatusCode != http.StatusNotFound {
		t.Errorf("非成员访问应 404, 实际 %d", notMember.StatusCode)
	}
	_ = notMember.Body.Close()

	// 不存在的媒体：404。
	var unknown pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT gen_random_uuid()").Scan(&unknown); err != nil {
		t.Fatal(err)
	}
	missing := doFile(t, env.Server, "/api/media/"+unknown.String(), memberToken, "")
	if missing.StatusCode != http.StatusNotFound {
		t.Errorf("不存在的媒体应 404, 实际 %d", missing.StatusCode)
	}
	_ = missing.Body.Close()

	// 未登录：401（chatProtected）。
	anonymous := doFile(t, env.Server, path, "", "")
	if anonymous.StatusCode != http.StatusUnauthorized {
		t.Errorf("未登录访问媒体应 401, 实际 %d", anonymous.StatusCode)
	}
	_ = anonymous.Body.Close()
}
