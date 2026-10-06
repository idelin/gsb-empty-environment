package dedup

import (
	"fmt"
	"sync"
)

// EventKind classifies ledger entries.
type EventKind int

const (
	EventAcquire EventKind = iota
	EventRelease
	EventEvictResult
	EventEvictTombstone
)

func (k EventKind) String() string {
	switch k {
	case EventAcquire:
		return "acquire"
	case EventRelease:
		return "release"
	case EventEvictResult:
		return "evict-result"
	case EventEvictTombstone:
		return "evict-tombstone"
	default:
		return "unknown"
	}
}

// Event is one ledger entry: an acquire, release, or eviction.
type Event struct {
	Kind EventKind
	Key  string
	// RefsAfter is the outstanding reference count for Key after the
	// event. For evictions it is the reference count at eviction time.
	RefsAfter int
}

// Ledger is the reference account （引用账） of the store. It tracks the
// outstanding reference balance per key and an ordered audit trail of
// acquires, releases and evictions, so cleanup can be proven safe:
// a result evicted while still referenced would strand its holders and,
// once its tombstone is also gone, make the key resolve unknown and
// force the business operation to re-execute.
type Ledger struct {
	mu       sync.Mutex
	balances map[string]int
	events   []Event
}

// NewLedger creates an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{balances: make(map[string]int)}
}

func (l *Ledger) acquire(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[key]++
	l.events = append(l.events, Event{Kind: EventAcquire, Key: key, RefsAfter: l.balances[key]})
}

func (l *Ledger) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.balances[key]--
	l.events = append(l.events, Event{Kind: EventRelease, Key: key, RefsAfter: l.balances[key]})
}

func (l *Ledger) evictResult(key string, r *Result) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, Event{Kind: EventEvictResult, Key: key, RefsAfter: r.refs})
}

func (l *Ledger) evictTombstone(key string, t *Tombstone) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, Event{Kind: EventEvictTombstone, Key: key, RefsAfter: l.balances[key]})
}

// Outstanding returns the live reference balance for key.
func (l *Ledger) Outstanding(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.balances[key]
}

// Events returns a copy of the audit trail.
func (l *Ledger) Events() []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Event, len(l.events))
	copy(out, l.events)
	return out
}

// Verify audits the ledger and proves cleanup cannot trigger business
// re-execution. It fails if:
//
//   - any key's reference balance went negative or ended non-zero after
//     a result eviction (a release was lost, or a result was collected
//     under a live reference);
//   - any result was evicted with outstanding references;
//   - any key's tombstone was evicted while references were outstanding
//     (holders would lose their duplicate guard).
func (l *Ledger) Verify() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	balance := make(map[string]int)
	evictedResult := make(map[string]bool)
	for i, ev := range l.events {
		switch ev.Kind {
		case EventAcquire:
			balance[ev.Key]++
		case EventRelease:
			balance[ev.Key]--
			if balance[ev.Key] < 0 {
				return fmt.Errorf("ledger: event %d: key %q released more than acquired", i, ev.Key)
			}
		case EventEvictResult:
			if ev.RefsAfter != 0 || balance[ev.Key] != 0 {
				return fmt.Errorf("ledger: event %d: result %q evicted with %d live references", i, ev.Key, balance[ev.Key])
			}
			evictedResult[ev.Key] = true
		case EventEvictTombstone:
			if balance[ev.Key] != 0 {
				return fmt.Errorf("ledger: event %d: tombstone %q evicted with %d live references", i, ev.Key, balance[ev.Key])
			}
		}
	}
	for key, n := range balance {
		if n != 0 && evictedResult[key] {
			return fmt.Errorf("ledger: key %q has %d unbalanced references after its result was evicted", key, n)
		}
	}
	return nil
}
