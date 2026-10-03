package wal

import (
	"fmt"
	"sync"
)

// WAL assigns LSNs and appends encoded records to a LogStore.
// Append is safe for concurrent use.
type WAL struct {
	mu      sync.Mutex
	store   LogStore
	nextLSN uint64
}

// New creates a WAL whose first assigned LSN is startLSN.
func New(store LogStore, startLSN uint64) *WAL {
	return &WAL{store: store, nextLSN: startLSN}
}

// Append assigns the next LSN to rec, encodes and buffers it, and
// returns the assigned LSN. The record is not durable until Flush.
func (w *WAL) Append(rec *Record) uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	rec.LSN = w.nextLSN
	w.nextLSN++
	w.store.AppendVolatile(EncodeFrame(rec.Encode()))
	return rec.LSN
}

// Flush makes all buffered records durable.
func (w *WAL) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.store.Flush()
}

// LoseVolatile discards buffered (unflushed) records. Models a crash.
// The nextLSN counter is intentionally left alone: after a restart the
// WAL must be re-created and Resume'd from stable storage.
func (w *WAL) LoseVolatile() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.store.LoseVolatile()
}

// ReadAll decodes every durable record in LSN order.
func (w *WAL) ReadAll() ([]*Record, error) {
	frames, err := w.store.ReadStable()
	if err != nil {
		return nil, err
	}
	payloads := make([][]byte, 0, len(frames))
	for i, fr := range frames {
		recs, err := DecodeFrames(fr)
		if err != nil {
			return nil, fmt.Errorf("wal: frame %d: %v", i, err)
		}
		payloads = append(payloads, recs...)
	}
	return DecodeAll(payloads)
}

// Resume positions nextLSN past the maximum durable LSN. Call after a
// restart before appending new records.
func (w *WAL) Resume() error {
	recs, err := w.ReadAll()
	if err != nil {
		return err
	}
	var max uint64
	for _, r := range recs {
		if r.LSN > max {
			max = r.LSN
		}
	}
	w.mu.Lock()
	w.nextLSN = max + 1
	w.mu.Unlock()
	return nil
}

// NextLSN returns the LSN that the next Append will assign.
func (w *WAL) NextLSN() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.nextLSN
}

// Store exposes the underlying LogStore.
func (w *WAL) Store() LogStore { return w.store }
