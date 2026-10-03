package twopc

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Participant is the transport-level interface the coordinator talks to.
// All operations must be idempotent: after a crash the coordinator may
// re-send any of them.
type Participant interface {
	Prepare(ctx context.Context, txnID string) (Vote, error)
	Commit(ctx context.Context, txnID string) error
	Abort(ctx context.Context, txnID string) error
	Query(ctx context.Context, txnID string) (ParticipantState, error)
}

// MemoryParticipant is an in-process Participant with fault injection,
// used by the tests and the demo. Its transaction table models durable
// participant state (it survives SetDown, which simulates a crash).
type MemoryParticipant struct {
	mu     sync.Mutex
	id     string
	down   bool
	delay  time.Duration
	voteFn func(txnID string) Vote
	txns   map[string]ParticipantState
	votes  map[string]Vote
}

func NewMemoryParticipant(id string) *MemoryParticipant {
	return &MemoryParticipant{
		id:     id,
		voteFn: func(string) Vote { return VoteYes },
		txns:   map[string]ParticipantState{},
		votes:  map[string]Vote{},
	}
}

// SetVoteFunc controls how the participant votes on new transactions.
func (p *MemoryParticipant) SetVoteFunc(fn func(txnID string) Vote) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.voteFn = fn
}

// SetDown simulates a crash (true) or restart (false) of the participant.
func (p *MemoryParticipant) SetDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

// SetDelay makes Prepare slow, to exercise coordinator-side timeouts.
func (p *MemoryParticipant) SetDelay(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delay = d
}

// State reports the participant's durable state for a transaction.
func (p *MemoryParticipant) State(txnID string) ParticipantState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if st, ok := p.txns[txnID]; ok {
		return st
	}
	return PStateNone
}

func (p *MemoryParticipant) Prepare(ctx context.Context, txnID string) (Vote, error) {
	p.mu.Lock()
	if p.down {
		p.mu.Unlock()
		return "", ErrUnreachable
	}
	delay := p.delay
	p.mu.Unlock()

	if delay > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(delay):
		}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return "", ErrUnreachable
	}
	// Duplicate Prepare (coordinator retry): answer with the original vote.
	if v, ok := p.votes[txnID]; ok {
		return v, nil
	}
	v := p.voteFn(txnID)
	p.votes[txnID] = v
	if v == VoteYes {
		p.txns[txnID] = PStatePrepared
	} else {
		// A NO voter may unilaterally abort and release its resources.
		p.txns[txnID] = PStateAborted
	}
	return v, nil
}

func (p *MemoryParticipant) Commit(ctx context.Context, txnID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return ErrUnreachable
	}
	st, ok := p.txns[txnID]
	if !ok {
		return ErrUnknownTxn
	}
	if st == PStateAborted {
		return fmt.Errorf("txn %s already aborted", txnID)
	}
	p.txns[txnID] = PStateCommitted // idempotent: re-commit is a no-op
	return nil
}

func (p *MemoryParticipant) Abort(ctx context.Context, txnID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return ErrUnreachable
	}
	st, ok := p.txns[txnID]
	if !ok {
		return nil // never prepared: nothing to undo
	}
	if st == PStateCommitted {
		return fmt.Errorf("txn %s already committed", txnID)
	}
	p.txns[txnID] = PStateAborted // idempotent
	return nil
}

func (p *MemoryParticipant) Query(ctx context.Context, txnID string) (ParticipantState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		return PStateNone, ErrUnreachable
	}
	if st, ok := p.txns[txnID]; ok {
		return st, nil
	}
	return PStateNone, nil
}
