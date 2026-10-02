package ss2pl

import (
	"fmt"
	"sync"
)

// Monitor observes lock-manager and transaction-manager events and checks
// the central strict-2PL invariant: no lock of a transaction may be
// released before that transaction has reached its commit/abort decision.
type Monitor struct {
	mu         sync.Mutex
	decided    map[int]bool // txnID -> commit/abort decision recorded
	violations []string
	log        []string
}

// NewMonitor attaches a monitor to the given managers.
func NewMonitor(tm *TxnManager, lm *LockManager) *Monitor {
	m := &Monitor{decided: make(map[int]bool)}
	lm.SetHook(m.onLockEvent)
	tm.SetHook(m.onTxnEvent)
	return m
}

func (m *Monitor) onLockEvent(ev LockEvent) {
	kind := "ACQUIRE"
	if ev.Kind == EventRelease {
		kind = "RELEASE"
	}
	m.mu.Lock()
	if ev.Kind == EventRelease && !m.decided[ev.TxnID] {
		m.violations = append(m.violations, fmt.Sprintf(
			"txn %d released %s-lock on %q before its commit/abort decision",
			ev.TxnID, ev.Mode, ev.Resource))
	}
	m.log = append(m.log, fmt.Sprintf("lock  %-7s txn=%d %s %q", kind, ev.TxnID, ev.Mode, ev.Resource))
	m.mu.Unlock()
}

func (m *Monitor) onTxnEvent(ev TxnEvent) {
	var text string
	m.mu.Lock()
	switch ev.Kind {
	case TxnEventBegin:
		text = "BEGIN"
	case TxnEventCommit:
		m.decided[ev.TxnID] = true
		text = "COMMIT"
	case TxnEventAbort:
		m.decided[ev.TxnID] = true
		text = "ABORT"
	case TxnEventCrash:
		text = "CRASH"
	}
	m.log = append(m.log, fmt.Sprintf("txn   %-7s txn=%d", text, ev.TxnID))
	m.mu.Unlock()
}

// Violations returns every observed breach of the strict-2PL invariant.
// An empty result means no lock was ever released before its transaction
// committed or aborted.
func (m *Monitor) Violations() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.violations))
	copy(out, m.violations)
	return out
}

// Log returns the full chronological event trace.
func (m *Monitor) Log() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.log))
	copy(out, m.log)
	return out
}
