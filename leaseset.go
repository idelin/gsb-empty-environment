package netsec

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// Admission limits for LeaseSet2 storage requests, expressed against the
// router's local clock.
const (
	// MaxFutureSkew is how far in the future a LeaseSet's Published timestamp
	// may be. Anything strictly later than now+MaxFutureSkew is rejected.
	MaxFutureSkew = 60 * time.Second

	// MaxExpires bounds the LeaseSet2 Expires field, which is carried on the
	// wire as an offset from Published. The offset must be in (0, MaxExpires].
	MaxExpires = 660 * time.Second

	// MinLeases and MaxLeases bound the number of leases carried by a
	// submitted LeaseSet. The count is checked before expired leases are
	// filtered out of the accepted copy.
	MinLeases = 1
	MaxLeases = 16
)

// Sentinel errors returned by Admit. Callers should use errors.Is to match
// the specific admission rule that rejected an entry.
var (
	// ErrReplay means the incoming Published timestamp is not strictly newer
	// than the Published timestamp already stored for the destination.
	ErrReplay = errors.New("leaseset: published timestamp is not newer than the stored entry")

	// ErrFuturePublished means Published is more than MaxFutureSkew ahead of
	// the local clock.
	ErrFuturePublished = errors.New("leaseset: published timestamp is too far in the future")

	// ErrExpiresOutOfRange means the Expires offset is not within
	// (0, MaxExpires].
	ErrExpiresOutOfRange = errors.New("leaseset: expires offset must be greater than 0 and at most 660s")

	// ErrLeaseCount means the submitted entry carries fewer than MinLeases or
	// more than MaxLeases leases.
	ErrLeaseCount = errors.New("leaseset: lease count must be between 1 and 16")

	// ErrLeaseBeyondExpiry means a lease's Ends time is later than the
	// LeaseSet expiration (Published + Expires).
	ErrLeaseBeyondExpiry = errors.New("leaseset: lease ends after the leaseset expiration time")

	// ErrOfflineExpired means the offline signature block has already
	// expired relative to the local clock.
	ErrOfflineExpired = errors.New("leaseset: offline signature has already expired")

	// ErrOfflineTooShort means the offline signature expires before the
	// LeaseSet itself, so it cannot cover the LeaseSet's full lifetime.
	ErrOfflineTooShort = errors.New("leaseset: offline signature expires before the leaseset")

	// ErrSignature wraps a failure reported by the injected Verifier.
	ErrSignature = errors.New("leaseset: signature verification failed")

	// ErrInvalidArgument means Admit was called with a nil LeaseSet or nil
	// Verifier.
	ErrInvalidArgument = errors.New("leaseset: invalid admission argument")
)

// Lease is one gateway entry of a LeaseSet2: traffic for the destination can
// be sent through GatewayID/TunnelID until Ends.
type Lease struct {
	GatewayID [32]byte
	TunnelID  uint32

	// Ends is the absolute expiration instant of this lease.
	Ends time.Time
}

// OfflineSignature is the offline-signature block attached when the
// destination's permanent key is kept offline. Only the timing rules are
// enforced here; the cryptographic check is part of the injected Verifier.
type OfflineSignature struct {
	// Expires is the absolute expiration instant of the offline signature.
	Expires time.Time

	// Signature is the opaque offline-signature blob.
	Signature []byte
}

// LeaseSet is the subset of a LeaseSet2 that admission control needs.
type LeaseSet struct {
	// Destination identifies the destination the leases belong to.
	Destination [32]byte

	// Published is the absolute publication timestamp.
	Published time.Time

	// Expires is the lifetime offset added to Published to obtain the
	// absolute expiration of the LeaseSet, mirroring the LS2 wire field.
	Expires time.Duration

	// Leases are the gateway leases, between MinLeases and MaxLeases on
	// submission.
	Leases []Lease

	// OfflineSignature is nil for ordinary online signatures.
	OfflineSignature *OfflineSignature

	// Raw is the canonical byte representation covered by Signature.
	Raw []byte

	// Signature is the destination (or transient-key) signature over Raw.
	Signature []byte
}

// Expiration returns the absolute expiration instant Published + Expires.
func (ls *LeaseSet) Expiration() time.Time {
	return ls.Published.Add(ls.Expires)
}

// Verifier cryptographically authenticates a submitted LeaseSet. The netDb
// storage handler supplies the concrete implementation (destination-key
// lookup, Ed25519/Red25519 checks, blinded/transient and offline-signature
// verification, ...). A non-nil return rejects the entry; the admission
// package itself never touches key material.
type Verifier func(ls *LeaseSet) error

// Store is the netDb view admission control needs for replay protection.
type Store interface {
	// GetLeaseSet returns the LeaseSet currently stored for dest and true,
	// or nil and false when no entry exists for dest.
	GetLeaseSet(dest [32]byte) (*LeaseSet, bool)
}

// Admit runs admission control on a LeaseSet2 storage request and returns the
// entry to persist. The returned LeaseSet is a copy of the input with
// already-expired leases removed; the caller's input is never mutated.
//
// The reference time now is injected so callers and tests control the clock;
// use AdmitNow to pass time.Now().
//
// Rules, in order:
//  1. the injected Verifier must accept the signature;
//  2. Published must not be more than 60s later than now;
//  3. the Expires offset must be in (0, 660s];
//  4. the entry must carry 1..16 leases, each ending no later than
//     Published+Expires;
//  5. an offline signature must not already be expired and must cover at
//     least the full LeaseSet lifetime;
//  6. Published must be strictly newer than the stored entry for the same
//     destination (replay protection).
func Admit(ls *LeaseSet, store Store, verify Verifier, now time.Time) (*LeaseSet, error) {
	if ls == nil {
		return nil, fmt.Errorf("%w: nil leaseset", ErrInvalidArgument)
	}
	if verify == nil {
		return nil, fmt.Errorf("%w: nil verifier", ErrInvalidArgument)
	}

	if err := verify(ls); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrSignature, err)
	}

	if ls.Published.After(now.Add(MaxFutureSkew)) {
		return nil, fmt.Errorf("%w: published=%s now=%s",
			ErrFuturePublished, ls.Published.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	}

	if ls.Expires <= 0 || ls.Expires > MaxExpires {
		return nil, fmt.Errorf("%w: expires=%s", ErrExpiresOutOfRange, ls.Expires)
	}
	expiration := ls.Expiration()

	if len(ls.Leases) < MinLeases || len(ls.Leases) > MaxLeases {
		return nil, fmt.Errorf("%w: got %d leases", ErrLeaseCount, len(ls.Leases))
	}
	for i, lease := range ls.Leases {
		if lease.Ends.After(expiration) {
			return nil, fmt.Errorf("%w: lease %d ends %s after expiration %s",
				ErrLeaseBeyondExpiry, i,
				lease.Ends.UTC().Format(time.RFC3339Nano),
				expiration.UTC().Format(time.RFC3339Nano))
		}
	}

	if off := ls.OfflineSignature; off != nil {
		if off.Expires.Before(now) {
			return nil, fmt.Errorf("%w: offline expires %s now %s",
				ErrOfflineExpired,
				off.Expires.UTC().Format(time.RFC3339Nano),
				now.UTC().Format(time.RFC3339Nano))
		}
		if off.Expires.Before(expiration) {
			return nil, fmt.Errorf("%w: offline expires %s before leaseset expiration %s",
				ErrOfflineTooShort,
				off.Expires.UTC().Format(time.RFC3339Nano),
				expiration.UTC().Format(time.RFC3339Nano))
		}
	}

	if store != nil {
		if stored, ok := store.GetLeaseSet(ls.Destination); ok && !ls.Published.After(stored.Published) {
			return nil, fmt.Errorf("%w: incoming published=%s stored published=%s",
				ErrReplay,
				ls.Published.UTC().Format(time.RFC3339Nano),
				stored.Published.UTC().Format(time.RFC3339Nano))
		}
	}

	accepted := *ls
	accepted.Leases = make([]Lease, 0, len(ls.Leases))
	for _, lease := range ls.Leases {
		if lease.Ends.Before(now) {
			continue
		}
		accepted.Leases = append(accepted.Leases, lease)
	}
	return &accepted, nil
}

// AdmitNow calls Admit with the local wall clock as reference time.
func AdmitNow(ls *LeaseSet, store Store, verify Verifier) (*LeaseSet, error) {
	return Admit(ls, store, verify, time.Now())
}

// MemoryStore is an in-memory Store implementation. It is safe for
// concurrent use.
type MemoryStore struct {
	mu sync.RWMutex
	m  map[[32]byte]*LeaseSet
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{m: make(map[[32]byte]*LeaseSet)}
}

// GetLeaseSet implements Store.
func (s *MemoryStore) GetLeaseSet(dest [32]byte) (*LeaseSet, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ls, ok := s.m[dest]
	return ls, ok
}

// Put stores ls under its Destination, replacing any previous entry. A
// defensive copy is taken so later mutations of the caller's LeaseSet do not
// affect the store.
func (s *MemoryStore) Put(ls *LeaseSet) {
	if ls == nil {
		return
	}
	stored := *ls
	stored.Leases = append([]Lease(nil), ls.Leases...)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ls.Destination] = &stored
}
