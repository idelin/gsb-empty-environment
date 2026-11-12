package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

var (
	// ErrMappingConflict 表示新映射与已发布映射对同一个旧令牌给出了不一致的新令牌。
	ErrMappingConflict = errors.New("netsec: mapping conflict during atomic publish")
)

// MappingStore 持久化旧代次令牌到新代次令牌的换代映射。
//
// 实现必须保证 Commit 是原子的：要么整批映射对后续 View 可见，
// 要么返回错误且已发布状态保持不变。存储中只允许出现令牌字符串，
// 调用方与实现都不得写入原始秘密。
type MappingStore interface {
	// View 在快照上执行只读访问；fn 内不得修改快照。
	View(ctx context.Context, fn func(mapping map[string]string) error) error
	// Commit 将一批映射合并发布；与既有映射冲突时返回 ErrMappingConflict，
	// 任何错误都不得产生部分可见的更新。
	Commit(ctx context.Context, batch map[string]string) error
}

// MemoryStore 是适用于单进程追踪宿主的内存映射存储，写时复制保证读快照稳定。
type MemoryStore struct {
	mu      sync.RWMutex
	current map[string]string
}

// NewMemoryStore 创建空内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{current: make(map[string]string)}
}

// View 实现 MappingStore。
func (s *MemoryStore) View(_ context.Context, fn func(mapping map[string]string) error) error {
	s.mu.RLock()
	snapshot := make(map[string]string, len(s.current))
	for oldToken, newToken := range s.current {
		snapshot[oldToken] = newToken
	}
	s.mu.RUnlock()
	return fn(snapshot)
}

// Commit 实现 MappingStore，合并在锁外完成、发布在锁内一次性切换。
func (s *MemoryStore) Commit(_ context.Context, batch map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	merged := make(map[string]string, len(s.current)+len(batch))
	for oldToken, newToken := range s.current {
		merged[oldToken] = newToken
	}
	for oldToken, newToken := range batch {
		if existing, ok := merged[oldToken]; ok && existing != newToken {
			return fmt.Errorf("%w: %s already mapped", ErrMappingConflict, oldToken)
		}
		merged[oldToken] = newToken
	}
	s.current = merged
	return nil
}

// fileStoreFormat 是文件存储的磁盘格式，仅包含令牌到令牌的映射。
type fileStoreFormat struct {
	Version int               `json:"version"`
	Mapping map[string]string `json:"mapping"`
}

// FileStore 通过"临时文件 + 原子 rename"实现跨进程安全的原子发布。
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore 打开（不存在则初始化）一个文件映射存储。
func NewFileStore(path string) (*FileStore, error) {
	store := &FileStore{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return store, nil
	}
	var onDisk fileStoreFormat
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return nil, fmt.Errorf("netsec: decode mapping store: %w", err)
	}
	if onDisk.Version != 1 {
		return nil, fmt.Errorf("netsec: unsupported mapping store version %d", onDisk.Version)
	}
	return store, nil
}

// View 实现 MappingStore，每次从磁盘读取最新已发布快照。
func (s *FileStore) View(_ context.Context, fn func(mapping map[string]string) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fn(map[string]string{})
		}
		return err
	}
	var onDisk fileStoreFormat
	if err := json.Unmarshal(data, &onDisk); err != nil {
		return fmt.Errorf("netsec: decode mapping store: %w", err)
	}
	snapshot := onDisk.Mapping
	if snapshot == nil {
		snapshot = map[string]string{}
	}
	return fn(snapshot)
}

// Commit 实现 MappingStore：先在内存中合并与冲突检查，再写临时文件并原子替换。
// rename 之前的任何失败都不会触碰已发布文件。
func (s *FileStore) Commit(ctx context.Context, batch map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	merged := make(map[string]string, len(batch))
	if data, err := os.ReadFile(s.path); err == nil {
		var onDisk fileStoreFormat
		if err := json.Unmarshal(data, &onDisk); err != nil {
			return fmt.Errorf("netsec: decode mapping store: %w", err)
		}
		for oldToken, newToken := range onDisk.Mapping {
			merged[oldToken] = newToken
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	for oldToken, newToken := range batch {
		if existing, ok := merged[oldToken]; ok && existing != newToken {
			return fmt.Errorf("%w: %s already mapped", ErrMappingConflict, oldToken)
		}
		merged[oldToken] = newToken
	}

	payload, err := json.MarshalIndent(fileStoreFormat{Version: 1, Mapping: merged}, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".mapping-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(payload); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	select {
	case <-ctx.Done():
		cleanup()
		return ctx.Err()
	default:
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		cleanup()
		return err
	}
	return nil
}

// FaultInjector 包装 MappingStore，用于验证换代流程在存储失败时的原子性。
// FailNextCommit 使下一次 Commit 在委托底层存储之前失败；View 始终透传。
type FaultInjector struct {
	inner       MappingStore
	mu          sync.Mutex
	failPending bool
	failErr     error
}

// NewFaultInjector 创建故障注入包装器。
func NewFaultInjector(inner MappingStore) *FaultInjector {
	return &FaultInjector{inner: inner, failErr: errors.New("netsec: injected storage failure")}
}

// FailNextCommit 安排下一次 Commit 返回注入错误且不触达底层存储。
func (f *FaultInjector) FailNextCommit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failPending = true
}

// View 实现 MappingStore。
func (f *FaultInjector) View(ctx context.Context, fn func(mapping map[string]string) error) error {
	return f.inner.View(ctx, fn)
}

// Commit 实现 MappingStore，有待注入故障时直接失败且不修改底层状态。
func (f *FaultInjector) Commit(ctx context.Context, batch map[string]string) error {
	f.mu.Lock()
	if f.failPending {
		f.failPending = false
		err := f.failErr
		f.mu.Unlock()
		return err
	}
	f.mu.Unlock()
	return f.inner.Commit(ctx, batch)
}
