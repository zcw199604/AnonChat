//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/anonchat/anonchat/backend/internal/testutil"
)

// testKey 生成 32 字节测试密钥。
func testKey(seed byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = seed + byte(i)
	}
	return k
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

// TestPublicUGCRequiresVerification 验证发布公开内容的接口遵守实名开关（审查意见 R3）：
// VERIFICATION_MODE=required 时，未实名身份调用 POST /api/broadcasts 与 POST /api/stories
// 都必须返回 verification_required（与 V1 漂流瓶等 UGC 接口一致，挂 chatProtected）。
func TestPublicUGCRequiresVerification(t *testing.T) {
	pool := testutil.MustPool(t)
	testutil.Truncate(t, pool)
	env := testutil.MustEnv(t, pool, testutil.Config{
		VerificationMode: "required",
		EncKey:           testKey(1),
		HmacKey:          testKey(101),
		SMS:              testutil.NewCapturingSMS(),
	})

	created := testutil.Do(t, env.Server, http.MethodPost, "/api/identity", nil, "", "")
	if created.Status != http.StatusOK || created.Token == "" {
		t.Fatalf("创建身份失败: %d, body=%s", created.Status, created.Body)
	}
	token := created.Token

	cases := []struct {
		label string
		path  string
		body  any
	}{
		{"发布广播", "/api/broadcasts", map[string]string{"content": "我的广播"}},
		{"发起故事", "/api/stories", map[string]any{
			"title": "实名校验", "opening": "这是一段用于实名校验的开场白，需要足够长。",
			"tags": []string{"读书"}, "roleA": "店主", "roleB": "旅人", "hostRole": "a",
		}},
	}
	for _, c := range cases {
		r := testutil.Do(t, env.Server, http.MethodPost, c.path, c.body, token, "")
		if r.Status != http.StatusForbidden {
			t.Errorf("%s（未实名）状态 = %d, 期望 403, body=%s", c.label, r.Status, r.Body)
		}
		if code := errorCode(t, r.Body); code != "verification_required" {
			t.Errorf("%s（未实名）错误码 = %q, 期望 verification_required", c.label, code)
		}
	}

	// 只读的公开接口仍然只需身份（protected），不应被实名拦截。
	feed := testutil.Do(t, env.Server, http.MethodGet, "/api/feed", nil, token, "")
	if feed.Status != http.StatusOK {
		t.Errorf("匿名动态（未实名）状态 = %d, 期望 200, body=%s", feed.Status, feed.Body)
	}
}
