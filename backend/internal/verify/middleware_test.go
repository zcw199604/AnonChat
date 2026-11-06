package verify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/store"
)

func TestRequireVerified(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	newReq := func(ident *store.Identity) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/protected", nil)
		if ident != nil {
			r = r.WithContext(identity.WithIdentity(r.Context(), ident))
		}
		return r
	}

	codeOf := func(t *testing.T, rec *httptest.ResponseRecorder) string {
		t.Helper()
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return body.Error.Code
	}

	t.Run("required 且无身份 -> 403", func(t *testing.T) {
		rec := httptest.NewRecorder()
		RequireVerified("required")(ok).ServeHTTP(rec, newReq(nil))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("状态码 = %d, 期望 403", rec.Code)
		}
		if codeOf(t, rec) != "verification_required" {
			t.Errorf("错误码 = %q, 期望 verification_required", codeOf(t, rec))
		}
	})

	t.Run("required 且未绑定账号 -> 403", func(t *testing.T) {
		rec := httptest.NewRecorder()
		RequireVerified("required")(ok).ServeHTTP(rec, newReq(&store.Identity{}))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("状态码 = %d, 期望 403", rec.Code)
		}
	})

	t.Run("required 且已绑定账号 -> 放行", func(t *testing.T) {
		ident := &store.Identity{AccountID: pgtype.UUID{Bytes: [16]byte{1}, Valid: true}}
		rec := httptest.NewRecorder()
		RequireVerified("required")(ok).ServeHTTP(rec, newReq(ident))
		if rec.Code != http.StatusOK {
			t.Fatalf("状态码 = %d, 期望 200", rec.Code)
		}
	})

	t.Run("optional/off 未验证也放行", func(t *testing.T) {
		for _, mode := range []string{"optional", "off"} {
			rec := httptest.NewRecorder()
			RequireVerified(mode)(ok).ServeHTTP(rec, newReq(nil))
			if rec.Code != http.StatusOK {
				t.Errorf("mode=%s 状态码 = %d, 期望 200", mode, rec.Code)
			}
		}
	})
}
