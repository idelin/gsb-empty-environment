package cache

import "sync"

// entry is a cached value plus its version.
type entry struct {
	value   string
	version uint64
}

// L2 is the shared second-level cache. It is deliberately simple and
// in-memory; failures are injectable so degradation rules can be tested
// without any remote dependency.
type L2 struct {
	mu   sync.Mutex
	data map[string]entry
	gets int
	// GetErr/SetErr, when non-nil, are consulted on every operation;
	// returning a non-nil error simulates an L2 failure.
	GetErr func(key string) error
	SetErr func(key string) error
}

// NewL2 returns an empty L2.
func NewL2() *L2 { return &L2{data: map[string]entry{}} }

func (l *L2) get(key string) (entry, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gets++
	if l.GetErr != nil {
		if err := l.GetErr(key); err != nil {
			return entry{}, false, err
		}
	}
	e, ok := l.data[key]
	return e, ok, nil
}

// set stores e, rejecting version rollback.
func (l *L2) set(key string, e entry) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.SetErr != nil {
		if err := l.SetErr(key); err != nil {
			return err
		}
	}
	if cur, ok := l.data[key]; ok && e.version < cur.version {
		return ErrVersionRollback
	}
	l.data[key] = e
	return nil
}

// Gets reports how many get calls L2 has served (used to verify that
// strong reads within the validation TTL do not touch L2).
func (l *L2) Gets() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gets
}
