package cursorpage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const checkpointSchemaVersion = 1

// Save 以“同目录临时文件 + fsync + rename”原子写入断点文件。
// 临时文件与目标文件放在同一目录，保证 rename 在同一文件系统上、原子生效。
func (f *FileStore) Save(ctx context.Context, cp Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cp.SchemaVersion = checkpointSchemaVersion
	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("cursorpage: marshal checkpoint: %w", err)
	}

	dir := filepath.Dir(f.path)
	tmp, err := os.CreateTemp(dir, ".checkpoint-*.tmp")
	if err != nil {
		return fmt.Errorf("cursorpage: create temp checkpoint: %w", err)
	}
	tmpName := tmp.Name()
	// 任何失败路径都清理临时文件，避免残留。
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("cursorpage: write checkpoint: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("cursorpage: fsync checkpoint: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("cursorpage: close checkpoint: %w", err)
	}
	if err := os.Rename(tmpName, f.path); err != nil {
		cleanup()
		return fmt.Errorf("cursorpage: rename checkpoint: %w", err)
	}

	// fsync 目录，确保 rename 元数据落盘（崩溃断电场景下更稳妥）。
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// Load 读取断点文件。文件不存在时返回 ok=false，调用方按全新任务启动。
// 文件存在但内容损坏时返回错误，避免在不可信状态上继续推进而漏数。
func (f *FileStore) Load(ctx context.Context) (cp Checkpoint, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, false, err
	}
	data, err := os.ReadFile(f.path)
	if errors.Is(err, fs.ErrNotExist) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, fmt.Errorf("cursorpage: read checkpoint: %w", err)
	}
	if len(data) == 0 {
		return Checkpoint{}, false, errors.New("cursorpage: checkpoint file is empty (torn write)")
	}
	if err := json.Unmarshal(data, &cp); err != nil {
		return Checkpoint{}, false, fmt.Errorf("cursorpage: parse checkpoint: %w", err)
	}
	if cp.SchemaVersion != checkpointSchemaVersion {
		return Checkpoint{}, false, fmt.Errorf("cursorpage: unsupported checkpoint schema %d", cp.SchemaVersion)
	}
	if cp.Delivered == nil {
		cp.Delivered = map[string]int64{}
	}
	return cp, true, nil
}
