package netsec

import (
	"fmt"
	"io"
)

const (
	// GreetingSize is the fixed size of a ZMTP 3.x greeting.
	GreetingSize = 64

	greetingSignatureFirst = 0xFF
	greetingSignatureLast  = 0x7F
	greetingMajorOffset    = 10
	greetingMinorOffset    = 11
	greetingMechanismStart = 12
	greetingMechanismEnd   = 32
	greetingAsServerOffset = 32
)

// Role identifies the security role of the local endpoint.
type Role uint8

const (
	// RoleClient is the ZMTP client role. Its greeting has as-server = 0.
	RoleClient Role = iota
	// RoleServer is the ZMTP server role. Its greeting has as-server = 1.
	RoleServer
)

// String returns the role name.
func (r Role) String() string {
	switch r {
	case RoleClient:
		return "client"
	case RoleServer:
		return "server"
	default:
		return "unknown"
	}
}

func (r Role) valid() bool {
	return r == RoleClient || r == RoleServer
}

// peerRole returns the role that a peer must announce to this role.
func (r Role) peerRole() Role {
	if r == RoleServer {
		return RoleClient
	}
	return RoleServer
}

// Mechanism is a ZMTP security mechanism name.
type Mechanism string

const (
	// MechanismNull provides no authentication or confidentiality.
	MechanismNull Mechanism = "NULL"
	// MechanismPlain provides username/password authentication.
	MechanismPlain Mechanism = "PLAIN"
	// MechanismCurve provides CurveZMQ authentication and confidentiality.
	MechanismCurve Mechanism = "CURVE"
)

// NewMechanism validates name according to the ZMTP mechanism grammar.
func NewMechanism(name string) (Mechanism, error) {
	if len(name) == 0 || len(name) > 20 {
		return "", fmt.Errorf("%w: length %d", ErrInvalidMechanism, len(name))
	}
	for i := 0; i < len(name); i++ {
		if !validMechanismChar(name[i]) {
			return "", fmt.Errorf("%w: invalid octet 0x%02x at %d", ErrInvalidMechanism, name[i], i)
		}
	}
	return Mechanism(name), nil
}

func validMechanismChar(c byte) bool {
	return c >= 'A' && c <= 'Z' ||
		c >= '0' && c <= '9' ||
		c == '-' || c == '_' || c == '.'
}

func (m Mechanism) valid() bool {
	_, err := NewMechanism(string(m))
	return err == nil
}

// Field returns the mechanism encoded as a 20-octet, NUL-padded field.
func (m Mechanism) Field() ([20]byte, error) {
	var field [20]byte
	if !m.valid() {
		return field, fmt.Errorf("%w: %q", ErrInvalidMechanism, string(m))
	}
	copy(field[:], []byte(m))
	return field, nil
}

// ParseMechanismField decodes a 20-octet mechanism field. The name must
// occupy a contiguous prefix and every octet after the first NUL must be
// zero; trailing garbage is rejected rather than silently ignored.
func ParseMechanismField(field [20]byte) (Mechanism, error) {
	nameLength := len(field)
	for i, c := range field {
		if c == 0 {
			nameLength = i
			for j := i; j < len(field); j++ {
				if field[j] != 0 {
					return "", fmt.Errorf("%w: octet 0x%02x at %d", ErrMechanismPadding, field[j], j)
				}
			}
			break
		}
	}
	if nameLength == 0 {
		return "", fmt.Errorf("%w: empty name", ErrInvalidMechanism)
	}
	return NewMechanism(string(field[:nameLength]))
}

// Greeting is a decoded ZMTP 3.x greeting.
type Greeting struct {
	Major     byte
	Minor     byte
	Mechanism Mechanism
	AsServer  bool
}

// NewGreeting constructs a ZMTP 3.1 greeting for role and mechanism.
func NewGreeting(role Role, mechanism Mechanism) (Greeting, error) {
	if !role.valid() {
		return Greeting{}, fmt.Errorf("%w: role %d", ErrRoleMismatch, role)
	}
	if !mechanism.valid() {
		return Greeting{}, fmt.Errorf("%w: %q", ErrInvalidMechanism, string(mechanism))
	}
	return Greeting{
		Major:     3,
		Minor:     1,
		Mechanism: mechanism,
		AsServer:  role == RoleServer,
	}, nil
}

// MarshalBinary encodes the greeting as exactly 64 octets. The version
// major must be at least 3 so this package cannot emit a pre-3.0 greeting.
func (g Greeting) MarshalBinary() ([]byte, error) {
	if g.Major < 3 {
		return nil, fmt.Errorf("%w: major %d", ErrUnsupportedVersion, g.Major)
	}
	field, err := g.Mechanism.Field()
	if err != nil {
		return nil, err
	}

	out := make([]byte, GreetingSize)
	out[0] = greetingSignatureFirst
	out[9] = greetingSignatureLast
	out[greetingMajorOffset] = g.Major
	out[greetingMinorOffset] = g.Minor
	copy(out[greetingMechanismStart:greetingMechanismEnd], field[:])
	if g.AsServer {
		out[greetingAsServerOffset] = 1
	}
	return out, nil
}

// ParseGreeting decodes and validates a 64-octet ZMTP greeting. Extra
// octets after the greeting are ignored so callers may pass a larger
// buffer without losing the following frame.
func ParseGreeting(data []byte) (Greeting, error) {
	if len(data) < GreetingSize {
		return Greeting{}, fmt.Errorf("%w: got %d octets", ErrGreetingTruncated, len(data))
	}
	if data[0] != greetingSignatureFirst || data[9] != greetingSignatureLast {
		return Greeting{}, fmt.Errorf(
			"%w: first=0x%02x last=0x%02x",
			ErrGreetingSignature, data[0], data[9],
		)
	}

	raw := data[:GreetingSize]
	major := raw[greetingMajorOffset]
	if major < 3 {
		return Greeting{}, fmt.Errorf("%w: major %d", ErrUnsupportedVersion, major)
	}

	var mechanismField [20]byte
	copy(mechanismField[:], raw[greetingMechanismStart:greetingMechanismEnd])
	mechanism, err := ParseMechanismField(mechanismField)
	if err != nil {
		return Greeting{}, err
	}

	var asServer bool
	switch raw[greetingAsServerOffset] {
	case 0:
		asServer = false
	case 1:
		asServer = true
	default:
		return Greeting{}, fmt.Errorf(
			"%w: value %d", ErrMalformedAsServer, raw[greetingAsServerOffset],
		)
	}

	return Greeting{
		Major:     major,
		Minor:     raw[greetingMinorOffset],
		Mechanism: mechanism,
		AsServer:  asServer,
	}, nil
}

// ReadGreeting reads exactly one greeting from r.
func ReadGreeting(r io.Reader) (Greeting, error) {
	data, err := readGreetingBytes(r)
	if err != nil {
		return Greeting{}, err
	}
	return ParseGreeting(data)
}

// readGreetingBytes reads the 10-octet signature and major version first,
// so a pre-3.0 peer is rejected without waiting for the remaining 53
// octets of a ZMTP 3.x greeting.
func readGreetingBytes(r io.Reader) ([]byte, error) {
	data := make([]byte, GreetingSize)
	if _, err := io.ReadFull(r, data[:greetingMajorOffset+1]); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGreetingTruncated, err)
	}
	if data[0] != greetingSignatureFirst || data[9] != greetingSignatureLast {
		return nil, fmt.Errorf(
			"%w: first=0x%02x last=0x%02x",
			ErrGreetingSignature, data[0], data[9],
		)
	}
	if data[greetingMajorOffset] < 3 {
		return nil, fmt.Errorf("%w: major %d", ErrUnsupportedVersion, data[greetingMajorOffset])
	}
	if _, err := io.ReadFull(r, data[greetingMajorOffset+1:]); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGreetingTruncated, err)
	}
	return data, nil
}

// WriteGreeting writes one greeting to w.
func WriteGreeting(w io.Writer, g Greeting) error {
	data, err := g.MarshalBinary()
	if err != nil {
		return err
	}
	return writeFull(w, data)
}

// Config configures one handshake endpoint.
type Config struct {
	// Role is the local endpoint's role.
	Role Role

	// Mechanism is the only mechanism this endpoint will accept. A peer
	// announcing anything else is rejected.
	Mechanism Mechanism

	// Major and Minor select the version sent in the local greeting. A
	// zero Major defaults to ZMTP 3.1. The local major version must be at
	// least 3.
	Major byte
	Minor byte

	// MaxFrameSize limits individual frames read from the peer. Values
	// less than or equal to zero use DefaultMaxFrameSize.
	MaxFrameSize int64
}

// DefaultMaxFrameSize is the default limit for a single ZMTP frame body.
const DefaultMaxFrameSize int64 = 8 << 20

func (c Config) effectiveVersion() (byte, byte) {
	if c.Major == 0 {
		return 3, 1
	}
	return c.Major, c.Minor
}

func (c Config) effectiveMaxFrameSize() int64 {
	if c.MaxFrameSize <= 0 {
		return DefaultMaxFrameSize
	}
	return c.MaxFrameSize
}

// Validate checks the local configuration without touching the network.
func (c Config) Validate() error {
	if !c.Role.valid() {
		return fmt.Errorf("%w: role %d", ErrRoleMismatch, c.Role)
	}
	if !c.Mechanism.valid() {
		return fmt.Errorf("%w: %q", ErrInvalidMechanism, string(c.Mechanism))
	}
	major, _ := c.effectiveVersion()
	if major < 3 {
		return fmt.Errorf("%w: major %d", ErrUnsupportedVersion, major)
	}
	return nil
}

func (c Config) localGreeting() (Greeting, error) {
	if err := c.Validate(); err != nil {
		return Greeting{}, err
	}
	major, minor := c.effectiveVersion()
	return Greeting{
		Major:     major,
		Minor:     minor,
		Mechanism: c.Mechanism,
		AsServer:  c.Role == RoleServer,
	}, nil
}

// ValidatePeer checks the peer greeting against the local security policy.
// The mechanism must match exactly, and the peer's as-server bit must
// identify the complementary role.
func (c Config) ValidatePeer(peer Greeting) error {
	if peer.Major < 3 {
		return fmt.Errorf("%w: major %d", ErrUnsupportedVersion, peer.Major)
	}
	if peer.Mechanism != c.Mechanism {
		mismatch := fmt.Errorf(
			"%w: peer=%q local=%q",
			ErrMechanismMismatch, string(peer.Mechanism), string(c.Mechanism),
		)
		if peer.Mechanism == MechanismNull && c.Mechanism != MechanismNull {
			return fmt.Errorf("%w: %w", ErrMechanismDowngrade, mismatch)
		}
		return mismatch
	}

	wantAsServer := c.Role.peerRole() == RoleServer
	if peer.AsServer != wantAsServer {
		return fmt.Errorf(
			"%w: peer as-server=%t, local role=%s",
			ErrRoleMismatch, peer.AsServer, c.Role,
		)
	}
	return nil
}
