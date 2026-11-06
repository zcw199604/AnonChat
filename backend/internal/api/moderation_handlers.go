package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
)

// maxBodyBytes 是 REST 请求体大小上限。
const maxBodyBytes = 64 << 10

// banMiddleware 在 RequireIdentity 之后统一检查封禁，被封返回 403 banned。
func (s *Server) banMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Ban != nil {
			ident, ok := identity.FromContext(r.Context())
			if !ok {
				httpx.WriteError(w, identity.ErrUnauthorized)
				return
			}
			ip := ""
			if s.deps.Resolver != nil {
				ip = s.deps.Resolver.ClientIP(r)
			}
			if s.deps.Ban.IsBanned(r.Context(), ident, ip) {
				httpx.WriteError(w, httpx.NewError(httpx.CodeBanned, "你已被封禁"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// handleBlockConversation 处理 POST /api/conversations/{id}/block（1v1 屏蔽对方并结束会话）。
func (s *Server) handleBlockConversation(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	convID, err := parseUUIDParam(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "会话 ID 非法"))
		return
	}
	if err := s.deps.Conversation.BlockPeer(r.Context(), ident.ID, convID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleBlockMember 处理 POST /api/conversations/{id}/members/{memberId}/block（房间屏蔽成员）。
func (s *Server) handleBlockMember(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	convID, err := parseUUIDParam(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "会话 ID 非法"))
		return
	}
	memberID, err := strconv.Atoi(r.PathValue("memberId"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "成员 ID 非法"))
		return
	}
	if err := s.deps.Conversation.BlockMember(r.Context(), ident.ID, convID, int32(memberID)); err != nil {
		httpx.WriteError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// reportRequestBody 是 POST /api/reports 的请求体。
type reportRequestBody struct {
	ConversationID string  `json:"conversationId"`
	MemberID       int32   `json:"memberId"`
	MessageIDs     []int64 `json:"messageIds"`
	Reason         string  `json:"reason"`
	Note           string  `json:"note"`
}

// handleReport 处理 POST /api/reports。
func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body reportRequestBody
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.WriteError(w, err)
		return
	}
	convID, err := parseUUIDParam(body.ConversationID)
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "会话 ID 非法"))
		return
	}
	ip := ""
	if s.deps.Resolver != nil {
		ip = s.deps.Resolver.ClientIP(r)
	}
	id, err := s.deps.Report.Report(r.Context(), ident.ID, moderation.ReportRequest{
		ConversationID: convID,
		MemberID:       body.MemberID,
		MessageIDs:     body.MessageIDs,
		Reason:         body.Reason,
		Note:           body.Note,
	}, ip)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"id": id.String()})
}

// parseUUIDParam 解析 UUID 字符串参数。
func parseUUIDParam(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, httpx.NewError(httpx.CodeInvalidRequest, "UUID 为空")
	}
	return u, nil
}
