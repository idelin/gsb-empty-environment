package importer

import (
	"errors"
	"fmt"
	"sync"
)

// BatchCommit 是提交给目标端的一个批次，携带源侧定位信息，
// 使目标端能够支持恢复时的对账（回执丢失场景）。
type BatchCommit struct {
	Batch        int64    // 单调递增批号，从 1 开始
	StartOffset  int64    // 本批第一条记录在源文件中的起始偏移
	EndOffset    int64    // 本批最后一条记录结束偏移（记录边界）
	PrefixDigest string   // 源文件 [0, EndOffset) 的摘要
	Records      [][]byte // 记录内容（不含分隔符）
}

// BatchMeta 是目标端保存的批次元信息，用于恢复对账。
type BatchMeta struct {
	Batch        int64
	StartOffset  int64
	EndOffset    int64
	PrefixDigest string
}

// Sink 抽象目标存储。实现必须保证：
//   - CommitBatch 幂等：同一批号重复提交不得产生重复数据；
//   - LastCommittedBatch 返回已持久化的最大连续批号；
//   - BatchMetaOf 返回指定批次的元信息，供恢复对账。
type Sink interface {
	LastCommittedBatch() (int64, error)
	CommitBatch(c BatchCommit) error
	BatchMetaOf(batch int64) (BatchMeta, error)
}

// ErrBatchNotFound 表示目标端不存在指定批次。
var ErrBatchNotFound = errors.New("importer: batch not found in sink")

// MemSink 是内存版 Sink，用于测试；支持注入回执丢失故障。
type MemSink struct {
	mu sync.Mutex

	metas   map[int64]BatchMeta
	records [][]byte
	last    int64

	// LoseReceiptFor 命中的批号：数据照常落盘，但返回 ErrReceiptLost，
	// 模拟“目标已提交、回执丢失”的故障。
	LoseReceiptFor map[int64]bool
}

// ErrReceiptLost 表示提交已在目标端生效但回执丢失。
var ErrReceiptLost = errors.New("importer: commit receipt lost")

func NewMemSink() *MemSink {
	return &MemSink{
		metas:          make(map[int64]BatchMeta),
		LoseReceiptFor: make(map[int64]bool),
	}
}

func (m *MemSink) LastCommittedBatch() (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last, nil
}

func (m *MemSink) CommitBatch(c BatchCommit) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c.Batch <= m.last {
		if _, ok := m.metas[c.Batch]; ok {
			return nil // 幂等：已提交的批次直接成功
		}
		return fmt.Errorf("importer: batch %d below committed watermark %d", c.Batch, m.last)
	}
	if c.Batch != m.last+1 {
		return fmt.Errorf("importer: non-contiguous batch %d, last is %d", c.Batch, m.last)
	}
	m.metas[c.Batch] = BatchMeta{
		Batch:        c.Batch,
		StartOffset:  c.StartOffset,
		EndOffset:    c.EndOffset,
		PrefixDigest: c.PrefixDigest,
	}
	m.records = append(m.records, c.Records...)
	m.last = c.Batch
	if m.LoseReceiptFor[c.Batch] {
		return ErrReceiptLost
	}
	return nil
}

func (m *MemSink) BatchMetaOf(batch int64) (BatchMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meta, ok := m.metas[batch]
	if !ok {
		return BatchMeta{}, ErrBatchNotFound
	}
	return meta, nil
}

// Records 返回目标端已收到的全部记录（按提交顺序）。
func (m *MemSink) Records() [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([][]byte, len(m.records))
	copy(out, m.records)
	return out
}

// CountRecords 统计各记录出现次数，用于断言“不漏不重”。
func CountRecords(records [][]byte) map[string]int {
	counts := make(map[string]int, len(records))
	for _, r := range records {
		counts[string(r)]++
	}
	return counts
}
