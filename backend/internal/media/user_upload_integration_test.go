//go:build integration

package media_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/testutil"
	"github.com/jackc/pgx/v5/pgtype"
)

func uploadUserFile(t *testing.T, srv *httptest.Server, token string, content []byte) resp {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "test.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/media/upload", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", testutil.DefaultOrigin)
	if token != "" {
		req.AddCookie(&http.Cookie{Name: identity.CookieName, Value: token})
	}
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Body.Close()
	data, err := io.ReadAll(out.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp{status: out.StatusCode, body: data}
}

func TestUserUploadAuthenticationPreviewAndRateLimit(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{})
	_, owner := mkIdentity(t, pool, "upload-owner")
	_, other := mkIdentity(t, pool, "upload-other")
	if got := uploadUserFile(t, env.Server, "", pngBytes); got.status != 401 {
		t.Fatalf("未登录上传=%d want401: %s", got.status, got.body)
	}
	got := uploadUserFile(t, env.Server, owner, []byte("not an image"))
	if got.status != http.StatusUnsupportedMediaType || errorCode(t, got.body) != "media_rejected" {
		t.Fatalf("非法类型=%d %s", got.status, got.body)
	}
	got = uploadUserFile(t, env.Server, owner, pngBytes)
	if got.status != 200 {
		t.Fatalf("上传=%d %s", got.status, got.body)
	}
	var item struct {
		MediaID   string `json:"mediaId"`
		Kind      string `json:"kind"`
		Mime      string `json:"mime"`
		SizeBytes int64  `json:"sizeBytes"`
	}
	got.JSON(t, &item)
	if item.MediaID == "" || item.Kind != "image" || item.Mime != "image/png" || item.SizeBytes != int64(len(pngBytes)) {
		t.Fatalf("上传响应=%+v", item)
	}
	for _, tc := range []struct {
		token  string
		status int
	}{{owner, 200}, {other, 404}} {
		out := doFile(t, env.Server, "/api/media/"+item.MediaID, tc.token, "")
		out.Body.Close()
		if out.StatusCode != tc.status {
			t.Fatalf("预览=%d want%d", out.StatusCode, tc.status)
		}
	}
	// 失败的类型检查同样消耗上传配额，前面已请求两次。
	for i := 2; i < 10; i++ {
		if got = uploadUserFile(t, env.Server, owner, pngBytes); got.status != 200 {
			t.Fatalf("第%d次上传=%d %s", i+1, got.status, got.body)
		}
	}
	if got = uploadUserFile(t, env.Server, owner, pngBytes); got.status != 429 || errorCode(t, got.body) != "rate_limited" {
		t.Fatalf("超频=%d %s", got.status, got.body)
	}
	if got = uploadUserFile(t, env.Server, other, pngBytes); got.status != 200 {
		t.Fatalf("另一身份独立额度=%d %s", got.status, got.body)
	}
}

func TestUserUploadVerificationLimitSettingsAndSize(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	settings := moderation.NewLimitSettings(pool, 1, nil)
	env := testutil.MustEnv(t, pool, testutil.Config{LimitSettings: settings, UserMediaMaxUploadMB: 1})
	_, owner := mkIdentity(t, pool, "settings-owner")
	rule := settings.Current().Rules[moderation.RuleMediaUpload]
	rule.Limit = 1
	if err := settings.Update(ctx, []moderation.Rule{rule}, pgtype.UUID{}); err != nil {
		t.Fatal(err)
	}
	got := uploadUserFile(t, env.Server, owner, append(append([]byte{}, pngBytes...), make([]byte, 1<<20)...))
	if errorCode(t, got.body) != "media_rejected" {
		t.Fatalf("HTTP用户大小上限=%d %s", got.status, got.body)
	}
	got = uploadUserFile(t, env.Server, owner, pngBytes)
	if got.status != 429 {
		t.Fatalf("热更新的上传额度未生效=%d %s", got.status, got.body)
	}
	verifiedEnv := testutil.MustEnv(t, pool, testutil.Config{VerificationMode: "required", EncKey: make([]byte, 32), HmacKey: make([]byte, 32)})
	_, unverified := mkIdentity(t, pool, "unverified-upload")
	got = uploadUserFile(t, verifiedEnv.Server, unverified, pngBytes)
	if got.status != 403 || errorCode(t, got.body) != "verification_required" {
		t.Fatalf("发送消息同等验证要求=%d %s", got.status, got.body)
	}
}
