package aggregator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ErrNoCheckpoint 表示存储中还没有任何检查点（首次启动）。
var ErrNoCheckpoint = errors.New("aggregator: no checkpoint found")

// Store 持久化检查点。Save 必须原子生效：
// 要么完整写入新检查点，要么保留旧检查点，不允许出现半写状态。
type Store interface {
	Save(cp Checkpoint) error
	Load() (Checkpoint, error)
}

// FileStore 通过“写临时文件 + rename”保证原子性。
type FileStore struct {
	path string
}

func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

func (s *FileStore) Save(cp Checkpoint) error {
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *FileStore) Load() (Checkpoint, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, ErrNoCheckpoint
	}
	if err != nil {
		return Checkpoint{}, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, err
	}
	if cp.Sums == nil {
		cp.Sums = make(map[string]int64)
	}
	if cp.Emitted == nil {
		cp.Emitted = make(map[string]int64)
	}
	return cp, nil
}

// 确保 FileStore 的路径目录存在。
func (s *FileStore) MkdirAll() error {
	return os.MkdirAll(filepath.Dir(s.path), 0o755)
}
