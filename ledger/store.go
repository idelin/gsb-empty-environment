package ledger

import (
	"fmt"
	"sync"
)

const maxCommitRetries = 64

// accountState is the mutable, versioned per-account aggregate: balances
// plus the freezes it owns. All mutations happen on a cloned snapshot and
// are committed by compare-and-swap on Version.
type accountState struct {
	account *Account
	freezes map[string]*Freeze
}

func (s *accountState) clone() *accountState {
	c := &accountState{
		account: s.account.clone(),
		freezes: make(map[string]*Freeze, len(s.freezes)),
	}
	for id, f := range s.freezes {
		fc := *f
		c.freezes[id] = &fc
	}
	return c
}

// inflight deduplicates concurrent submissions of the same request id so a
// fault replay shares one execution and one receipt (singleflight).
type inflight struct {
	done chan struct{}
	rec  Receipt
	err  error
}

// Store is the ledger. It is safe for concurrent use.
type Store struct {
	mu        sync.Mutex // guards commit check-and-swap and counters
	accounts  map[string]*accountState
	settled   map[string]Money // per-currency amount settled out of the system
	supply    map[string]Money // per-currency total ever deposited
	seq       uint64
	freezeSeq uint64

	rmu      sync.Mutex
	receipts map[string]Receipt
	pending  map[string]*inflight
}

// NewStore returns an empty ledger.
func NewStore() *Store {
	return &Store{
		accounts: make(map[string]*accountState),
		settled:  make(map[string]Money),
		supply:   make(map[string]Money),
		receipts: make(map[string]Receipt),
		pending:  make(map[string]*inflight),
	}
}

// CreateAccount registers an account with zero balances.
func (s *Store) CreateAccount(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[id]; ok {
		return ErrAccountExists
	}
	s.accounts[id] = &accountState{
		account: &Account{ID: id, Available: map[string]Money{}, Frozen: map[string]Money{}},
		freezes: map[string]*Freeze{},
	}
	return nil
}

// Account returns a snapshot copy of an account.
func (s *Store) Account(id string) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.accounts[id]
	if !ok {
		return nil, ErrAccountNotFound
	}
	return st.account.clone(), nil
}

// GetFreeze returns a snapshot copy of a freeze owned by an account.
func (s *Store) GetFreeze(accountID, freezeID string) (*Freeze, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.accounts[accountID]
	if !ok {
		return nil, ErrAccountNotFound
	}
	f, ok := st.freezes[freezeID]
	if !ok {
		return nil, ErrFreezeNotFound
	}
	fc := *f
	return &fc, nil
}

// snapshot reads the current state and its commit version.
func (s *Store) snapshot(accountID string) (*accountState, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.accounts[accountID]
	if !ok {
		return nil, 0, ErrAccountNotFound
	}
	return st.clone(), st.account.Version, nil
}

// effects carries per-currency ledger movements produced by a mutation:
// settled amounts leave the frozen bucket for good, supply amounts enter
// the system via deposits. Both fold into the conservation model atomically
// with the balance commit.
type effects struct {
	settled map[string]Money
	supply  map[string]Money
}

func newEffects() *effects {
	return &effects{settled: map[string]Money{}, supply: map[string]Money{}}
}

// commit applies staged per-account states iff every expected version still
// matches. It is the single adjudication point: concurrent settle/release
// (or batch) commits race here and exactly one wins per version.
func (s *Store) commit(staged map[string]*accountState, expected map[string]uint64, fx *effects) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range staged {
		cur, ok := s.accounts[id]
		if !ok {
			return 0, ErrAccountNotFound
		}
		if cur.account.Version != expected[id] {
			return 0, ErrVersionConflict
		}
		_ = st
	}
	s.seq++
	for id, st := range staged {
		st.account.Version++
		for _, f := range st.freezes {
			f.Version = st.account.Version
		}
		s.accounts[id] = st
	}
	for cur, d := range fx.settled {
		s.settled[cur] += d
	}
	for cur, d := range fx.supply {
		s.supply[cur] += d
	}
	return s.seq, nil
}

// mutateAccount runs fn against a fresh snapshot and commits it, retrying
// while races are detected. fn may record per-currency ledger movements into
// fx, which the commit folds in atomically. Deterministic validation errors
// (insufficient funds, closed freeze, ...) abort at once.
func (s *Store) mutateAccount(accountID string, fn func(st *accountState, fx *effects) error) (uint64, error) {
	for attempt := 0; attempt < maxCommitRetries; attempt++ {
		st, ver, err := s.snapshot(accountID)
		if err != nil {
			return 0, err
		}
		fx := newEffects()
		if err := fn(st, fx); err != nil {
			return 0, err
		}
		seq, err := s.commit(map[string]*accountState{accountID: st}, map[string]uint64{accountID: ver}, fx)
		if err == ErrVersionConflict {
			continue
		}
		if err != nil {
			return 0, err
		}
		return seq, nil
	}
	return 0, ErrVersionConflict
}

// withReceipt executes op at most once per request id. Replays — including
// concurrent duplicates racing after a fault — observe the same receipt.
func (s *Store) withReceipt(reqID string, op func() (Receipt, error)) (Receipt, error) {
	if reqID == "" {
		return Receipt{}, ErrEmptyRequestID
	}
	s.rmu.Lock()
	if rec, ok := s.receipts[reqID]; ok {
		s.rmu.Unlock()
		return rec, nil
	}
	if f, ok := s.pending[reqID]; ok {
		s.rmu.Unlock()
		<-f.done
		return f.rec, f.err
	}
	f := &inflight{done: make(chan struct{})}
	s.pending[reqID] = f
	s.rmu.Unlock()

	rec, err := op()

	s.rmu.Lock()
	if err == nil {
		s.receipts[reqID] = rec
	}
	delete(s.pending, reqID)
	f.rec, f.err = rec, err
	close(f.done)
	s.rmu.Unlock()
	return rec, err
}

// CheckConservation verifies the per-currency conservation model:
//
//	supply[c] == sum_accounts(available[c] + frozen[c]) + settled[c]
//
// It returns a descriptive error naming the first violated currency.
func (s *Store) CheckConservation() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	held := map[string]Money{}
	for _, st := range s.accounts {
		for c, v := range st.account.Available {
			held[c] += v
		}
		for c, v := range st.account.Frozen {
			held[c] += v
		}
	}
	currencies := map[string]struct{}{}
	for c := range s.supply {
		currencies[c] = struct{}{}
	}
	for c := range held {
		currencies[c] = struct{}{}
	}
	for c := range s.settled {
		currencies[c] = struct{}{}
	}
	for c := range currencies {
		if held[c]+s.settled[c] != s.supply[c] {
			return fmt.Errorf("ledger: conservation violated for %s: held=%d settled=%d supply=%d",
				c, held[c], s.settled[c], s.supply[c])
		}
	}
	return nil
}

// Settled reports how much of a currency has been settled out of the system.
func (s *Store) Settled(currency string) Money {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settled[currency]
}
