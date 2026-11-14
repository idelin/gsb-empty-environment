package netsec

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// StatelessResetTokenSize is the fixed size of a QUIC stateless reset token,
// as required by RFC 9000 section 10.3.
const StatelessResetTokenSize = 16

// MinStatelessResetPacketSize is the smallest packet that may be a stateless
// reset. RFC 9000 requires reset packets to contain at least 21 bytes.
const MinStatelessResetPacketSize = 21

// Default CID and retention policy values. A QUIC stack should normally set
// these from its maximum packet lifetime / idle timeout policy.
const (
	DefaultMaxCIDLength      = 20
	DefaultMaxPacketLifetime = 30 * time.Second
	DefaultConfirmationGrace = 3 * time.Second
)

var (
	// ErrCIDReused is returned when a generator produces a CID that has
	// already been used. The component fails closed instead of reissuing it.
	ErrCIDReused = errors.New("netsec: connection ID already used")
	// ErrTokenCollision is returned when a newly derived token collides with
	// a live token. Colliding tokens are not installed.
	ErrTokenCollision = errors.New("netsec: stateless reset token collision")
	// ErrConnectionClosed is returned for operations that require an open connection.
	ErrConnectionClosed = errors.New("netsec: connection is closed")
	// ErrKeyGenerationNotFound is returned for an unknown key generation.
	ErrKeyGenerationNotFound = errors.New("netsec: key generation not found")
	// ErrKeyInUse is returned when a key generation still has CID references.
	ErrKeyInUse = errors.New("netsec: key generation still has connection ID references")
	// ErrCurrentKeyGeneration is returned when attempting to destroy the active generation.
	ErrCurrentKeyGeneration = errors.New("netsec: cannot destroy the current key generation")
	// ErrJournalRollback is returned when a log record would duplicate or
	// move behind the durable high-water mark.
	ErrJournalRollback = errors.New("netsec: journal rollback or duplicate record")
	// ErrJournalCorrupt is returned when the audit hash chain is invalid.
	ErrJournalCorrupt = errors.New("netsec: journal hash chain is corrupt")
	// ErrCheckpointMismatch is returned when a checkpoint does not match the log.
	ErrCheckpointMismatch = errors.New("netsec: checkpoint does not match journal")
	// ErrRollbackDetected is returned when recovery would move behind a signed checkpoint.
	ErrRollbackDetected = errors.New("netsec: recovery log is behind checkpoint")
	// ErrNoRecoveryKey is returned when checkpoint operations lack a recovery key.
	ErrNoRecoveryKey = errors.New("netsec: no checkpoint/recovery key configured")
	// ErrAlreadyRecovered is returned when recovery is attempted on a manager
	// that has already applied state or local mutations.
	ErrAlreadyRecovered = errors.New("netsec: manager already contains state")
	// ErrInvalidCID is returned for a CID that violates the configured length bounds.
	ErrInvalidCID = errors.New("netsec: invalid connection ID")
	// ErrCIDNotFound is returned when a lifecycle operation names an unknown CID.
	ErrCIDNotFound = errors.New("netsec: connection ID not found")
	// ErrInvalidConfig is returned by constructors for invalid policy values.
	ErrInvalidConfig = errors.New("netsec: invalid configuration")
)

// RoutePartition identifies an edge gateway routing partition. Tokens and CIDs
// are bound to the partition in which they were issued.
type RoutePartition string

// ConnectionID is a QUIC connection ID. CIDs are opaque byte strings and are
// normally 1..20 bytes long.
type ConnectionID []byte

// Clone returns a defensive copy of the CID.
func (c ConnectionID) Clone() []byte {
	if c == nil {
		return nil
	}
	out := make([]byte, len(c))
	copy(out, c)
	return out
}

// Hex returns the canonical hex representation used in audit records.
func (c ConnectionID) Hex() string {
	return hex.EncodeToString(c)
}

// CIDState is the deterministic lifecycle state of a connection ID.
//
//	none -> issued -> active -> retiring -> retired -> expired
//	                   \___________________________> closed -> expired
//
// Retired and closed CIDs remain in quarantine until both the maximum packet
// lifetime and the peer-confirmation grace have elapsed. Tokens are removed
// and key references are dropped only at the transition to expired.
type CIDState uint8

const (
	CIDNone CIDState = iota
	CIDIssued
	CIDActive
	CIDRetiring
	CIDRetired
	CIDClosed
	CIDExpired
)

func (s CIDState) String() string {
	switch s {
	case CIDNone:
		return "none"
	case CIDIssued:
		return "issued"
	case CIDActive:
		return "active"
	case CIDRetiring:
		return "retiring"
	case CIDRetired:
		return "retired"
	case CIDClosed:
		return "closed"
	case CIDExpired:
		return "expired"
	default:
		return fmt.Sprintf("cid-state(%d)", uint8(s))
	}
}

// MarshalText makes CIDState human-readable in durable audit records.
func (s CIDState) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText restores a state from a durable audit record.
func (s *CIDState) UnmarshalText(b []byte) error {
	switch string(b) {
	case "none":
		*s = CIDNone
	case "issued":
		*s = CIDIssued
	case "active":
		*s = CIDActive
	case "retiring":
		*s = CIDRetiring
	case "retired":
		*s = CIDRetired
	case "closed":
		*s = CIDClosed
	case "expired":
		*s = CIDExpired
	default:
		return fmt.Errorf("netsec: unknown CID state %q", string(b))
	}
	return nil
}

// IsQuarantined reports whether a CID still owns a token/key reference while
// waiting out packet lifetime and confirmation grace.
func (s CIDState) IsQuarantined() bool { return s == CIDRetired || s == CIDClosed }

// allowedTransition is the CID state machine. It is deliberately small and
// total: every lifecycle operation checks this table before mutating state.
func allowedTransition(from, to CIDState) bool {
	switch from {
	case CIDNone:
		return to == CIDIssued
	case CIDIssued:
		return to == CIDActive || to == CIDRetiring || to == CIDRetired || to == CIDClosed
	case CIDActive:
		return to == CIDRetiring || to == CIDRetired || to == CIDClosed
	case CIDRetiring:
		return to == CIDRetired || to == CIDClosed || to == CIDExpired
	case CIDRetired:
		return to == CIDExpired
	case CIDClosed:
		return to == CIDExpired
	case CIDExpired:
		return false
	default:
		return false
	}
}

// AuditKind identifies the cause of an audit record / state transition.
type AuditKind string

const (
	AuditCIDIssued          AuditKind = "cid.issued"
	AuditCIDConfirmed       AuditKind = "cid.peer_confirmed"
	AuditCIDRetireRequested AuditKind = "cid.retire_requested"
	AuditCIDPeerRetired     AuditKind = "cid.peer_retired"
	AuditCIDClosed          AuditKind = "cid.closed"
	AuditCIDExpired         AuditKind = "cid.expired"
	AuditCIDReuseRejected   AuditKind = "cid.reuse_rejected"
	AuditTokenCollision     AuditKind = "token.collision"
	AuditConnectionClosed   AuditKind = "connection.closed"
	AuditPathRebound        AuditKind = "path.rebound"
	AuditKeyRotated         AuditKind = "key.rotated"
	AuditKeyDestroyed       AuditKind = "key.destroyed"
	AuditKeyDestroyRejected AuditKind = "key.destroy_rejected"
	AuditResetAccepted      AuditKind = "reset.accepted"
	AuditResetAmbiguous     AuditKind = "reset.ambiguous"
	AuditRecoveryComplete   AuditKind = "recovery.completed"
)

// AuditWitness is an append-only, hash-chained record of the input that caused
// a state transition. It is returned by every mutating API and can also be
// persisted verbatim for crash recovery.
type AuditWitness struct {
	Seq        uint64         `json:"seq"`
	At         time.Time      `json:"at"`
	Kind       AuditKind      `json:"kind"`
	Connection string         `json:"connection,omitempty"`
	CIDHex     string         `json:"cid,omitempty"`
	CIDSeq     uint64         `json:"cid_seq"`
	Generation uint64         `json:"generation"`
	Route      RoutePartition `json:"route,omitempty"`
	From       CIDState       `json:"from"`
	To         CIDState       `json:"to"`
	Reason     string         `json:"reason,omitempty"`
	PrevHash   string         `json:"prev_hash"`
	Hash       string         `json:"hash"`
	MAC        string         `json:"mac,omitempty"`
}

// Bytes returns the decoded CID in a witness.
func (w AuditWitness) Bytes() []byte {
	b, _ := hex.DecodeString(w.CIDHex)
	return b
}

// canonicalWitnessBytes excludes the hash itself and uses explicit
// fixed-width/length-prefixed fields so hashes are stable across processes.
func canonicalWitnessBytes(w AuditWitness) []byte {
	var b []byte
	b = appendField(b, []byte("q0183/audit/v1"))
	b = appendU64(b, w.Seq)
	b = appendU64(b, uint64(w.At.UTC().UnixNano()))
	b = appendField(b, []byte(w.Kind))
	b = appendField(b, []byte(w.Connection))
	b = appendField(b, []byte(w.CIDHex))
	b = appendU64(b, w.CIDSeq)
	b = appendU64(b, w.Generation)
	b = appendField(b, []byte(w.Route))
	b = appendU64(b, uint64(w.From))
	b = appendU64(b, uint64(w.To))
	b = appendField(b, []byte(w.Reason))
	b = appendField(b, []byte(w.PrevHash))
	return b
}

func appendU64(dst []byte, v uint64) []byte {
	var tmp [8]byte
	for i := 7; i >= 0; i-- {
		tmp[i] = byte(v)
		v >>= 8
	}
	return append(dst, tmp[:]...)
}

func appendField(dst, field []byte) []byte {
	dst = appendU64(dst, uint64(len(field)))
	return append(dst, field...)
}

func witnessHash(prev [32]byte, w AuditWitness) [32]byte {
	w.PrevHash = hex.EncodeToString(prev[:])
	h := sha256.New()
	_, _ = h.Write(canonicalWitnessBytes(w))
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// VerifyAuditChain checks the order and hash links of a freshly read log.
func VerifyAuditChain(records []AuditWitness) error {
	var prev [32]byte
	for i, w := range records {
		if w.Seq != uint64(i)+1 {
			return fmt.Errorf("%w: record %d has sequence %d", ErrJournalCorrupt, i, w.Seq)
		}
		if w.PrevHash != hex.EncodeToString(prev[:]) {
			return fmt.Errorf("%w: record %d has wrong previous hash", ErrJournalCorrupt, i)
		}
		if !w.At.IsZero() && w.At.Location() != time.UTC {
			w.At = w.At.UTC()
		}
		h := witnessHash(prev, w)
		if w.Hash != hex.EncodeToString(h[:]) {
			return fmt.Errorf("%w: record %d hash mismatch", ErrJournalCorrupt, i)
		}
		prev = h
	}
	return nil
}

// Clock allows deterministic tests and production use.
type Clock interface {
	Now() time.Time
}

// RealClock is the process wall clock.
type RealClock struct{}

// Now implements Clock.
func (RealClock) Now() time.Time { return time.Now().UTC() }

// ManualClock is a controllable clock. It is safe for concurrent use.
type ManualClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewManualClock creates a clock at t. A zero t defaults to Unix epoch.
func NewManualClock(t time.Time) *ManualClock {
	if t.IsZero() {
		t = time.Unix(0, 0).UTC()
	}
	return &ManualClock{now: t.UTC()}
}

// Now implements Clock.
func (c *ManualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the clock forward. Negative durations panic.
func (c *ManualClock) Advance(d time.Duration) {
	if d < 0 {
		panic("netsec: ManualClock cannot go backwards")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set moves the clock to t.
func (c *ManualClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t.UTC()
}

// CIDGenerator is the injection point used by tests and the QUIC stack.
type CIDGenerator interface {
	NextCID() ([]byte, error)
}

// CIDGeneratorFunc adapts a function to CIDGenerator.
type CIDGeneratorFunc func() ([]byte, error)

// NextCID implements CIDGenerator.
func (f CIDGeneratorFunc) NextCID() ([]byte, error) { return f() }

// RandomCIDGenerator emits random CIDs with an optional fixed prefix.
type RandomCIDGenerator struct {
	mu     sync.Mutex
	rnd    io.Reader
	length int
	prefix []byte
}

// NewRandomCIDGenerator creates a cryptographic random CID generator.
func NewRandomCIDGenerator(rnd io.Reader, length int, prefix []byte) (*RandomCIDGenerator, error) {
	if rnd == nil {
		rnd = rand.Reader
	}
	if length <= 0 || length > DefaultMaxCIDLength {
		return nil, fmt.Errorf("%w: CID length %d", ErrInvalidConfig, length)
	}
	if len(prefix) > length {
		return nil, fmt.Errorf("%w: prefix longer than CID", ErrInvalidConfig)
	}
	p := append([]byte(nil), prefix...)
	return &RandomCIDGenerator{rnd: rnd, length: length, prefix: p}, nil
}

// NextCID implements CIDGenerator.
func (g *RandomCIDGenerator) NextCID() ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]byte, g.length)
	copy(out, g.prefix)
	if _, err := io.ReadFull(g.rnd, out[len(g.prefix):]); err != nil {
		return nil, err
	}
	return out, nil
}

// SequentialCIDGenerator is a deterministic generator for tests. It encodes
// a monotonically increasing counter after the prefix.
func SequentialCIDGenerator(prefix []byte, length int) CIDGenerator {
	var mu sync.Mutex
	var counter uint64
	return CIDGeneratorFunc(func() ([]byte, error) {
		mu.Lock()
		defer mu.Unlock()
		if length <= 0 || length > DefaultMaxCIDLength || len(prefix) >= length {
			return nil, fmt.Errorf("%w: sequential CID dimensions", ErrInvalidConfig)
		}
		out := make([]byte, length)
		copy(out, prefix)
		v := counter
		counter++
		for i := length - 1; i >= len(prefix); i-- {
			out[i] = byte(v)
			v >>= 8
		}
		return out, nil
	})
}

// CIDLedger is a process-wide (or gateway-wide) tombstone set. Reserve is
// called for every issued CID and never releases the value, which is stronger
// than the required quarantine: a CID can never be reissued through this
// ledger, even after its token expires.
type CIDLedger struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewCIDLedger creates an empty ledger.
func NewCIDLedger() *CIDLedger {
	return &CIDLedger{seen: make(map[string]time.Time)}
}

// Reserve atomically records a CID. It returns false if the value was used
// before. At is retained for diagnostics.
func (l *CIDLedger) Reserve(cid []byte, at time.Time) bool {
	if l == nil {
		return true
	}
	key := string(cid)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[key]; ok {
		return false
	}
	l.seen[key] = at.UTC()
	return true
}

// Used reports whether the value has ever been reserved.
func (l *CIDLedger) Used(cid []byte) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.seen[string(cid)]
	return ok
}

// Len returns the number of tombstoned CIDs.
func (l *CIDLedger) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}

// TokenLedger tombstones every stateless reset token ever issued. CID values
// are already tombstoned, but a second independent defense prevents even a
// cryptographic token collision from binding an observed old token to a new
// CID. The ledger is shared by all connections in a Gateway.
type TokenLedger struct {
	mu   sync.Mutex
	seen map[ResetToken]time.Time
}

// NewTokenLedger creates an empty token tombstone set.
func NewTokenLedger() *TokenLedger {
	return &TokenLedger{seen: make(map[ResetToken]time.Time)}
}

// Reserve atomically records a token. It returns false if the value was ever
// issued before.
func (l *TokenLedger) Reserve(token ResetToken, at time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[token]; ok {
		return false
	}
	l.seen[token] = at.UTC()
	return true
}

// Used reports whether a token value has ever been reserved.
func (l *TokenLedger) Used(token ResetToken) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, ok := l.seen[token]
	return ok
}

// Len returns the number of tombstoned tokens.
func (l *TokenLedger) Len() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.seen)
}

// jsonMarshalAudit is kept here so FileJournal and other backends share one
// canonical serialization.
func jsonMarshalAudit(w AuditWitness) ([]byte, error) { return json.Marshal(w) }
