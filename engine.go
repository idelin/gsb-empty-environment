package kvsm

import (
	"fmt"
	"sort"
)

// Engine is a multi-partition deterministic key-value state machine. It
// replays a local deterministic log of two-phase (prepare/commit) events.
// Events may be delivered out of order (e.g. shuffled network packets);
// a bounded reorder buffer restores the deterministic log order before
// application, so every replica converges to identical state.
type Engine struct {
	numParts int
	parts    []map[string]string

	nextSeq uint64           // next log seq expected by the applier
	buf     map[uint64]Event // reorder buffer for out-of-order delivery
	maxBuf  int              // bound on reorder buffer size (memory bound)

	pending    map[string]*PendingTxn // prepared, not yet committed/aborted
	maxPending int                    // bound on concurrent prepared txns

	waiting map[string]Event // commits deferred on unresolved dependencies

	evidence []Evidence // auditable tie-break records
}

// NewEngine creates an engine with numParts partitions. maxBuf bounds the
// reorder buffer and maxPending bounds concurrently prepared transactions;
// both enforce bounded memory. Pass 0 for a sane default.
func NewEngine(numParts int, maxBuf, maxPending int) *Engine {
	if numParts <= 0 {
		numParts = 1
	}
	if maxBuf <= 0 {
		maxBuf = 1024
	}
	if maxPending <= 0 {
		maxPending = 1024
	}
	e := &Engine{
		numParts:   numParts,
		parts:      make([]map[string]string, numParts),
		nextSeq:    1,
		buf:        make(map[uint64]Event),
		maxBuf:     maxBuf,
		pending:    make(map[string]*PendingTxn),
		maxPending: maxPending,
		waiting:    make(map[string]Event),
	}
	for i := range e.parts {
		e.parts[i] = make(map[string]string)
	}
	return e
}

// NumParts returns the partition count.
func (e *Engine) NumParts() int { return e.numParts }

// Evidence returns the auditable tie-break records emitted so far.
func (e *Engine) Evidence() []Evidence {
	out := make([]Evidence, len(e.evidence))
	copy(out, e.evidence)
	return out
}

// Deliver feeds an event received from the (possibly unreliable, reordering)
// transport. Events are applied strictly in log sequence order; out-of-order
// events wait in the bounded reorder buffer. Returns an error if the buffer
// is full, which back-pressures the transport (memory stays bounded).
func (e *Engine) Deliver(ev Event) error {
	if ev.Seq < e.nextSeq {
		return nil // duplicate or already applied; idempotent
	}
	if ev.Seq == e.nextSeq {
		if err := e.apply(ev); err != nil {
			return err
		}
		e.nextSeq++
		return e.drain()
	}
	if len(e.buf) >= e.maxBuf {
		return fmt.Errorf("kvsm: reorder buffer full (%d), cannot accept seq %d", e.maxBuf, ev.Seq)
	}
	e.buf[ev.Seq] = ev
	return nil
}

// DeliverAll delivers events in the given (possibly shuffled) order.
func (e *Engine) DeliverAll(events []Event) error {
	for _, ev := range events {
		if err := e.Deliver(ev); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) drain() error {
	for {
		ev, ok := e.buf[e.nextSeq]
		if !ok {
			return nil
		}
		delete(e.buf, e.nextSeq)
		if err := e.apply(ev); err != nil {
			return err
		}
		e.nextSeq++
	}
}

// apply applies one event in deterministic log order.
func (e *Engine) apply(ev Event) error {
	for _, p := range ev.Parts {
		if p < 0 || p >= e.numParts {
			return fmt.Errorf("kvsm: event %d references bad partition %d", ev.Seq, p)
		}
	}
	switch ev.Kind {
	case KindPrepare:
		if _, dup := e.pending[ev.TxnID]; dup {
			return fmt.Errorf("kvsm: duplicate prepare for txn %q", ev.TxnID)
		}
		if len(e.pending) >= e.maxPending {
			return fmt.Errorf("kvsm: too many pending transactions (%d)", e.maxPending)
		}
		e.pending[ev.TxnID] = &PendingTxn{
			ID:         ev.TxnID,
			PrepareSeq: ev.Seq,
			Parts:      ev.Parts,
			Writes:     ev.Writes,
		}
	case KindCommit:
		return e.applyCommit(ev)
	case KindAbort:
		txn, ok := e.pending[ev.TxnID]
		if !ok {
			return fmt.Errorf("kvsm: abort for unknown txn %q", ev.TxnID)
		}
		delete(e.pending, ev.TxnID)
		// An abort resolves dependencies: waiters blocked on this txn may
		// now proceed. Record auditable evidence for each unblocked loser.
		e.recordAbortEvidence(txn)
		return e.drainWaiting()
	}
	return nil
}

// applyCommit commits a prepared transaction. If earlier-prepared conflicting
// transactions are still pending, the commit is deferred: the dependency
// graph (edges from earlier prepares to later conflicting prepares) is
// resolved by deterministic topological order, ties broken by (prepareSeq,
// txnID). Every deferral and resolution emits auditable evidence.
func (e *Engine) applyCommit(ev Event) error {
	txn, ok := e.pending[ev.TxnID]
	if !ok {
		return fmt.Errorf("kvsm: commit for unknown txn %q", ev.TxnID)
	}
	if blockers := e.blockersOf(txn); len(blockers) > 0 {
		e.waiting[ev.TxnID] = ev
		for _, b := range blockers {
			part, key, _ := txn.conflictsWith(b)
			e.evidence = append(e.evidence, Evidence{
				Rule:      "prepare-order-topo",
				Winner:    txn.ID, // later prepare wins once it applies
				Loser:     b.ID,
				Part:      part,
				Key:       key,
				WinnerSeq: txn.PrepareSeq,
				LoserSeq:  b.PrepareSeq,
				Action:    "defer-commit",
				Detail:    fmt.Sprintf("commit of %s deferred: %s prepared earlier (seq %d) and conflicts on part %d key %q", txn.ID, b.ID, b.PrepareSeq, part, key),
			})
		}
		return nil
	}
	e.commitWrites(txn)
	return e.drainWaiting()
}

// blockersOf returns pending transactions that prepared before txn and
// conflict with it, in deterministic (prepareSeq, txnID) order.
func (e *Engine) blockersOf(txn *PendingTxn) []*PendingTxn {
	var out []*PendingTxn
	for _, other := range e.pending {
		if other.ID == txn.ID || other.PrepareSeq >= txn.PrepareSeq {
			continue
		}
		if _, _, hit := txn.conflictsWith(other); hit {
			out = append(out, other)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].PrepareSeq != out[j].PrepareSeq {
			return out[i].PrepareSeq < out[j].PrepareSeq
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// drainWaiting repeatedly applies deferred commits whose dependencies have
// resolved, in deterministic topological order (prepareSeq, txnID).
func (e *Engine) drainWaiting() error {
	for {
		ids := make([]string, 0, len(e.waiting))
		for id := range e.waiting {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		progressed := false
		for _, id := range ids {
			txn, ok := e.pending[id]
			if !ok {
				delete(e.waiting, id)
				continue
			}
			if len(e.blockersOf(txn)) > 0 {
				continue
			}
			ev := e.waiting[id]
			delete(e.waiting, id)
			// Emit resolution evidence against the txns this one was
			// ordered after (they have committed by now).
			e.recordResolveEvidence(txn)
			e.commitWrites(txn)
			_ = ev
			progressed = true
		}
		if !progressed {
			return nil
		}
	}
}

// commitWrites applies a transaction's writes to its partitions and removes
// it from the pending set.
func (e *Engine) commitWrites(txn *PendingTxn) {
	for part, kvs := range txn.Writes {
		for k, v := range kvs {
			e.parts[part][k] = v
		}
	}
	delete(e.pending, txn.ID)
}

// recordResolveEvidence emits evidence that txn's deferred commit now takes
// effect, overwriting any earlier-prepared conflicting (already committed)
// transactions. The winner is the later prepare: deterministic and auditable.
func (e *Engine) recordResolveEvidence(txn *PendingTxn) {
	for _, ev := range e.evidence {
		if ev.Winner == txn.ID && ev.Action == "defer-commit" {
			e.evidence = append(e.evidence, Evidence{
				Rule:      ev.Rule,
				Winner:    txn.ID,
				Loser:     ev.Loser,
				Part:      ev.Part,
				Key:       ev.Key,
				WinnerSeq: txn.PrepareSeq,
				LoserSeq:  ev.LoserSeq,
				Action:    "apply-after-resolve",
				Detail:    fmt.Sprintf("commit of %s applied after %s resolved; %s wins part %d key %q by later prepare (seq %d > %d)", txn.ID, ev.Loser, txn.ID, ev.Part, ev.Key, txn.PrepareSeq, ev.LoserSeq),
			})
		}
	}
}

// recordAbortEvidence emits evidence for waiters unblocked by an abort: the
// aborted txn's writes are discarded, so the waiting txn wins the key.
func (e *Engine) recordAbortEvidence(aborted *PendingTxn) {
	ids := make([]string, 0, len(e.waiting))
	for id := range e.waiting {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		txn, ok := e.pending[id]
		if !ok {
			continue
		}
		part, key, hit := txn.conflictsWith(aborted)
		if !hit {
			continue
		}
		e.evidence = append(e.evidence, Evidence{
			Rule:      "prepare-order-topo",
			Winner:    txn.ID,
			Loser:     aborted.ID,
			Part:      part,
			Key:       key,
			WinnerSeq: txn.PrepareSeq,
			LoserSeq:  aborted.PrepareSeq,
			Action:    "abort-loser",
			Detail:    fmt.Sprintf("%s aborted; waiting commit of %s wins part %d key %q", aborted.ID, txn.ID, part, key),
		})
	}
}

// Get returns the value of key on partition part.
func (e *Engine) Get(part int, key string) (string, bool) {
	v, ok := e.parts[part][key]
	return v, ok
}

// State returns a deterministic string-keyed snapshot of all partitions:
// "part/key" -> value. Used to compare replicas for identical final state.
func (e *Engine) State() map[string]string {
	out := make(map[string]string)
	for p, kvs := range e.parts {
		for k, v := range kvs {
			out[fmt.Sprintf("%d/%s", p, k)] = v
		}
	}
	return out
}

// PendingCount reports the number of prepared, uncommitted transactions.
func (e *Engine) PendingCount() int { return len(e.pending) }

// BufferedCount reports the reorder-buffer occupancy.
func (e *Engine) BufferedCount() int { return len(e.buf) }
