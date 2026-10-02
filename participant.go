package twopc

import (
	"errors"
	"sync"
)

// ErrUnavailable is returned by the transport when a participant cannot be
// reached within the (simulated) timeout.
var ErrUnavailable = errors.New("participant unavailable")

// Participant is a simulated 2PC participant with durable local state.
// Setting Down to true simulates a crash: all messages time out.
type Participant struct {
	mu         sync.Mutex
	Down       bool
	VoteToCast Vote

	states map[string]ParticipantState

	// Counters for assertions in tests and demos.
	PrepareCalls int
	CommitCalls  int
	AbortCalls   int
	QueryCalls   int
}

// NewParticipant creates an online participant that casts the given vote.
func NewParticipant(v Vote) *Participant {
	return &Participant{VoteToCast: v, states: make(map[string]ParticipantState)}
}

// Prepare records a prepared state (on YES) or aborts unilaterally (on NO).
// Re-prepare of an already prepared transaction is idempotent.
func (p *Participant) Prepare(txID string) (Vote, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.PrepareCalls++
	if p.Down {
		return "", ErrUnavailable
	}
	switch p.states[txID] {
	case StatePrepared:
		return VoteYes, nil
	case StateCommitted, StateAborted:
		return VoteNo, nil
	}
	if p.VoteToCast == VoteYes {
		p.states[txID] = StatePrepared
		return VoteYes, nil
	}
	p.states[txID] = StateAborted
	return VoteNo, nil
}

// Commit transitions a prepared transaction to committed.
func (p *Participant) Commit(txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.CommitCalls++
	if p.Down {
		return ErrUnavailable
	}
	p.states[txID] = StateCommitted
	return nil
}

// Abort transitions the transaction to aborted (idempotent).
func (p *Participant) Abort(txID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.AbortCalls++
	if p.Down {
		return ErrUnavailable
	}
	p.states[txID] = StateAborted
	return nil
}

// Query reports the participant's local state for the transaction.
func (p *Participant) Query(txID string) (ParticipantState, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.QueryCalls++
	if p.Down {
		return StateNone, ErrUnavailable
	}
	if st, ok := p.states[txID]; ok {
		return st, nil
	}
	return StateNone, nil
}

// State exposes the participant's local state for tests and demos.
func (p *Participant) State(txID string) ParticipantState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.states[txID]
}

// LocalTransport delivers messages to in-process participants.
type LocalTransport struct {
	Participants map[string]*Participant
}

// NewLocalTransport builds a transport over the given participants.
func NewLocalTransport(ps map[string]*Participant) *LocalTransport {
	return &LocalTransport{Participants: ps}
}

func (t *LocalTransport) get(name string) (*Participant, error) {
	p, ok := t.Participants[name]
	if !ok || p == nil {
		return nil, ErrUnavailable
	}
	return p, nil
}

// Prepare delivers a prepare request.
func (t *LocalTransport) Prepare(participant, txID string) (Vote, error) {
	p, err := t.get(participant)
	if err != nil {
		return "", err
	}
	return p.Prepare(txID)
}

// Commit delivers a commit decision.
func (t *LocalTransport) Commit(participant, txID string) error {
	p, err := t.get(participant)
	if err != nil {
		return err
	}
	return p.Commit(txID)
}

// Abort delivers an abort decision.
func (t *LocalTransport) Abort(participant, txID string) error {
	p, err := t.get(participant)
	if err != nil {
		return err
	}
	return p.Abort(txID)
}

// Query delivers a status query.
func (t *LocalTransport) Query(participant, txID string) (ParticipantState, error) {
	p, err := t.get(participant)
	if err != nil {
		return StateNone, err
	}
	return p.Query(txID)
}
