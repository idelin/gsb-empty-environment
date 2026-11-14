package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Config configures one connection's CID and token lifecycle manager.
type Config struct {
	Clock             Clock
	Generator         CIDGenerator
	Route             RoutePartition
	MaxPacketLifetime time.Duration
	ConfirmationGrace time.Duration
	MinCIDLength      int
	MaxCIDLength      int

	// MasterSecret feeds deterministic key-generation derivation. Persist it
	// if crash recovery must reconstruct the same tokens after process restart.
	MasterSecret []byte
	// Keys can be supplied when the QUIC stack already owns a key ring.
	Keys *KeyRing
	// Ledger is shared by a gateway so a CID value cannot be issued by a
	// different connection while the original is quarantined.
	Ledger *CIDLedger
	// TokenLedger is shared by a gateway so a token value is never rebound to
	// a new CID, even after the original CID's quarantine has ended.
	TokenLedger *TokenLedger
	// Journal durably records every state transition. Nil keeps audit records
	// in memory only.
	Journal Journal
	// RecoveryKey authenticates checkpoints. If nil, a key is derived from the
	// key-ring master secret.
	RecoveryKey []byte
	// Label is copied into audit witnesses to identify the connection.
	Label string
}

// CIDGrant is returned when the stack issues a new NEW_CONNECTION_ID entry.
// Token must be sent to the peer together with CID.
type CIDGrant struct {
	CID        []byte
	Sequence   uint64
	Generation uint64
	Route      RoutePartition
	Token      ResetToken
	IssuedAt   time.Time
	Witness    AuditWitness
}

// CIDInfo is an immutable view of a CID record.
type CIDInfo struct {
	CID               []byte
	Sequence          uint64
	State             CIDState
	Generation        uint64
	Route             RoutePartition
	Token             ResetToken
	IssuedAt          time.Time
	RetireRequestedAt time.Time
	PeerRetiredAt     time.Time
	ClosedAt          time.Time
	RetentionDeadline time.Time
}

// Transition reports the optional audit witness that caused a CID state
// change. Changed is false for idempotent/no-op operations.
type Transition struct {
	CID     []byte
	From    CIDState
	To      CIDState
	Changed bool
	Witness *AuditWitness
}

type cidRecord struct {
	cid               []byte
	key               string
	sequence          uint64
	generation        uint64
	route             RoutePartition
	token             ResetToken
	state             CIDState
	issuedAt          time.Time
	retireRequestedAt time.Time
	peerRetiredAt     time.Time
	closedAt          time.Time
	deadline          time.Time
}

// Manager owns the deterministic CID/token lifecycle for one QUIC connection.
// It is safe for concurrent use.
type Manager struct {
	mu        sync.Mutex
	clock     Clock
	generator CIDGenerator
	route     RoutePartition
	label     string

	maxPacketLifetime time.Duration
	confirmationGrace time.Duration
	minCIDLength      int
	maxCIDLength      int

	keys        *KeyRing
	ledger      *CIDLedger
	tokenLedger *TokenLedger

	journal        Journal
	recoveryKey    [32]byte
	hasRecoveryKey bool

	records map[string]*cidRecord
	bySeq   map[uint64]*cidRecord
	tokens  map[ResetToken][]*cidRecord

	nextCIDSeq uint64
	seq        uint64
	lastHash   [32]byte
	audit      []AuditWitness

	closed      bool
	closeReason string
	lastPath    string
	mutated     bool
}

// NewManager validates configuration and creates an empty connection state.
func NewManager(cfg Config) (*Manager, error) {
	clock := cfg.Clock
	if clock == nil {
		clock = RealClock{}
	}
	if cfg.MaxPacketLifetime == 0 {
		cfg.MaxPacketLifetime = DefaultMaxPacketLifetime
	}
	if cfg.ConfirmationGrace == 0 {
		cfg.ConfirmationGrace = DefaultConfirmationGrace
	}
	if cfg.MaxPacketLifetime < 0 || cfg.ConfirmationGrace < 0 {
		return nil, fmt.Errorf("%w: negative retention duration", ErrInvalidConfig)
	}
	if cfg.MinCIDLength == 0 {
		cfg.MinCIDLength = 1
	}
	if cfg.MaxCIDLength == 0 {
		cfg.MaxCIDLength = DefaultMaxCIDLength
	}
	if cfg.MinCIDLength < 0 || cfg.MaxCIDLength < cfg.MinCIDLength || cfg.MaxCIDLength > DefaultMaxCIDLength {
		return nil, fmt.Errorf("%w: CID length bounds", ErrInvalidConfig)
	}
	generator := cfg.Generator
	if generator == nil {
		var err error
		generator, err = NewRandomCIDGenerator(rand.Reader, 16, nil)
		if err != nil {
			return nil, err
		}
	}
	keys := cfg.Keys
	if keys == nil {
		var err error
		keys, err = NewKeyRing(cfg.MasterSecret, clock)
		if err != nil {
			return nil, err
		}
	}
	ledger := cfg.Ledger
	if ledger == nil {
		ledger = NewCIDLedger()
	}
	tokenLedger := cfg.TokenLedger
	if tokenLedger == nil {
		tokenLedger = NewTokenLedger()
	}
	m := &Manager{
		clock:             clock,
		generator:         generator,
		route:             cfg.Route,
		label:             cfg.Label,
		maxPacketLifetime: cfg.MaxPacketLifetime,
		confirmationGrace: cfg.ConfirmationGrace,
		minCIDLength:      cfg.MinCIDLength,
		maxCIDLength:      cfg.MaxCIDLength,
		keys:              keys,
		ledger:            ledger,
		tokenLedger:       tokenLedger,
		journal:           cfg.Journal,
		records:           make(map[string]*cidRecord),
		bySeq:             make(map[uint64]*cidRecord),
		tokens:            make(map[ResetToken][]*cidRecord),
	}
	if len(cfg.RecoveryKey) > 0 {
		if len(cfg.RecoveryKey) < 16 {
			return nil, fmt.Errorf("%w: recovery key too short", ErrInvalidConfig)
		}
		m.recoveryKey = sha256.Sum256(cfg.RecoveryKey)
		m.hasRecoveryKey = true
	}
	return m, nil
}

func (m *Manager) checkpointKey() ([32]byte, bool) {
	if m.hasRecoveryKey {
		return m.recoveryKey, true
	}
	if m.keys != nil {
		return m.keys.recoveryKey(), true
	}
	return [32]byte{}, false
}

func (m *Manager) retentionDeadline(from time.Time) time.Time {
	// Deliberately sequential and conservative: neither the maximum packet
	// lifetime nor confirmation grace can still be running at this deadline.
	return from.Add(m.maxPacketLifetime).Add(m.confirmationGrace)
}

func (m *Manager) newWitnessLocked(kind AuditKind, rec *cidRecord, from, to CIDState, reason string) AuditWitness {
	w := AuditWitness{
		Seq:        m.seq + 1,
		At:         m.clock.Now().UTC(),
		Kind:       kind,
		Connection: m.label,
		Generation: m.keys.CurrentGeneration(),
		From:       from,
		To:         to,
		Reason:     reason,
		PrevHash:   hex.EncodeToString(m.lastHash[:]),
	}
	if rec != nil {
		w.CIDHex = hex.EncodeToString(rec.cid)
		w.CIDSeq = rec.sequence
		w.Generation = rec.generation
		w.Route = rec.route
	}
	return m.resealWitnessLocked(w)
}

// resealWitnessLocked recomputes the hash-chain link and the record MAC after
// a caller has filled audit-specific fields (CID/gen/route).
func (m *Manager) resealWitnessLocked(w AuditWitness) AuditWitness {
	h := witnessHash(m.lastHash, w)
	w.Hash = hex.EncodeToString(h[:])
	key, _ := m.checkpointKey()
	w.MAC = signAuditWitness(w, key)
	return w
}

// commitLocked persists the witness first, then applies the in-memory
// mutation. A crash between the two is repaired by journal replay.
func (m *Manager) commitLocked(w AuditWitness, mutate func()) error {
	if w.Seq != m.seq+1 || w.PrevHash != hex.EncodeToString(m.lastHash[:]) {
		return fmt.Errorf("%w: witness sequence/link mismatch", ErrJournalCorrupt)
	}
	h := witnessHash(m.lastHash, w)
	if w.Hash != hex.EncodeToString(h[:]) {
		return fmt.Errorf("%w: witness hash mismatch", ErrJournalCorrupt)
	}
	key, _ := m.checkpointKey()
	if w.MAC == "" || w.MAC != signAuditWitness(w, key) {
		return fmt.Errorf("%w: witness MAC mismatch", ErrJournalCorrupt)
	}
	if m.journal != nil {
		if err := m.journal.Append(w); err != nil {
			return err
		}
	}
	if mutate != nil {
		mutate()
	}
	decoded, err := hex.DecodeString(w.Hash)
	if err != nil || len(decoded) != len(h) {
		return ErrJournalCorrupt
	}
	copy(m.lastHash[:], decoded)
	m.seq = w.Seq
	m.audit = append(m.audit, w)
	m.mutated = true
	return nil
}

func (m *Manager) validateCID(cid []byte) error {
	if len(cid) < m.minCIDLength || len(cid) > m.maxCIDLength {
		return fmt.Errorf("%w: length %d outside [%d,%d]", ErrInvalidCID, len(cid), m.minCIDLength, m.maxCIDLength)
	}
	return nil
}

// IssueCID allocates a fresh CID and derives its token from the current key
// generation, route partition, and complete CID. It fails closed if the
// generator repeats a burned value or a live token collides.
func (m *Manager) IssueCID() (CIDGrant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return CIDGrant{}, err
	}
	if m.closed {
		return CIDGrant{}, ErrConnectionClosed
	}
	cid, err := m.generator.NextCID()
	if err != nil {
		return CIDGrant{}, err
	}
	cid = append([]byte(nil), cid...)
	if err := m.validateCID(cid); err != nil {
		return CIDGrant{}, err
	}
	now := m.clock.Now().UTC()
	if !m.ledger.Reserve(cid, now) {
		// The value may not be reissued, but this is an auditable refusal.
		w := m.newWitnessLocked(AuditCIDReuseRejected, nil, CIDNone, CIDNone, "cid value already used")
		w.CIDHex = hex.EncodeToString(cid)
		w.Route = m.route
		w = m.resealWitnessLocked(w)
		if jerr := m.commitLocked(w, nil); jerr != nil {
			return CIDGrant{}, jerr
		}
		return CIDGrant{}, fmt.Errorf("%w: %x", ErrCIDReused, cid)
	}

	generation := m.keys.CurrentGeneration()
	token, err := m.keys.Token(generation, m.route, cid)
	if err != nil {
		return CIDGrant{}, err
	}
	if live := m.liveTokenRecordsLocked(token); len(live) != 0 || !m.tokenLedger.Reserve(token, now) {
		w := m.newWitnessLocked(AuditTokenCollision, nil, CIDNone, CIDNone, "derived token collides with live token")
		w.CIDHex = hex.EncodeToString(cid)
		w.Generation = generation
		w.Route = m.route
		w = m.resealWitnessLocked(w)
		if jerr := m.commitLocked(w, nil); jerr != nil {
			return CIDGrant{}, jerr
		}
		return CIDGrant{}, ErrTokenCollision
	}
	if err := m.keys.Acquire(generation); err != nil {
		return CIDGrant{}, err
	}

	rec := &cidRecord{
		cid:        cid,
		key:        string(cid),
		sequence:   m.nextCIDSeq,
		generation: generation,
		route:      m.route,
		token:      token,
		state:      CIDIssued,
		issuedAt:   now,
	}
	w := m.newWitnessLocked(AuditCIDIssued, rec, CIDNone, CIDIssued, "issued")
	if err := m.commitLocked(w, func() {
		m.records[rec.key] = rec
		m.bySeq[rec.sequence] = rec
		m.tokens[rec.token] = append(m.tokens[rec.token], rec)
		m.nextCIDSeq++
	}); err != nil {
		_ = m.keys.Release(generation)
		return CIDGrant{}, err
	}
	return CIDGrant{
		CID:        append([]byte(nil), rec.cid...),
		Sequence:   rec.sequence,
		Generation: rec.generation,
		Route:      rec.route,
		Token:      rec.token,
		IssuedAt:   rec.issuedAt,
		Witness:    w,
	}, nil
}

// Issue is an alias for IssueCID.
func (m *Manager) Issue() (CIDGrant, error) { return m.IssueCID() }

// ConfirmPeerUse records that the peer has demonstrated use of an issued CID.
// It moves issued -> active. Repeated confirmation is idempotent.
func (m *Manager) ConfirmPeerUse(cid []byte) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return Transition{}, err
	}
	rec, ok := m.records[string(cid)]
	if !ok {
		return Transition{}, ErrCIDNotFound
	}
	if m.closed {
		return Transition{CID: append([]byte(nil), cid...), From: rec.state, To: rec.state}, ErrConnectionClosed
	}
	if rec.state != CIDIssued {
		return Transition{CID: append([]byte(nil), cid...), From: rec.state, To: rec.state}, nil
	}
	w := m.newWitnessLocked(AuditCIDConfirmed, rec, CIDIssued, CIDActive, "peer confirmed use")
	if err := m.commitLocked(w, func() { rec.state = CIDActive }); err != nil {
		return Transition{}, err
	}
	return Transition{CID: append([]byte(nil), cid...), From: CIDIssued, To: CIDActive, Changed: true, Witness: &w}, nil
}

// Confirm is an alias for ConfirmPeerUse.
func (m *Manager) Confirm(cid []byte) (Transition, error) { return m.ConfirmPeerUse(cid) }

// RequestRetire starts local retirement (for example retire_prior_to). The
// token remains live until the peer confirms retirement and the quarantine
// deadline passes.
func (m *Manager) RequestRetire(cid []byte, reason string) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return Transition{}, err
	}
	rec, ok := m.records[string(cid)]
	if !ok {
		return Transition{}, ErrCIDNotFound
	}
	if m.closed {
		return Transition{CID: append([]byte(nil), cid...), From: rec.state, To: rec.state}, ErrConnectionClosed
	}
	switch rec.state {
	case CIDIssued, CIDActive:
		now := m.clock.Now().UTC()
		w := m.newWitnessLocked(AuditCIDRetireRequested, rec, rec.state, CIDRetiring, reason)
		if err := m.commitLocked(w, func() {
			rec.state = CIDRetiring
			rec.retireRequestedAt = now
			if rec.deadline.Before(m.retentionDeadline(now)) {
				rec.deadline = m.retentionDeadline(now)
			}
		}); err != nil {
			return Transition{}, err
		}
		return Transition{CID: append([]byte(nil), cid...), From: w.From, To: CIDRetiring, Changed: true, Witness: &w}, nil
	case CIDRetiring:
		return Transition{CID: append([]byte(nil), cid...), From: CIDRetiring, To: CIDRetiring}, nil
	default:
		return Transition{CID: append([]byte(nil), cid...), From: rec.state, To: rec.state}, nil
	}
}

// Retire is an alias for RequestRetire.
func (m *Manager) Retire(cid []byte, reason string) (Transition, error) {
	return m.RequestRetire(cid, reason)
}

// RetireOutcome classifies a RETIRE_CONNECTION_ID frame.
type RetireOutcome string

const (
	RetireApplied   RetireOutcome = "applied"
	RetireDuplicate RetireOutcome = "duplicate"
	RetireUnknown   RetireOutcome = "unknown_sequence"
	RetireIgnored   RetireOutcome = "ignored"
)

// RetireResult is returned for a peer RETIRE_CONNECTION_ID frame. Out-of-order
// or unknown sequence numbers are ignored; they never close the connection.
type RetireResult struct {
	Outcome  RetireOutcome
	CID      []byte
	Sequence uint64
	Changed  bool
	Witness  *AuditWitness
}

// RetireFromPeer applies a peer's RETIRE_CONNECTION_ID. It intentionally never
// closes the connection: an attacker can reorder or replay RETIRE frames.
func (m *Manager) RetireFromPeer(sequence uint64) (RetireResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return RetireResult{}, err
	}
	rec, ok := m.bySeq[sequence]
	if !ok {
		return RetireResult{Outcome: RetireUnknown, Sequence: sequence}, nil
	}
	switch rec.state {
	case CIDIssued, CIDActive, CIDRetiring:
		now := m.clock.Now().UTC()
		w := m.newWitnessLocked(AuditCIDPeerRetired, rec, rec.state, CIDRetired, "peer RETIRE_CONNECTION_ID")
		if err := m.commitLocked(w, func() {
			rec.state = CIDRetired
			rec.peerRetiredAt = now
			if d := m.retentionDeadline(now); rec.deadline.Before(d) {
				rec.deadline = d
			}
		}); err != nil {
			return RetireResult{}, err
		}
		return RetireResult{
			Outcome:  RetireApplied,
			CID:      append([]byte(nil), rec.cid...),
			Sequence: sequence,
			Changed:  true,
			Witness:  &w,
		}, nil
	case CIDRetired:
		return RetireResult{Outcome: RetireDuplicate, CID: append([]byte(nil), rec.cid...), Sequence: sequence}, nil
	default:
		return RetireResult{Outcome: RetireIgnored, CID: append([]byte(nil), rec.cid...), Sequence: sequence}, nil
	}
}

// HandleRetireConnectionID is the QUIC-stack-facing alias for RetireFromPeer.
func (m *Manager) HandleRetireConnectionID(sequence uint64) (RetireResult, error) {
	return m.RetireFromPeer(sequence)
}

// ConfirmRetire is an alias for RetireFromPeer, emphasizing that the peer has
// confirmed it will no longer use the CID.
func (m *Manager) ConfirmRetire(sequence uint64) (RetireResult, error) {
	return m.RetireFromPeer(sequence)
}

// CloseResult reports the deterministic connection close.
type CloseResult struct {
	CIDsClosed int
	Witnesses  []AuditWitness
}

// Close retires every live CID into quarantine. Tokens remain matchable for
// this old connection until their deadlines, but new CIDs cannot be issued.
func (m *Manager) Close(reason string) (CloseResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return CloseResult{}, err
	}
	return m.closeLocked(reason)
}

func (m *Manager) closeLocked(reason string) (CloseResult, error) {
	var result CloseResult
	if !m.closed {
		w := m.newWitnessLocked(AuditConnectionClosed, nil, CIDNone, CIDNone, reason)
		if err := m.commitLocked(w, func() {
			m.closed = true
			m.closeReason = reason
		}); err != nil {
			return result, err
		}
		result.Witnesses = append(result.Witnesses, w)
	}
	now := m.clock.Now().UTC()
	for _, rec := range m.sortedRecordsLocked() {
		if rec.state != CIDIssued && rec.state != CIDActive && rec.state != CIDRetiring {
			continue
		}
		from := rec.state
		w := m.newWitnessLocked(AuditCIDClosed, rec, from, CIDClosed, reason)
		if err := m.commitLocked(w, func() {
			rec.state = CIDClosed
			rec.closedAt = now
			if d := m.retentionDeadline(now); rec.deadline.Before(d) {
				rec.deadline = d
			}
		}); err != nil {
			return result, err
		}
		result.CIDsClosed++
		result.Witnesses = append(result.Witnesses, w)
	}
	return result, nil
}

// IsClosed reports whether the connection has entered the closed state.
func (m *Manager) IsClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// KeyRotation reports old/new generations and the audit witness.
type KeyRotation struct {
	OldGeneration uint64
	NewGeneration uint64
	Witness       AuditWitness
}

// RotateKeys advances the key generation. Existing CIDs and tokens keep their
// old generation until they are retired and expire.
func (m *Manager) RotateKeys() (KeyRotation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return KeyRotation{}, err
	}
	old := m.keys.CurrentGeneration()
	next := old + 1
	if m.keys.HasGeneration(next) {
		return KeyRotation{}, fmt.Errorf("netsec: key generation %d already exists", next)
	}
	w := m.newWitnessLocked(AuditKeyRotated, nil, CIDNone, CIDNone, fmt.Sprintf("%d->%d", old, next))
	w.Generation = next
	w = m.resealWitnessLocked(w)
	var rotateErr error
	// Persist the intent before changing key material. Replay applies the same
	// generation transition, so a crash cannot leave a later CID.issued record
	// referring to a generation that the recovered key ring does not know.
	if err := m.commitLocked(w, func() {
		_, _, rotateErr = m.keys.Rotate()
	}); err != nil {
		return KeyRotation{}, err
	}
	if rotateErr != nil {
		return KeyRotation{}, rotateErr
	}
	return KeyRotation{OldGeneration: old, NewGeneration: next, Witness: w}, nil
}

// KeyReferenceStatus is the reference audit performed before key destruction.
type KeyReferenceStatus struct {
	Generation    uint64
	Active        int
	Quarantined   int
	Expired       int
	KeyReferences int
	Destroyable   bool
}

// KeyStatus counts active and quarantined CIDs referencing a generation.
func (m *Manager) KeyStatus(generation uint64) KeyReferenceStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	var s KeyReferenceStatus
	s.Generation = generation
	for _, rec := range m.records {
		if rec.generation != generation {
			continue
		}
		switch rec.state {
		case CIDIssued, CIDActive, CIDRetiring:
			s.Active++
		case CIDRetired, CIDClosed:
			s.Quarantined++
		case CIDExpired:
			s.Expired++
		}
	}
	s.KeyReferences = m.keys.References(generation)
	s.Destroyable = m.keys.HasGeneration(generation) &&
		generation != m.keys.CurrentGeneration() &&
		s.Active == 0 && s.Quarantined == 0 && s.KeyReferences == 0
	return s
}

// DestroyKeyGeneration first expires due CIDs, then verifies that neither an
// active nor a quarantined CID references the generation. Only then is key
// material erased.
func (m *Manager) DestroyKeyGeneration(generation uint64) (Transition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.purgeExpiredLocked(); err != nil {
		return Transition{}, err
	}
	if generation == m.keys.CurrentGeneration() {
		return Transition{}, ErrCurrentKeyGeneration
	}
	status := m.keyStatusLocked(generation)
	if status.Active > 0 || status.Quarantined > 0 || status.KeyReferences > 0 {
		return Transition{}, fmt.Errorf("%w: generation %d active=%d quarantined=%d refs=%d",
			ErrKeyInUse, generation, status.Active, status.Quarantined, status.KeyReferences)
	}
	if err := m.keys.Destroy(generation); err != nil {
		return Transition{}, err
	}
	w := m.newWitnessLocked(AuditKeyDestroyed, nil, CIDNone, CIDNone, "key material destroyed")
	w.Generation = generation
	w = m.resealWitnessLocked(w)
	if err := m.commitLocked(w, nil); err != nil {
		return Transition{}, err
	}
	return Transition{Changed: true, Witness: &w}, nil
}

func (m *Manager) keyStatusLocked(generation uint64) KeyReferenceStatus {
	var s KeyReferenceStatus
	s.Generation = generation
	for _, rec := range m.records {
		if rec.generation != generation {
			continue
		}
		switch rec.state {
		case CIDIssued, CIDActive, CIDRetiring:
			s.Active++
		case CIDRetired, CIDClosed:
			s.Quarantined++
		case CIDExpired:
			s.Expired++
		}
	}
	s.KeyReferences = m.keys.References(generation)
	s.Destroyable = m.keys.HasGeneration(generation) &&
		generation != m.keys.CurrentGeneration() &&
		s.Active == 0 && s.Quarantined == 0 && s.KeyReferences == 0
	return s
}

func (m *Manager) liveTokenRecordsLocked(token ResetToken) []*cidRecord {
	var out []*cidRecord
	for _, rec := range m.tokens[token] {
		if rec.state != CIDExpired && rec.state != CIDNone {
			out = append(out, rec)
		}
	}
	return out
}

func (m *Manager) removeTokenLocked(rec *cidRecord) {
	list := m.tokens[rec.token]
	out := list[:0]
	for _, candidate := range list {
		if candidate != rec {
			out = append(out, candidate)
		}
	}
	if len(out) == 0 {
		delete(m.tokens, rec.token)
		return
	}
	m.tokens[rec.token] = out
}

// PurgeExpired moves due retired/closed CIDs to expired, removes their tokens,
// and releases key-generation references. It returns expiry witnesses.
func (m *Manager) PurgeExpired() ([]AuditWitness, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	before := len(m.audit)
	if err := m.purgeExpiredLocked(); err != nil {
		return copyAudit(m.audit[before:]), err
	}
	return copyAudit(m.audit[before:]), nil
}

func (m *Manager) purgeExpiredLocked() error {
	now := m.clock.Now().UTC()
	for _, rec := range m.sortedRecordsLocked() {
		if (rec.state != CIDRetiring && !rec.state.IsQuarantined()) ||
			rec.deadline.IsZero() || now.Before(rec.deadline) {
			continue
		}
		from := rec.state
		w := m.newWitnessLocked(AuditCIDExpired, rec, from, CIDExpired, "retention deadline elapsed")
		generation := rec.generation
		if err := m.commitLocked(w, func() {
			rec.state = CIDExpired
			rec.deadline = time.Time{}
			m.removeTokenLocked(rec)
		}); err != nil {
			return err
		}
		if err := m.keys.Release(generation); err != nil {
			return err
		}
	}
	return nil
}

// RebindPath handles a NAT rebinding/address change. It never closes the
// connection and never changes the routing partition binding.
func (m *Manager) RebindPath(newPath string) (AuditWitness, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return AuditWitness{}, ErrConnectionClosed
	}
	w := m.newWitnessLocked(AuditPathRebound, nil, CIDNone, CIDNone, fmt.Sprintf("%s->%s", m.lastPath, newPath))
	if err := m.commitLocked(w, func() { m.lastPath = newPath }); err != nil {
		return AuditWitness{}, err
	}
	return w, nil
}

// HandleNATRebind is the QUIC-stack-facing alias for RebindPath.
func (m *Manager) HandleNATRebind(newPath string) (AuditWitness, error) {
	return m.RebindPath(newPath)
}

// Route returns the immutable routing partition for new CIDs.
func (m *Manager) Route() RoutePartition {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.route
}

// CurrentKeyGeneration returns the generation used for new CIDs.
func (m *Manager) CurrentKeyGeneration() uint64 { return m.keys.CurrentGeneration() }

// TokenForCID returns the still-live token for a CID.
func (m *Manager) TokenForCID(cid []byte) (ResetToken, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[string(cid)]
	if !ok || rec.state == CIDExpired || rec.state == CIDNone {
		return ResetToken{}, false
	}
	if (rec.state == CIDRetiring || rec.state.IsQuarantined()) &&
		!rec.deadline.IsZero() && !m.clock.Now().Before(rec.deadline) {
		// Due entries are expired by PurgeExpired/DetectReset; do not report a
		// token as live between the deadline and physical cleanup.
		return ResetToken{}, false
	}
	return rec.token, true
}

// CIDInfo returns a copy of a CID record.
func (m *Manager) CIDInfo(cid []byte) (CIDInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.records[string(cid)]
	if !ok {
		return CIDInfo{}, false
	}
	return infoFromRecord(rec), true
}

func infoFromRecord(rec *cidRecord) CIDInfo {
	return CIDInfo{
		CID:               append([]byte(nil), rec.cid...),
		Sequence:          rec.sequence,
		State:             rec.state,
		Generation:        rec.generation,
		Route:             rec.route,
		Token:             rec.token,
		IssuedAt:          rec.issuedAt,
		RetireRequestedAt: rec.retireRequestedAt,
		PeerRetiredAt:     rec.peerRetiredAt,
		ClosedAt:          rec.closedAt,
		RetentionDeadline: rec.deadline,
	}
}

func (m *Manager) sortedRecordsLocked() []*cidRecord {
	out := make([]*cidRecord, 0, len(m.records))
	for _, rec := range m.records {
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].sequence < out[j].sequence })
	return out
}

// ManagerState is a deterministic snapshot for diagnostics and tests.
type ManagerState struct {
	Label             string
	Route             RoutePartition
	Closed            bool
	CurrentGeneration uint64
	AuditHead         uint64
	CIDs              []CIDInfo
}

// State returns a point-in-time snapshot ordered by CID sequence.
func (m *Manager) State() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := ManagerState{
		Label:             m.label,
		Route:             m.route,
		Closed:            m.closed,
		CurrentGeneration: m.keys.CurrentGeneration(),
		AuditHead:         m.seq,
	}
	for _, rec := range m.sortedRecordsLocked() {
		st.CIDs = append(st.CIDs, infoFromRecord(rec))
	}
	return st
}

// AuditLog returns a copy of all in-memory witnesses.
func (m *Manager) AuditLog() []AuditWitness {
	m.mu.Lock()
	defer m.mu.Unlock()
	return copyAudit(m.audit)
}

// AuditHead returns the current sequence/hash head.
func (m *Manager) AuditHead() (uint64, [32]byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seq, m.lastHash
}

// Label returns the audit label for this connection.
func (m *Manager) Label() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.label
}
