package external

import (
	"context"
	"net/http"
	"time"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

type SettingsHandler struct {
	cache *SettingsCache
	admin *admin.Service
}

func NewSettingsHandler(cache *SettingsCache, adminSvc *admin.Service) *SettingsHandler {
	return &SettingsHandler{cache: cache, admin: adminSvc}
}

func (h *SettingsHandler) GetSettingsHandler(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, settingsResponse(h.cache.Plan()))
}

func (h *SettingsHandler) UpdateSettingsHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	var in UpdateInput
	if err := httpx.DecodeJSON(r, &in); err != nil {
		httpx.WriteError(w, err)
		return
	}
	adminID, _ := admin.AdminIDFromContext(r.Context())
	updated, err := h.cache.Update(r.Context(), in, adminID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	h.admin.LogAudit(auditCtx, adminID, "external.settings.update", "external-settings", map[string]any{
		"enabled": updated.Enabled, "chat2019Enabled": updated.Chat2019Enabled, "wanfengEnabled": updated.WanfengEnabled,
		"onlineThreshold": updated.OnlineThreshold, "localWaitSeconds": in.LocalWaitSeconds, "externalMatchSeconds": in.ExternalMatchSeconds,
		"chat2019MaxSessions": updated.Chat2019MaxSessions, "wanfengMaxSessions": updated.WanfengMaxSessions,
	})
	httpx.WriteJSON(w, http.StatusOK, settingsResponse(updated))
}

func settingsResponse(s Settings) UpdateInput {
	return UpdateInput{Enabled: s.Enabled, Chat2019Enabled: s.Chat2019Enabled, WanfengEnabled: s.WanfengEnabled,
		OnlineThreshold: s.OnlineThreshold, LocalWaitSeconds: int(s.LocalWait.Seconds()), ExternalMatchSeconds: int(s.ExternalMatchTimeout.Seconds()),
		Chat2019MaxSessions: s.Chat2019MaxSessions, WanfengMaxSessions: s.WanfengMaxSessions}
}
