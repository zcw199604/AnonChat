package room

import (
	"net/http"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

// roomListItem 是 GET /api/rooms 的元素。
type roomListItem struct {
	ID          string `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Online      int    `json:"online"`
	MaxMembers  int    `json:"maxMembers"`
}

// roomMessageItem 是房间消息的对外表示。
type roomMessageItem struct {
	ID         int64  `json:"id"`
	MemberID   int32  `json:"memberId"`
	Nickname   string `json:"nickname"`
	AvatarSeed string `json:"avatarSeed"`
	Kind       string `json:"kind"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
}

// ListRoomsHandler 处理 GET /api/rooms。
func (s *Service) ListRoomsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := s.q.ListRooms(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]roomListItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, roomListItem{
			ID:          row.ID.String(),
			Slug:        row.Slug,
			Name:        row.Name,
			Description: row.Description,
			Online:      s.hub.Count(presenceKey(row.ID)),
			MaxMembers:  int(row.MaxMembers),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// RoomMessagesHandler 处理 GET /api/rooms/{id}/messages?before=&limit=。
// 公开房间：任何通过身份/封禁/实名检查的用户都可读，仍过滤屏蔽关系。
func (s *Service) RoomMessagesHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	roomID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "房间 ID 非法"))
		return
	}
	room, err := s.q.GetRoomByID(r.Context(), roomID)
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "房间不存在"))
			return
		}
		httpx.WriteError(w, internalErr())
		return
	}

	limit := parseLimit(r.URL.Query().Get("limit"))
	var beforePtr *int64
	if v, ok := parseOptionalInt64(r.URL.Query().Get("before")); ok {
		beforePtr = &v
	}

	since := pgtype.Timestamptz{Time: s.now().Add(-s.messageRetention), Valid: true}
	rows, err := s.q.ListRoomMessagesDesc(r.Context(), store.ListRoomMessagesDescParams{
		ConversationID: room.ConversationID,
		Since:          since,
		BeforeID:       beforePtr,
		Requester:      ident.ID,
		Lim:            int32(limit),
	})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]roomMessageItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, roomMessageItem{
			ID:         row.ID,
			MemberID:   row.SenderMemberID,
			Nickname:   row.Nickname,
			AvatarSeed: row.AvatarSeed,
			Kind:       row.Kind,
			Body:       row.Body,
			CreatedAt:  formatTS(row.CreatedAt),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	httpx.WriteJSON(w, http.StatusOK, out)
}

func internalErr() error { return httpx.NewError(httpx.CodeInternal, "服务器内部错误") }

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format("2006-01-02T15:04:05Z07:00")
}

func parseLimit(s string) int {
	if s == "" {
		return defaultLimit
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

func parseOptionalInt64(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
