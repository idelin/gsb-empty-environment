package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// checkpoint 绑定：输入段摘要、各段游标（字节偏移）、
// 已发布（flush+fsync 完成）输出前缀的长度与摘要。
// 只有写进检查点的状态才可被信任；未同步的缓冲一律作废。
type checkpoint struct {
	Version int       `json:"version"`
	Runs    []runInfo `json:"runs"`
	Cursors []int64   `json:"cursors"` // 每个运行段下一条未读记录的字节偏移
	Emitted uint64    `json:"emitted"` // 已写入输出的记录数
	OutPath string    `json:"outPath"`
	OutLen  int64     `json:"outLen"`    // 已发布输出前缀长度
	OutSHA  string    `json:"outSha256"` // 已发布输出前缀的 SHA-256
}

func checkpointPath(dir string) string {
	return filepath.Join(dir, "checkpoint.json")
}

func initialCheckpoint(runs []runInfo, outPath string) *checkpoint {
	empty := sha256.Sum256(nil)
	return &checkpoint{
		Version: 1,
		Runs:    runs,
		Cursors: make([]int64, len(runs)),
		OutPath: outPath,
		OutSHA:  hex.EncodeToString(empty[:]),
	}
}

// saveCheckpoint 原子提交：写临时文件 -> fsync -> rename -> fsync 目录。
func saveCheckpoint(dir string, ck *checkpoint) error {
	data, err := json.MarshalIndent(ck, "", "  ")
	if err != nil {
		return err
	}
	tmp := checkpointPath(dir) + ".tmp"
	f, err := openTracked(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		closeTracked(f)
		return err
	}
	if err := f.Sync(); err != nil {
		closeTracked(f)
		return err
	}
	if err := closeTracked(f); err != nil {
		return err
	}
	if err := os.Rename(tmp, checkpointPath(dir)); err != nil {
		return err
	}
	d, err := openTracked(dir, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer closeTracked(d)
	return d.Sync()
}

func loadCheckpoint(dir string) (*checkpoint, error) {
	data, err := os.ReadFile(checkpointPath(dir))
	if err != nil {
		return nil, err
	}
	var ck checkpoint
	if err := json.Unmarshal(data, &ck); err != nil {
		return nil, err
	}
	return &ck, nil
}

func checkpointExists(dir string) bool {
	_, err := os.Stat(checkpointPath(dir))
	return err == nil
}
