package wanfeng

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"
)

// recordHeaders 在 fakeUpstream 前挂一层，记录每个路径收到的 X-Get-Via-Post。
func recordHeaders(t *testing.T, f *fakeUpstream) func(path string) string {
	t.Helper()
	base := f.srv.Config.Handler
	var mu sync.Mutex
	seen := make(map[string]string)
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Get(DefaultProtocol().GetViaPostHeader)
		mu.Unlock()
		base.ServeHTTP(w, r)
	})
	return func(path string) string {
		mu.Lock()
		defer mu.Unlock()
		return seen[path]
	}
}

func viaPostTestClient(t *testing.T) (*APIClient, *fakeUpstream, context.Context, context.CancelFunc) {
	t.Helper()
	f := newFakeUpstream(t)
	api := NewAPIClient(APIClientConfig{BaseURL: f.URL(), Protocol: DefaultProtocol()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return api, f, ctx, cancel
}

// TestPostOnlyPathOmitsGetViaPost 校验 POST-only 接口不带 X-Get-Via-Post。
//
// 对方的 X-Get-Via-Post: 1 表示「这个 POST 其实是 GET」：带上它服务端按 GET 路由匹配，
// 于是 POST-only 的 account/identity-cookie 会 404「资源不存在: GET /api/...」，
// 身份 Cookie 永远换不回来，随后 home-data 等接口全部 40300。
func TestPostOnlyPathOmitsGetViaPost(t *testing.T) {
	api, f, ctx, _ := viaPostTestClient(t)
	getViaPost := recordHeaders(t, f)

	if err := api.BindIdentityCookie(ctx, "v1.token"); err != nil {
		t.Fatalf("绑定身份 Cookie 失败: %v", err)
	}
	if _, err := api.HomeData(ctx, "WFU1", 20); err != nil {
		t.Fatalf("HomeData 失败: %v", err)
	}

	proto := DefaultProtocol()
	if got := getViaPost(proto.IdentityCookiePath); got != "" {
		t.Fatalf("identity-cookie 不应带 %s，实际 %q", proto.GetViaPostHeader, got)
	}
	if got := getViaPost(proto.HomeDataPath); got != proto.GetViaPostValue {
		t.Fatalf("home-data 应带 %s=%s，实际 %q", proto.GetViaPostHeader, proto.GetViaPostValue, got)
	}
}

// TestIsPostOnlyPath 校验路径判定与覆盖能力（对方改版时可热修）。
func TestIsPostOnlyPath(t *testing.T) {
	proto := DefaultProtocol()
	if !proto.IsPostOnlyPath(proto.IdentityCookiePath) {
		t.Fatal("identity-cookie 应判定为 POST-only")
	}
	if proto.IsPostOnlyPath(proto.HomeDataPath) {
		t.Fatal("home-data 是 GET 风格接口，不应判定为 POST-only")
	}
	if proto.IsPostOnlyPath("") {
		t.Fatal("空路径不应判定为 POST-only")
	}
	// refresh-expiry 在站点 JS 里是裸 POST；带上 X-Get-Via-Post 会被当 GET 路由 → 404（实测踩过）
	if !proto.IsPostOnlyPath(proto.RefreshExpiryPath) {
		t.Fatal("refresh-expiry 应判定为 POST-only")
	}

	overridden, err := proto.ApplyOverrides(`{"postOnlyPaths":["/account/whoami"]}`)
	if err != nil {
		t.Fatalf("应用协议覆盖失败: %v", err)
	}
	if !overridden.IsPostOnlyPath("/api/nimingChat/account/whoami") {
		t.Fatal("覆盖后的 postOnlyPaths 应生效")
	}
	if overridden.IsPostOnlyPath(proto.IdentityCookiePath) {
		t.Fatal("覆盖后不应再包含 identity-cookie")
	}
}

// TestAPIErrorCarriesHTTPStatusAndIdentityNotReady 校验 40300（身份未就绪）能被识别。
func TestAPIErrorCarriesHTTPStatusAndIdentityNotReady(t *testing.T) {
	api, f, ctx, _ := viaPostTestClient(t)
	base := f.srv.Config.Handler
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == DefaultProtocol().HomeDataPath {
			w.WriteHeader(http.StatusForbidden)
			writeFakeJSON(w, map[string]any{"success": false, "code": 40300, "message": "请求错误，请刷新页面", "data": nil})
			return
		}
		base.ServeHTTP(w, r)
	})

	_, err := api.HomeData(ctx, "WFU1", 20)
	if err == nil {
		t.Fatal("40300 应返回错误")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型应为 *APIError: %v", err)
	}
	if apiErr.Code != "40300" || apiErr.Status != http.StatusForbidden {
		t.Fatalf("错误码/状态码不对: code=%q status=%d", apiErr.Code, apiErr.Status)
	}
	if !IdentityNotReady(err) {
		t.Fatal("IdentityNotReady 应识别 40300")
	}

	// 业务错误（非身份类）不应被当成身份未就绪
	if IdentityNotReady(&APIError{Code: "IDENTITY_EMPTY", Status: http.StatusOK}) {
		t.Fatal("IDENTITY_EMPTY 不应判定为身份未就绪")
	}
	if IdentityNotReady(errors.New("网络错误")) {
		t.Fatal("普通错误不应判定为身份未就绪")
	}
}

// TestCloneSharesSession 校验克隆客户端复用同一份加密会话（避免每身份多一次握手）。
func TestCloneSharesSession(t *testing.T) {
	api, _, ctx, _ := viaPostTestClient(t)
	if _, err := api.Session(ctx); err != nil {
		t.Fatalf("握手失败: %v", err)
	}
	clone := api.Clone()
	if clone == nil || clone.http.Jar == api.http.Jar {
		t.Fatal("克隆客户端应使用独立 Cookie Jar")
	}
	sess, err := clone.Session(ctx)
	if err != nil {
		t.Fatalf("克隆客户端取会话失败: %v", err)
	}
	base, err := api.Session(ctx)
	if err != nil {
		t.Fatalf("取会话失败: %v", err)
	}
	if sess.ID != base.ID {
		t.Fatalf("克隆客户端应复用同一会话: clone=%s base=%s", sess.ID, base.ID)
	}
}
