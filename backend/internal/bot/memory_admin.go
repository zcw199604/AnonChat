package bot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

var errMemoryNotFound = httpx.NewError(httpx.CodeNotFound, "记忆不存在")

// requireMemoryBot 在空列表和清空操作中同样区分不存在的 bot。
func (s *Service) requireMemoryBot(ctx context.Context, id pgtype.UUID) error {
	_, err := s.q.GetBot(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrBotNotFound
	}
	return err
}

type memoryResponse struct {
	IdentityID        string `json:"identityId"`
	PublicCode        string `json:"publicCode"`
	Nickname          string `json:"nickname"`
	Affinity          int16  `json:"affinity"`
	MoodLabel         string `json:"moodLabel"`
	MoodValue         int16  `json:"moodValue"`
	EventCount        int64  `json:"eventCount"`
	LastInteractionAt string `json:"lastInteractionAt"`
}

type memoryEventResponse struct {
	ID             string   `json:"id"`
	ConversationID *string  `json:"conversationId"`
	Gist           string   `json:"gist"`
	Tags           []string `json:"tags"`
	CreatedAt      string   `json:"createdAt"`
}

// memoryIDs 校验后台路径参数；所有查询始终携带 bot 与对方身份双键。
func memoryIDs(r *http.Request, peer bool) (pgtype.UUID, pgtype.UUID, error) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		return id, pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "bot ID 非法")
	}
	var identityID pgtype.UUID
	if peer {
		identityID, err = parseUUID(r.PathValue("identityId"))
		if err != nil {
			return id, identityID, httpx.NewError(httpx.CodeInvalidRequest, "身份 ID 非法")
		}
	}
	return id, identityID, nil
}

// memoryPage 限制单页最多 100 项，拒绝非法分页参数。
func memoryPage(r *http.Request) (int32, int32, error) {
	limit, offset := int64(20), int64(0)
	var err error
	if raw := r.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			return 0, 0, httpx.NewError(httpx.CodeInvalidRequest, "limit 需为 1–100")
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		offset, err = strconv.ParseInt(raw, 10, 32)
		if err != nil || offset < 0 {
			return 0, 0, httpx.NewError(httpx.CodeInvalidRequest, "offset 需为非负整数")
		}
	}
	return int32(limit), int32(offset), nil
}

// ListMemoriesHandler 返回按最后互动时间倒序排列的分页记忆列表。
func (h *Handler) ListMemoriesHandler(w http.ResponseWriter, r *http.Request) {
	id, _, err := memoryIDs(r, false)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	limit, offset, err := memoryPage(r)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err = h.svc.requireMemoryBot(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	rows, err := h.svc.q.ListBotMemories(r.Context(), store.ListBotMemoriesParams{BotID: id, Limit: limit, Offset: offset})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]memoryResponse, 0, len(rows))
	for _, m := range rows {
		out = append(out, memoryResponse{IdentityID: m.PeerIdentityID.String(), PublicCode: m.PublicCode, Nickname: m.Nickname, Affinity: m.Affinity, MoodLabel: m.MoodLabel, MoodValue: m.MoodValue, EventCount: m.EventCount, LastInteractionAt: formatTS(m.LastInteractionAt)})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// GetMemoryHandler 返回画像与全部保留事件；审计只包含标识和数量。
func (h *Handler) GetMemoryHandler(w http.ResponseWriter, r *http.Request) {
	id, peer, err := memoryIDs(r, true)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	m, err := h.svc.q.GetPeerMemory(r.Context(), store.GetPeerMemoryParams{BotID: id, PeerIdentityID: peer})
	if errors.Is(err, pgx.ErrNoRows) {
		httpx.WriteError(w, errMemoryNotFound)
		return
	}
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	events, err := h.svc.q.ListPeerEvents(r.Context(), store.ListPeerEventsParams{BotID: id, PeerIdentityID: peer, Limit: 50})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]memoryEventResponse, 0, len(events))
	for _, e := range events {
		out = append(out, memoryEventResponse{ID: e.ID.String(), ConversationID: uuidPtr(e.ConversationID), Gist: e.Gist, Tags: e.Tags, CreatedAt: formatTS(e.CreatedAt)})
	}
	h.audit(r, "bot.memory.view", id.String(), map[string]any{"id": id.String(), "identityId": peer.String(), "count": len(out)})
	httpx.WriteJSON(w, http.StatusOK, struct {
		Profile json.RawMessage       `json:"profile"`
		Events  []memoryEventResponse `json:"events"`
	}{Profile: m.Profile, Events: out})
}

// DeleteMemoryHandler 删除指定对方的画像及级联事件。
func (h *Handler) DeleteMemoryHandler(w http.ResponseWriter, r *http.Request) {
	id, peer, err := memoryIDs(r, true)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	n, err := h.svc.q.DeletePeerMemory(r.Context(), store.DeletePeerMemoryParams{BotID: id, PeerIdentityID: peer})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	if n == 0 {
		httpx.WriteError(w, errMemoryNotFound)
		return
	}
	h.audit(r, "bot.memory.delete", id.String(), map[string]any{"id": id.String(), "identityId": peer.String(), "count": n})
	w.WriteHeader(http.StatusNoContent)
}

// ClearMemoriesHandler 清空一个 bot 的画像及级联事件。
func (h *Handler) ClearMemoriesHandler(w http.ResponseWriter, r *http.Request) {
	id, _, err := memoryIDs(r, false)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	if err = h.svc.requireMemoryBot(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	n, err := h.svc.q.ClearBotMemories(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	h.audit(r, "bot.memory.clear", id.String(), map[string]any{"id": id.String(), "count": n})
	w.WriteHeader(http.StatusNoContent)
}
