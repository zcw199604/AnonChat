package media

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anonchat/anonchat/backend/internal/admin"
	"github.com/anonchat/anonchat/backend/internal/identity"
	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// maxBodyBytes 是 JSON 请求体的上限（上传走 multipart，单独限制）。
const maxBodyBytes = 64 << 10

// uploadMemoryBuffer 是 multipart 解析时保留在内存里的上限，超出部分落到临时文件。
const uploadMemoryBuffer = 8 << 20

// Handler 暴露媒体库的管理接口与用户侧媒体文件访问。
type Handler struct {
	svc *Service
	adm *admin.Service
}

// NewHandler 创建媒体 HTTP 处理器。adm 用于写操作审计日志，可以为 nil。
func NewHandler(svc *Service, adm *admin.Service) *Handler {
	return &Handler{svc: svc, adm: adm}
}

// folderResponse 是文件夹的对外表示。
type folderResponse struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ItemCount int64  `json:"itemCount"`
	CreatedAt string `json:"createdAt"`
}

// itemResponse 是媒体条目的对外表示。
type itemResponse struct {
	ID           string   `json:"id"`
	FolderID     string   `json:"folderId"`
	Kind         string   `json:"kind"`
	Mime         string   `json:"mime"`
	SizeBytes    int64    `json:"sizeBytes"`
	OriginalName string   `json:"originalName"`
	Tags         []string `json:"tags"`
	Caption      string   `json:"caption"`
	FileURL      string   `json:"fileUrl"`
	CreatedAt    string   `json:"createdAt"`
}

// ---- 管理后台：文件夹 ----

// ListFoldersHandler 处理 GET /api/admin/media-folders。
func (h *Handler) ListFoldersHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.ListFolders(r.Context())
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	out := make([]folderResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, folderResponse{
			ID:        row.ID.String(),
			Name:      row.Name,
			ItemCount: row.ItemCount,
			CreatedAt: formatTS(row.CreatedAt),
		})
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

type folderNameRequest struct {
	Name string `json:"name"`
}

// CreateFolderHandler 处理 POST /api/admin/media-folders。
func (h *Handler) CreateFolderHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req folderNameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	folder, err := h.svc.CreateFolder(r.Context(), req.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "media.folder.create", folder.ID.String(), map[string]any{"name": folder.Name})
	httpx.WriteJSON(w, http.StatusCreated, folderResponse{
		ID: folder.ID.String(), Name: folder.Name, CreatedAt: formatTS(folder.CreatedAt),
	})
}

// RenameFolderHandler 处理 PATCH /api/admin/media-folders/{id}。
func (h *Handler) RenameFolderHandler(w http.ResponseWriter, r *http.Request) {
	folderID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "文件夹 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req folderNameRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	folder, err := h.svc.RenameFolder(r.Context(), folderID, req.Name)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "media.folder.rename", folder.ID.String(), map[string]any{"name": folder.Name})
	httpx.WriteJSON(w, http.StatusOK, folderResponse{
		ID: folder.ID.String(), Name: folder.Name, CreatedAt: formatTS(folder.CreatedAt),
	})
}

// DeleteFolderHandler 处理 DELETE /api/admin/media-folders/{id}：删除文件夹、媒体行与磁盘文件。
func (h *Handler) DeleteFolderHandler(w http.ResponseWriter, r *http.Request) {
	folderID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "文件夹 ID 非法"))
		return
	}
	if err := h.svc.DeleteFolder(r.Context(), folderID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "media.folder.delete", folderID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 管理后台：媒体条目 ----

// ListItemsHandler 处理 GET /api/admin/media-folders/{id}/items。
func (h *Handler) ListItemsHandler(w http.ResponseWriter, r *http.Request) {
	folderID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "文件夹 ID 非法"))
		return
	}
	items, err := h.svc.ListItems(r.Context(), folderID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	out := make([]itemResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toItemResponse(item))
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// UploadItemsHandler 处理 POST /api/admin/media-folders/{id}/items：
// multipart 的 files（可多个）、tags（逗号分隔，应用到这批所有文件）、caption。
func (h *Handler) UploadItemsHandler(w http.ResponseWriter, r *http.Request) {
	folderID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "文件夹 ID 非法"))
		return
	}
	// 请求体总上限为单文件上限的 10 倍（how.md §4.2）。
	r.Body = http.MaxBytesReader(w, r.Body, h.svc.MaxUploadBytes()*10)
	if err := r.ParseMultipartForm(uploadMemoryBuffer); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, httpx.NewError(httpx.CodeMediaRejected, "上传内容超过大小上限"))
			return
		}
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "请求体格式错误"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	files := r.MultipartForm.File["files"]
	if len(files) == 0 {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "没有上传文件"))
		return
	}
	if len(files) > maxFilesPerUpload {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "一次最多上传 20 个文件"))
		return
	}
	tags := ParseTags(r.FormValue("tags"))
	caption := r.FormValue("caption")

	out := make([]itemResponse, 0, len(files))
	for _, fh := range files {
		f, oerr := fh.Open()
		if oerr != nil {
			httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "读取上传文件失败"))
			return
		}
		item, serr := h.svc.SaveItem(r.Context(), folderID, fh.Filename, f, tags, caption)
		_ = f.Close()
		if serr != nil {
			httpx.WriteError(w, serr)
			return
		}
		out = append(out, toItemResponse(item))
	}
	h.audit(r, "media.upload", folderID.String(), map[string]any{"count": len(out)})
	httpx.WriteJSON(w, http.StatusCreated, out)
}

type updateItemRequest struct {
	Tags    []string `json:"tags"`
	Caption string   `json:"caption"`
}

// UpdateItemHandler 处理 PATCH /api/admin/media-items/{id}。
func (h *Handler) UpdateItemHandler(w http.ResponseWriter, r *http.Request) {
	itemID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "媒体 ID 非法"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var req updateItemRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.WriteError(w, err)
		return
	}
	item, err := h.svc.UpdateItem(r.Context(), itemID, req.Tags, req.Caption)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "media.item.update", item.ID.String(), map[string]any{"tags": item.Tags})
	httpx.WriteJSON(w, http.StatusOK, toItemResponse(item))
}

// DeleteItemHandler 处理 DELETE /api/admin/media-items/{id}：删除行与磁盘文件。
func (h *Handler) DeleteItemHandler(w http.ResponseWriter, r *http.Request) {
	itemID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, httpx.NewError(httpx.CodeInvalidRequest, "媒体 ID 非法"))
		return
	}
	if err := h.svc.DeleteItem(r.Context(), itemID); err != nil {
		httpx.WriteError(w, err)
		return
	}
	h.audit(r, "media.item.delete", itemID.String(), nil)
	w.WriteHeader(http.StatusNoContent)
}

// AdminFileHandler 处理 GET /api/admin/media-items/{id}/file：后台预览。
func (h *Handler) AdminFileHandler(w http.ResponseWriter, r *http.Request) {
	itemID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, ErrItemNotFound)
		return
	}
	h.serveItem(w, r, itemID)
}

// ---- 用户侧 ----

// ServeFileHandler 处理 GET /api/media/{id}。
// 只有当媒体出现在请求者所属会话的未焚毁消息中时才放行；不存在与无权限都返回 404。
func (h *Handler) ServeFileHandler(w http.ResponseWriter, r *http.Request) {
	ident, ok := identity.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, identity.ErrUnauthorized)
		return
	}
	itemID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		httpx.WriteError(w, ErrItemNotFound)
		return
	}
	allowed, err := h.svc.CanIdentityAccess(r.Context(), itemID, ident.ID)
	if err != nil {
		httpx.WriteError(w, internalErr())
		return
	}
	if !allowed {
		httpx.WriteError(w, ErrItemNotFound)
		return
	}
	h.serveItem(w, r, itemID)
}

// serveItem 输出媒体文件内容：支持 Range（视频拖动进度条），并允许客户端缓存一天。
func (h *Handler) serveItem(w http.ResponseWriter, r *http.Request, itemID pgtype.UUID) {
	item, err := h.svc.GetItem(r.Context(), itemID)
	if err != nil {
		httpx.WriteError(w, err)
		return
	}
	f, err := h.svc.Storage().Open(item.StorageKey)
	if err != nil {
		httpx.WriteError(w, ErrItemNotFound)
		return
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil || stat.IsDir() {
		httpx.WriteError(w, ErrItemNotFound)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("Content-Type", item.Mime)
	http.ServeContent(w, r, item.OriginalName, stat.ModTime(), f)
}

// ---- 内部工具 ----

// audit 写一条管理员操作审计日志。
func (h *Handler) audit(r *http.Request, action, target string, detail map[string]any) {
	adminID, ok := admin.AdminIDFromContext(r.Context())
	if !ok || h.adm == nil {
		return
	}
	h.adm.LogAudit(r.Context(), adminID, action, target, detail)
}

// toItemResponse 构造媒体条目的响应。
func toItemResponse(item store.MediaItem) itemResponse {
	tags := item.Tags
	if tags == nil {
		tags = []string{}
	}
	return itemResponse{
		ID:           item.ID.String(),
		FolderID:     item.FolderID.String(),
		Kind:         item.Kind,
		Mime:         item.Mime,
		SizeBytes:    item.SizeBytes,
		OriginalName: item.OriginalName,
		Tags:         tags,
		Caption:      item.Caption,
		FileURL:      "/api/admin/media-items/" + item.ID.String() + "/file",
		CreatedAt:    formatTS(item.CreatedAt),
	}
}

// internalErr 构造 internal 错误（不泄漏底层细节）。
func internalErr() error { return httpx.NewError(httpx.CodeInternal, "服务器内部错误") }

// parseUUID 解析 UUID 字符串。
func parseUUID(s string) (pgtype.UUID, error) {
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		return pgtype.UUID{}, err
	}
	if !u.Valid {
		return pgtype.UUID{}, errors.New("UUID 为空")
	}
	return u, nil
}

// formatTS 把时间戳格式化为 RFC3339。
func formatTS(ts pgtype.Timestamptz) string {
	if !ts.Valid {
		return ""
	}
	return ts.Time.UTC().Format(time.RFC3339)
}
