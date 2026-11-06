package conversation

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

// 分页默认与上限。
const (
	defaultLimit = 50
	maxLimit     = 100
)

// conversationResponse 是 GET /api/conversations 的元素。
type conversationResponse struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	BurnAfterRead bool   `json:"burnAfterRead"`
	UnreadCount   int64  `json:"unreadCount"`
	CreatedAt     string `json:"createdAt"`
}

// messageResponse 是消息的对外表示（不含 identity_id）。
type messageResponse struct {
	ID            int64  `json:"id"`
	MemberID      int32  `json:"memberId"`
	Nickname      string `json:"nickname"`
	AvatarSeed    string `json:"avatarSeed"`
	Kind          string `json:"kind"`
	Body          string `json:"body"`
	CreatedAt     string `json:"createdAt"`
	BurnAfterRead bool   `json:"burnAfterRead"`
}

// ListConversationsHandler 处理 GET /api/conversations?kind=。
func (s *Service) ListConversationsHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	kindFilter := r.URL.Query().Get("kind")

	rows, err := s.q.ListConversations(r.Context(), ident.ID)
	if err != nil {
		httpx.WriteError(w, internalAPIError(err))
		return
	}
	out := make([]conversationResponse, 0, len(rows))
	for _, row := range rows {
		if kindFilter != "" && string(row.Kind) != kindFilter {
			continue
		}
		out = append(out, conversationResponse{
			ID:            row.ID.String(),
			Kind:          string(row.Kind),
			BurnAfterRead: row.BurnAfterRead,
			UnreadCount:   row.UnreadCount,
			CreatedAt:     formatTS(row.CreatedAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ListMessagesHandler 处理 GET /api/conversations/{id}/messages?before=&after=&limit=。
func (s *Service) ListMessagesHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	convID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "会话 ID 非法"))
		return
	}

	// 仅成员（含已离开）可读。
	if _, err := s.q.GetMember(r.Context(), store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID}); err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, httpx.NewError(httpx.CodeNotMember, "你不在该会话中"))
			return
		}
		httpx.WriteError(w, internalAPIError(err))
		return
	}

	conv, err := s.q.GetConversation(r.Context(), convID)
	if err != nil {
		httpx.WriteError(w, internalAPIError(err))
		return
	}
	// 私密链接会话已销毁后，历史接口返回 conversation_ended。
	if conv.Kind == store.ConversationKindPrivate {
		destroyed, derr := s.q.GetPrivateLinkDestroyedByConversation(r.Context(), convID)
		if derr == nil && destroyed.Valid {
			httpx.WriteError(w, httpx.NewError(httpx.CodeConversationEnded, "会话已销毁"))
			return
		}
	}

	limit := parseLimit(r.URL.Query().Get("limit"))
	before, hasBefore := parseOptionalInt64(r.URL.Query().Get("before"))
	after, hasAfter := parseOptionalInt64(r.URL.Query().Get("after"))

	out, err := s.fetchMessages(r, convID, ident.ID, limit, before, hasBefore, after, hasAfter)
	if err != nil {
		httpx.WriteError(w, internalAPIError(err))
		return
	}
	for i := range out {
		out[i].BurnAfterRead = conv.BurnAfterRead
		// 房间保留昵称；故事会话显示角色名；1v1 会话置空（前端改用匿名代号）。
		out[i].Nickname = s.nicknameFor(r.Context(), conv, out[i].Nickname, out[i].MemberID)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// fetchMessages 按 before/after 分页读取消息（屏蔽过滤已在 SQL 中完成），返回按 id 升序的列表。
func (s *Service) fetchMessages(r *http.Request, convID, requester pgtype.UUID, limit int, before int64, hasBefore bool, after int64, hasAfter bool) ([]messageResponse, error) {
	ctx := r.Context()
	if hasAfter {
		rows, err := s.q.ListMessagesAsc(ctx, store.ListMessagesAscParams{
			ConversationID:      convID,
			AfterID:             after,
			RequesterIdentityID: requester,
			Lim:                 int32(limit),
		})
		if err != nil {
			return nil, err
		}
		out := make([]messageResponse, 0, len(rows))
		for _, row := range rows {
			out = append(out, toMessageResponse(row.ID, row.SenderMemberID, row.Nickname, row.AvatarSeed, row.Kind, row.Body, row.CreatedAt))
		}
		return out, nil
	}

	var beforePtr *int64
	if hasBefore {
		beforePtr = &before
	}
	rows, err := s.q.ListMessagesDesc(ctx, store.ListMessagesDescParams{
		ConversationID:      convID,
		BeforeID:            beforePtr,
		RequesterIdentityID: requester,
		Lim:                 int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]messageResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMessageResponse(row.ID, row.SenderMemberID, row.Nickname, row.AvatarSeed, row.Kind, row.Body, row.CreatedAt))
	}
	// DESC 结果反转为升序。
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// toMessageResponse 构造消息响应。
func toMessageResponse(id int64, memberID int32, nickname, avatarSeed, kind, body string, createdAt pgtype.Timestamptz) messageResponse {
	return messageResponse{
		ID:         id,
		MemberID:   memberID,
		Nickname:   nickname,
		AvatarSeed: avatarSeed,
		Kind:       kind,
		Body:       body,
		CreatedAt:  formatTS(createdAt),
	}
}

// parseLimit 解析分页 limit，default 50，最大 100。
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

// parseOptionalInt64 解析可选的 int64 查询参数。
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

// internalAPIError 构造 internal 错误。
func internalAPIError(_ error) error {
	return httpx.NewError(httpx.CodeInternal, "服务器内部错误")
}

// conversationPeer 是对外返回的对方信息（不含 identity_id）。
type conversationPeer struct {
	MemberID   int32  `json:"memberId"`
	Nickname   string `json:"nickname"`
	AvatarSeed string `json:"avatarSeed"`
}

// ConversationHandler 处理 GET /api/conversations/{id}：成员可访问，返回会话基本信息与自身 memberId，
// 两人会话（match/bottle/private）额外返回对方 peer。
func (s *Service) ConversationHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	convID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "会话 ID 非法"))
		return
	}
	member, err := s.q.GetMember(r.Context(), store.GetMemberParams{ConversationID: convID, IdentityID: ident.ID})
	if err != nil {
		if err == pgx.ErrNoRows {
			httpx.WriteError(w, httpx.NewError(httpx.CodeNotMember, "你不在该会话中"))
			return
		}
		httpx.WriteError(w, internalAPIError(err))
		return
	}
	conv, err := s.q.GetConversation(r.Context(), convID)
	if err != nil {
		httpx.WriteError(w, internalAPIError(err))
		return
	}

	resp := map[string]any{
		"id":            conv.ID.String(),
		"kind":          string(conv.Kind),
		"burnAfterRead": conv.BurnAfterRead,
		"myMemberId":    member.MemberID,
	}
	if conv.EndedAt.Valid {
		resp["endedAt"] = formatTS(conv.EndedAt)
		resp["endReason"] = conv.EndReason
	} else {
		resp["endedAt"] = nil
		resp["endReason"] = nil
	}

	// 两人会话才返回 peer。
	if conv.Kind != store.ConversationKindRoom {
		others, err := s.q.ListOtherMembersWithIdentity(r.Context(), store.ListOtherMembersWithIdentityParams{
			ConversationID: convID,
			IdentityID:     ident.ID,
		})
		if err != nil {
			httpx.WriteError(w, internalAPIError(err))
			return
		}
		if len(others) > 0 {
			resp["peer"] = conversationPeer{
				MemberID:   others[0].MemberID,
				Nickname:   others[0].Nickname,
				AvatarSeed: others[0].AvatarSeed,
			}
		}
	}

	httpx.WriteJSON(w, http.StatusOK, resp)
}
