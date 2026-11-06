package media

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/anonchat/anonchat/backend/internal/platform/httpx"
	"github.com/anonchat/anonchat/backend/internal/store"
)

// 校验上限（how.md §4.1）。
const (
	maxTagRunes     = 20
	maxTags         = 20
	maxCaptionRunes = 200
	maxNameRunes    = 40
	// maxFilesPerUpload 是单次上传允许的文件数（防止一次请求太多文件）。
	maxFilesPerUpload = 20
)

// ErrTooLarge 表示上传文件超过大小上限。
var ErrTooLarge = errors.New("上传文件超过大小上限")

// allowedMIME 是允许的媒体类型：扩展名与类别由嗅探到的 MIME 决定。
var allowedMIME = map[string]struct {
	ext  string
	kind string
}{
	"image/jpeg": {".jpg", "image"},
	"image/png":  {".png", "image"},
	"image/gif":  {".gif", "image"},
	"image/webp": {".webp", "image"},
	"video/mp4":  {".mp4", "video"},
	"video/webm": {".webm", "video"},
}

// Service 提供媒体文件夹与媒体条目的增删改查，以及磁盘文件的管理。
type Service struct {
	q              *store.Queries
	storage        *Storage
	logger         *slog.Logger
	maxUploadBytes int64
}

// New 创建媒体服务。maxUploadMB 是单个文件的大小上限（MB）。
func New(pool *pgxpool.Pool, logger *slog.Logger, storage *Storage, maxUploadMB int) *Service {
	if maxUploadMB <= 0 {
		maxUploadMB = 100
	}
	return &Service{
		q:              store.New(pool),
		storage:        storage,
		logger:         logger,
		maxUploadBytes: int64(maxUploadMB) << 20,
	}
}

// Storage 返回底层存储。
func (s *Service) Storage() *Storage { return s.storage }

// MaxUploadBytes 返回单个文件的大小上限（字节）。
func (s *Service) MaxUploadBytes() int64 { return s.maxUploadBytes }

// ---- 文件夹 ----

// ListFolders 返回全部文件夹（带条目数）。
func (s *Service) ListFolders(ctx context.Context) ([]store.ListMediaFoldersRow, error) {
	return s.q.ListMediaFolders(ctx)
}

// GetFolder 返回单个文件夹。
func (s *Service) GetFolder(ctx context.Context, id pgtype.UUID) (store.MediaFolder, error) {
	return s.q.GetMediaFolder(ctx, id)
}

// CreateFolder 新建文件夹。
func (s *Service) CreateFolder(ctx context.Context, name string) (store.MediaFolder, error) {
	if err := validateName(name); err != nil {
		return store.MediaFolder{}, err
	}
	folder, err := s.q.InsertMediaFolder(ctx, name)
	if err != nil {
		return store.MediaFolder{}, duplicateNameError(err)
	}
	return folder, nil
}

// RenameFolder 重命名文件夹。
func (s *Service) RenameFolder(ctx context.Context, id pgtype.UUID, name string) (store.MediaFolder, error) {
	if err := validateName(name); err != nil {
		return store.MediaFolder{}, err
	}
	folder, err := s.q.RenameMediaFolder(ctx, store.RenameMediaFolderParams{ID: id, Name: name})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.MediaFolder{}, ErrFolderNotFound
		}
		return store.MediaFolder{}, duplicateNameError(err)
	}
	return folder, nil
}

// DeleteFolder 删除文件夹、其全部媒体行和磁盘文件。
func (s *Service) DeleteFolder(ctx context.Context, id pgtype.UUID) error {
	// 先取磁盘路径，行删除后（外键级联）就查不到了。
	keys, err := s.q.ListFolderStorageKeys(ctx, id)
	if err != nil {
		return err
	}
	affected, err := s.q.DeleteMediaFolder(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrFolderNotFound
	}
	for _, key := range keys {
		if rerr := s.storage.Remove(key); rerr != nil {
			// 磁盘清理失败不影响业务，只记录日志。
			s.logger.Warn("删除媒体文件失败", "storage_key", key, "error", rerr)
		}
	}
	return nil
}

// ---- 媒体条目 ----

// ListItems 返回文件夹内的媒体条目。
func (s *Service) ListItems(ctx context.Context, folderID pgtype.UUID) ([]store.MediaItem, error) {
	if _, err := s.q.GetMediaFolder(ctx, folderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFolderNotFound
		}
		return nil, err
	}
	return s.q.ListMediaItemsByFolder(ctx, folderID)
}

// GetItem 返回单个媒体条目。
func (s *Service) GetItem(ctx context.Context, id pgtype.UUID) (store.MediaItem, error) {
	item, err := s.q.GetMediaItem(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.MediaItem{}, ErrItemNotFound
		}
		return store.MediaItem{}, err
	}
	return item, nil
}

// UpdateItem 修改媒体条目的标签与描述。
func (s *Service) UpdateItem(ctx context.Context, id pgtype.UUID, tags []string, caption string) (store.MediaItem, error) {
	normalized, err := normalizeTags(tags)
	if err != nil {
		return store.MediaItem{}, err
	}
	capt := strings.TrimSpace(caption)
	if utf8.RuneCountInString(capt) > maxCaptionRunes {
		return store.MediaItem{}, httpx.NewError(httpx.CodeInvalidRequest, "描述最多 200 字")
	}
	item, err := s.q.UpdateMediaItem(ctx, store.UpdateMediaItemParams{ID: id, Tags: normalized, Caption: capt})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.MediaItem{}, ErrItemNotFound
		}
		return store.MediaItem{}, err
	}
	return item, nil
}

// DeleteItem 删除媒体行和磁盘文件（引用它的消息 media_id 由外键置空）。
func (s *Service) DeleteItem(ctx context.Context, id pgtype.UUID) error {
	item, err := s.GetItem(ctx, id)
	if err != nil {
		return err
	}
	affected, err := s.q.DeleteMediaItem(ctx, id)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrItemNotFound
	}
	if rerr := s.storage.Remove(item.StorageKey); rerr != nil {
		s.logger.Warn("删除媒体文件失败", "storage_key", item.StorageKey, "error", rerr)
	}
	return nil
}

// SaveItem 保存一个上传文件：先写临时文件，再按嗅探到的类型确定扩展名并 rename，
// 最后写数据库；数据库写入失败时删除文件（how.md §4.1）。
func (s *Service) SaveItem(ctx context.Context, folderID pgtype.UUID, originalName string, r io.Reader, tags []string, caption string) (store.MediaItem, error) {
	normalized, err := normalizeTags(tags)
	if err != nil {
		return store.MediaItem{}, err
	}
	capt := strings.TrimSpace(caption)
	if utf8.RuneCountInString(capt) > maxCaptionRunes {
		return store.MediaItem{}, httpx.NewError(httpx.CodeInvalidRequest, "描述最多 200 字")
	}
	if _, err := s.q.GetMediaFolder(ctx, folderID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.MediaItem{}, ErrFolderNotFound
		}
		return store.MediaItem{}, err
	}
	itemID, err := genUUID()
	if err != nil {
		return store.MediaItem{}, err
	}
	// 临时文件与最终文件同目录，rename 才能原子完成。
	tmpKey := folderID.String() + "/" + itemID.String()
	tmp, err := s.storage.CreateTemp(tmpKey)
	if err != nil {
		return store.MediaItem{}, err
	}
	size, err := s.storage.WriteTemp(tmp, r, s.maxUploadBytes)
	if err != nil {
		s.storage.Discard(tmp)
		if errors.Is(err, ErrTooLarge) {
			return store.MediaItem{}, httpx.NewError(httpx.CodeMediaRejected, "文件超过大小上限")
		}
		return store.MediaItem{}, err
	}

	// 只信任内容嗅探：读取临时文件的前 512 字节判断类型，忽略文件名与客户端声明。
	mime, err := sniffMIME(tmp.Name())
	if err != nil {
		s.storage.Discard(tmp)
		return store.MediaItem{}, err
	}
	info, ok := allowedMIME[mime]
	if !ok {
		s.storage.Discard(tmp)
		return store.MediaItem{}, httpx.NewError(httpx.CodeMediaRejected, "只支持 jpg/png/gif/webp 图片和 mp4/webm 视频")
	}

	storageKey := folderID.String() + "/" + itemID.String() + info.ext
	if err := s.storage.Commit(tmp, storageKey); err != nil {
		return store.MediaItem{}, err
	}

	item, err := s.q.InsertMediaItem(ctx, store.InsertMediaItemParams{
		ID:           itemID,
		FolderID:     folderID,
		Kind:         info.kind,
		Mime:         mime,
		SizeBytes:    size,
		StorageKey:   storageKey,
		OriginalName: truncateRunes(strings.TrimSpace(originalName), maxNameRunes*4),
		Tags:         normalized,
		Caption:      capt,
	})
	if err != nil {
		// 数据库写入失败：删掉刚保存的文件，避免产生孤儿文件。
		if rerr := s.storage.Remove(storageKey); rerr != nil {
			s.logger.Warn("回滚媒体文件失败", "storage_key", storageKey, "error", rerr)
		}
		return store.MediaItem{}, err
	}
	return item, nil
}

// CanIdentityAccess 判断该身份能否读取某个媒体：只有出现在它所属会话的未焚毁消息里才放行。
func (s *Service) CanIdentityAccess(ctx context.Context, mediaID, identityID pgtype.UUID) (bool, error) {
	return s.q.CanIdentityAccessMedia(ctx, store.CanIdentityAccessMediaParams{
		MediaID:    mediaID,
		IdentityID: identityID,
	})
}

// ---- 内部工具 ----

// ErrFolderNotFound 表示文件夹不存在。
var ErrFolderNotFound = httpx.NewError(httpx.CodeNotFound, "文件夹不存在")

// ErrItemNotFound 表示媒体条目不存在。
var ErrItemNotFound = httpx.NewError(httpx.CodeNotFound, "媒体不存在")

// validateName 校验文件夹名（1–40 字）。
func validateName(name string) error {
	n := strings.TrimSpace(name)
	if n == "" || utf8.RuneCountInString(n) > maxNameRunes {
		return httpx.NewError(httpx.CodeInvalidRequest, "文件夹名长度需为 1–40 字")
	}
	return nil
}

// duplicateNameError 把唯一约束冲突转成 invalid_request，其余错误原样返回。
func duplicateNameError(err error) error {
	if strings.Contains(err.Error(), "media_folders_name_key") {
		return httpx.NewError(httpx.CodeInvalidRequest, "文件夹名已存在")
	}
	return err
}

// normalizeTags 规范化标签：去掉首尾空白、去重，每个 1–20 字，最多 20 个。
func normalizeTags(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, t := range raw {
		tag := strings.TrimSpace(t)
		if tag == "" {
			continue
		}
		if utf8.RuneCountInString(tag) > maxTagRunes {
			return nil, httpx.NewError(httpx.CodeInvalidRequest, "每个标签最多 20 字")
		}
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	if len(out) > maxTags {
		return nil, httpx.NewError(httpx.CodeInvalidRequest, "标签最多 20 个")
	}
	return out, nil
}

// ParseTags 把逗号分隔的标签字符串（支持半角与全角逗号）拆成切片。
func ParseTags(raw string) []string {
	return strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '，' })
}

// sniffMIME 用 http.DetectContentType 嗅探文件前 512 字节，得到真实 MIME。
func sniffMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取上传文件失败: %w", err)
	}
	return http.DetectContentType(buf[:n]), nil
}

// truncateRunes 按字符截断字符串。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// genUUID 生成一个随机 UUID（媒体条目 ID，同时用作存储路径的一部分）。
func genUUID() (pgtype.UUID, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: b, Valid: true}, nil
}
