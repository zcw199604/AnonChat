package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/anonchat/anonchat/backend/internal/external/wanfeng"
)

// The in-memory store is the client's source of bindings; this wrapper durably
// saves every update before exposing it, including the connected identityToken.
type stateStore struct {
	*wanfeng.MemoryBindingStore
	mu       sync.Mutex
	path     string
	bindings map[string]wanfeng.Binding
	lastErr  error
}

// Resolve existing ancestors as well as the filename: a not-yet-created child of
// a symlink into a repository must not bypass the repository restriction.
func resolvedPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	for {
		if _, err := os.Lstat(abs); err == nil {
			base, err := filepath.EvalSymlinks(abs)
			if err != nil {
				return "", err
			}
			for i := len(suffix) - 1; i >= 0; i-- {
				base = filepath.Join(base, suffix[i])
			}
			return base, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		suffix = append(suffix, filepath.Base(abs))
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", errors.New("无法解析状态路径")
		}
		abs = parent
	}
}

func openState(path string) (*stateStore, error) {
	if info, err := os.Lstat(path); err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
		return nil, errors.New("state 必须为普通文件，不能是符号链接")
	}
	path, err := resolvedPath(path)
	if err != nil {
		return nil, errors.New("无法解析 state 路径")
	}
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return nil, errors.New("拒绝仓库内的 state 路径")
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0700); err != nil {
		return nil, errors.New("无法创建私有状态目录")
	}
	info, err := os.Stat(parent)
	if err != nil || info.Mode().Perm() != 0700 {
		return nil, errors.New("state 所在目录权限必须为 0700；请使用独立私有目录")
	}
	s := &stateStore{MemoryBindingStore: wanfeng.NewMemoryBindingStore(), path: path, bindings: make(map[string]wanfeng.Binding)}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, errors.New("无法读取状态文件")
	}
	info, err = os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		return nil, errors.New("state 文件权限必须为 0600")
	}
	if err := json.Unmarshal(data, &s.bindings); err != nil || s.bindings == nil {
		return nil, errors.New("状态文件不是有效的绑定 JSON")
	}
	for id, b := range s.bindings {
		if id == "" || id != b.IdentityID {
			return nil, errors.New("状态文件绑定身份无效")
		}
		_ = s.MemoryBindingStore.Save(context.Background(), b)
	}
	return s, nil
}

func (s *stateStore) Save(ctx context.Context, b wanfeng.Binding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.IdentityID == "" {
		return errors.New("绑定缺少身份")
	}
	next := make(map[string]wanfeng.Binding, len(s.bindings)+1)
	for k, v := range s.bindings {
		next[k] = v
	}
	next[b.IdentityID] = b
	err := s.write(next)
	if err != nil {
		s.lastErr = errors.New("状态持久化失败")
		return s.lastErr
	}
	s.bindings = next
	return s.MemoryBindingStore.Save(ctx, b)
}

func (s *stateStore) write(bindings map[string]wanfeng.Binding) error {
	data, err := json.Marshal(bindings)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), s.path)
}

func (s *stateStore) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastErr
}
