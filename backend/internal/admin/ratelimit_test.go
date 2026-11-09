package admin

import (
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRateLimitRulesMetadataAndFullUpdateValidation(t *testing.T) {
	s := &Service{limitSettings: moderation.NewLimitSettings(nil, 3, nil)}
	out := httptest.NewRecorder()
	s.RateLimitRulesHandler(out, httptest.NewRequest(http.MethodGet, "/", nil))
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), `"multiplier":3`) || !strings.Contains(out.Body.String(), `"banSubject":""`) {
		t.Fatalf("规则元数据: %d %s", out.Code, out.Body)
	}
	out = httptest.NewRecorder()
	s.UpdateRateLimitRulesHandler(out, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"rules":[]}`)))
	if out.Code != http.StatusBadRequest {
		t.Fatalf("全量校验: %d %s", out.Code, out.Body)
	}
}
