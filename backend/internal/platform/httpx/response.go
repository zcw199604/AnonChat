// Package httpx 提供 HTTP 层通用工具：JSON 响应、统一错误格式、中间件。
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
)

// ErrorCode 是统一错误响应中的机器可读错误码（snake_case）。
// 取值与 how.md「WebSocket 协议 / 错误码」保持一致。
type ErrorCode string

const (
	CodeUnauthorized         ErrorCode = "unauthorized"
	CodeVerificationRequired ErrorCode = "verification_required"
	CodeBanned               ErrorCode = "banned"
	CodeRateLimited          ErrorCode = "rate_limited"
	CodeContentBlocked       ErrorCode = "content_blocked"
	CodeNotMember            ErrorCode = "not_member"
	CodeConversationEnded    ErrorCode = "conversation_ended"
	CodeRoomFull             ErrorCode = "room_full"
	CodeLinkFull             ErrorCode = "link_full"
	CodeLinkExpired          ErrorCode = "link_expired"
	CodeBadPassword          ErrorCode = "bad_password"
	CodeEmptySea             ErrorCode = "empty_sea"
	CodeQuotaExceeded        ErrorCode = "quota_exceeded"
	CodeInvalidRequest       ErrorCode = "invalid_request"
	CodeInternal             ErrorCode = "internal"
	// CodeForbiddenOrigin 表示修改类请求的 Origin 不在白名单内。
	// 注意：how.md 的错误码表中未列出此码，属于本批安全加固新增，已在交付说明中注明。
	CodeForbiddenOrigin ErrorCode = "forbidden_origin"

	// 本包（资料 / VIP / 广播 / 故事书）新增错误码（how.md 3.7）。
	CodeVIPRequired     ErrorCode = "vip_required"
	CodeInvalidCode     ErrorCode = "invalid_code"
	CodeNoBroadcastCard ErrorCode = "no_broadcast_card"
	CodeStoryTaken      ErrorCode = "story_taken"
	CodeStoryNotValid   ErrorCode = "story_not_valid"
	CodeNotFound        ErrorCode = "not_found"
)

// errorBody 是错误响应体中的 error 对象。
type errorBody struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}

// errorResponse 是统一错误响应：{"error":{"code":...,"message":...}}。
type errorResponse struct {
	Error errorBody `json:"error"`
}

// APIError 是可携带错误码和 HTTP 状态码的业务错误。
type APIError struct {
	Code    ErrorCode
	Message string
	Status  int
}

// Error 实现 error 接口。
func (e *APIError) Error() string { return string(e.Code) + ": " + e.Message }

// NewError 构造一个 APIError，状态码默认根据错误码推导。
func NewError(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message, Status: statusForCode(code)}
}

// statusForCode 把错误码映射为默认 HTTP 状态码。
func statusForCode(code ErrorCode) int {
	switch code {
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeBanned, CodeVerificationRequired:
		return http.StatusForbidden
	case CodeRateLimited:
		return http.StatusTooManyRequests
	case CodeNotMember:
		return http.StatusForbidden
	case CodeConversationEnded, CodeRoomFull, CodeLinkFull, CodeLinkExpired:
		return http.StatusConflict
	case CodeBadPassword:
		return http.StatusForbidden
	case CodeForbiddenOrigin:
		return http.StatusForbidden
	case CodeEmptySea:
		return http.StatusNotFound
	case CodeQuotaExceeded:
		return http.StatusTooManyRequests
	case CodeContentBlocked:
		return http.StatusUnprocessableEntity
	case CodeVIPRequired:
		return http.StatusForbidden
	case CodeInvalidCode:
		return http.StatusBadRequest
	case CodeNoBroadcastCard, CodeStoryTaken, CodeStoryNotValid:
		return http.StatusConflict
	case CodeNotFound:
		return http.StatusNotFound
	case CodeInvalidRequest:
		return http.StatusBadRequest
	case CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}

// WriteJSON 以 JSON 形式写出响应体。
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// 此时响应头已写出，只能记录日志。
		slog.Error("写出 JSON 响应失败", "error", err)
	}
}

// WriteError 按统一格式写出错误响应。
// 如果 err 是 *APIError，则使用其错误码和状态码；否则返回 internal。
func WriteError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = NewError(CodeInternal, "服务器内部错误")
	}
	status := apiErr.Status
	if status == 0 {
		status = statusForCode(apiErr.Code)
	}
	WriteJSON(w, status, errorResponse{Error: errorBody{Code: apiErr.Code, Message: apiErr.Message}})
}

// DecodeJSON 解码请求体为 v，禁止未知字段，并要求请求体大小受限。
// 由调用方在使用前套上 http.MaxBytesReader 限制请求体大小。
func DecodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return NewError(CodeInvalidRequest, "请求体格式错误")
	}
	return nil
}
