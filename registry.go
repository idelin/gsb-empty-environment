package netsec

import (
	"crypto/rand"
	"sync"
)

// SuiteConfig binds two KEM adapters to a suite ID and fixes the canonical
// ciphertext length of each component. The host owns the adapters and their
// cryptographic implementations.
type SuiteConfig struct {
	Adapter1       Adapter
	Adapter2       Adapter
	Ciphertext1Len int
	Ciphertext2Len int
}

type suiteEntry struct {
	adapter1 Adapter
	adapter2 Adapter
	ct1Len   int
	ct2Len   int
}

// Registry holds the host-registered hybrid suites and the internal failure
// diagnostics. It is safe for concurrent use.
type Registry struct {
	mu       sync.RWMutex
	suites   map[[suiteIDSize]byte]suiteEntry
	maskHits map[FailureMask]uint64
}

// NewRegistry returns an empty suite Registry.
func NewRegistry() *Registry {
	return &Registry{
		suites:   make(map[[suiteIDSize]byte]suiteEntry),
		maskHits: make(map[FailureMask]uint64),
	}
}

// RegisterSuite registers the two adapters of a suite under a fixed suite ID
// together with the canonical ciphertext lengths. It rejects a missing
// adapter, a non-positive length, or a duplicate suite ID.
func (r *Registry) RegisterSuite(suiteID [suiteIDSize]byte, cfg SuiteConfig) error {
	if cfg.Adapter1 == nil || cfg.Adapter2 == nil {
		return ErrInvalidSuiteConfig
	}
	if cfg.Ciphertext1Len <= 0 || cfg.Ciphertext2Len <= 0 {
		return ErrInvalidSuiteConfig
	}
	if cfg.Ciphertext1Len > maxCiphertextLen || cfg.Ciphertext2Len > maxCiphertextLen {
		return ErrInvalidSuiteConfig
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.suites[suiteID]; exists {
		return ErrSuiteAlreadyRegistered
	}
	r.suites[suiteID] = suiteEntry{
		adapter1: cfg.Adapter1,
		adapter2: cfg.Adapter2,
		ct1Len:   cfg.Ciphertext1Len,
		ct2Len:   cfg.Ciphertext2Len,
	}
	return nil
}

// Decapsulate parses and validates the strict wire input and performs hybrid
// decapsulation.
//
// Length and suite errors are rejected before any adapter is invoked. On
// success it returns the 32-byte derived key and a nil error. If either
// component reports failure it returns a random-looking 32-byte failure key
// and the generic ErrDecapsulationFailed, without indicating which component
// failed.
func (r *Registry) Decapsulate(wire []byte) ([32]byte, error) {
	req, err := ParseRequest(wire)
	if err != nil {
		return [32]byte{}, err
	}

	r.mu.RLock()
	entry, ok := r.suites[req.SuiteID]
	r.mu.RUnlock()
	if !ok {
		return [32]byte{}, ErrUnknownSuite
	}

	// Canonical-length enforcement happens before the adapters run, so an
	// adapter only ever sees the length it was registered for.
	if len(req.Ciphertext1) != entry.ct1Len || len(req.Ciphertext2) != entry.ct2Len {
		return [32]byte{}, ErrMalformedInput
	}

	return r.decapsulate(entry, req)
}

// decapsulate always invokes both adapters, always derives the full key from
// both shared secrets, and only releases the derived key when both components
// are valid.
func (r *Registry) decapsulate(entry suiteEntry, req Request) ([32]byte, error) {
	ss1, valid1 := entry.adapter1.Decapsulate(req.Ciphertext1)
	ss2, valid2 := entry.adapter2.Decapsulate(req.Ciphertext2)

	// Derivation is unconditional: a failed component contributes its
	// implicit-rejection secret and never degrades to a single-secret KDF.
	prk := extractPRK(ss1, ss2)
	key := deriveKey(prk, req)

	var mask FailureMask
	if !valid1 {
		mask |= MaskComponent1
	}
	if !valid2 {
		mask |= MaskComponent2
	}
	if mask == 0 {
		return key, nil
	}

	// Retain only the bit mask internally; surface a uniform failure.
	r.mu.Lock()
	r.maskHits[mask]++
	r.mu.Unlock()

	var failureKey [32]byte
	if _, err := rand.Read(failureKey[:]); err != nil {
		return [32]byte{}, err
	}
	return failureKey, ErrDecapsulationFailed
}

// failureMaskCounts returns a copy of the internal failure diagnostics. Only
// component bit masks are recorded; no other failure detail is retained.
func (r *Registry) failureMaskCounts() map[FailureMask]uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[FailureMask]uint64, len(r.maskHits))
	for mask, count := range r.maskHits {
		out[mask] = count
	}
	return out
}
