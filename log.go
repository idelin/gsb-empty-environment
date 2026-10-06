// Package logalloc 实现批次序号日志分配器：
// 先预留序号再写数据，失败留下空洞；发布流必须用显式跳过记录
// 封闭空洞才能推进；恢复时区分「已预留未决定」与「已跳过」；
// 持久化切点保证连续消费与不重复分配。
package logalloc

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 条目状态机：reserved -> committed | skipped，决定后不可更改。
type state int

const (
	stateReserved  state = iota // 已预留，未决定（潜在空洞）
	stateCommitted              // 数据已写入
	stateSkipped                // 显式跳过（空洞已封闭）
)

type entry struct {
	state state
	data  []byte
}

// Log 是分配器与发布流共享的持久化状态。
type Log struct {
	mu         sync.Mutex
	w          *wal
	entries    map[uint64]*entry
	nextSeq    uint64 // 下一个可分配序号，只增不减，保证不重复分配
	checkpoint uint64 // 持久化切点：已连续发布到的序号
}

// Report 是恢复结果，明确区分「已预留未决定」与「已跳过」。
type Report struct {
	Undecided  []uint64 // 已预留但未决定：需由上层 Commit 或 Skip 来裁决
	Skipped    []uint64 // 已显式跳过：终态，无需处理
	Committed  []uint64 // 已提交未发布：等待发布流消费
	NextSeq    uint64   // 恢复后的分配起点（已用最大序号 +1）
	Checkpoint uint64   // 恢复出的持久化切点
}

// Open 打开（或创建）日志目录并重放 WAL 完成恢复。
// 重放阶段即校验：序号不重复分配、决定不重复、切点连续推进。
func Open(dir string) (*Log, *Report, error) {
	w, err := openWAL(dir)
	if err != nil {
		return nil, nil, err
	}
	l := &Log{w: w, entries: make(map[uint64]*entry), nextSeq: 1}

	err = replayWAL(dir, func(rec walRecord) error {
		switch rec.Type {
		case recReserve:
			if rec.Seq == 0 {
				return errors.New("序号 0 非法")
			}
			if _, dup := l.entries[rec.Seq]; dup {
				return fmt.Errorf("序号 %d 重复分配", rec.Seq)
			}
			l.entries[rec.Seq] = &entry{state: stateReserved}
			if rec.Seq >= l.nextSeq {
				l.nextSeq = rec.Seq + 1
			}
		case recCommit, recSkip:
			e, ok := l.entries[rec.Seq]
			if !ok {
				return fmt.Errorf("序号 %d 未预留就被决定", rec.Seq)
			}
			if e.state != stateReserved {
				return fmt.Errorf("序号 %d 重复决定", rec.Seq)
			}
			if rec.Type == recCommit {
				e.state, e.data = stateCommitted, rec.Data
			} else {
				e.state = stateSkipped
			}
		case recCheckpoint:
			// 切点必须恰好 +1 推进：这是连续消费的结构性保证。
			if rec.Seq != l.checkpoint+1 {
				return fmt.Errorf("切点不连续: 期望 %d, 实际 %d", l.checkpoint+1, rec.Seq)
			}
			e, ok := l.entries[rec.Seq]
			if !ok || e.state == stateReserved {
				return fmt.Errorf("切点 %d 越过了未决定的空洞", rec.Seq)
			}
			l.checkpoint = rec.Seq
		default:
			return fmt.Errorf("未知记录类型 %q", rec.Type)
		}
		return nil
	})
	if err != nil {
		w.close()
		return nil, nil, err
	}
	return l, l.reportLocked(), nil
}

// reportLocked 生成恢复报告；调用方需持有锁或处于单线程恢复期。
func (l *Log) reportLocked() *Report {
	r := &Report{NextSeq: l.nextSeq, Checkpoint: l.checkpoint}
	for seq, e := range l.entries {
		switch {
		case e.state == stateReserved:
			r.Undecided = append(r.Undecided, seq)
		case e.state == stateSkipped:
			r.Skipped = append(r.Skipped, seq)
		case e.state == stateCommitted && seq > l.checkpoint:
			r.Committed = append(r.Committed, seq)
		}
	}
	sort.Slice(r.Undecided, func(i, j int) bool { return r.Undecided[i] < r.Undecided[j] })
	sort.Slice(r.Skipped, func(i, j int) bool { return r.Skipped[i] < r.Skipped[j] })
	sort.Slice(r.Committed, func(i, j int) bool { return r.Committed[i] < r.Committed[j] })
	return r
}

// Close 关闭底层 WAL 文件。
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.close()
}
