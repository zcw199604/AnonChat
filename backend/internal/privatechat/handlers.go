package privatechat

import (
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

const maxBodyBytes = 64 << 10

type createRequest struct {
	Password      string `json:"password"`
	TTLHours      int    `json:"ttlHours"`
	BurnAfterRead bool   `json:"burnAfterRead"`
}

// CreateHandler 处理 POST /api/private-links。
func (s *Service) CreateHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	res, err := s.Create(r.Context(), ident.ID, req.Password, req.TTLHours, req.BurnAfterRead)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"token":          res.Token,
		"conversationId": res.ConversationID.String(),
		"expiresAt":      res.ExpiresAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	})
}

// LookupHandler 处理 GET /api/private-links/{token}。
func (s *Service) LookupHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := identity.FromContext(r.Context()); !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	st, err := s.Lookup(r.Context(), r.PathValue("token"))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"needPassword":  st.NeedPassword,
		"burnAfterRead": st.BurnAfterRead,
		"status":        st.Status,
	})
}

type joinRequest struct {
	Password string `json:"password"`
}

// JoinHandler 处理 POST /api/private-links/{token}/join。
func (s *Service) JoinHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req joinRequest
	// 允许空 body。
	_ = httpx.DecodeJSON(r, &req)
	ip := ""
	if s.resolver != nil {
		ip = s.resolver.ClientIP(r)
	}
	convID, err := s.Join(r.Context(), ident, r.PathValue("token"), req.Password, ip)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"conversationId": convID.String()})
}

// DestroyHandler 处理 POST /api/private-links/{token}/destroy。
func (s *Service) DestroyHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	if err := s.Destroy(r.Context(), ident.ID, r.PathValue("token")); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
