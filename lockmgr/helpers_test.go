package lockmgr

import (
	"errors"
	"testing"
	"time"
)

func testResources() (Resource, Resource, Resource, Resource) {
	db := NewDB("db1")
	table := NewTable("db1", "t1")
	page := NewPage("db1", "t1", "p1")
	row := NewRow("db1", "t1", "p1", "r1")
	return db, table, page, row
}

// newManager returns a manager whose transactions are all aborted when the
// test finishes, preventing blocked Lock goroutines from leaking across tests.
func newManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	t.Cleanup(func() {
		m.mu.Lock()
		ids := make([]uint64, 0, len(m.txns))
		for id := range m.txns {
			ids = append(ids, id)
		}
		m.mu.Unlock()
		for _, id := range ids {
			m.mu.Lock()
			txn, ok := m.txns[id]
			m.mu.Unlock()
			if ok {
				_ = m.Abort(txn)
			}
		}
	})
	return m
}

// lockResult receives the outcome of an asynchronous Lock call.
type lockResult struct {
	err error
}

func asyncLock(m *Manager, t *Transaction, r Resource, mode LockMode) <-chan lockResult {
	ch := make(chan lockResult, 1)
	go func() {
		ch <- lockResult{err: m.Lock(t, r, mode)}
	}()
	return ch
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	if !cond() {
		t.Fatalf("condition not met within %v", timeout)
	}
}

func mustLock(t *testing.T, m *Manager, txn *Transaction, r Resource, mode LockMode) {
	t.Helper()
	if err := m.Lock(txn, r, mode); err != nil {
		t.Fatalf("unexpected Lock(%s on %d) error: %v", mode, r.Level, err)
	}
}

func isDeadlock(err error) bool {
	return errors.Is(err, ErrDeadlockRollback)
}

// collectResults drains the given asynchronous Lock channels, mapping each
// result to its transaction id positionally.
func collectResults(t *testing.T, chs ...<-chan lockResult) map[int]error {
	t.Helper()
	out := make(map[int]error, len(chs))
	for i, ch := range chs {
		select {
		case res := <-ch:
			out[i] = res.err
		case <-time.After(3 * time.Second):
			t.Fatalf("result %d of %d not delivered", i+1, len(chs))
		}
	}
	return out
}

// victimResult returns the deadlock error and the 0-based index of the victim
// among the collected results.
func victimResult(results map[int]error) (error, int) {
	for i, err := range results {
		if isDeadlock(err) {
			return err, i
		}
	}
	return nil, -1
}

// acquireHierarchy grants the intention locks needed to reach row level.
func acquireHierarchy(t *testing.T, m *Manager, txn *Transaction, db, table, page Resource, mode LockMode) {
	t.Helper()
	parent := minParentMode(mode)
	mustLock(t, m, txn, db, parent)
	mustLock(t, m, txn, table, parent)
	mustLock(t, m, txn, page, parent)
}
