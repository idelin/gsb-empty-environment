package logalloc

import "errors"

var (
	// ErrHoleNotClosed 表示切点之后是「已预留未决定」的空洞，
	// 发布流必须等其被 Commit 或显式 Skip 封闭后才能推进。
	ErrHoleNotClosed = errors.New("logalloc: 空洞未封闭，发布流无法推进")
	// ErrNothingToPublish 表示切点已追上分配头，没有可发布的序号。
	ErrNothingToPublish = errors.New("logalloc: 没有可发布的序号")
)

// Outcome 是一次发布推进的结果。
type Outcome struct {
	Seq     uint64
	Data    []byte // Skipped 为 true 时为 nil
	Skipped bool   // true 表示该序号被显式跳过，仅封闭空洞、无数据
}

// Publisher 是发布流：严格按切点 +1 顺序消费，推进即持久化新切点。
type Publisher struct{ log *Log }

// Publisher 返回该日志的发布流。
func (l *Log) Publisher() *Publisher { return &Publisher{log: l} }

// Checkpoint 返回当前持久化切点（已连续发布到的序号）。
func (p *Publisher) Checkpoint() uint64 {
	p.log.mu.Lock()
	defer p.log.mu.Unlock()
	return p.log.checkpoint
}

// Next 发布切点之后的下一个序号并推进持久化切点。
// 已提交则返回数据；已跳过则仅封闭空洞；未决定则返回 ErrHoleNotClosed。
func (p *Publisher) Next() (*Outcome, error) {
	l := p.log
	l.mu.Lock()
	defer l.mu.Unlock()
	seq := l.checkpoint + 1
	e, ok := l.entries[seq]
	if !ok {
		return nil, ErrNothingToPublish
	}
	if e.state == stateReserved {
		return nil, ErrHoleNotClosed
	}
	if err := l.w.append(walRecord{Type: recCheckpoint, Seq: seq}); err != nil {
		return nil, err
	}
	l.checkpoint = seq
	return &Outcome{Seq: seq, Data: e.data, Skipped: e.state == stateSkipped}, nil
}
