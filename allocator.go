package logalloc

import "errors"

var (
	// ErrNotReserved 表示对未预留（或不存在）的序号做了决定。
	ErrNotReserved = errors.New("logalloc: 序号未预留")
	// ErrAlreadyDecided 表示序号已被 commit 或 skip，决定不可更改。
	ErrAlreadyDecided = errors.New("logalloc: 序号已决定")
)

// Allocator 负责先预留批次序号、后写数据的分配侧。
type Allocator struct{ log *Log }

// Allocator 返回该日志的分配器。
func (l *Log) Allocator() *Allocator { return &Allocator{log: l} }

// Reserve 预留下一个批次序号并先落盘，随后才可 Commit/Skip。
// 预留后崩溃会留下「已预留未决定」的空洞，恢复时出现在 Report.Undecided。
func (a *Allocator) Reserve() (uint64, error) {
	l := a.log
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.nextSeq
	if err := l.w.append(walRecord{Type: recReserve, Seq: seq}); err != nil {
		return 0, err
	}
	l.entries[seq] = &entry{state: stateReserved}
	l.nextSeq = seq + 1
	return seq, nil
}

// Commit 为已预留的序号补上数据，封闭该序号。
func (a *Allocator) Commit(seq uint64, data []byte) error {
	return a.decide(recCommit, seq, data)
}

// Skip 为已预留的序号写入显式跳过记录，封闭空洞使发布流得以推进。
func (a *Allocator) Skip(seq uint64) error {
	return a.decide(recSkip, seq, nil)
}

func (a *Allocator) decide(t recordType, seq uint64, data []byte) error {
	l := a.log
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.entries[seq]
	if !ok {
		return ErrNotReserved
	}
	if e.state != stateReserved {
		return ErrAlreadyDecided
	}
	if err := l.w.append(walRecord{Type: t, Seq: seq, Data: data}); err != nil {
		return err
	}
	if t == recCommit {
		e.state, e.data = stateCommitted, data
	} else {
		e.state = stateSkipped
	}
	return nil
}
