package migsim

import (
	"errors"
	"sync"
)

// Op is the kind of reference operation a receipt records.
type Op int

const (
	OpAddRef Op = iota
	OpDelRef
)

// Receipt is the durable record of one reference operation. Replays are
// deduplicated by sequence number, so re-delivering an old receipt is a
// no-op and can never double-count a reference.
type Receipt struct {
	Seq    uint64
	Object string
	Block  string
	Op     Op
}

// ErrNoSuchRef is returned when deleting a reference that does not exist.
var ErrNoSuchRef = errors.New("migsim: no such reference")

// Ledger is the independent book of object -> block references. It holds no
// block internals; Verify cross-checks it against the physical generations.
type Ledger struct {
	mu      sync.Mutex
	seq     uint64
	applied map[uint64]struct{}
	refs    map[string]map[string]int // object -> block -> count
}

// NewLedger returns an empty ledger.
func NewLedger() *Ledger {
	return &Ledger{
		applied: make(map[uint64]struct{}),
		refs:    make(map[string]map[string]int),
	}
}

// AddRef records a new reference from obj to blk and returns its receipt.
func (l *Ledger) AddRef(obj, blk string) Receipt {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	r := Receipt{Seq: l.seq, Object: obj, Block: blk, Op: OpAddRef}
	l.applyLocked(r)
	return r
}

// DelRef removes one reference from obj to blk and returns its receipt.
func (l *Ledger) DelRef(obj, blk string) (Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refs[obj][blk] <= 0 {
		return Receipt{}, ErrNoSuchRef
	}
	l.seq++
	r := Receipt{Seq: l.seq, Object: obj, Block: blk, Op: OpDelRef}
	l.applyLocked(r)
	return r, nil
}

// Replay re-delivers a receipt. Already-applied receipts are ignored and
// Replay reports false; the ledger state is left untouched.
func (l *Ledger) Replay(r Receipt) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, seen := l.applied[r.Seq]; seen {
		return false
	}
	l.applyLocked(r)
	return true
}

func (l *Ledger) applyLocked(r Receipt) {
	l.applied[r.Seq] = struct{}{}
	switch r.Op {
	case OpAddRef:
		m := l.refs[r.Object]
		if m == nil {
			m = make(map[string]int)
			l.refs[r.Object] = m
		}
		m[r.Block]++
	case OpDelRef:
		if m := l.refs[r.Object]; m != nil && m[r.Block] > 0 {
			m[r.Block]--
			if m[r.Block] == 0 {
				delete(m, r.Block)
			}
		}
	}
}

// RefCount returns the live reference count of obj on blk.
func (l *Ledger) RefCount(obj, blk string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.refs[obj][blk]
}

// Snapshot returns a deep copy of the live reference table.
func (l *Ledger) Snapshot() map[string]map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make(map[string]map[string]int, len(l.refs))
	for obj, m := range l.refs {
		cp := make(map[string]int, len(m))
		for blk, n := range m {
			cp[blk] = n
		}
		out[obj] = cp
	}
	return out
}
