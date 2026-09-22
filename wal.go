package twopc

import (
	"encoding/json"
	"os"
	"sync"
)

// WAL 记录类型。协调者与参与者各自持有独立的 WAL 文件。
const (
	RBegin    = "CBegin"    // 协调者：事务开始（记录参与者集合）
	RDecision = "CDecision" // 协调者：持久决策记录（COMMIT/ABORT），先落盘再发消息
	RPromise  = "PPromise"  // 参与者：prepare 承诺，资源已加锁
	RDone     = "PDone"     // 参与者：决策已执行，锁已释放（含中止墓碑，防迟到 prepare 重新占锁）
)

// Record 是一条追加到 WAL 的日志记录。
type Record struct {
	Kind     string   `json:"kind"`
	TxID     string   `json:"txid"`
	Parts    []string `json:"parts,omitempty"`
	Decision string   `json:"decision,omitempty"` // COMMIT / ABORT
	Outcome  string   `json:"outcome,omitempty"`  // committed / aborted
}

// WAL 是仅追加的持久日志：每条记录写入后立即 fsync，
// 崩溃后内存状态全部丢失，仅靠 WAL 重建。
type WAL struct {
	mu      sync.Mutex
	f       *os.File
	records []Record
}

func OpenWAL(path string) (*WAL, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	w := &WAL{f: f}
	if _, err := f.Seek(0, 0); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(f)
	for {
		var r Record
		if err := dec.Decode(&r); err != nil {
			break // EOF 或尾部半条记录（崩溃截断），均停止重放
		}
		w.records = append(w.records, r)
	}
	return w, nil
}

// Append 追加一条记录并 fsync，返回后记录即持久。
func (w *WAL) Append(r Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if _, err := w.f.Write(append(b, '\n')); err != nil {
		return err
	}
	if err := w.f.Sync(); err != nil {
		return err
	}
	w.records = append(w.records, r)
	return nil
}

func (w *WAL) Records() []Record {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]Record, len(w.records))
	copy(out, w.records)
	return out
}
