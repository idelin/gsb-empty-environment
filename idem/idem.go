// Package idem provides idempotent execution of requests: results are cached
// by idempotency id, the business effect and its result are persisted in one
// atomic commit, duplicate ids carrying different parameters are rejected,
// and expired results leave a non-replayable tombstone.
package idem

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"time"
)

// State is the lifecycle state of an idempotency record.
type State int

const (
	// StateInProgress means an executor holds the claim; the effect has not
	// been committed yet.
	StateInProgress State = iota
	// StateCompleted means effect and result were atomically committed.
	StateCompleted
	// StateTombstone means the result expired; the id can never be
	// re-executed.
	StateTombstone
)

func (s State) String() string {
	switch s {
	case StateInProgress:
		return "in-progress"
	case StateCompleted:
		return "completed"
	case StateTombstone:
		return "tombstone"
	}
	return "unknown"
}

var (
	// ErrFingerprintMismatch is returned when an id is reused with
	// parameters different from the original request.
	ErrFingerprintMismatch = errors.New("idem: duplicate id with different parameters")
	// ErrExpired is returned when the result has expired and only a
	// non-replayable tombstone remains.
	ErrExpired = errors.New("idem: result expired, id is tombstoned and cannot be replayed")
	// ErrLostClaim is returned to a stale executor whose claim was taken
	// over after its lease expired.
	ErrLostClaim = errors.New("idem: claim lost to another executor")
)

// Record is the persisted idempotency record.
type Record struct {
	ID          string
	Fingerprint string
	State       State
	Token       string
	LeaseUntil  time.Time
	Result      []byte
	ExpiresAt   time.Time
}

// Fingerprint canonically hashes request parameters. Params must be
// JSON-marshalable; encoding/json sorts map keys, so logically equal
// parameters produce equal fingerprints.
func Fingerprint(params any) (string, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("idem: fingerprint params: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Executor runs functions at most once per idempotency id.
type Executor struct {
	store  Store
	ttl    time.Duration
	lease  time.Duration
	poll   time.Duration
	now    func() time.Time
	prefix string
	seq    atomic.Uint64
}

// Option configures an Executor.
type Option func(*Executor)

// WithNow overrides the clock (mainly for tests).
func WithNow(now func() time.Time) Option {
	return func(e *Executor) { e.now = now }
}

// WithPollInterval overrides the wait interval while another executor holds
// the claim.
func WithPollInterval(d time.Duration) Option {
	return func(e *Executor) { e.poll = d }
}

// NewExecutor builds an Executor. ttl is how long a completed result is
// served before sweeping turns it into a tombstone; lease is how long a
// claim is held before a crashed executor's work may be taken over.
func NewExecutor(store Store, ttl, lease time.Duration, opts ...Option) *Executor {
	e := &Executor{
		store:  store,
		ttl:    ttl,
		lease:  lease,
		poll:   10 * time.Millisecond,
		now:    time.Now,
		prefix: fmt.Sprintf("exec-%d-", time.Now().UnixNano()),
	}
	for _, opt := range opts {
		opt(e)
	}
	return e
}

// Do runs fn exactly once for the given idempotency id and returns its
// result. fn is pure: it computes the result and an Effect; the Effect and
// the result are then persisted in a single atomic commit, so a crash can
// never leave the business effect applied without its result (or vice
// versa). If the commit's receipt is lost to a crash, a retried Do serves
// the persisted outcome instead of re-executing.
//
// Duplicate calls with the same id and equal params (completed or
// in-flight) converge on the single committed result. Equal id with
// different params fails with ErrFingerprintMismatch. Equal id after the
// result expired fails with ErrExpired and never re-executes.
func (e *Executor) Do(ctx context.Context, id string, params any, fn func() (Effect, []byte, error)) ([]byte, error) {
	fp, err := Fingerprint(params)
	if err != nil {
		return nil, err
	}
	token := fmt.Sprintf("%s%d", e.prefix, e.seq.Add(1))
	for {
		rec, found, err := e.store.Get(id)
		if err != nil {
			return nil, err
		}
		if found {
			if rec.Fingerprint != fp {
				return nil, ErrFingerprintMismatch
			}
			switch rec.State {
			case StateCompleted:
				return rec.Result, nil
			case StateTombstone:
				return nil, ErrExpired
			case StateInProgress:
				if e.now().Before(rec.LeaseUntil) {
					if err := sleepCtx(ctx, e.poll); err != nil {
						return nil, err
					}
					continue
				}
			}
		}
		now := e.now()
		_, claimed, err := e.store.Claim(id, fp, token, now, now.Add(e.lease))
		if err != nil {
			return nil, err
		}
		if !claimed {
			continue
		}
		effect, result, err := fn()
		if err != nil {
			// Nothing was applied; free the id so it may be retried.
			_ = e.store.Release(id, token)
			return nil, err
		}
		if err := e.store.Commit(id, token, effect, result, e.now().Add(e.ttl)); err != nil {
			return nil, err
		}
		return result, nil
	}
}

// Sweep turns every completed record whose result expired at now into a
// non-replayable tombstone, dropping the cached result but keeping the
// fingerprint so parameter mismatches are still detected.
func (e *Executor) Sweep() (int, error) {
	return e.store.Sweep(e.now())
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
