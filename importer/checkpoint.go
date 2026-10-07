package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Checkpoint 记录一次有效恢复所需的全部状态：
//   - Offset:       源文件中已提交到的记录边界字节偏移
//   - PrefixDigest: 源文件 [0, Offset) 前缀内容的摘要
//   - Batch:        目标端已确认提交的批号
//
// 三者缺一不可：只保存偏移不算有效恢复。
type Checkpoint struct {
	Offset       int64  `json:"offset"`
	PrefixDigest string `json:"prefixDigest"`
	Batch        int64  `json:"batch"`
}

// ErrInvalidCheckpoint 表示检查点缺少有效恢复所需的字段。
var ErrInvalidCheckpoint = errors.New("importer: invalid checkpoint")

// Validate 校验检查点完整性。仅含偏移（缺摘要或批号）视为无效。
func (c Checkpoint) Validate() error {
	if c.Offset < 0 {
		return fmt.Errorf("%w: negative offset %d", ErrInvalidCheckpoint, c.Offset)
	}
	if c.PrefixDigest == "" {
		return fmt.Errorf("%w: missing prefix digest (offset-only checkpoint is not resumable)", ErrInvalidCheckpoint)
	}
	if c.Batch < 0 {
		return fmt.Errorf("%w: negative batch %d", ErrInvalidCheckpoint, c.Batch)
	}
	return nil
}

// CheckpointStore 负责检查点的持久化，写入采用临时文件 + rename 保证原子性。
type CheckpointStore struct {
	path string
}

func NewCheckpointStore(path string) *CheckpointStore {
	return &CheckpointStore{path: path}
}

// Load 读取并校验检查点。文件不存在时返回 (nil, nil) 表示全新导入。
func (s *CheckpointStore) Load() (*Checkpoint, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCheckpoint, err)
	}
	if err := cp.Validate(); err != nil {
		return nil, err
	}
	return &cp, nil
}

// Save 原子写入检查点。
func (s *CheckpointStore) Save(cp Checkpoint) error {
	if err := cp.Validate(); err != nil {
		return err
	}
	data, err := json.Marshal(cp)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return err
	}
	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
