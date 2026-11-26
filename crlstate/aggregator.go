package crlstate

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ForkError is returned when a newly supplied manifest conflicts with an
// already accepted manifest under the same (IssuerID, Epoch). The issuer is
// locked and the evidence is available through Evidence.
type ForkError struct {
	Evidence ForkEvidence
}

func (e *ForkError) Error() string {
	return fmt.Sprintf("crlstate: issuer %q epoch %d has conflicting valid manifests (%s and %s)",
		e.Evidence.IssuerID, e.Evidence.Epoch,
		digestString(e.Evidence.First.Digest), digestString(e.Evidence.Second.Digest))
}

func (e *ForkError) Unwrap() error { return ErrIssuerLocked }

// LockedError is returned when an issuer is already locked by fork evidence.
type LockedError struct {
	Evidence ForkEvidence
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("crlstate: issuer %q is locked by fork evidence", e.Evidence.IssuerID)
}

func (e *LockedError) Unwrap() error { return ErrIssuerLocked }

// ChangeKind identifies an aggregator mutation relevant to caches.
type ChangeKind string

const (
	ChangeManifest  ChangeKind = "manifest"
	ChangeMigration ChangeKind = "migration"
	ChangeLock      ChangeKind = "lock"
)

// Change is delivered to subscribers after an aggregator mutation.
type Change struct {
	Kind      ChangeKind
	IssuerID  IssuerID
	Manifest  *Manifest
	Migration *Migration
	Fork      *ForkEvidence
	// InvalidateFrom/Until delimit the cache-invalidation time domain for a
	// manifest change. A zero Until means "unbounded future", which is used
	// when a newly started higher epoch can supersede all later times.
	InvalidateFrom  time.Time
	InvalidateUntil time.Time
}

// Aggregator ingests verified full and delta CRL partition manifests.
type Aggregator struct {
	mu sync.RWMutex

	verifier Verifier

	manifests  map[IssuerID][]*Manifest
	migrations map[IssuerID][]*Migration
	highest    map[IssuerID]uint64
	locked     map[IssuerID]*ForkEvidence
	versions   map[IssuerID]uint64

	subsMu sync.RWMutex
	subs   []func(Change)
}

// NewAggregator creates an aggregator. A nil verifier trusts the caller to
// have authenticated inputs; production hosts should supply a Verifier.
func NewAggregator(verifier Verifier) *Aggregator {
	return &Aggregator{
		verifier:   verifier,
		manifests:  make(map[IssuerID][]*Manifest),
		migrations: make(map[IssuerID][]*Migration),
		highest:    make(map[IssuerID]uint64),
		locked:     make(map[IssuerID]*ForkEvidence),
		versions:   make(map[IssuerID]uint64),
	}
}

// Subscribe registers a change callback. The returned function unsubscribes.
func (a *Aggregator) Subscribe(fn func(Change)) func() {
	if fn == nil {
		return func() {}
	}
	a.subsMu.Lock()
	id := len(a.subs)
	a.subs = append(a.subs, fn)
	a.subsMu.Unlock()
	return func() {
		a.subsMu.Lock()
		defer a.subsMu.Unlock()
		a.subs[id] = nil
	}
}

func (a *Aggregator) emit(ch Change) {
	a.subsMu.RLock()
	subs := append([]func(Change){}, a.subs...)
	a.subsMu.RUnlock()
	for _, fn := range subs {
		if fn != nil {
			fn(ch)
		}
	}
}

// AddManifest validates, authenticates and installs m. It returns ForkError and
// locks the issuer if m conflicts with an accepted manifest of the same
// (IssuerID, Epoch).
func (a *Aggregator) AddManifest(m *Manifest) error {
	if m == nil {
		return fmt.Errorf("%w: nil", ErrInvalidManifest)
	}
	candidate := m.Clone()
	if err := ValidateManifest(candidate); err != nil {
		return err
	}
	if a.verifier != nil {
		d := candidate.SigningDigest()
		if !a.verifier.Verify(candidate.RootID, candidate.IssuerID, d, candidate.Signature) {
			return fmt.Errorf("%w: manifest %s root %q", ErrBadSignature, candidate.PartitionID, candidate.RootID)
		}
	}

	a.mu.Lock()
	if ev, ok := a.locked[candidate.IssuerID]; ok {
		a.mu.Unlock()
		return &LockedError{Evidence: *ev}
	}

	existing := a.manifests[candidate.IssuerID]
	for _, old := range existing {
		if old.SigningDigest() == candidate.SigningDigest() && bytes.Equal(old.Signature, candidate.Signature) {
			a.mu.Unlock()
			return nil
		}
	}
	if high, ok := a.highest[candidate.IssuerID]; ok && candidate.Epoch < high {
		a.mu.Unlock()
		return fmt.Errorf("%w: manifest epoch %d is below issuer high-water mark %d",
			ErrEpochRegression, candidate.Epoch, high)
	}
	for _, old := range existing {
		if manifestsConflict(old, candidate) {
			ev := a.makeForkEvidence(old, candidate)
			a.locked[candidate.IssuerID] = &ev
			a.versions[candidate.IssuerID]++
			a.mu.Unlock()
			a.emit(Change{Kind: ChangeLock, IssuerID: candidate.IssuerID, Manifest: candidate.Clone(), Fork: &ev})
			return &ForkError{Evidence: ev}
		}
	}

	previousMax := a.highest[candidate.IssuerID]
	a.manifests[candidate.IssuerID] = append(existing, candidate)
	if candidate.Epoch > a.highest[candidate.IssuerID] {
		a.highest[candidate.IssuerID] = candidate.Epoch
	}
	a.versions[candidate.IssuerID]++
	until := a.invalidationHorizonLocked(candidate, previousMax)
	a.mu.Unlock()

	event := Change{
		Kind:            ChangeManifest,
		IssuerID:        candidate.IssuerID,
		Manifest:        candidate.Clone(),
		InvalidateFrom:  candidate.ValidFrom,
		InvalidateUntil: until,
	}
	a.emit(event)
	return nil
}

// invalidationHorizonLocked returns the end of the time domain whose cached
// status can change when candidate is installed. A new higher epoch
// supersedes the issuer indefinitely, so zero means unbounded. An equal-epoch
// manifest can only add coverage within that epoch's known windows.
func (a *Aggregator) invalidationHorizonLocked(candidate *Manifest, previousMax uint64) time.Time {
	if candidate.Epoch > previousMax {
		return time.Time{}
	}
	return a.dependentHorizonLocked(candidate)
}

// dependentHorizonLocked follows contiguous deltas in the same partition
// channel. Those are the only same-epoch manifests whose usable coverage can
// be extended by candidate's arrival.
func (a *Aggregator) dependentHorizonLocked(candidate *Manifest) time.Time {
	horizon := candidate.ValidUntil
	reachable := map[uint64]bool{}

	// Seed the candidate and all of its ancestors in this channel. A new
	// delta can close a gap that lets later cumulative or chained deltas
	// extend coverage well beyond the candidate's own window.
	for cur := candidate; cur != nil; {
		reachable[cur.CRLNumber] = true
		if cur.BaseDelta == 0 {
			break
		}
		var parent *Manifest
		for _, m := range a.manifests[candidate.IssuerID] {
			if m.CRLNumber == cur.BaseDelta && sameChannel(candidate, m) {
				parent = m
				break
			}
		}
		cur = parent
	}
	for {
		changed := false
		for _, m := range a.manifests[candidate.IssuerID] {
			if !m.IsDelta || !sameChannel(candidate, m) {
				continue
			}
			if reachable[m.CRLNumber] || !reachable[m.BaseDelta] {
				continue
			}
			if m.ValidFrom.After(horizon) {
				// A later start is a coverage hole, not a continuation.
				continue
			}
			reachable[m.CRLNumber] = true
			if m.ValidUntil.After(horizon) {
				horizon = m.ValidUntil
			}
			changed = true
		}
		if !changed {
			return horizon
		}
	}
}

func sameChannel(a, b *Manifest) bool {
	return a.Epoch == b.Epoch && a.RootID == b.RootID &&
		a.PartitionID == b.PartitionID &&
		a.SerialLo.Cmp(b.SerialLo) == 0 && a.SerialHi.Cmp(b.SerialHi) == 0 &&
		a.Reasons == b.Reasons
}

// AddMigration validates and authenticates a split/merge declaration.
func (a *Aggregator) AddMigration(m *Migration) error {
	if m == nil {
		return fmt.Errorf("%w: nil", ErrInvalidMigration)
	}
	candidate := m.Clone()
	if err := ValidateMigration(candidate); err != nil {
		return err
	}
	if candidate.ID == "" {
		candidate.ID = "migration:" + digestString(candidate.SigningDigest())
	}
	if a.verifier != nil {
		d := candidate.SigningDigest()
		if !a.verifier.Verify(candidate.OldRoot, candidate.IssuerID, d, candidate.OldSignature) {
			return fmt.Errorf("%w: migration old root %q", ErrBadSignature, candidate.OldRoot)
		}
		if !a.verifier.Verify(candidate.NewRoot, candidate.IssuerID, d, candidate.NewSignature) {
			return fmt.Errorf("%w: migration new root %q", ErrBadSignature, candidate.NewRoot)
		}
	}

	a.mu.Lock()
	if ev, ok := a.locked[candidate.IssuerID]; ok {
		a.mu.Unlock()
		return &LockedError{Evidence: *ev}
	}
	for _, old := range a.migrations[candidate.IssuerID] {
		if old.SigningDigest() == candidate.SigningDigest() &&
			bytes.Equal(old.OldSignature, candidate.OldSignature) &&
			bytes.Equal(old.NewSignature, candidate.NewSignature) {
			a.mu.Unlock()
			return nil
		}
	}
	a.migrations[candidate.IssuerID] = append(a.migrations[candidate.IssuerID], candidate)
	a.versions[candidate.IssuerID]++
	a.mu.Unlock()

	a.emit(Change{Kind: ChangeMigration, IssuerID: candidate.IssuerID, Migration: candidate.Clone()})
	return nil
}

func (a *Aggregator) makeForkEvidence(first, second *Manifest) ForkEvidence {
	return ForkEvidence{
		IssuerID: first.IssuerID,
		Epoch:    first.Epoch,
		At:       time.Now().UTC(),
		First:    first.snapshot(first.SigningDigest()),
		Second:   second.snapshot(second.SigningDigest()),
	}
}

func manifestsConflict(a, b *Manifest) bool {
	if a.IssuerID != b.IssuerID || a.Epoch != b.Epoch {
		return false
	}
	if a.ContentDigest() == b.ContentDigest() {
		return false
	}
	if a.PartitionID == b.PartitionID && a.CRLNumber == b.CRLNumber {
		// One partition generation cannot have two different bodies, even if
		// their validity windows happen not to overlap.
		return true
	}
	if a.SerialLo.Cmp(b.SerialHi) >= 0 || b.SerialLo.Cmp(a.SerialHi) >= 0 {
		return false
	}
	if a.Reasons.Intersect(b.Reasons) == 0 {
		return false
	}
	if !timeIntervalsOverlap(a.ValidFrom, a.ValidUntil, b.ValidFrom, b.ValidUntil) {
		return false
	}
	if a.CRLNumber == b.CRLNumber && a.IsDelta == b.IsDelta && a.BaseDelta == b.BaseDelta {
		return true
	}
	return false
}

func timeIntervalsOverlap(aLo, aHi, bLo, bHi time.Time) bool {
	return aLo.Before(bHi) && bLo.Before(aHi)
}

// Manifests returns deep copies of all manifests for issuer.
func (a *Aggregator) Manifests(issuer IssuerID) []*Manifest {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Manifest, 0, len(a.manifests[issuer]))
	for _, m := range a.manifests[issuer] {
		out = append(out, m.clone())
	}
	return out
}

// Migrations returns deep copies of all migrations for issuer.
func (a *Aggregator) Migrations(issuer IssuerID) []*Migration {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*Migration, 0, len(a.migrations[issuer]))
	for _, m := range a.migrations[issuer] {
		out = append(out, m.clone())
	}
	return out
}

// ForkEvidence returns the lock evidence for issuer, if locked.
func (a *Aggregator) ForkEvidence(issuer IssuerID) (ForkEvidence, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	ev, ok := a.locked[issuer]
	if !ok {
		return ForkEvidence{}, false
	}
	return cloneFork(*ev), true
}

// Locked reports whether issuer is locked.
func (a *Aggregator) Locked(issuer IssuerID) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	_, ok := a.locked[issuer]
	return ok
}

// HighestEpoch returns the issuer high-water revocation epoch.
func (a *Aggregator) HighestEpoch(issuer IssuerID) uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.highest[issuer]
}

// Version returns a monotonically increasing change counter for issuer. It is
// used by caches to close the gap between an ingestion and change delivery.
func (a *Aggregator) Version(issuer IssuerID) uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.versions[issuer]
}

func cloneFork(ev ForkEvidence) ForkEvidence {
	out := ev
	out.First.Signature = append([]byte(nil), ev.First.Signature...)
	out.Second.Signature = append([]byte(nil), ev.Second.Signature...)
	return out
}

// ErrRevoked and ErrStatusUnknown are convenience sentinels for Validate.
var (
	ErrRevoked       = errors.New("crlstate: certificate revoked")
	ErrStatusUnknown = errors.New("crlstate: certificate status unknown")
)

// Validate runs Check and converts the result into an error for path
// validation callers. StatusGood returns nil.
func (a *Aggregator) Validate(c Certificate) error {
	res := a.Check(c)
	switch res.Status {
	case StatusGood:
		return nil
	case StatusRevoked:
		return fmt.Errorf("%w: serial %s reason %d at %s", ErrRevoked, c.Serial, res.Reason, res.RevokedAt)
	case StatusLocked:
		return fmt.Errorf("%w: issuer %s", ErrIssuerLocked, c.IssuerID)
	default:
		return fmt.Errorf("%w: issuer %s serial %s", ErrStatusUnknown, c.IssuerID, c.Serial)
	}
}
