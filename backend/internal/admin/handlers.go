package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/moderation"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/realtime"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const maxBodyBytes = 64 << 10

// ---- 登录 / 登出 ----

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoginHandler 处理 POST /api/admin/login。
func (s *Service) LoginHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req loginRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	token, err := s.Login(r.Context(), req.Username, req.Password, s.clientIP(r))
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	httpx.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clientIP 解析客户端 IP（优先使用注入的 resolver）。
func (s *Service) clientIP(r *http.Request) string {
	if s.resolver != nil {
		return s.resolver.ClientIP(r)
	}
	return rawClientIP(r)
}

func rawClientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		parts := strings.Split(v, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}

// LogoutHandler 处理 POST /api/admin/logout。
func (s *Service) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	adminID, _ := AdminIDFromContext(r.Context())
	if c, err := r.Cookie(SessionCookie); err == nil {
		_ = s.Logout(r.Context(), c.Value, adminID)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// ---- 举报 ----

// ListReportsHandler 处理 GET /api/admin/reports?status=&cursor=&limit=。
func (s *Service) ListReportsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var status *string
	if st := q.Get("status"); st == "open" || st == "resolved" {
		status = &st
	}
	limit := parseLimit(q.Get("limit"))
	cursorCreated, cursorID, hasCursor := parseReportCursor(q.Get("cursor"))

	rows, err := s.q.ListReports(r.Context(), store.ListReportsParams{
		Status:        status,
		CursorCreated: cursorCreated,
		CursorID:      cursorID,
		Lim:           int32(limit),
	})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, rep := range rows {
		out = append(out, reportSummary(rep))
	}
	var nextCursor *string
	if len(rows) == limit && limit > 0 {
		last := rows[len(rows)-1]
		nc := encodeReportCursor(last.CreatedAt, last.ID.String())
		nextCursor = &nc
	}
	_ = hasCursor
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reports": out, "nextCursor": nextCursor})
}

// GetReportHandler 处理 GET /api/admin/reports/{id}。
func (s *Service) GetReportHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "举报 ID 非法"))
		return
	}
	rep, err := s.q.GetReportByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, notFound("举报不存在"))
			return
		}
		httpx.WriteError(w, internalErr())
		return
	}
	out := reportSummary(rep)
	out["snapshot"] = rawJSON(rep.Snapshot)

	summary, bans, err := s.identitySummary(r.Context(), rep.ReportedIdentityID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out["reported"] = summary
	out["activeBans"] = bans
	httpx.WriteJSON(w, http.StatusOK, out)
}

// ResolveReportHandler 处理 POST /api/admin/reports/{id}/resolve。
func (s *Service) ResolveReportHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "举报 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req resolveRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if req.Action != "dismiss" && req.Action != "ban" {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "action 非法"))
		return
	}
	rep, err := s.q.GetReportByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, notFound("举报不存在"))
			return
		}
		httpx.WriteError(w, internalErr())
		return
	}
	if rep.Status != "open" {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "举报已处理"))
		return
	}
	adminID, _ := AdminIDFromContext(r.Context())

	if req.Action == "ban" {
		if err := s.applyBan(r.Context(), rep, req.Ban, adminID); err != nil {
			httpx.WriteError(w, err)
			return
		}
	}

	affected, err := s.q.ResolveReport(r.Context(), store.ResolveReportParams{ID: id, HandledBy: adminID})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	if affected == 0 {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "举报已处理"))
		return
	}
	s.audit(r.Context(), adminID, ActionResolve, id.String(), map[string]any{"action": req.Action})
	w.WriteHeader(http.StatusNoContent)
}

// applyBan 依据被举报者解析封禁主体并创建封禁。
func (s *Service) applyBan(ctx context.Context, rep store.Report, spec *banSpec, adminID pgtype.UUID) error {
	if spec == nil || len(spec.Subjects) == 0 {
		return httpx.NewError(httpx.CodeInvalidRequest, "缺少封禁主体")
	}
	summary, _, err := s.identitySummary(ctx, rep.ReportedIdentityID)
	if err != nil {
		return internalErr()
	}
	lastIP, _ := summary["lastIp"].(string)
	// 重新读取账号信息以获取 account uuid。
	info, err := s.q.GetIdentityNicknameCreatedIP(ctx, rep.ReportedIdentityID)
	if err != nil {
		return internalErr()
	}

	var subjects []moderation.BanSubject
	for _, t := range spec.Subjects {
		switch t {
		case moderation.SubjectIdentity:
			subjects = append(subjects, moderation.BanSubject{Type: moderation.SubjectIdentity, Value: rep.ReportedIdentityID.String()})
		case moderation.SubjectIP:
			ip := lastIP
			if ip == "" {
				return httpx.NewError(httpx.CodeInvalidRequest, "被举报者没有可用的 IP")
			}
			subjects = append(subjects, moderation.BanSubject{Type: moderation.SubjectIP, Value: ip})
		case moderation.SubjectAccount:
			if !info.AccountID.Valid {
				return httpx.NewError(httpx.CodeInvalidRequest, "被举报者未实名，无法按账号封禁")
			}
			subjects = append(subjects, moderation.BanSubject{Type: moderation.SubjectAccount, Value: info.AccountID.String()})
		default:
			return httpx.NewError(httpx.CodeInvalidRequest, "未知封禁主体")
		}
	}
	duration := durationFromHours(spec.DurationHours)
	reason := spec.Reason
	if reason == "" {
		reason = "举报处理"
	}
	if _, err := s.banSvc.Create(ctx, subjects, duration, reason, adminID); err != nil {
		return internalErr()
	}
	return nil
}

// ---- 封禁 ----

// ListBansHandler 处理 GET /api/admin/bans?active=&cursor=&limit=。
func (s *Service) ListBansHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var active *bool
	if a := q.Get("active"); a == "true" {
		v := true
		active = &v
	} else if a == "false" {
		v := false
		active = &v
	}
	limit := parseLimit(q.Get("limit"))
	cursorCreated, cursorID, _ := parseReportCursor(q.Get("cursor"))
	rows, err := s.q.ListBans(r.Context(), store.ListBansParams{Active: active, CursorCreated: cursorCreated, CursorID: cursorID, Lim: int32(limit)})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, b := range rows {
		out = append(out, banJSON(b))
	}
	var nextCursor *string
	if len(rows) == limit && limit > 0 {
		last := rows[len(rows)-1]
		nc := encodeReportCursor(last.CreatedAt, last.ID.String())
		nextCursor = &nc
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"bans": out, "nextCursor": nextCursor})
}

// CreateBanHandler 处理 POST /api/admin/bans。
func (s *Service) CreateBanHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req createBanRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	if len(req.Subjects) == 0 {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "缺少封禁主体"))
		return
	}
	subjects := make([]moderation.BanSubject, 0, len(req.Subjects))
	auditSubjects := make([]map[string]string, 0, len(req.Subjects))
	for _, sub := range req.Subjects {
		value, err := normalizeBanValue(sub.Type, sub.Value)
		if err != nil {
			httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, err.Error()))
			return
		}
		subjects = append(subjects, moderation.BanSubject{Type: sub.Type, Value: value})
		auditSubjects = append(auditSubjects, map[string]string{"type": sub.Type, "value": value})
	}
	adminID, _ := AdminIDFromContext(r.Context())
	bans, err := s.banSvc.Create(r.Context(), subjects, durationFromHours(req.DurationHours), req.Reason, adminID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	s.audit(r.Context(), adminID, ActionCreateBan, "", map[string]any{
		"count":    len(bans),
		"reason":   req.Reason,
		"subjects": auditSubjects,
	})
	out := make([]map[string]any, 0, len(bans))
	for _, b := range bans {
		out = append(out, banJSON(b))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"bans": out})
}

// RevokeBanHandler 处理 DELETE /api/admin/bans/{id}。
func (s *Service) RevokeBanHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "封禁 ID 非法"))
		return
	}
	if err := s.banSvc.Revoke(r.Context(), id); err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	adminID, _ := AdminIDFromContext(r.Context())
	s.audit(r.Context(), adminID, ActionRevokeBan, id.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 在线用户 ----

// maxOnlineUsers 是在线用户列表一次返回的最大条数（防止极端情况下响应过大）。
const maxOnlineUsers = 500

// OnlineUsersHandler 处理 GET /api/admin/online?q=。
// 返回当前在线身份的快照（同一身份的多条连接聚合为一条），可按 q 过滤。
func (s *Service) OnlineUsersHandler(w http.ResponseWriter, r *http.Request) {
	if s.online == nil {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": []map[string]any{}, "total": 0})
		return
	}
	snapshot := s.online.OnlineSnapshot()
	total := len(snapshot)

	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	users := make([]map[string]any, 0, len(snapshot))
	for _, u := range snapshot {
		if q != "" && !onlineIdentityMatches(u, q) {
			continue
		}
		if len(users) >= maxOnlineUsers {
			break
		}
		users = append(users, onlineIdentityJSON(u))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": users, "total": total})
}

// onlineIdentityMatches 报告在线身份是否匹配搜索词（昵称 / 匿名编号 / 任一 IP 子串）。
func onlineIdentityMatches(u realtime.OnlineIdentity, q string) bool {
	if strings.Contains(strings.ToLower(u.Nickname), q) {
		return true
	}
	if strings.Contains(strings.ToLower(u.PublicCode), q) {
		return true
	}
	for _, ip := range u.IPs {
		if strings.Contains(strings.ToLower(ip), q) {
			return true
		}
	}
	return false
}

// onlineIdentityJSON 把在线身份快照转为响应 JSON（时间为 RFC3339）。
func onlineIdentityJSON(u realtime.OnlineIdentity) map[string]any {
	accountID := ""
	if u.AccountID.Valid {
		accountID = u.AccountID.String()
	}
	ips := u.IPs
	if ips == nil {
		ips = []string{}
	}
	connectedAt := ""
	if !u.ConnectedAt.IsZero() {
		connectedAt = u.ConnectedAt.UTC().Format(time.RFC3339)
	}
	return map[string]any{
		"identityId":  u.IdentityID.String(),
		"publicCode":  u.PublicCode,
		"nickname":    u.Nickname,
		"accountId":   accountID,
		"ips":         ips,
		"connCount":   u.ConnCount,
		"connectedAt": connectedAt,
		"userAgent":   u.UserAgent,
	}
}

// ---- 统计 / 审计 ----

// StatsHandler 处理 GET /api/admin/stats。
func (s *Service) StatsHandler(w http.ResponseWriter, r *http.Request) {
	online := 0
	if s.stats != nil {
		online = s.stats.OnlineCount()
	}
	active, err := s.q.CountActiveConversations(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	msgs, err := s.q.CountMessagesSince(r.Context(), pgtype.Timestamptz{Time: s.now().Add(-24 * time.Hour), Valid: true})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	open, err := s.q.CountOpenReports(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"online":              online,
		"activeConversations": active,
		"messages24h":         msgs,
		"openReports":         open,
	})
}

// AuditLogsHandler 处理 GET /api/admin/audit-logs?cursor=&limit=。
func (s *Service) AuditLogsHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := parseLimit(q.Get("limit"))
	var beforeID *int64
	if v, ok := parseOptionalInt64(q.Get("cursor")); ok {
		beforeID = &v
	}
	rows, err := s.q.ListAdminAuditLogs(r.Context(), store.ListAdminAuditLogsParams{BeforeID: beforeID, Lim: int32(limit)})
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, a := range rows {
		out = append(out, map[string]any{
			"id":        a.ID,
			"adminId":   a.AdminID.String(),
			"action":    a.Action,
			"target":    a.Target,
			"detail":    rawJSON(a.Detail),
			"createdAt": formatTS(a.CreatedAt),
		})
	}
	var nextCursor *int64
	if len(rows) == limit && limit > 0 {
		nc := rows[len(rows)-1].ID
		nextCursor = &nc
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"logs": out, "nextCursor": nextCursor})
}

// ---- 内部工具与 DTO ----

func (s *Service) identitySummary(ctx context.Context, identityID pgtype.UUID) (map[string]any, []map[string]any, error) {
	info, err := s.q.GetIdentityNicknameCreatedIP(ctx, identityID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return map[string]any{"nickname": "", "createdIp": "", "lastIp": "", "phoneMasked": "", "reportCount": 0}, nil, nil
		}
		return nil, nil, err
	}
	createdIP := ""
	if info.CreatedIp != nil {
		createdIP = info.CreatedIp.String()
	}
	lastIP := createdIP
	if lip, err := s.q.GetLastWSConnectIP(ctx, identityID); err == nil && lip != nil {
		lastIP = lip.String()
	}
	masked := ""
	accountVal := ""
	if info.AccountID.Valid {
		accountVal = info.AccountID.String()
		if m, err := s.q.GetVerifiedMasked(ctx, info.AccountID); err == nil {
			masked = m
		}
	}
	count, _ := s.q.CountReportsAgainstIdentity(ctx, identityID)
	bans, _ := s.q.ListActiveBansForSubjects(ctx, store.ListActiveBansForSubjectsParams{
		ExpiresAt:      pgtype.Timestamptz{Time: s.now(), Valid: true},
		SubjectValue:   identityID.String(),
		SubjectValue_2: accountVal,
		SubjectValue_3: lastIP,
	})
	banList := make([]map[string]any, 0, len(bans))
	for _, b := range bans {
		banList = append(banList, banJSON(b))
	}
	summary := map[string]any{
		"nickname":    info.Nickname,
		"createdIp":   createdIP,
		"lastIp":      lastIP,
		"phoneMasked": masked,
		"reportCount": count,
	}
	return summary, banList, nil
}

func reportSummary(rep store.Report) map[string]any {
	var note *string
	if rep.Note != nil {
		note = rep.Note
	}
	var convID *string
	if rep.ConversationID.Valid {
		c := rep.ConversationID.String()
		convID = &c
	}
	return map[string]any{
		"id":                 rep.ID.String(),
		"reportedIdentityId": rep.ReportedIdentityID.String(),
		"conversationId":     convID,
		"reason":             rep.Reason,
		"note":               note,
		"status":             rep.Status,
		"createdAt":          formatTS(rep.CreatedAt),
	}
}

func banJSON(b store.Ban) map[string]any {
	var expires *string
	if b.ExpiresAt.Valid {
		e := formatTS(b.ExpiresAt)
		expires = &e
	}
	active := !b.RevokedAt.Valid && (!b.ExpiresAt.Valid || b.ExpiresAt.Time.After(time.Now()))
	return map[string]any{
		"id":           b.ID.String(),
		"subjectType":  b.SubjectType,
		"subjectValue": b.SubjectValue,
		"reason":       b.Reason,
		"expiresAt":    expires,
		"active":       active,
	}
}

func rawJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	return v
}

func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}

func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, errors.New("空 UUID")
	}
	return u, nil
}

func parseLimit(s string) int {
	if s == "" {
		return 20
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 20
	}
	if n > 100 {
		return 100
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

func parseReportCursor(s string) (pgtype.Timestamptz, pgtype.UUID, bool) {
	if s == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	ns, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	var id pgtype.UUID
	if err := id.Scan(parts[1]); err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, false
	}
	return pgtype.Timestamptz{Time: time.Unix(0, ns), Valid: true}, id, true
}

func encodeReportCursor(created pgtype.Timestamptz, id string) string {
	return strconv.FormatInt(created.Time.UnixNano(), 10) + ":" + id
}

func durationFromHours(hours *int) time.Duration {
	if hours == nil || *hours <= 0 {
		return 0
	}
	return time.Duration(*hours) * time.Hour
}

func internalErr() error { return httpx.NewError(httpx.CodeInternal, "服务器内部错误") }

func notFound(msg string) error {
	e := httpx.NewError(httpx.CodeInvalidRequest, msg)
	e.Status = http.StatusNotFound
	return e
}

type banSpec struct {
	Subjects      []string `json:"subjects"`
	DurationHours *int     `json:"durationHours"`
	Reason        string   `json:"reason"`
}

type resolveRequest struct {
	Action string   `json:"action"`
	Ban    *banSpec `json:"ban"`
}

type banSubjectJSON struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type createBanRequest struct {
	Subjects      []banSubjectJSON `json:"subjects"`
	DurationHours *int             `json:"durationHours"`
	Reason        string           `json:"reason"`
}

// ---- 按匿名编号查身份 ----

// LookupIdentityByCodeHandler 处理 GET /api/admin/identities/by-code/{code}。
// 返回匿名身份摘要（身份 ID、创建时间、VIP 状态、封禁状态），可直接发起封禁。
func (s *Service) LookupIdentityByCodeHandler(w http.ResponseWriter, r *http.Request) {
	code := identity.NormalizePublicCode(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "匿名编号不能为空"))
		return
	}
	ident, err := s.q.GetIdentityByPublicCode(r.Context(), code)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			httpx.WriteError(w, notFound("匿名编号不存在"))
			return
		}
		httpx.WriteError(w, internalErr())
		return
	}
	summary, bans, err := s.identitySummary(r.Context(), ident.ID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	vipActive := ident.VipUntil.Valid && ident.VipUntil.Time.After(s.now())
	banned := len(bans) > 0
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"identityId": ident.ID.String(),
		"publicCode": ident.PublicCode,
		"createdAt":  formatTS(ident.CreatedAt),
		"vipActive":  vipActive,
		"banned":     banned,
		"activeBans": bans,
		"summary":    summary,
	})
}
