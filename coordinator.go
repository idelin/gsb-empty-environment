// Package twopc implements a crash-recoverable two-phase commit coordinator
// for discrete-event environments, with a write-ahead log, participant
// status queries on recovery, bounded retries and pending audit events.
package twopc

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// Vote is a participant's response to a prepare request.
type Vote string

const (
	VoteYes Vote = "YES"
	VoteNo  Vote = "NO"
)

// Decision is the coordinator's final outcome for a transaction.
type Decision string

const (
	DecisionNone   Decision = ""
	DecisionCommit Decision = "COMMIT"
	DecisionAbort  Decision = "ABORT"
)

// ParticipantState is the per-transaction state reported by a participant
// when queried during recovery.
type ParticipantState string

const (
	StateNone      ParticipantState = "NONE"
	StatePrepared  ParticipantState = "PREPARED"
	StateCommitted ParticipantState = "COMMITTED"
	StateAborted   ParticipantState = "ABORTED"
)

// Transport abstracts message delivery to participants. A returned error
// means the message was not delivered within the (simulated) timeout, e.g.
// because the participant crashed or the network dropped it.
type Transport interface {
	Prepare(participant, txID string) (Vote, error)
	Commit(participant, txID string) error
	Abort(participant, txID string) error
	Query(participant, txID string) (ParticipantState, error)
}

// Config tunes retry behaviour.
type Config struct {
	// MaxRetries is the number of additional delivery attempts after the
	// first failure before a pending audit event is recorded.
	MaxRetries int
	// RetryDelay is slept between attempts (zero in discrete-event tests).
	RetryDelay time.Duration
}

// txState is the in-memory view of one transaction, rebuilt from the WAL.
type txState struct {
	id           string
	participants []string
	votes        map[string]Vote
	decision     Decision
	acks         map[string]bool
	done         bool
	audits       map[string]bool // dedupe key -> already logged
}

// Coordinator drives 2PC transactions and recovers them after a crash.
type Coordinator struct {
	cfg Config
	wal WAL
	tr  Transport

	mu  sync.Mutex
	txs map[string]*txState
}

// NewCoordinator creates a coordinator, replaying any previously persisted
// WAL records to rebuild in-memory state. Call Recover afterwards to finish
// transactions that were in flight when the previous instance crashed.
func NewCoordinator(cfg Config, wal WAL, tr Transport, records []Record) *Coordinator {
	c := &Coordinator{cfg: cfg, wal: wal, tr: tr, txs: make(map[string]*txState)}
	c.replay(records)
	return c
}

func (c *Coordinator) replay(records []Record) {
	for _, r := range records {
		tx := c.txs[r.TxID]
		if tx == nil && r.Type == RecBegin {
			tx = &txState{
				id:           r.TxID,
				participants: append([]string(nil), r.Participants...),
				votes:        make(map[string]Vote),
				acks:         make(map[string]bool),
				audits:       make(map[string]bool),
			}
			c.txs[r.TxID] = tx
		}
		if tx == nil {
			continue
		}
		switch r.Type {
		case RecVote:
			if _, ok := tx.votes[r.Participant]; !ok {
				tx.votes[r.Participant] = r.Vote
			}
		case RecDecision:
			if tx.decision == DecisionNone {
				tx.decision = r.Decision
			}
		case RecAck:
			tx.acks[r.Participant] = true
		case RecDone:
			tx.done = true
		case RecAudit:
			tx.audits[r.Participant+"|"+r.Reason] = true
		}
	}
}

func (c *Coordinator) appendLocked(r Record) error {
	r.Time = time.Now().UTC()
	return c.wal.Append(r)
}

// Begin starts a new transaction and durably logs its participant set.
func (c *Coordinator) Begin(txID string, participants []string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.txs[txID]; ok {
		return fmt.Errorf("transaction %q already exists", txID)
	}
	tx := &txState{
		id:           txID,
		participants: append([]string(nil), participants...),
		votes:        make(map[string]Vote),
		acks:         make(map[string]bool),
		audits:       make(map[string]bool),
	}
	if err := c.appendLocked(Record{Type: RecBegin, TxID: txID, Participants: participants}); err != nil {
		return err
	}
	c.txs[txID] = tx
	return nil
}

// Run executes a full 2PC round for a new transaction and returns the
// decision. Delivery to unreachable participants continues in the
// background of the state machine: call Recover to drive it further.
func (c *Coordinator) Run(txID string, participants []string) (Decision, error) {
	if err := c.Begin(txID, participants); err != nil {
		return DecisionNone, err
	}
	c.prepare(txID)
	c.decide(txID)
	c.resolve(txID)
	return c.DecisionOf(txID), nil
}

// PreparePhase collects votes for txID. It is exported so drivers can step
// the protocol phase by phase (e.g. to simulate a crash between phases).
func (c *Coordinator) PreparePhase(txID string) { c.prepare(txID) }

// DecidePhase logs the atomic decision once all votes are recorded.
func (c *Coordinator) DecidePhase(txID string) { c.decide(txID) }

// ResolvePhase delivers the decision to all participants, auditing any
// participant that stays unreachable beyond the retry budget.
func (c *Coordinator) ResolvePhase(txID string) { c.resolve(txID) }

// prepare sends prepare requests to all participants that have no recorded
// vote yet. A transport error is a vote timeout and is recorded as NO
// (presumed abort), so the transaction can never block indefinitely.
func (c *Coordinator) prepare(txID string) {
	c.mu.Lock()
	tx := c.txs[txID]
	pending := make([]string, 0, len(tx.participants))
	for _, p := range tx.participants {
		if _, ok := tx.votes[p]; !ok {
			pending = append(pending, p)
		}
	}
	c.mu.Unlock()

	for _, p := range pending {
		v, err := c.tr.Prepare(p, txID)
		if err != nil {
			c.recordVote(txID, p, VoteNo, "vote timeout: "+err.Error())
			continue
		}
		c.recordVote(txID, p, v, "")
	}
}

// RecordVote is the idempotent vote handler. Duplicate deliveries of the
// same participant's vote are ignored. It is safe for transport layers to
// call this directly when a vote arrives out of band.
func (c *Coordinator) RecordVote(txID, participant string, v Vote) bool {
	return c.recordVote(txID, participant, v, "")
}

func (c *Coordinator) recordVote(txID, participant string, v Vote, reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txs[txID]
	if tx == nil || tx.decision != DecisionNone || tx.done {
		return false
	}
	if !containsString(tx.participants, participant) {
		return false
	}
	if _, dup := tx.votes[participant]; dup {
		return false // duplicate vote: first one wins
	}
	tx.votes[participant] = v
	c.appendLocked(Record{Type: RecVote, TxID: txID, Participant: participant, Vote: v, Reason: reason})
	return true
}

// decide logs the atomic commit/abort decision once all votes are in.
func (c *Coordinator) decide(txID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	tx := c.txs[txID]
	if tx == nil || tx.decision != DecisionNone {
		return
	}
	d := DecisionCommit
	for _, p := range tx.participants {
		if tx.votes[p] != VoteYes {
			d = DecisionAbort
			break
		}
	}
	tx.decision = d
	c.appendLocked(Record{Type: RecDecision, TxID: txID, Decision: d})
}

// resolve drives a transaction to completion: it guarantees a decision
// exists (presumed abort otherwise) and delivers it to every participant
// that has not acknowledged it, querying participant state first. After
// MaxRetries failed attempts a pending audit event is recorded and the
// transaction stays pending for a future Recover call — it is never
// silently dropped, so no orphan transaction leaks participant resources.
func (c *Coordinator) resolve(txID string) {
	c.mu.Lock()
	tx := c.txs[txID]
	if tx == nil || tx.done {
		c.mu.Unlock()
		return
	}
	if tx.decision == DecisionNone {
		// Recovery path: if every vote survived in the WAL, decide from
		// them; otherwise presume abort (a participant may have voted NO
		// or never responded, and prepared participants must not block).
		d := DecisionCommit
		reason := ""
		for _, p := range tx.participants {
			v, ok := tx.votes[p]
			if !ok {
				d = DecisionAbort
				reason = "presumed abort: recovery found no decision record and missing votes"
				break
			}
			if v != VoteYes {
				d = DecisionAbort
			}
		}
		tx.decision = d
		c.appendLocked(Record{Type: RecDecision, TxID: txID, Decision: d, Reason: reason})
	}
	decision := tx.decision
	parts := append([]string(nil), tx.participants...)
	c.mu.Unlock()

	allAcked := true
	for _, p := range parts {
		c.mu.Lock()
		acked := tx.acks[p]
		c.mu.Unlock()
		if acked {
			continue
		}
		if c.deliverWithRetry(tx, p, decision) {
			c.mu.Lock()
			tx.acks[p] = true
			c.appendLocked(Record{Type: RecAck, TxID: txID, Participant: p})
			c.mu.Unlock()
			continue
		}
		allAcked = false
		c.mu.Lock()
		reason := "pending: participant unreachable after retries; decision " + string(decision) + " not yet delivered"
		if !tx.audits[p+"|"+reason] {
			tx.audits[p+"|"+reason] = true
			c.appendLocked(Record{Type: RecAudit, TxID: txID, Participant: p, Reason: reason})
		}
		c.mu.Unlock()
	}

	if allAcked {
		c.mu.Lock()
		if !tx.done {
			tx.done = true
			c.appendLocked(Record{Type: RecDone, TxID: txID})
		}
		c.mu.Unlock()
	}
}

// deliverWithRetry queries the participant and (re)delivers the decision if
// needed. It returns true once the participant is known to hold the decision.
func (c *Coordinator) deliverWithRetry(tx *txState, p string, d Decision) bool {
	for attempt := 0; attempt <= c.cfg.MaxRetries; attempt++ {
		if attempt > 0 && c.cfg.RetryDelay > 0 {
			time.Sleep(c.cfg.RetryDelay)
		}
		st, err := c.tr.Query(p, tx.id)
		if err != nil {
			continue
		}
		switch st {
		case finalStateFor(d):
			return true // already holds our decision (idempotent delivery)
		case StateCommitted, StateAborted:
			// Participant finalised the opposite way: a protocol violation
			// that must be surfaced, not retried.
			c.mu.Lock()
			reason := "conflict: participant in state " + string(st) + " but decision is " + string(d)
			if !tx.audits[p+"|"+reason] {
				tx.audits[p+"|"+reason] = true
				c.appendLocked(Record{Type: RecAudit, TxID: tx.id, Participant: p, Reason: reason})
			}
			c.mu.Unlock()
			return true
		}
		// NONE or PREPARED: (re)send the decision.
		var derr error
		if d == DecisionCommit {
			derr = c.tr.Commit(p, tx.id)
		} else {
			derr = c.tr.Abort(p, tx.id)
		}
		if derr == nil {
			return true
		}
	}
	return false
}

// Recover finishes every transaction that is not fully resolved. It is
// idempotent and safe to call repeatedly, e.g. after each restart.
func (c *Coordinator) Recover() {
	c.mu.Lock()
	ids := make([]string, 0, len(c.txs))
	for id, tx := range c.txs {
		if !tx.done {
			ids = append(ids, id)
		}
	}
	c.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		c.resolve(id)
	}
}

// DecisionOf returns the recorded decision for txID ("" if none yet).
func (c *Coordinator) DecisionOf(txID string) Decision {
	c.mu.Lock()
	defer c.mu.Unlock()
	if tx := c.txs[txID]; tx != nil {
		return tx.decision
	}
	return DecisionNone
}

// Pending returns the IDs of transactions not yet fully resolved.
// A non-empty result always has corresponding AUDIT records in the WAL.
func (c *Coordinator) Pending() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ids []string
	for id, tx := range c.txs {
		if !tx.done {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func finalStateFor(d Decision) ParticipantState {
	if d == DecisionCommit {
		return StateCommitted
	}
	return StateAborted
}

func containsString(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
