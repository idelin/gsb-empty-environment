package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrNoState 表示存储中没有任何发布状态（全新部署）。
var ErrNoState = errors.New("release: no persisted state")

// Store 持久化发布状态。Save 必须原子生效，Load 返回深拷贝。
type Store interface {
	Save(ctx context.Context, st *State) error
	Load(ctx context.Context) (*State, error)
}

// MemStore 是进程内存储，适用于测试与单次运行场景。
type MemStore struct {
	mu sync.Mutex
	st *State
}

// NewMemStore 创建空的内存存储。
func NewMemStore() *MemStore { return &MemStore{} }

// Save 实现 Store。
func (m *MemStore) Save(_ context.Context, st *State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.st = st.clone()
	return nil
}

// Load 实现 Store。
func (m *MemStore) Load(_ context.Context) (*State, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.st == nil {
		return nil, ErrNoState
	}
	return m.st.clone(), nil
}

// FileStore 以 JSON 文件持久化状态，写入通过临时文件 + rename 保证原子性，
// 适用于控制器进程重启后的状态恢复。
type FileStore struct {
	Path string
}

// NewFileStore 创建文件存储，path 为状态文件路径。
func NewFileStore(path string) *FileStore { return &FileStore{Path: path} }

// Save 实现 Store。
func (f *FileStore) Save(_ context.Context, st *State) error {
	data, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("release: marshal state: %w", err)
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("release: write state: %w", err)
	}
	if err := os.Rename(tmp, f.Path); err != nil {
		return fmt.Errorf("release: commit state: %w", err)
	}
	return nil
}

// Load 实现 Store。
func (f *FileStore) Load(_ context.Context) (*State, error) {
	data, err := os.ReadFile(filepath.Clean(f.Path))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoState
	}
	if err != nil {
		return nil, fmt.Errorf("release: read state: %w", err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("release: decode state: %w", err)
	}
	return &st, nil
}
