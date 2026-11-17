package netsec

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

const stateVersion = 1

var (
	ErrNotInitialized     = errors.New("netsec: trust-anchor set is not initialised")
	ErrAlreadyInitialized = errors.New("netsec: trust-anchor set is already initialised")
	ErrConfigMismatch     = errors.New("netsec: observation period differs from persisted state")
	ErrCorruptState       = errors.New("netsec: corrupt persisted state")
	ErrInvalidAnchor      = errors.New("netsec: invalid trust anchor")
	ErrUnknownAnchor      = errors.New("netsec: target is not an active trust anchor")
	ErrInvalidSignature   = errors.New("netsec: revocation is not signed by an active trust anchor")
	ErrLastAnchor         = errors.New("netsec: refusing to revoke the last active trust anchor")
)

// KeyID identifies an Ed25519 public key by its SHA-256 digest.
type KeyID string

// Anchor is a trust anchor public key. Name is optional host metadata and does
// not participate in identity: identity is always the public key digest.
type Anchor struct {
	Name      string            `json:"name,omitempty"`
	PublicKey ed25519.PublicKey `json:"public_key"`
}

// KeyID returns the stable identity of the anchor's public key.
func (a Anchor) KeyID() KeyID {
	sum := sha256.Sum256(a.PublicKey)
	return KeyID("ed25519:" + hex.EncodeToString(sum[:]))
}

// RevocationPayload is the exact byte string an active trust anchor signs to
// authorise removal of target. The domain prefix prevents signatures for other
// protocols from being replayed as trust-anchor revocations.
func RevocationPayload(target KeyID) []byte {
	const prefix = "netsec/trust-anchor-revoke/v1\x00"
	out := make([]byte, 0, len(prefix)+len(target))
	out = append(out, prefix...)
	out = append(out, target...)
	return out
}

// Config controls the state machine. Clock and Store are mandatory.
type Config struct {
	// ObservationPeriod is the continuous period for which a newly seen key
	// must remain present before it is promoted to an active trust anchor.
	ObservationPeriod time.Duration
	// Clock must report trusted monotonic nanoseconds.
	Clock Clock
	// Store persists the full state. It is required so observation deadlines
	// survive a restart.
	Store Store
}

// PendingAnchor is a newly observed key that has not yet completed its
// continuous observation period.
type PendingAnchor struct {
	Anchor    Anchor
	Since     int64
	Remaining time.Duration
}

// Revocation records a successfully authenticated removal.
type Revocation struct {
	Target     KeyID
	VerifiedBy KeyID
	Tick       int64
}

// Snapshot is an immutable copy of the machine's persisted state.
type Snapshot struct {
	Initialized bool
	Active      []Anchor
	Pending     []PendingAnchor
	Revocations []Revocation
	LastTick    int64
}

// ReconcileResult reports the effects of one complete observation snapshot.
type ReconcileResult struct {
	Active         []Anchor
	Promoted       []Anchor
	Pending        []PendingAnchor
	Reset          []KeyID
	IgnoredRevoked []KeyID
	ClockRegressed bool
}

// RevokeResult reports a successfully authenticated revocation.
type RevokeResult struct {
	Removed    Anchor
	VerifiedBy KeyID
	Tick       int64
}

type pendingRecord struct {
	Anchor Anchor `json:"anchor"`
	Since  int64  `json:"since"`
}

type revocationRecord struct {
	Target     KeyID `json:"target"`
	VerifiedBy KeyID `json:"verified_by"`
	Tick       int64 `json:"tick"`
}

type persistedState struct {
	Version          int                `json:"version"`
	ObservationNanos int64              `json:"observation_period_ns"`
	LastTick         int64              `json:"last_tick_ns"`
	Initialized      bool               `json:"initialized"`
	Active           []Anchor           `json:"active"`
	Pending          []pendingRecord    `json:"pending"`
	Revocations      []revocationRecord `json:"revocations"`
}

type Machine struct {
	mu    sync.Mutex
	cfg   Config
	state persistedState
}

// Open loads state from cfg.Store. If no state exists, the returned machine is
// uninitialised and must be bootstrapped.
func Open(cfg Config) (*Machine, error) {
	if cfg.ObservationPeriod <= 0 {
		return nil, fmt.Errorf("netsec: observation period must be positive")
	}
	if cfg.Clock == nil {
		return nil, errors.New("netsec: trusted clock is required")
	}
	if cfg.Store == nil {
		return nil, errors.New("netsec: persistent store is required")
	}

	m := &Machine{cfg: cfg}
	data, err := cfg.Store.Load()
	if errors.Is(err, ErrNoState) {
		m.state = persistedState{
			Version:          stateVersion,
			ObservationNanos: int64(cfg.ObservationPeriod),
		}
		return m, nil
	}
	if err != nil {
		return nil, err
	}

	var state persistedState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptState, err)
	}
	if err := validateState(&state, cfg.ObservationPeriod); err != nil {
		return nil, err
	}
	m.state = state
	return m, nil
}

func validateState(state *persistedState, period time.Duration) error {
	if state.Version != stateVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrCorruptState, state.Version)
	}
	if state.ObservationNanos != int64(period) {
		return fmt.Errorf("%w: persisted %s, configured %s", ErrConfigMismatch,
			time.Duration(state.ObservationNanos), period)
	}
	if !state.Initialized {
		if len(state.Active) != 0 || len(state.Pending) != 0 || len(state.Revocations) != 0 {
			return fmt.Errorf("%w: uninitialised state contains anchors", ErrCorruptState)
		}
		return nil
	}
	if len(state.Active) == 0 {
		return fmt.Errorf("%w: initialised state has no active anchor", ErrCorruptState)
	}

	active := make(map[KeyID]struct{}, len(state.Active))
	for _, anchor := range state.Active {
		if err := validateAnchor(anchor); err != nil {
			return err
		}
		id := anchor.KeyID()
		if _, ok := active[id]; ok {
			return fmt.Errorf("%w: duplicate active key %s", ErrCorruptState, id)
		}
		active[id] = struct{}{}
	}

	pending := make(map[KeyID]struct{}, len(state.Pending))
	for _, record := range state.Pending {
		if err := validateAnchor(record.Anchor); err != nil {
			return err
		}
		id := record.Anchor.KeyID()
		if _, ok := active[id]; ok {
			return fmt.Errorf("%w: active key %s is also pending", ErrCorruptState, id)
		}
		if _, ok := pending[id]; ok {
			return fmt.Errorf("%w: duplicate pending key %s", ErrCorruptState, id)
		}
		if record.Since > state.LastTick {
			return fmt.Errorf("%w: pending key %s starts in the future", ErrCorruptState, id)
		}
		pending[id] = struct{}{}
	}

	revoked := make(map[KeyID]struct{}, len(state.Revocations))
	for _, record := range state.Revocations {
		if record.Target == "" || record.VerifiedBy == "" {
			return fmt.Errorf("%w: incomplete revocation", ErrCorruptState)
		}
		if record.Tick > state.LastTick {
			return fmt.Errorf("%w: revocation occurs in the future", ErrCorruptState)
		}
		if _, ok := revoked[record.Target]; ok {
			return fmt.Errorf("%w: duplicate revocation for %s", ErrCorruptState, record.Target)
		}
		if _, ok := active[record.Target]; ok {
			return fmt.Errorf("%w: revoked key %s is still active", ErrCorruptState, record.Target)
		}
		if _, ok := pending[record.Target]; ok {
			return fmt.Errorf("%w: revoked key %s is still pending", ErrCorruptState, record.Target)
		}
		revoked[record.Target] = struct{}{}
	}
	return nil
}

func validateAnchor(anchor Anchor) error {
	if len(anchor.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: Ed25519 public key must be %d bytes", ErrInvalidAnchor,
			ed25519.PublicKeySize)
	}
	return nil
}

func (m *Machine) Initialized() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Initialized
}

// Bootstrap installs the initial trust anchors. It is valid exactly once for a
// new store.
func (m *Machine) Bootstrap(initial []Anchor) error {
	if len(initial) == 0 {
		return fmt.Errorf("%w: bootstrap needs at least one anchor", ErrInvalidAnchor)
	}
	anchors, err := normalizeAnchors(initial)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Initialized {
		return ErrAlreadyInitialized
	}
	tick, _ := m.tickLocked()
	next := m.state
	next.Initialized = true
	next.Active = anchors
	next.Pending = nil
	next.Revocations = nil
	next.LastTick = tick
	return m.commitLocked(next)
}

// Reconcile consumes a complete snapshot of currently published keys, not a
// delta. A pending key absent from the snapshot loses its observation history.
// Active anchors are never removed by omission; removal requires a verified
// Revoke operation.
func (m *Machine) Reconcile(present []Anchor) (ReconcileResult, error) {
	present, err := normalizeAnchors(present)
	if err != nil {
		return ReconcileResult{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Initialized {
		return ReconcileResult{}, ErrNotInitialized
	}

	tick, regressed := m.tickLocked()
	next := m.state
	next.LastTick = tick

	active := make(map[KeyID]struct{}, len(next.Active))
	for _, anchor := range next.Active {
		active[anchor.KeyID()] = struct{}{}
	}
	revoked := make(map[KeyID]struct{}, len(next.Revocations))
	for _, record := range next.Revocations {
		revoked[record.Target] = struct{}{}
	}

	oldPending := make(map[KeyID]pendingRecord, len(next.Pending))
	for _, record := range next.Pending {
		oldPending[record.Anchor.KeyID()] = record
	}

	nextPending := make(map[KeyID]pendingRecord)
	var ignored []KeyID
	for _, anchor := range present {
		id := anchor.KeyID()
		if _, ok := active[id]; ok {
			continue
		}
		if _, ok := revoked[id]; ok {
			ignored = append(ignored, id)
			continue
		}
		if record, ok := oldPending[id]; ok {
			nextPending[id] = record
			continue
		}
		nextPending[id] = pendingRecord{Anchor: anchor, Since: tick}
	}

	var reset []KeyID
	for id := range oldPending {
		if _, stillPending := nextPending[id]; stillPending {
			continue
		}
		reset = append(reset, id)
	}

	activeAnchors := cloneAnchors(next.Active)
	var promoted []Anchor
	for _, id := range sortedPendingIDs(nextPending) {
		record := nextPending[id]
		if tick-record.Since < int64(m.cfg.ObservationPeriod) {
			continue
		}
		activeAnchors = append(activeAnchors, record.Anchor)
		promoted = append(promoted, record.Anchor)
		delete(nextPending, id)
	}

	next.Active = activeAnchors
	next.Pending = pendingRecords(nextPending)
	if err := m.commitLocked(next); err != nil {
		return ReconcileResult{}, err
	}

	sort.Slice(reset, func(i, j int) bool { return reset[i] < reset[j] })
	sort.Slice(ignored, func(i, j int) bool { return ignored[i] < ignored[j] })
	return ReconcileResult{
		Active:         cloneAnchors(activeAnchors),
		Promoted:       cloneAnchors(promoted),
		Pending:        m.pendingSnapshotLocked(),
		Reset:          reset,
		IgnoredRevoked: ignored,
		ClockRegressed: regressed,
	}, nil
}

// Revoke removes target when signature verifies under at least one currently
// active trust anchor. Candidate/newly observed keys cannot authorise a
// revocation. The revoked key remains tombstoned and cannot be re-added by
// later observation.
func (m *Machine) Revoke(target KeyID, signature []byte) (RevokeResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.state.Initialized {
		return RevokeResult{}, ErrNotInitialized
	}
	if len(signature) != ed25519.SignatureSize {
		return RevokeResult{}, ErrInvalidSignature
	}

	var targetAnchor Anchor
	found := false
	for _, anchor := range m.state.Active {
		if anchor.KeyID() == target {
			targetAnchor = anchor
			found = true
			break
		}
	}
	if !found {
		return RevokeResult{}, ErrUnknownAnchor
	}

	payload := RevocationPayload(target)
	var signer KeyID
	for _, anchor := range m.state.Active {
		if ed25519.Verify(anchor.PublicKey, payload, signature) {
			signer = anchor.KeyID()
			break
		}
	}
	if signer == "" {
		return RevokeResult{}, ErrInvalidSignature
	}
	if len(m.state.Active) == 1 {
		return RevokeResult{}, ErrLastAnchor
	}

	tick, _ := m.tickLocked()
	next := m.state
	next.LastTick = tick
	next.Active = make([]Anchor, 0, len(m.state.Active)-1)
	for _, anchor := range m.state.Active {
		if anchor.KeyID() != target {
			next.Active = append(next.Active, anchor)
		}
	}
	next.Revocations = append(cloneRevocations(m.state.Revocations), revocationRecord{
		Target:     target,
		VerifiedBy: signer,
		Tick:       tick,
	})
	if err := m.commitLocked(next); err != nil {
		return RevokeResult{}, err
	}
	return RevokeResult{Removed: targetAnchor, VerifiedBy: signer, Tick: tick}, nil
}

func (m *Machine) Snapshot() Snapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	snapshot := Snapshot{
		Initialized: m.state.Initialized,
		Active:      cloneAnchors(m.state.Active),
		Pending:     m.pendingSnapshotLocked(),
		Revocations: make([]Revocation, 0, len(m.state.Revocations)),
		LastTick:    m.state.LastTick,
	}
	for _, record := range m.state.Revocations {
		snapshot.Revocations = append(snapshot.Revocations, Revocation{
			Target:     record.Target,
			VerifiedBy: record.VerifiedBy,
			Tick:       record.Tick,
		})
	}
	return snapshot
}

func (m *Machine) tickLocked() (int64, bool) {
	tick := m.cfg.Clock.Now()
	if tick < m.state.LastTick {
		return m.state.LastTick, true
	}
	return tick, false
}

func (m *Machine) pendingSnapshotLocked() []PendingAnchor {
	pending := make([]PendingAnchor, 0, len(m.state.Pending))
	period := int64(m.cfg.ObservationPeriod)
	for _, record := range m.state.Pending {
		elapsed := m.state.LastTick - record.Since
		remaining := period - elapsed
		if remaining < 0 {
			remaining = 0
		}
		pending = append(pending, PendingAnchor{
			Anchor:    cloneAnchor(record.Anchor),
			Since:     record.Since,
			Remaining: time.Duration(remaining),
		})
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].Anchor.KeyID() < pending[j].Anchor.KeyID()
	})
	return pending
}

func (m *Machine) commitLocked(next persistedState) error {
	data, err := json.Marshal(next)
	if err != nil {
		return fmt.Errorf("netsec: encode state: %w", err)
	}
	if err := m.cfg.Store.Save(data); err != nil {
		return err
	}
	m.state = next
	return nil
}

func normalizeAnchors(anchors []Anchor) ([]Anchor, error) {
	byID := make(map[KeyID]Anchor, len(anchors))
	for _, anchor := range anchors {
		if err := validateAnchor(anchor); err != nil {
			return nil, err
		}
		anchor.PublicKey = append(ed25519.PublicKey(nil), anchor.PublicKey...)
		if _, ok := byID[anchor.KeyID()]; ok {
			// The public key is the identity; duplicate metadata is ignored.
			continue
		}
		byID[anchor.KeyID()] = anchor
	}
	ids := make([]KeyID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]Anchor, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out, nil
}

func pendingRecords(byID map[KeyID]pendingRecord) []pendingRecord {
	ids := sortedPendingIDs(byID)
	out := make([]pendingRecord, 0, len(ids))
	for _, id := range ids {
		record := byID[id]
		record.Anchor = cloneAnchor(record.Anchor)
		out = append(out, record)
	}
	return out
}

func sortedPendingIDs(byID map[KeyID]pendingRecord) []KeyID {
	ids := make([]KeyID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func cloneAnchor(anchor Anchor) Anchor {
	anchor.PublicKey = append(ed25519.PublicKey(nil), anchor.PublicKey...)
	return anchor
}

func cloneAnchors(anchors []Anchor) []Anchor {
	out := make([]Anchor, 0, len(anchors))
	for _, anchor := range anchors {
		out = append(out, cloneAnchor(anchor))
	}
	return out
}

func cloneRevocations(records []revocationRecord) []revocationRecord {
	return append([]revocationRecord(nil), records...)
}
