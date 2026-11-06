package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOriginCheck(t *testing.T) {
	allowed := []string{"http://localhost:5173"}
	h := OriginCheck(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	cases := []struct {
		name       string
		method     string
		origin     string
		wantStatus int
	}{
		{"GET 不校验", http.MethodGet, "", http.StatusNoContent},
		{"POST 允许的 Origin", http.MethodPost, "http://localhost:5173", http.StatusNoContent},
		{"POST 未知 Origin", http.MethodPost, "http://evil.example", http.StatusForbidden},
		{"POST 缺少 Origin", http.MethodPost, "", http.StatusForbidden},
		{"DELETE 未知 Origin", http.MethodDelete, "http://evil.example", http.StatusForbidden},
		{"PUT 允许的 Origin", http.MethodPut, "http://localhost:5173", http.StatusNoContent},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/api/x", nil)
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.wantStatus {
				t.Errorf("状态码 = %d, 期望 %d", rec.Code, c.wantStatus)
			}
		})
	}
}
