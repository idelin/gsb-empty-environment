// Package idemexec executes requests idempotently: the business effect and
// its result are committed atomically under an idempotency key, duplicate
// keys with different parameters are rejected, and expired results leave a
// non-replayable tombstone behind.
package idemexec

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// State is the lifecycle state of an idempotency record.
type State int

const (
	// StateCommitted means the effect+result pair is durable and the
	// result may be replayed to the caller until it expires.
	StateCommitted State = iota
	// StateTombstone means the result has expired and been dropped.
	// The record is kept forever so the key can never be re-executed.
	StateTombstone
)

// Record is the durable idempotency entry for one key.
type Record struct {
	Key         string
	Fingerprint string // hash of the canonical parameters
	State       State
	Result      []byte // nil once tombstoned
	ExpiresAt   time.Time
}

func cloneRecord(r *Record) *Record {
	c := *r
	if r.Result != nil {
		c.Result = append([]byte(nil), r.Result...)
	}
	return &c
}

// Tx stages reads and writes against the store. Everything staged on a Tx
// — business effects and the idempotency record alike — is committed
// atomically by Store.Update.
type Tx interface {
	GetRecord(key string) (*Record, bool)
	PutRecord(rec *Record)
	RangeRecords(fn func(rec *Record) bool)
	EffectGet(table, key string) ([]byte, bool)
	EffectPut(table, key string, val []byte)
}

// Store runs transactional batches with an atomic commit.
type Store interface {
	Update(fn func(tx Tx) error) error
	View(fn func(tx Tx) error) error
}

// CutPoint identifies a crashable point on the persistence path.
type CutPoint int

const (
	// CutBeforeApply is reached with staged writes not yet durable.
	CutBeforeApply CutPoint = iota
	// CutAfterApply is reached with writes durable but the receipt not
	// yet returned to the caller — the "lost receipt" window.
	CutAfterApply
)

func (c CutPoint) String() string {
	switch c {
	case CutBeforeApply:
		return "before-apply"
	case CutAfterApply:
		return "after-apply"
	}
	return "unknown"
}

// ErrCrash is the (recovered) signal of a simulated crash. It never
// escapes the library; Injector panics with a crashPanic value instead.
var ErrCrash = errors.New("idemexec: simulated crash at persistence cut point")

type crashPanic struct{ point CutPoint }

// IsCrash reports whether a recovered panic value is a simulated crash
// injected at a persistence cut point.
func IsCrash(v interface{}) bool {
	_, ok := v.(crashPanic)
	return ok
}

// Injector decides when the store simulates a crash. It is armed with a
// 1-based cut-point hit index; when that hit is reached the store panics
// with a crashPanic and the Injector disarms itself.
type Injector struct {
	mu     sync.Mutex
	target int
	hits   int
}

// NewInjector returns a disarmed Injector.
func NewInjector() *Injector { return &Injector{} }

// Arm resets the hit counter and crashes on the target-th cut-point hit.
// A target <= 0 disarms the injector.
func (inj *Injector) Arm(target int) {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	inj.target = target
	inj.hits = 0
}

// Hits returns how many cut points have been reached since the last Arm.
func (inj *Injector) Hits() int {
	inj.mu.Lock()
	defer inj.mu.Unlock()
	return inj.hits
}

func (inj *Injector) hit(point CutPoint) {
	if inj == nil {
		return
	}
	inj.mu.Lock()
	inj.hits++
	fire := inj.target > 0 && inj.hits == inj.target
	if fire {
		inj.target = 0
	}
	inj.mu.Unlock()
	if fire {
		panic(crashPanic{point: point})
	}
}

// durable is the state that survives a process restart.
type durable struct {
	records map[string]*Record
	tables  map[string]map[string][]byte
}

// MemStore is an in-memory Store with an atomic commit and crashable
// persistence cut points. It models a database: one MemStore instance is
// one "process"; Restart builds a fresh process over the same durable
// state.
type MemStore struct {
	mu  sync.Mutex
	inj *Injector
	d   *durable
}

// NewMemStore returns a store with empty durable state. inj may be nil.
func NewMemStore(inj *Injector) *MemStore {
	return &MemStore{
		inj: inj,
		d: &durable{
			records: make(map[string]*Record),
			tables:  make(map[string]map[string][]byte),
		},
	}
}

// Restart models a process restart: durable state survives, in-memory
// latches do not.
func (s *MemStore) Restart() *MemStore {
	return &MemStore{inj: s.inj, d: s.d}
}

// Update runs fn and atomically commits everything it staged. If fn
// returns an error, nothing is applied.
func (s *MemStore) Update(fn func(tx Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx := &memTx{
		d:       s.d,
		records: make(map[string]*Record),
		tables:  make(map[string]map[string][]byte),
	}
	if err := fn(tx); err != nil {
		return err
	}
	if len(tx.records) == 0 && len(tx.tables) == 0 {
		return nil
	}
	s.inj.hit(CutBeforeApply)
	for k, r := range tx.records {
		s.d.records[k] = cloneRecord(r)
	}
	for table, writes := range tx.tables {
		dst := s.d.tables[table]
		if dst == nil {
			dst = make(map[string][]byte)
			s.d.tables[table] = dst
		}
		for k, v := range writes {
			dst[k] = append([]byte(nil), v...)
		}
	}
	s.inj.hit(CutAfterApply)
	return nil
}

// View runs fn against a read-only snapshot of the durable state.
func (s *MemStore) View(fn func(tx Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn(&memTx{d: s.d})
}

type memTx struct {
	d       *durable
	records map[string]*Record           // nil => read-only
	tables  map[string]map[string][]byte // nil => read-only
}

func (tx *memTx) GetRecord(key string) (*Record, bool) {
	if r, ok := tx.records[key]; ok {
		return cloneRecord(r), true
	}
	if r, ok := tx.d.records[key]; ok {
		return cloneRecord(r), true
	}
	return nil, false
}

func (tx *memTx) PutRecord(rec *Record) {
	if tx.records == nil {
		panic("idemexec: PutRecord on read-only tx")
	}
	tx.records[rec.Key] = cloneRecord(rec)
}

func (tx *memTx) RangeRecords(fn func(rec *Record) bool) {
	keys := make([]string, 0, len(tx.d.records))
	for k := range tx.d.records {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !fn(cloneRecord(tx.d.records[k])) {
			return
		}
	}
}

func (tx *memTx) EffectGet(table, key string) ([]byte, bool) {
	if writes, ok := tx.tables[table]; ok {
		if v, ok := writes[key]; ok {
			return append([]byte(nil), v...), true
		}
	}
	if t, ok := tx.d.tables[table]; ok {
		if v, ok := t[key]; ok {
			return append([]byte(nil), v...), true
		}
	}
	return nil, false
}

func (tx *memTx) EffectPut(table, key string, val []byte) {
	if tx.tables == nil {
		panic("idemexec: EffectPut on read-only tx")
	}
	writes := tx.tables[table]
	if writes == nil {
		writes = make(map[string][]byte)
		tx.tables[table] = writes
	}
	writes[key] = append([]byte(nil), val...)
}
