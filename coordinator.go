package twopc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrCrashed is returned by Submit when a test hook simulates a coordinator
// crash in the middle of the protocol.
var ErrCrashed = errors.New("coordinator crashed (simulated)")

type decision string

const (
	decisionCommit decision = "COMMIT"
	decisionAbort  decision = "ABORT"
)

// Config tunes timeouts and retries.
type Config struct {
	// RPCTimeout bounds a single call to a participant.
	RPCTimeout time.Duration
	// MaxRetries is the number of retries after the first failed attempt.
	// Once exhausted, the coordinator records a pending audit event.
	MaxRetries int
	// RetryBackoff is the delay between retries.
	RetryBackoff time.Duration
}

func DefaultConfig() Config {
	return Config{
		RPCTimeout:   500 * time.Millisecond,
		MaxRetries:   5,
		RetryBackoff: 100 * time.Millisecond,
	}
}

// Hooks are test-only fault injection points.
type Hooks struct {
	// BeforeCommitSend runs after the COMMITTING record is durable but
	// before any Commit message is sent. Returning true simulates a
	// coordinator crash at exactly that point.
	BeforeCommitSend func(txnID string) bool
}

// txn is the coordinator's in-memory view of one transaction, rebuilt from
// the WAL during recovery.
type txn struct {
	id           string
	state        TxnState
	participants []string
	votes        map[string]Vote
	pending      map[string]bool // participants that never acked the decision
}

// Coordinator drives the two-phase commit protocol and recovers in-doubt
// transactions from its WAL after a crash.
type Coordinator struct {
	wal    *WAL
	cfg    Config
	reg    map[string]Participant
	hooks  Hooks
	logger *log.Logger

	mu   sync.Mutex
	txns map[string]*txn
}

func NewCoordinator(wal *WAL, cfg Config, reg map[string]Participant, hooks Hooks, logger *log.Logger) *Coordinator {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Coordinator{
		wal: wal, cfg: cfg, reg: reg, hooks: hooks,
		logger: logger, txns: map[string]*txn{},
	}
}

// Submit runs a transaction through the full 2PC protocol and returns its
// final state. If some participants never acknowledge the decision, the
// state remains COMMITTING/ABORTING and the transaction shows up in Pending.
func (c *Coordinator) Submit(txnID string, participantIDs []string) (TxnState, error) {
	t := &txn{
		id:           txnID,
		state:        StatePreparing,
		participants: append([]string(nil), participantIDs...),
		votes:        map[string]Vote{},
		pending:      map[string]bool{},
	}
	c.mu.Lock()
	c.txns[txnID] = t
	c.mu.Unlock()

	c.append(Record{TxnID: txnID, Type: RecordTxnBegin, Detail: strings.Join(participantIDs, ",")})
	c.setState(t, StatePreparing)
	c.collectVotes(t)

	if c.allVotedYes(t) {
		c.setState(t, StatePrepared)
		if err := c.commitTxn(t); err != nil {
			return t.state, err
		}
	} else {
		c.abortTxn(t)
	}
	return t.state, nil
}

// Recover replays the WAL and drives every non-terminal transaction to a
// resolution, querying participants where the local log alone cannot decide.
// This is what prevents orphaned (prepared but never resolved) transactions
// from leaking resources on participants.
func (c *Coordinator) Recover() error {
	if err := c.replay(); err != nil {
		return err
	}
	ids := make([]string, 0, len(c.txns))
	for id := range c.txns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c.resolve(c.txns[id])
	}
	return nil
}

// State returns the coordinator-side state of a transaction.
func (c *Coordinator) State(txnID string) (TxnState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.txns[txnID]
	if !ok {
		return "", false
	}
	return t.state, true
}

// Pending returns the participants that have not acknowledged the final
// decision, keyed by transaction ID. These are the in-doubt transactions
// surfaced by audit events.
func (c *Coordinator) Pending() map[string][]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string][]string{}
	for id, t := range c.txns {
		if len(t.pending) == 0 {
			continue
		}
		var ps []string
		for pid := range t.pending {
			ps = append(ps, pid)
		}
		sort.Strings(ps)
		out[id] = ps
	}
	return out
}

// --- protocol phases ---

func (c *Coordinator) collectVotes(t *txn) {
	var wg sync.WaitGroup
	for _, pid := range t.participants {
		c.mu.Lock()
		_, haveVote := t.votes[pid]
		c.mu.Unlock()
		if haveVote {
			continue // vote already durable in the WAL (recovery path)
		}
		wg.Add(1)
		go func(pid string) {
			defer wg.Done()
			vote, err := c.prepareWithRetry(pid, t.id)
			if err != nil {
				c.logger.Printf("txn %s: no vote from %s: %v", t.id, pid, err)
				return
			}
			c.mu.Lock()
			_, dup := t.votes[pid]
			if !dup {
				t.votes[pid] = vote
			}
			c.mu.Unlock()
			if !dup {
				c.append(Record{TxnID: t.id, Type: RecordVote, Participant: pid, Vote: vote})
			}
		}(pid)
	}
	wg.Wait()
}

func (c *Coordinator) commitTxn(t *txn) error {
	c.setState(t, StateCommitting)
	if c.hooks.BeforeCommitSend != nil && c.hooks.BeforeCommitSend(t.id) {
		return ErrCrashed
	}
	c.deliver(t, decisionCommit)
	c.mu.Lock()
	pending := len(t.pending)
	c.mu.Unlock()
	if pending == 0 {
		c.setState(t, StateCommitted)
	} else {
		c.logger.Printf("txn %s: COMMIT pending for %d participant(s)", t.id, pending)
	}
	return nil
}

func (c *Coordinator) abortTxn(t *txn) {
	c.setState(t, StateAborting)
	c.deliver(t, decisionAbort)
	c.mu.Lock()
	pending := len(t.pending)
	c.mu.Unlock()
	if pending == 0 {
		c.setState(t, StateAborted)
	} else {
		c.logger.Printf("txn %s: ABORT pending for %d participant(s)", t.id, pending)
	}
}

// deliver sends the final decision to every participant. Participants that
// stay unreachable past the retry limit are recorded as pending audit
// events; delivery is idempotent, so recovery can safely re-run it.
func (c *Coordinator) deliver(t *txn, d decision) {
	c.mu.Lock()
	t.pending = map[string]bool{}
	c.mu.Unlock()
	for _, pid := range t.participants {
		if err := c.sendDecision(pid, t.id, d); err != nil {
			c.mu.Lock()
			t.pending[pid] = true
			c.mu.Unlock()
			c.append(Record{
				TxnID: t.id, Type: RecordAudit, Participant: pid,
				Detail: fmt.Sprintf("%s undelivered after %d retries: %v", d, c.cfg.MaxRetries, err),
			})
			c.logger.Printf("txn %s: AUDIT pending participant %s (%s)", t.id, pid, d)
		}
	}
}

// --- recovery ---

func (c *Coordinator) replay() error {
	return c.wal.Replay(func(r Record) error {
		t, ok := c.txns[r.TxnID]
		if !ok {
			t = &txn{id: r.TxnID, votes: map[string]Vote{}, pending: map[string]bool{}}
			c.txns[r.TxnID] = t
		}
		switch r.Type {
		case RecordTxnBegin:
			t.participants = strings.Split(r.Detail, ",")
			t.state = StatePreparing
		case RecordVote:
			t.votes[r.Participant] = r.Vote // duplicate votes dedupe here
		case RecordState:
			t.state = r.State
		case RecordAudit:
			t.pending[r.Participant] = true
		}
		return nil
	})
}

func (c *Coordinator) resolve(t *txn) {
	c.logger.Printf("txn %s: recovering from %s", t.id, t.state)
	switch t.state {
	case StateCommitted, StateAborted:
		// Terminal: nothing to do.
	case StatePreparing:
		// No decision was ever logged, so no participant can have
		// committed: aborting is always safe and releases locks.
		c.abortTxn(t)
	case StatePrepared:
		c.resolvePrepared(t)
	case StateCommitting:
		c.deliver(t, decisionCommit)
		if len(t.pending) == 0 {
			c.setState(t, StateCommitted)
		}
	case StateAborting:
		c.deliver(t, decisionAbort)
		if len(t.pending) == 0 {
			c.setState(t, StateAborted)
		}
	}
}

// resolvePrepared handles a transaction whose participants all voted YES
// but whose decision was never logged. The coordinator queries the
// participants: if any of them already learned a decision, it is followed;
// otherwise the unanimous YES vote makes committing safe.
func (c *Coordinator) resolvePrepared(t *txn) {
	sawCommit, sawAbort := false, false
	for _, pid := range t.participants {
		st, err := c.queryWithRetry(pid, t.id)
		if err != nil {
			c.logger.Printf("txn %s: query %s failed: %v", t.id, pid, err)
			continue
		}
		switch st {
		case PStateCommitted:
			sawCommit = true
		case PStateAborted:
			sawAbort = true
		}
	}
	if sawAbort && !sawCommit {
		c.abortTxn(t)
		return
	}
	if err := c.commitTxn(t); err != nil {
		c.logger.Printf("txn %s: recovery commit interrupted: %v", t.id, err)
	}
}

// --- RPC helpers with timeout + bounded retries ---

func (c *Coordinator) prepareWithRetry(pid, txnID string) (Vote, error) {
	p, ok := c.reg[pid]
	if !ok {
		return "", fmt.Errorf("unknown participant %s", pid)
	}
	var err error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		var vote Vote
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RPCTimeout)
		vote, err = p.Prepare(ctx, txnID)
		cancel()
		if err == nil {
			return vote, nil
		}
		time.Sleep(c.cfg.RetryBackoff)
	}
	return "", err
}

func (c *Coordinator) sendDecision(pid, txnID string, d decision) error {
	p, ok := c.reg[pid]
	if !ok {
		return fmt.Errorf("unknown participant %s", pid)
	}
	var err error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RPCTimeout)
		if d == decisionCommit {
			err = p.Commit(ctx, txnID)
		} else {
			err = p.Abort(ctx, txnID)
		}
		cancel()
		if err == nil {
			return nil
		}
		time.Sleep(c.cfg.RetryBackoff)
	}
	return err
}

func (c *Coordinator) queryWithRetry(pid, txnID string) (ParticipantState, error) {
	p, ok := c.reg[pid]
	if !ok {
		return PStateNone, fmt.Errorf("unknown participant %s", pid)
	}
	var err error
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		var st ParticipantState
		ctx, cancel := context.WithTimeout(context.Background(), c.cfg.RPCTimeout)
		st, err = p.Query(ctx, txnID)
		cancel()
		if err == nil {
			return st, nil
		}
		time.Sleep(c.cfg.RetryBackoff)
	}
	return PStateNone, err
}

// --- small helpers ---

func (c *Coordinator) allVotedYes(t *txn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(t.votes) != len(t.participants) {
		return false
	}
	for _, v := range t.votes {
		if v != VoteYes {
			return false
		}
	}
	return true
}

func (c *Coordinator) setState(t *txn, s TxnState) {
	c.mu.Lock()
	t.state = s
	c.mu.Unlock()
	c.append(Record{TxnID: t.id, Type: RecordState, State: s})
	c.logger.Printf("txn %s: -> %s", t.id, s)
}

func (c *Coordinator) append(r Record) {
	if err := c.wal.Append(r); err != nil {
		c.logger.Printf("txn %s: WAL append failed: %v", r.TxnID, err)
	}
}
