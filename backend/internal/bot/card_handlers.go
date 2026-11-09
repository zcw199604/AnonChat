package bot

import (
	"io"
	"net/http"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

const (
	maxCardUploadBytes = 50 << 20
	maxCardJSONBytes   = 5 << 20
	cardUploadBuffer   = 1 << 20
)

// input 只复制可编辑字段，来源格式和表情数量由服务端维护。
func (req cardRequest) input() CardInput {
	return CardInput{
		Name: req.Name, Description: req.Description, Personality: req.Personality,
		Scenario: req.Scenario, FirstMessage: req.FirstMessage, ExampleDialogue: req.ExampleDialogue,
		SystemPrompt: req.SystemPrompt, HeartPrompt: req.HeartPrompt, ReasonPrompt: req.ReasonPrompt,
		AlternateGreetings: req.AlternateGreetings, PostHistoryInstructions: req.PostHistoryInstructions,
		Lorebook: req.Lorebook, Tools: req.Tools, CreatorNotes: req.CreatorNotes,
	}
}

// readCardUpload 限制请求总大小与实际文件大小，并清理 multipart 临时文件。
func readCardUpload(w http.ResponseWriter, r *http.Request, limit int64) (string, []byte, error) {
	// 额外空间只用于 multipart 边界和字段，文件本身仍严格受 limit 限制。
	r.Body = http.MaxBytesReader(w, r.Body, limit+(64<<10))
	if err := r.ParseMultipartForm(cardUploadBuffer); err != nil {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
		return "", nil, httpx.NewError(httpx.CodeInvalidRequest, "上传格式错误或超过大小上限")
	}
	defer r.MultipartForm.RemoveAll()
	count := 0
	for _, files := range r.MultipartForm.File {
		count += len(files)
	}
	files := r.MultipartForm.File["file"]
	if count != 1 || len(files) != 1 {
		return "", nil, httpx.NewError(httpx.CodeInvalidRequest, "请上传一个 file 文件")
	}
	if files[0].Size > limit {
		return "", nil, httpx.NewError(httpx.CodeInvalidRequest, "上传文件超过大小上限")
	}
	f, err := files[0].Open()
	if err != nil {
		return "", nil, httpx.NewError(httpx.CodeInvalidRequest, "读取上传文件失败")
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return "", nil, httpx.NewError(httpx.CodeInvalidRequest, "读取上传文件失败或超过大小上限")
	}
	return files[0].Filename, data, nil
}

func (h *Handler) writeCard(w http.ResponseWriter, r *http.Request, status int, card store.CharacterCard) {
	resp := toCardResponse(card)
	count, err := h.svc.ExpressionCount(r.Context(), card.ID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	resp.ExpressionCount = count
	httpx.WriteJSON(w, status, resp)
}

// GetCardHandler 返回单张角色卡及只读统计。
func (h *Handler) GetCardHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "角色卡 ID 非法"))
		return
	}
	card, err := h.svc.GetCard(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.writeCard(w, r, http.StatusOK, card)
}

// ImportCardHandler 导入 ST 角色卡或 .krp 人设包。
func (h *Handler) ImportCardHandler(w http.ResponseWriter, r *http.Request) {
	name, data, err := readCardUpload(w, r, maxCardUploadBytes)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	result, err := h.svc.ImportCard(r.Context(), name, data)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	resp := toCardResponse(result.Card)
	resp.ExpressionCount, err = h.svc.ExpressionCount(r.Context(), result.Card.ID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	h.audit(r, "bot.card.import", result.Card.ID.String(), map[string]any{"id": result.Card.ID.String(), "expressionCount": resp.ExpressionCount, "warningCount": len(result.Warnings)})
	warnings := result.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	httpx.WriteJSON(w, http.StatusCreated, struct {
		Card     cardResponse `json:"card"`
		Warnings []string     `json:"warnings"`
	}{resp, warnings})
}

type expressionResponse struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Description string `json:"description"`
	MediaItemID string `json:"mediaItemId"`
	Sort        int32  `json:"sort"`
}

func toExpressionResponse(row store.CardExpression) expressionResponse {
	return expressionResponse{row.ID.String(), row.Key, row.Description, row.MediaItemID.String(), row.Sort}
}

// ListExpressionsHandler 列出角色卡表情，顺序由 sort 与 key 决定。
func (h *Handler) ListExpressionsHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "角色卡 ID 非法"))
		return
	}
	rows, err := h.svc.ListExpressions(r.Context(), id)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := make([]expressionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toExpressionResponse(row))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// CreateExpressionHandler 将图片存入角色卡专属的表情文件夹。
func (h *Handler) CreateExpressionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "角色卡 ID 非法"))
		return
	}
	name, data, err := readCardUpload(w, r, h.svc.MaxExpressionBytes())
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	row, err := h.svc.CreateExpression(r.Context(), id, r.FormValue("key"), r.FormValue("description"), name, data)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.expression.create", row.ID.String(), map[string]any{"id": row.ID.String(), "cardId": id.String()})
	h.invalidate()
	httpx.WriteJSON(w, http.StatusCreated, toExpressionResponse(row))
}

// UpdateExpressionHandler 只更新表情标识、说明和排序。
func (h *Handler) UpdateExpressionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "表情 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req struct {
		Key         string `json:"key"`
		Description string `json:"description"`
		Sort        int32  `json:"sort"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	row, err := h.svc.UpdateExpression(r.Context(), id, req.Key, req.Description, req.Sort)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.expression.update", row.ID.String(), map[string]any{"id": row.ID.String()})
	h.invalidate()
	httpx.WriteJSON(w, http.StatusOK, toExpressionResponse(row))
}

// DeleteExpressionHandler 同时删除表情对应的媒体。
func (h *Handler) DeleteExpressionHandler(w http.ResponseWriter, r *http.Request) {
	id, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "表情 ID 非法"))
		return
	}
	if err := h.svc.DeleteExpression(r.Context(), id); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "bot.expression.delete", id.String(), map[string]any{"id": id.String()})
	h.invalidate()
	w.WriteHeader(http.StatusNoContent)
}
