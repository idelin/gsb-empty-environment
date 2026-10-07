package idemexec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	// ErrEmptyKey is returned when Do is called without an idempotency key.
	ErrEmptyKey = errors.New("idemexec: empty idempotency key")
	// ErrParamsMismatch is returned when a key is reused with parameters
	// that differ from the ones first committed under that key.
	ErrParamsMismatch = errors.New("idemexec: idempotency key reused with different parameters")
	// ErrTombstoned is returned when the key's result has expired: the
	// tombstone permanently forbids re-execution.
	ErrTombstoned = errors.New("idemexec: idempotency key expired; replay permanently forbidden")
)

// EffectFunc performs the business effect. Everything it stages on the Tx
// commits atomically with the idempotency record holding its result, so a
// crash can never persist one without the other. If EffectFunc returns an
// error, nothing is persisted and the key remains free for a retry.
type EffectFunc func(tx Tx) ([]byte, error)

// Executor executes requests idempotently against a Store.
type Executor struct {
	store Store
	ttl   time.Duration
	now   func() time.Time
}

// NewExecutor returns an Executor. resultTTL is how long a committed
// result is replayable; once it lapses the record becomes a tombstone.
// A resultTTL <= 0 means results never expire.
func NewExecutor(store Store, resultTTL time.Duration) *Executor {
	return &Executor{store: store, ttl: resultTTL, now: time.Now}
}

// SetClock overrides the time source (for tests and simulation).
func (e *Executor) SetClock(now func() time.Time) { e.now = now }

func fingerprint(params []byte) string {
	sum := sha256.Sum256(params)
	return hex.EncodeToString(sum[:])
}

func (e *Executor) expired(rec *Record) bool {
	return !rec.ExpiresAt.IsZero() && !e.now().Before(rec.ExpiresAt)
}

// Do runs fn exactly once for the given key and parameters.
//
//   - First call for a key: fn executes inside the commit transaction and
//     its result is stored atomically with its staged effects.
//   - Duplicate call, same parameters, result still valid: the cached
//     result is returned and fn does not run.
//   - Duplicate call, different parameters: ErrParamsMismatch.
//   - Call after the result expired: ErrTombstoned; fn never runs again.
func (e *Executor) Do(key string, params []byte, fn EffectFunc) ([]byte, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	fp := fingerprint(params)
	var result []byte
	var outcome error
	err := e.store.Update(func(tx Tx) error {
		if rec, ok := tx.GetRecord(key); ok {
			if rec.Fingerprint != fp {
				outcome = ErrParamsMismatch
				return nil
			}
			if rec.State == StateTombstone || e.expired(rec) {
				if rec.State != StateTombstone {
					// Lazily convert the expired record: drop the
					// result payload, keep the key+fingerprint
					// forever as a non-replayable tombstone. The
					// conversion must commit even though the call
					// itself is rejected.
					rec.State = StateTombstone
					rec.Result = nil
					tx.PutRecord(rec)
				}
				outcome = ErrTombstoned
				return nil
			}
			result = append([]byte(nil), rec.Result...)
			return nil
		}
		res, err := fn(tx)
		if err != nil {
			// Business failure aborts the transaction: nothing is
			// persisted and the key stays free for a retry.
			return err
		}
		rec := &Record{
			Key:         key,
			Fingerprint: fp,
			State:       StateCommitted,
			Result:      append([]byte(nil), res...),
		}
		if e.ttl > 0 {
			rec.ExpiresAt = e.now().Add(e.ttl)
		}
		tx.PutRecord(rec)
		result = append([]byte(nil), res...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, outcome
}

// Sweep converts every expired committed record into a tombstone, dropping
// its result payload. Tombstones themselves are never deleted, so a swept
// key can never be re-executed. It returns the number of records swept.
func (e *Executor) Sweep() (int, error) {
	swept := 0
	err := e.store.Update(func(tx Tx) error {
		tx.RangeRecords(func(rec *Record) bool {
			if rec.State == StateCommitted && e.expired(rec) {
				rec.State = StateTombstone
				rec.Result = nil
				tx.PutRecord(rec)
				swept++
			}
			return true
		})
		return nil
	})
	if err != nil {
		return 0, err
	}
	return swept, nil
}

// Lookup returns the durable record for key, for inspection and tests.
func (e *Executor) Lookup(key string) (*Record, bool, error) {
	var rec *Record
	found := false
	err := e.store.View(func(tx Tx) error {
		rec, found = tx.GetRecord(key)
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return rec, found, nil
}
