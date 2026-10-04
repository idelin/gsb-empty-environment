package msgproc

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// 哨兵错误，供调用者用 == 判断。
var (
	ErrMessageInvalid   = errors.New("msgproc: message ID must not be empty")
	ErrTicketNotFound   = errors.New("msgproc: ticket not found")
	ErrAlreadyRunning   = errors.New("msgproc: business execution already running")
	ErrExecutionFailed  = errors.New("msgproc: business execution failed")
	ErrExecutionTimeout = errors.New("msgproc: business execution timeout")
	ErrResultLost       = errors.New("msgproc: business result lost")
)

// Handler 是业务处理器，输入消息载荷，返回业务结果。
type Handler func(ctx context.Context, payload []byte) ([]byte, error)

// Processor 提供签收、执行与查询能力。崩溃恢复时用同一 Store 重建即可。
type Processor struct {
	store       *Store
	execTimeout time.Duration
	faults      Faults
	idSeq       int64
}

// Option 配置 Processor。
type Option func(*Processor)

// WithExecTimeout 设置单次业务执行的超时时间。
func WithExecTimeout(d time.Duration) Option {
	return func(p *Processor) { p.execTimeout = d }
}

// WithFaults 注入故障，仅用于测试与演练。
func WithFaults(f Faults) Option {
	return func(p *Processor) { p.faults = f }
}

// NewProcessor 基于给定 Store 创建处理器。
func NewProcessor(store *Store, opts ...Option) *Processor {
	p := &Processor{store: store, execTimeout: 5 * time.Second}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Acknowledge 签收消息并签发处理票据。
// 签收按 Message.ID 幂等：重复签收返回同一票据，不产生新记录。
// 签收成功不代表业务成功；若注入崩溃故障，签收持久化后返回 ErrSimulatedCrash。
func (p *Processor) Acknowledge(msg Message) (Ticket, error) {
	if msg.ID == "" {
		return Ticket{}, ErrMessageInvalid
	}
	if rec, ok := p.store.findByMessage(msg.ID); ok {
		return rec.ticket, nil
	}
	seq := atomic.AddInt64(&p.idSeq, 1)
	ticket := Ticket{
		ID:        fmt.Sprintf("tk-%d-%d", time.Now().UnixNano(), seq),
		MessageID: msg.ID,
		IssuedAt:  time.Now(),
	}
	p.store.insert(&record{
		ticket:   ticket,
		payload:  append([]byte(nil), msg.Payload...),
		receipt:  ReceiptAcked,
		business: BizPending,
	})
	if p.faults.crashAfterAck(msg.ID) {
		// 模拟崩溃：签收已持久化，业务永远不会在本次“进程”中执行。
		return ticket, ErrSimulatedCrash
	}
	return ticket, nil
}

// Execute 对票据执行一次业务处理，每次调用追加一条 Attempt 记录，
// 重试不会覆盖历史。返回本次尝试记录；结果状态通过 Query 查询。
func (p *Processor) Execute(ctx context.Context, ticketID string, handler Handler) (Attempt, error) {
	rec, ok := p.store.get(ticketID)
	if !ok {
		return Attempt{}, ErrTicketNotFound
	}

	p.store.mu.Lock()
	if rec.business == BizRunning {
		p.store.mu.Unlock()
		return Attempt{}, ErrAlreadyRunning
	}
	rec.business = BizRunning
	attempt := Attempt{Seq: len(rec.attempts) + 1, StartedAt: time.Now()}
	rec.attempts = append(rec.attempts, attempt)
	idx := len(rec.attempts) - 1
	p.store.mu.Unlock()

	outcome, result, execErr := p.runOnce(ctx, ticketID, rec.ticket.MessageID, handler)

	p.store.mu.Lock()
	attempt = rec.attempts[idx]
	attempt.EndedAt = time.Now()
	attempt.Outcome = outcome
	if execErr != nil {
		attempt.Err = execErr.Error()
	}
	rec.attempts[idx] = attempt
	rec.business = outcome
	if outcome == BizSucceeded {
		rec.result = result
	}
	p.store.mu.Unlock()

	return attempt, execErr
}

func (p *Processor) runOnce(ctx context.Context, ticketID, messageID string, handler Handler) (BusinessState, []byte, error) {
	rec, _ := p.store.get(ticketID)
	p.store.mu.Lock()
	payload := append([]byte(nil), rec.payload...)
	p.store.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, p.execTimeout)
	defer cancel()

	if p.faults.hangInExecute(ticketID) {
		<-ctx.Done()
		return BizTimeout, nil, ErrExecutionTimeout
	}

	type outcome struct {
		result []byte
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{err: fmt.Errorf("handler panic: %v", r)}
			}
		}()
		result, err := handler(ctx, payload)
		done <- outcome{result: result, err: err}
	}()

	select {
	case o := <-done:
		if o.err != nil {
			return BizFailed, nil, fmt.Errorf("%v: %v", ErrExecutionFailed, o.err)
		}
		if p.faults.loseResult(ticketID) {
			// 业务已成功，但结果被丢弃，调用者无法得知真实结果。
			return BizResultLost, nil, ErrResultLost
		}
		return BizSucceeded, o.result, nil
	case <-ctx.Done():
		return BizTimeout, nil, ErrExecutionTimeout
	}
}

// Query 查询票据当前的两层状态视图，供调用者区分三个阶段。
func (p *Processor) Query(ticketID string) (StatusView, error) {
	rec, ok := p.store.get(ticketID)
	if !ok {
		return StatusView{}, ErrTicketNotFound
	}
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	view := StatusView{
		Ticket:      rec.ticket,
		Receipt:     rec.receipt,
		Business:    rec.business,
		Attempts:    append([]Attempt(nil), rec.attempts...),
		ResultKnown: rec.business == BizSucceeded,
	}
	if rec.business == BizSucceeded {
		view.Result = append([]byte(nil), rec.result...)
	}
	return view, nil
}
