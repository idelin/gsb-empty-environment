// Package arbiter implements a multi-node conflict arbiter: many competing
// execution bodies (nodes) race to commit values into a single linearizable
// register, and the arbiter serializes them with fencing tokens.
//
// Guarantees:
//   - Atomicity: every Propose/Commit is an indivisible state transition.
//   - Linearizability: the committed log is a total order; a commit is
//     accepted only while its fencing token (epoch) is still the latest.
//   - Deadlock-free: a single mutex, never held across blocking calls, no
//     lock ordering, no condition variables, no goroutines spawned.
//   - Race-free: all shared state is touched only under the mutex.
package arbiter

import (
	"errors"
	"fmt"
	"sync"
)

// ErrStale is returned by Commit when the presented epoch is no longer the
// arbiter's current epoch: a newer proposal has superseded it.
var ErrStale = errors.New("arbiter: stale fencing token")

// ErrNotHolder is returned by Commit when the node presenting the current
// epoch is not the node that epoch was granted to.
var ErrNotHolder = errors.New("arbiter: epoch held by another node")

// CommitError describes a rejected commit. Kind is one of the package's
// sentinel errors, allowing callers to classify failures.
type CommitError struct {
	Kind    error  // ErrStale or ErrNotHolder
	Epoch   uint64 // epoch presented by the caller
	Current uint64 // epoch held by the arbiter at rejection time
	Holder  int    // node the current epoch belongs to
}

func (e *CommitError) Error() string {
	return fmt.Sprintf("%v: presented epoch %d, current epoch %d held by node %d",
		e.Kind, e.Epoch, e.Current, e.Holder)
}

// Cause returns the sentinel classifying this failure (ErrStale or
// ErrNotHolder), compatible with errors unwrapping helpers.
func (e *CommitError) Cause() error { return e.Kind }

// Entry is one committed value in the arbiter's totally ordered log.
type Entry struct {
	Epoch uint64
	Node  int
	Value interface{}
}

// Stats summarizes arbitration outcomes.
type Stats struct {
	Proposals uint64 // epochs granted
	Commits   uint64 // successful commits
	StaleWins uint64 // commits rejected because a newer epoch existed
	WrongNode uint64 // commits rejected because of holder mismatch
}

// Arbiter serializes competing nodes. It is safe for concurrent use.
type Arbiter struct {
	mu     sync.Mutex
	epoch  uint64 // highest granted epoch; also the live fencing token
	holder int    // node the current epoch was granted to; -1 before first grant
	log    []Entry
	stats  Stats
}

// New returns an Arbiter with no holder and epoch 0.
func New() *Arbiter {
	return &Arbiter{holder: -1}
}

// Propose grants the calling node a new, strictly greater epoch and returns
// it as a fencing token. Granting invalidates every previously granted
// token: any in-flight commit carrying an older epoch will be rejected.
//
// Propose always succeeds; contention is resolved at Commit time, so
// competitors can never block each other (no deadlock, no livelock at the
// arbiter level).
func (a *Arbiter) Propose(node int) (epoch uint64) {
	a.mu.Lock()
	a.epoch++
	a.holder = node
	a.stats.Proposals++
	epoch = a.epoch
	a.mu.Unlock()
	return epoch
}

// Commit attempts to append value to the log under the given fencing token.
// It succeeds iff epoch is still the current epoch and was granted to node.
// The check and the append are one atomic transition, which is what makes
// the log linearizable.
func (a *Arbiter) Commit(epoch uint64, node int, value interface{}) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if epoch != a.epoch {
		a.stats.StaleWins++
		return &CommitError{Kind: ErrStale, Epoch: epoch, Current: a.epoch, Holder: a.holder}
	}
	if node != a.holder {
		a.stats.WrongNode++
		return &CommitError{Kind: ErrNotHolder, Epoch: epoch, Current: a.epoch, Holder: a.holder}
	}
	a.log = append(a.log, Entry{Epoch: epoch, Node: node, Value: value})
	a.stats.Commits++
	return nil
}

// Epoch returns the current epoch (the live fencing token).
func (a *Arbiter) Epoch() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.epoch
}

// Log returns a copy of the committed entries in commit order.
func (a *Arbiter) Log() []Entry {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Entry, len(a.log))
	copy(out, a.log)
	return out
}

// Stats returns a snapshot of arbitration counters.
func (a *Arbiter) Stats() Stats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats
}
