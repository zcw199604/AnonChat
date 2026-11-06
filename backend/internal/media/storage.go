// Package media 提供后台媒体库（文件夹、图片/视频条目）与用户侧媒体文件访问（how.md §4）。
// 文件保存在 MEDIA_ROOT 下，路径为 <folder_id>/<item_id><ext>；先写同目录临时文件再 rename。
package media

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Storage 管理 MEDIA_ROOT 下的媒体文件。
type Storage struct {
	root string
}

// NewStorage 创建存储并确保根目录存在（启动时自动创建）。
func NewStorage(root string) (*Storage, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("媒体根目录不能为空")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("解析媒体根目录失败: %w", err)
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, fmt.Errorf("创建媒体根目录失败: %w", err)
	}
	return &Storage{root: abs}, nil
}

// Root 返回媒体根目录的绝对路径。
func (s *Storage) Root() string { return s.root }

// abs 把相对 key 拼成绝对路径；key 中的越界成分（.. 等）会被清理掉。
func (s *Storage) abs(key string) (string, error) {
	clean := strings.TrimPrefix(filepath.Clean("/"+key), "/")
	if clean == "" || clean == "." {
		return "", fmt.Errorf("非法的媒体路径: %q", key)
	}
	full := filepath.Join(s.root, clean)
	if full != s.root && !strings.HasPrefix(full, s.root+string(os.PathSeparator)) {
		return "", fmt.Errorf("非法的媒体路径: %q", key)
	}
	return full, nil
}

// CreateTemp 在 key 所在目录创建临时文件，写完后由 Commit 移动到位。
func (s *Storage) CreateTemp(key string) (*os.File, error) {
	full, err := s.abs(key)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建媒体目录失败: %w", err)
	}
	f, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}
	return f, nil
}

// Commit 把临时文件重命名到 key 指向的最终路径。
func (s *Storage) Commit(tmp *os.File, key string) error {
	full, err := s.abs(key)
	if err != nil {
		s.Discard(tmp)
		return err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(name, full); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("保存媒体文件失败: %w", err)
	}
	return nil
}

// Discard 删除临时文件（失败忽略）。
func (s *Storage) Discard(tmp *os.File) {
	name := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(name)
}

// Remove 删除 key 对应的文件；文件不存在视为成功。
func (s *Storage) Remove(key string) error {
	full, err := s.abs(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Open 打开 key 对应的文件。
func (s *Storage) Open(key string) (*os.File, error) {
	full, err := s.abs(key)
	if err != nil {
		return nil, err
	}
	return os.Open(full)
}

// WriteTemp 把 r 的内容写入临时文件，返回写入字节数；写入过程中超过 limit 时返回 ErrTooLarge。
func (s *Storage) WriteTemp(tmp *os.File, r io.Reader, limit int64) (int64, error) {
	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if err != nil {
		return n, fmt.Errorf("写入临时文件失败: %w", err)
	}
	if n > limit {
		return n, ErrTooLarge
	}
	return n, nil
}
