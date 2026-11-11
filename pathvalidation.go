// Package netsec implements path validation for a connection-migration-capable
// transport host.
//
// After a mobile network handoff, a validation response arriving on one path
// must never authenticate a different candidate address. Every challenge is
// therefore bound to three things:
//
//   - the connection instance ([ConnID]) that owns the validation,
//   - the candidate [Path] (local socket and peer address), and
//   - an unpredictable random [Nonce] carried by the probe.
//
// Application data is restricted to the single active path, and a path can
// only become active once it has been validated and the configured [Strategy]
// has selected it among all validated candidates.
package netsec

import (
	"errors"
	"net/netip"
	"time"
)

// DefaultRetryInterval is used when Config.RetryInterval is zero.
const DefaultRetryInterval = 250 * time.Millisecond

// ConnID identifies a connection instance. Every network handoff that mints a
// new connection instance gets a fresh ID; validation material produced for an
// older instance can never authenticate a newer one.
type ConnID uint64

// Nonce is the random identifier carried by a PATH_CHALLENGE probe and echoed
// back by PATH_RESPONSE.
type Nonce [8]byte

// Path is a candidate communication path. All fields participate in the
// challenge binding: a response is accepted only when its connection instance,
// local socket address and peer source address match a registered candidate.
type Path struct {
	Conn  ConnID
	Local netip.AddrPort
	Peer  netip.AddrPort
}

// Sentinel errors returned by the validator.
var (
	// ErrClosed is returned after the validator has been closed.
	ErrClosed = errors.New("netsec: validator closed")
	// ErrStaleConnection means the operation references a connection instance
	// that is no longer current.
	ErrStaleConnection = errors.New("netsec: stale connection instance")
	// ErrDuplicateCandidate is returned when the same path is registered twice.
	ErrDuplicateCandidate = errors.New("netsec: duplicate candidate path")
	// ErrInvalidDeadline is returned when the deadline is not in the future.
	ErrInvalidDeadline = errors.New("netsec: validation deadline must be in the future")
	// ErrUnknownPath is returned for a response or send on an unregistered path.
	ErrUnknownPath = errors.New("netsec: unknown candidate path")
	// ErrNonceMismatch is returned when the response nonce was never issued for
	// the candidate path (it is stale, forged, or belongs to another path).
	ErrNonceMismatch = errors.New("netsec: nonce does not match an outstanding challenge")
	// ErrPathNotValidated is returned when app data is sent on a path whose
	// challenge has not completed yet.
	ErrPathNotValidated = errors.New("netsec: path not validated")
	// ErrPathNotActive is returned when app data is sent on a validated path
	// that the strategy did not select as the active path.
	ErrPathNotActive = errors.New("netsec: path is not the active path")
)

// FailureReason explains why a candidate was terminated.
type FailureReason string

const (
	// ReasonDeadline means the fixed overall validation deadline elapsed.
	ReasonDeadline FailureReason = "deadline exceeded"
	// ReasonCanceled means the candidate was canceled explicitly or by Close.
	ReasonCanceled FailureReason = "canceled"
	// ReasonMigrated means the connection moved to a new connection instance.
	ReasonMigrated FailureReason = "connection migrated"
	// ReasonNonceSource means fresh random nonces could not be produced.
	ReasonNonceSource FailureReason = "nonce source failed"
)

// EventKind enumerates validator lifecycle events.
type EventKind int

const (
	// EventValidated fires when a candidate answer matches an outstanding
	// challenge bound to that path.
	EventValidated EventKind = iota + 1
	// EventActivated fires when the active path changes.
	EventActivated
	// EventDeactivated fires when the previous active path goes away and no
	// replacement is available.
	EventDeactivated
	// EventFailed fires when a candidate is terminated without validation.
	EventFailed
)

// Event is a validator lifecycle notification.
type Event struct {
	Kind   EventKind
	Path   Path
	Reason FailureReason
}

// SendFunc transmits a PATH_CHALLENGE probe. The implementation MUST send from
// path.Local to path.Peer; the bound addresses are handed over explicitly so
// callers (and tests) can assert the datagram actually leaves on the candidate
// path and not on some other socket.
type SendFunc func(path Path, nonce Nonce) error

// NonceSource mints fresh random challenge identifiers.
type NonceSource func() (Nonce, error)

// Strategy picks the active path among all currently validated candidates.
// The validated slice is ordered by candidate registration (oldest first).
// Returning ok=false leaves the host without an active path.
type Strategy interface {
	Select(validated []Path) (chosen Path, ok bool)
}

// Timer is the controllable timer abstraction used by the validator.
type Timer interface {
	Stop() bool
	Reset(d time.Duration) bool
}

// Clock abstracts time so retry behavior can be driven deterministically.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, fn func()) Timer
}

// Config configures a [Validator].
type Config struct {
	// Conn is the initial connection instance.
	Conn ConnID
	// Clock defaults to the wall clock. Tests inject a fake clock.
	Clock Clock
	// RetryInterval is the spacing between challenge retries. Retries never
	// extend the per-candidate deadline.
	RetryInterval time.Duration
	// NonceSource defaults to crypto/rand. Tests may inject a deterministic one.
	NonceSource NonceSource
	// Send is the probe transmission callback. Required.
	Send SendFunc
	// Strategy defaults to preferring the most recently registered candidate.
	Strategy Strategy
	// OnEvent, when set, receives lifecycle events. It is invoked without the
	// validator lock held.
	OnEvent func(Event)
}
