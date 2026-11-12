package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// FileStore 把永久记录以每键一个 JSON 文件的形式保存在目录中。
//
// 写入采用临时文件 + fsync + 原子 rename + 目录 fsync 的崩溃安全流程；
// 任何阶段失败都不会让 Load 观察到半写记录（临时文件对 Load 不可见）。
// Hook 字段用于测试在各阶段注入中断（错误、取消或模拟崩溃）。
type FileStore struct {
	dir  string
	perm os.FileMode
	// Hook 在每个写入阶段被调用；返回非 nil 错误时该阶段中断、写入失败。
	// 阶段取值：StageTempCreate、StageTempWrite、StageTempSync、
	// StageRename、StageDirSync。
	Hook func(ctx context.Context, stage string, key string) error
}

// FileStore 写入阶段标识。
const (
	StageTempCreate = "temp_create"
	StageTempWrite  = "temp_write"
	StageTempSync   = "temp_sync"
	StageRename     = "rename"
	StageDirSync    = "dir_sync"
)

// NewFileStore 打开（必要时创建）持久化目录。
func NewFileStore(dir string) (*FileStore, error) {
	if dir == "" {
		return nil, errors.New("netsec: FileStore directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("netsec: create store dir: %w", err)
	}
	return &FileStore{dir: dir, perm: 0o600}, nil
}

func (s *FileStore) path(key string) string {
	return filepath.Join(s.dir, "rec-"+key+".json")
}

func (s *FileStore) tempPath(key string) (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, ".rec-"+key+".tmp-"+hex.EncodeToString(b[:])), nil
}

func (s *FileStore) hook(ctx context.Context, stage, key string) error {
	if s.Hook == nil {
		return nil
	}
	return s.Hook(ctx, stage, key)
}

// Load 读取并解码持久化记录；临时文件与其他非记录文件一律忽略。
func (s *FileStore) Load(ctx context.Context, key string) (Record, error) {
	if err := ctx.Err(); err != nil {
		return Record{}, err
	}
	data, err := os.ReadFile(s.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, fmt.Errorf("%w: %s", ErrNotFound, key)
	}
	if err != nil {
		return Record{}, err
	}
	var rec Record
	if err := json.Unmarshal(data, &rec); err != nil {
		return Record{}, fmt.Errorf("netsec: corrupt record %s: %w", key, err)
	}
	// 记录内容必须与请求的键一致，防止文件被改名复用。
	if Key(rec.DeviceID, rec.Field, rec.ParamVersion) != key {
		return Record{}, fmt.Errorf("netsec: corrupt record %s: embedded key mismatch", key)
	}
	return rec, nil
}

// Commit 原子写入记录，中断点之前的失败只留下对 Load 不可见的临时文件。
func (s *FileStore) Commit(ctx context.Context, key string, rec Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if Key(rec.DeviceID, rec.Field, rec.ParamVersion) != key {
		return errors.New("netsec: record does not match storage key")
	}

	// 已存在最终文件时保持幂等，绝不覆盖既有永久记忆。
	if _, err := os.Stat(s.path(key)); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	tmp, err := s.tempPath(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, s.perm)
	if err != nil {
		return err
	}
	if err := s.hook(ctx, StageTempCreate, key); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := s.hook(ctx, StageTempWrite, key); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := s.hook(ctx, StageTempSync, key); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}

	if err := s.hook(ctx, StageRename, key); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, s.path(key)); err != nil {
		os.Remove(tmp)
		return err
	}

	if err := s.hook(ctx, StageDirSync, key); err != nil {
		// rename 已完成，记录事实上已持久化；目录 fsync 失败只影响掉电耐久性。
		return err
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return err
	}
	err = dir.Sync()
	dir.Close()
	return err
}
