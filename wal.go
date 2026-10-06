package logalloc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// WAL 记录类型：先预留(reserve)，再决定(commit/skip)，发布推进写切点(checkpoint)。
type recordType string

const (
	recReserve    recordType = "reserve"
	recCommit     recordType = "commit"
	recSkip       recordType = "skip"
	recCheckpoint recordType = "checkpoint"
)

type walRecord struct {
	Type recordType `json:"type"`
	Seq  uint64     `json:"seq"`
	Data []byte     `json:"data,omitempty"`
}

// wal 是仅追加的持久化日志，每条记录写入后立即 fsync。
type wal struct {
	f *os.File
}

func openWAL(dir string) (*wal, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "logalloc.wal"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &wal{f: f}, nil
}

func (w *wal) append(rec walRecord) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.f.Sync()
}

func (w *wal) close() error { return w.f.Close() }

// replay 逐条读出 WAL 记录，交给 apply 回调重建内存状态。
func replayWAL(dir string, apply func(walRecord) error) error {
	f, err := os.Open(filepath.Join(dir, "logalloc.wal"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()

	r := bufio.NewReaderSize(f, 1<<20)
	for line := 1; ; line++ {
		b, err := r.ReadBytes('\n')
		if len(b) > 0 {
			var rec walRecord
			if jerr := json.Unmarshal(b, &rec); jerr != nil {
				return fmt.Errorf("wal: 第 %d 行损坏: %v", line, jerr)
			}
			if aerr := apply(rec); aerr != nil {
				return fmt.Errorf("wal: 第 %d 行: %v", line, aerr)
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
