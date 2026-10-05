package msgproc

import (
	"context"
	"errors"
	"time"
)

// ErrCrashAfterAck 表示签收已持久化、但响应前进程“崩溃”。
// 调用者重新签收同一消息会拿到同一张票据。
var ErrCrashAfterAck = errors.New("msgproc: crashed after ack persisted")

// ErrResultLost 表示业务已成功但结果记录丢失。
var ErrResultLost = errors.New("msgproc: result record lost")

// Handler 是业务处理函数。
type Handler func(ctx context.Context, payload []byte) (interface{}, error)

// Faults 是故障注入点。
type Faults struct {
	// CrashAfterAck 在签收持久化后、返回票据前模拟崩溃。
	CrashAfterAck bool
	// BusinessTimeout 让业务执行超过 ExecTimeout。
	BusinessTimeout bool
	// ResultLoss 在业务成功后丢弃结果记录。
	ResultLoss bool
}

// Processor 把签收与业务执行分开记录。
type Processor struct {
	store       *Store
	handler     Handler
	faults      *Faults
	execTimeout time.Duration
}

func NewProcessor(store *Store, handler Handler, faults *Faults, execTimeout time.Duration) *Processor {
	if faults == nil {
		faults = &Faults{}
	}
	if execTimeout <= 0 {
		execTimeout = 5 * time.Second
	}
	return &Processor{store: store, handler: handler, faults: faults, execTimeout: execTimeout}
}

// Ack 签收消息并返回处理票据。签收是幂等的：
// 同一 messageID 重复签收返回同一张票据。
// 签收成功只代表消息被受理，不代表业务执行成功。
func (p *Processor) Ack(messageID string, payload []byte) (string, error) {
	p.store.mu.Lock()
	rec, _ := p.store.getOrCreateLocked(messageID, payload)
	id := rec.id
	p.store.mu.Unlock()

	if p.faults.CrashAfterAck {
		return "", ErrCrashAfterAck
	}
	return id, nil
}

// Execute 对票据执行一次业务尝试，尝试记录追加到历史中。
func (p *Processor) Execute(ctx context.Context, ticketID string) error {
	p.store.mu.Lock()
	rec, ok := p.store.tickets[ticketID]
	if !ok {
		p.store.mu.Unlock()
		return ErrTicketNotFound
	}
	rec.phase = PhaseExecuting
	rec.attempts = append(rec.attempts, Attempt{
		Seq:       len(rec.attempts) + 1,
		StartedAt: time.Now(),
	})
	idx := len(rec.attempts) - 1
	payload := rec.payload
	p.store.mu.Unlock()

	if p.faults.BusinessTimeout {
		p.finishAttempt(ticketID, idx, OutcomeTimeout, "execution timeout", PhaseUnknown)
		return context.DeadlineExceeded
	}

	type outcome struct {
		res interface{}
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := p.handler(ctx, payload)
		done <- outcome{res, err}
	}()

	select {
	case out := <-done:
		if out.err != nil {
			p.finishAttempt(ticketID, idx, OutcomeFailure, out.err.Error(), PhaseFailed)
			return out.err
		}
		p.store.mu.Lock()
		defer p.store.mu.Unlock()
		rec = p.store.tickets[ticketID]
		rec.attempts[idx].FinishedAt = time.Now()
		rec.attempts[idx].Outcome = OutcomeSuccess
		rec.phase = PhaseSucceeded
		if p.faults.ResultLoss {
			rec.result = nil
			rec.resultLost = true
		} else {
			rec.result = out.res
		}
		return nil
	case <-time.After(p.execTimeout):
		p.finishAttempt(ticketID, idx, OutcomeTimeout, "execution timeout", PhaseUnknown)
		return context.DeadlineExceeded
	case <-ctx.Done():
		p.finishAttempt(ticketID, idx, OutcomeTimeout, ctx.Err().Error(), PhaseUnknown)
		return ctx.Err()
	}
}

func (p *Processor) finishAttempt(ticketID string, idx int, oc Outcome, errMsg string, phase Phase) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	rec, ok := p.store.tickets[ticketID]
	if !ok {
		return
	}
	rec.attempts[idx].FinishedAt = time.Now()
	rec.attempts[idx].Outcome = oc
	rec.attempts[idx].Err = errMsg
	rec.phase = phase
}

// Recover 在进程重启后调用，把崩溃时停留在执行中的
// 尝试标记为中断，票据阶段置为 UNKNOWN。
func (p *Processor) Recover() {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	for _, rec := range p.store.tickets {
		if rec.phase != PhaseExecuting {
			continue
		}
		for i := range rec.attempts {
			if rec.attempts[i].FinishedAt.IsZero() {
				rec.attempts[i].FinishedAt = time.Now()
				rec.attempts[i].Outcome = OutcomeInterrupted
				rec.attempts[i].Err = "interrupted by crash"
			}
		}
		rec.phase = PhaseUnknown
	}
}

// Query 返回票据的两层状态与完整尝试历史。
func (p *Processor) Query(ticketID string) (Status, error) {
	p.store.mu.Lock()
	defer p.store.mu.Unlock()
	rec, ok := p.store.tickets[ticketID]
	if !ok {
		return Status{}, ErrTicketNotFound
	}
	st := Status{
		TicketID:   rec.id,
		MessageID:  rec.messageID,
		Phase:      rec.phase,
		Result:     rec.result,
		ResultLost: rec.resultLost,
	}
	st.Attempts = append(st.Attempts, rec.attempts...)
	return st, nil
}

// Result 只取业务结果；结果丢失时返回 ErrResultLost，
// 使调用者能区分“业务失败”与“结果不可得”。
func (p *Processor) Result(ticketID string) (interface{}, error) {
	st, err := p.Query(ticketID)
	if err != nil {
		return nil, err
	}
	if st.ResultLost {
		return nil, ErrResultLost
	}
	if st.Phase != PhaseSucceeded {
		return nil, errors.New("msgproc: no result, phase=" + st.Phase.String())
	}
	return st.Result, nil
}
