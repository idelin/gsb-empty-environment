package taskpkg

import (
	"errors"
	"fmt"
)

// Generation identifies a connection's packet number space. Generations only
// advance; they never wrap.
type Generation uint64

// PacketNumber is a packet number within one connection generation.
type PacketNumber uint64

// FrameID identifies a retransmittable frame. The same FrameID may be
// referenced by more than one sent packet.
type FrameID uint64

// Direction identifies which direction of data an acknowledgment covers.
type Direction uint8

const (
	// DirectionOutbound acknowledges data sent by this host.
	DirectionOutbound Direction = iota + 1
	// DirectionInbound acknowledges data sent by the peer.
	DirectionInbound
)

func (d Direction) String() string {
	switch d {
	case DirectionOutbound:
		return "outbound"
	case DirectionInbound:
		return "inbound"
	default:
		return fmt.Sprintf("direction(%d)", uint8(d))
	}
}

// Identity is the authenticated identity of one endpoint's packet number
// space.
type Identity struct {
	Generation Generation
	Direction  Direction
}

// Interval is a closed packet-number interval [First, Last].
type Interval struct {
	First PacketNumber
	Last  PacketNumber
}

// Valid reports whether the interval is non-empty and does not wrap.
func (i Interval) Valid() bool {
	return i.First <= i.Last
}

// AckMessage is an acknowledgment supplied by the peer. AuthTag is opaque to
// this package and is passed to the injected Authenticator.
type AckMessage struct {
	Generation Generation
	Direction  Direction
	Intervals  []Interval
	AuthTag    []byte
}

// Authenticator verifies an acknowledgment before any packet bookkeeping is
// inspected. Implementations must check that msg's generation and direction
// match expected, and must verify AuthTag or any other connection binding.
//
// The message passed to Authenticate is a defensive copy. Mutating it cannot
// affect the admission decision.
type Authenticator interface {
	Authenticate(msg AckMessage, expected Identity) error
}

// AuthenticatorFunc adapts a function to Authenticator.
type AuthenticatorFunc func(msg AckMessage, expected Identity) error

// Authenticate implements Authenticator.
func (f AuthenticatorFunc) Authenticate(msg AckMessage, expected Identity) error {
	if f == nil {
		return ErrNoAuthenticator
	}
	return f(msg, expected)
}

// Config controls a new Admission.
type Config struct {
	Generation Generation
	Direction  Direction

	// PacketNumberLimit is the exclusive upper bound of the packet number
	// space: valid numbers are in [0, PacketNumberLimit). A zero value uses
	// the complete uint64 packet number space.
	//
	// The limit is useful for protocols whose wire packet number field is
	// smaller than 64 bits.
	PacketNumberLimit uint64

	// OnFrameReleased, when non-nil, is called once each time an admitted
	// acknowledgment drops a frame's reference count to zero. It is called
	// after the admission lock is released. It is not called for frames
	// abandoned by a connection migration.
	OnFrameReleased func(FrameID)
}

// AckResult describes the effect of an admitted acknowledgment. It is only
// fully populated when AdmitAck returns a nil error.
type AckResult struct {
	Admitted            bool
	AcknowledgedPackets int
	Packets             []PacketNumber
	ReleasedFrames      []FrameID
	// Rejected is set when admission failed because an interval contained a
	// packet number that was never sent. Number is the smallest such packet
	// number across all intervals in the message.
	Rejected *UnsentPacketError
}

// MigrationResult describes the state discarded when moving to a new
// connection generation.
type MigrationResult struct {
	From             Generation
	To               Generation
	AbandonedPackets int
	AbandonedFrames  []FrameID
}

var (
	// ErrNoAuthenticator is returned when an Admission is created without an
	// Authenticator.
	ErrNoAuthenticator = errors.New("taskpkg: authenticator is required")
	// ErrInvalidDirection is returned for a zero or otherwise unusable
	// direction.
	ErrInvalidDirection = errors.New("taskpkg: invalid direction")
	// ErrAuthentication wraps any failure returned by the Authenticator.
	ErrAuthentication = errors.New("taskpkg: acknowledgment authentication failed")
	// ErrConnectionChanged means a migration completed while authentication
	// was in progress.
	ErrConnectionChanged = errors.New("taskpkg: connection changed during authentication")
	// ErrGenerationMismatch means the authenticated message names a different
	// connection generation than the host.
	ErrGenerationMismatch = errors.New("taskpkg: acknowledgment generation mismatch")
	// ErrDirectionMismatch means the authenticated message names a different
	// direction than the host.
	ErrDirectionMismatch = errors.New("taskpkg: acknowledgment direction mismatch")
	// ErrMalformedInterval means an interval is empty or wraps.
	ErrMalformedInterval = errors.New("taskpkg: malformed acknowledgment interval")
	// ErrUnsentPacket means an interval references a packet number that was
	// never sent. Use errors.As with *UnsentPacketError to obtain the
	// smallest rejected packet number.
	ErrUnsentPacket = errors.New("taskpkg: acknowledgment references an unsent packet number")
	// ErrPacketNumberOutOfRange means the packet number is outside the
	// configured packet number space.
	ErrPacketNumberOutOfRange = errors.New("taskpkg: packet number outside the configured space")
	// ErrPacketNumberReused means a packet number was sent twice in one
	// generation.
	ErrPacketNumberReused = errors.New("taskpkg: packet number reused")
	// ErrPacketNumberOutOfOrder means packet numbers are not strictly
	// increasing.
	ErrPacketNumberOutOfOrder = errors.New("taskpkg: packet number is not increasing")
	// ErrPacketNumberExhausted means no larger packet number exists in the
	// current generation. The host must migrate to a greater generation.
	ErrPacketNumberExhausted = errors.New("taskpkg: packet number space exhausted")
	// ErrGenerationNotAdvanced means a migration did not increase the
	// generation.
	ErrGenerationNotAdvanced = errors.New("taskpkg: generation did not advance")
)

// MalformedIntervalError identifies an invalid closed interval.
type MalformedIntervalError struct {
	Index    int
	Interval Interval
}

func (e *MalformedIntervalError) Error() string {
	return fmt.Sprintf("ack interval %d [%d,%d] is malformed", e.Index, e.Interval.First, e.Interval.Last)
}

func (e *MalformedIntervalError) Unwrap() error {
	return ErrMalformedInterval
}

// UnsentPacketError identifies the smallest packet number in an acknowledgment
// that was never sent. All intervals are rejected when one such number exists.
type UnsentPacketError struct {
	Index    int
	Interval Interval
	Number   PacketNumber
}

func (e *UnsentPacketError) Error() string {
	return fmt.Sprintf("ack interval %d [%d,%d] references unsent packet number %d",
		e.Index, e.Interval.First, e.Interval.Last, e.Number)
}

func (e *UnsentPacketError) Unwrap() error {
	return ErrUnsentPacket
}

// SmallestUnsent returns the smallest unsent packet number carried by an
// admission error. It returns false for any other kind of error.
func SmallestUnsent(err error) (PacketNumber, bool) {
	var target *UnsentPacketError
	if errors.As(err, &target) {
		return target.Number, true
	}
	return 0, false
}

func cloneAckMessage(msg AckMessage) AckMessage {
	msg.Intervals = append([]Interval(nil), msg.Intervals...)
	msg.AuthTag = append([]byte(nil), msg.AuthTag...)
	return msg
}
