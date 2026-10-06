package seq

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// maxReserveRetries bounds how many CAS conflicts a single Next call
// tolerates before giving up.
const maxReserveRetries = 32

// ErrContention is returned when a reservation keeps losing CAS races
// against other processes.
var ErrContention = errors.New("seq: reservation contention too high")

// Generator hands out unique, monotonically increasing sequence numbers
// from reserved segments. Numbers are issued from memory; the store is
// touched only when a new segment must be reserved, so the flush cost
// is amortized over Step numbers.
//
// A Generator is safe for concurrent use within one process. Multiple
// processes may share the same key as long as they use the same Store.
type Generator struct {
	store Store
	key   string
	step  uint64

	mu     sync.Mutex
	next   uint64 // next number to issue
	max    uint64 // highest number covered by the current reservation
	loaded bool   // whether the persisted high-water mark was loaded
}

// NewGenerator creates a Generator that reserves segments of step
// numbers for key in store. Step must be at least 1.
func NewGenerator(store Store, key string, step uint64) (*Generator, error) {
	if store == nil {
		return nil, errors.New("seq: nil store")
	}
	if step == 0 {
		return nil, errors.New("seq: step must be >= 1")
	}
	return &Generator{store: store, key: key, step: step}, nil
}

// Next returns the next sequence number. It reserves a new segment from
// the store when the current one is exhausted. If the reservation cannot
// be persisted, Next returns an error and issues no number.
func (g *Generator) Next(ctx context.Context) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if !g.loaded {
		if err := g.load(ctx); err != nil {
			return 0, err
		}
	}
	if g.next > g.max {
		if err := g.reserve(ctx); err != nil {
			return 0, err
		}
	}
	id := g.next
	g.next++
	return id, nil
}

// load reads the persisted high-water mark so a restarted process
// resumes above every previously reserved number.
func (g *Generator) load(ctx context.Context) error {
	max, err := g.store.MaxReserved(ctx, g.key)
	if err != nil {
		return fmt.Errorf("seq: load high-water mark: %v", err)
	}
	if max == ^uint64(0) {
		return ErrExhausted
	}
	g.max = max
	g.next = max + 1
	g.loaded = true
	return nil
}

// reserve persists a new segment [max+1, max+step]. On a CAS conflict
// it reloads the high-water mark and retries; numbers already reserved
// by this generator stay valid because conflicting writers only ever
// move the mark forward.
func (g *Generator) reserve(ctx context.Context) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if g.max > ^uint64(0)-g.step {
			return ErrExhausted
		}
		next := g.max + g.step
		err := g.store.CompareAndSwapMaxReserved(ctx, g.key, g.max, next)
		switch err {
		case nil:
			g.max = next
			g.next = g.max - g.step + 1
			return nil
		case ErrCASConflict:
			if attempt >= maxReserveRetries {
				return ErrContention
			}
			persisted, lerr := g.store.MaxReserved(ctx, g.key)
			if lerr != nil {
				return fmt.Errorf("seq: reload high-water mark: %v", lerr)
			}
			if persisted > g.max {
				g.max = persisted
			}
		default:
			// The reservation may or may not have been persisted.
			// Reload so a later retry never reuses a number that a
			// crashed-then-restarted peer could observe.
			persisted, lerr := g.store.MaxReserved(ctx, g.key)
			if lerr != nil {
				return fmt.Errorf("seq: persist segment: %v (reload: %v)", err, lerr)
			}
			if persisted > g.max {
				g.max = persisted
				g.next = g.max + 1
			}
			return fmt.Errorf("seq: persist segment: %v", err)
		}
	}
}
