// Package nat64 implements a stateful NAT64 gateway (RFC 6146) together
// with the stateless IP/ICMP translation algorithm of RFC 7915.
//
// An Engine maintains three coordinated data structures:
//
//   - the Binding Information Base (BIB): endpoint-independent (EIM)
//     bindings between an IPv6 (address, transport port) and a pool IPv4
//     (address, transport port), keyed from both address families;
//   - the five-tuple session table: one entry per active flow, also keyed
//     from both address families, referencing its BIB entry;
//   - the RFC 6146 six-state TCP finite state machine, including the
//     V4_INIT 6-second unsolicited-SYN hold.
//
// ICMP query identifiers are treated as pseudo-ports so ICMP echo sessions
// reuse the same BIB/session machinery as TCP and UDP.
//
// All timekeeping is injected through Config.Now, which makes the state
// machine fully deterministic. Translated packets are handed to the caller
// through Config.OnV4 / Config.OnV6. An Engine is not safe for concurrent
// use; callers must serialize access.
package nat64

import (
	"errors"
	"net/netip"
	"time"
)

// Protocol numbers used inside the BIB and session tables. ProtoICMP is a
// local pseudo-protocol: ICMP identifiers are modeled as ports.
const (
	ProtoTCP  uint8 = 6
	ProtoUDP  uint8 = 17
	ProtoICMP uint8 = 1
)

// WellKnownPrefix is the RFC 6052 Well-Known Prefix used when Config.Prefix6
// is left unspecified.
var WellKnownPrefix = netip.MustParsePrefix("64:ff9b::/96")

// Default timer values from RFC 6146 section 3.5.
const (
	DefaultUDPTimeout       = 5 * time.Minute
	DefaultTCPEstTimeout    = 2 * time.Hour
	DefaultTCPTransTimeout  = 4 * time.Minute
	DefaultICMPTimeout      = 60 * time.Second
	DefaultV4InitSynTimeout = 6 * time.Second
)

// TCPState is the RFC 6146 TCP session state. UDP and ICMP sessions use
// StateNone.
type TCPState int

const (
	StateNone TCPState = iota
	StateV6Init
	StateV4Init
	StateV6Established
	StateV4Established
	StateV6Trans
	StateV4Trans
)

// String returns the RFC 6146 state name.
func (s TCPState) String() string {
	switch s {
	case StateNone:
		return "NONE"
	case StateV6Init:
		return "V6_INIT"
	case StateV4Init:
		return "V4_INIT"
	case StateV6Established:
		return "V6_ESTABLISHED"
	case StateV4Established:
		return "V4_ESTABLISHED"
	case StateV6Trans:
		return "V6_TRANS"
	case StateV4Trans:
		return "V4_TRANS"
	default:
		return "UNKNOWN"
	}
}

// BIBV6Key identifies a BIB entry from the IPv6 side.
type BIBV6Key struct {
	Addr  netip.Addr
	Proto uint8
	Port  uint16
}

// BIBV4Key identifies a BIB entry from the IPv4 side.
type BIBV4Key struct {
	Addr  netip.Addr
	Proto uint8
	Port  uint16
}

// BIBEntry is one endpoint-independent mapping (RFC 6146 section 4.1).
type BIBEntry struct {
	Proto    uint8
	V6Addr   netip.Addr
	V6Port   uint16
	V4Addr   netip.Addr
	V4Port   uint16
	Refs     int
	Created  time.Time
	LastUsed time.Time
}

// V6SessionKey is the IPv6-side five tuple of a session.
type V6SessionKey struct {
	Src   netip.Addr
	Dst   netip.Addr
	Proto uint8
	SPort uint16
	DPort uint16
}

// V4SessionKey is the IPv4-side five tuple of a session.
type V4SessionKey struct {
	Src   netip.Addr
	Dst   netip.Addr
	Proto uint8
	SPort uint16
	DPort uint16
}

// Session is one five-tuple flow (RFC 6146 section 4.2).
type Session struct {
	Proto       uint8
	V6          V6SessionKey
	V4          V4SessionKey
	State       TCPState
	Created     time.Time
	LastUsed    time.Time
	ExpiresAt   time.Time
	CachedV4SYN []byte
}

// SessionInfo is an inspected, value-copied view of a Session.
type SessionInfo struct {
	Proto        uint8
	V6           V6SessionKey
	V4           V4SessionKey
	State        TCPState
	Created      time.Time
	LastUsed     time.Time
	ExpiresAt    time.Time
	HasCachedSYN bool
}

// Stats counters exposed for observability and tests.
type Stats struct {
	PacketsV6In    uint64
	PacketsV4In    uint64
	PacketsV4Out   uint64
	PacketsV6Out   uint64
	SessionsClosed uint64
	BIBCreated     uint64
	V4InitTimeouts uint64
	Drops          uint64
}

// Config configures an Engine. Empty duration fields take RFC defaults.
type Config struct {
	// PoolV4 is the IPv4 address pool; at least one IPv4 address is required.
	PoolV4 []netip.Addr
	// Prefix6 is the RFC 6052 prefix (default: 64:ff9b::/96).
	Prefix6 netip.Prefix
	// Now is the clock (default: time.Now).
	Now func() time.Time

	UDPTimeout            time.Duration
	TCPEstablishedTimeout time.Duration
	TCPTransitoryTimeout  time.Duration
	ICMPTimeout           time.Duration
	// V4InitSynTimeout is the RFC 6146 V4_INIT unsolicited-SYN timer.
	V4InitSynTimeout time.Duration

	// OnV4 receives translated IPv4 packets; OnV6 receives translated IPv6
	// packets and locally generated ICMPv4 errors are delivered via OnV4.
	OnV4 func(packet []byte)
	OnV6 func(packet []byte)
}

// Engine is the stateful NAT64 translator.
type Engine struct {
	cfg    Config
	bibV6  map[BIBV6Key]*BIBEntry
	bibV4  map[BIBV4Key]*BIBEntry
	sessV6 map[V6SessionKey]*Session
	sessV4 map[V4SessionKey]*Session
	ipID   uint16
	stats  Stats
}

// New constructs an Engine. It returns an error when the pool is empty,
// contains non-IPv4 addresses, or the prefix is invalid.
func New(cfg Config) (*Engine, error) {
	if len(cfg.PoolV4) == 0 {
		return nil, errors.New("nat64: PoolV4 must contain at least one address")
	}
	for _, a := range cfg.PoolV4 {
		if !a.Is4() {
			return nil, errors.New("nat64: PoolV4 entries must be IPv4 addresses")
		}
	}
	prefix := cfg.Prefix6
	if !prefix.IsValid() {
		prefix = WellKnownPrefix
	}
	if prefix.Bits() != 32 && prefix.Bits() != 40 && prefix.Bits() != 48 &&
		prefix.Bits() != 56 && prefix.Bits() != 64 && prefix.Bits() != 96 {
		return nil, errors.New("nat64: RFC 6052 prefix length must be 32/40/48/56/64/96")
	}
	cfg.Prefix6 = prefix.Masked()
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.UDPTimeout <= 0 {
		cfg.UDPTimeout = DefaultUDPTimeout
	}
	if cfg.TCPEstablishedTimeout <= 0 {
		cfg.TCPEstablishedTimeout = DefaultTCPEstTimeout
	}
	if cfg.TCPTransitoryTimeout <= 0 {
		cfg.TCPTransitoryTimeout = DefaultTCPTransTimeout
	}
	if cfg.ICMPTimeout <= 0 {
		cfg.ICMPTimeout = DefaultICMPTimeout
	}
	if cfg.V4InitSynTimeout <= 0 {
		cfg.V4InitSynTimeout = DefaultV4InitSynTimeout
	}
	if cfg.OnV4 == nil {
		cfg.OnV4 = func([]byte) {}
	}
	if cfg.OnV6 == nil {
		cfg.OnV6 = func([]byte) {}
	}
	return &Engine{
		cfg:    cfg,
		bibV6:  make(map[BIBV6Key]*BIBEntry),
		bibV4:  make(map[BIBV4Key]*BIBEntry),
		sessV6: make(map[V6SessionKey]*Session),
		sessV4: make(map[V4SessionKey]*Session),
		ipID:   1,
	}, nil
}

// Stats returns a copy of the internal counters.
func (e *Engine) Stats() Stats { return e.stats }

// BIBEntries returns value copies of all current BIB entries.
func (e *Engine) BIBEntries() []BIBEntry {
	out := make([]BIBEntry, 0, len(e.bibV6))
	for _, b := range e.bibV6 {
		out = append(out, *b)
	}
	return out
}

// Sessions returns value copies of all current sessions.
func (e *Engine) Sessions() []SessionInfo {
	out := make([]SessionInfo, 0, len(e.sessV4))
	for _, s := range e.sessV4 {
		out = append(out, SessionInfo{
			Proto:        s.Proto,
			V6:           s.V6,
			V4:           s.V4,
			State:        s.State,
			Created:      s.Created,
			LastUsed:     s.LastUsed,
			ExpiresAt:    s.ExpiresAt,
			HasCachedSYN: len(s.CachedV4SYN) > 0,
		})
	}
	return out
}

func (e *Engine) now() time.Time { return e.cfg.Now() }

func (e *Engine) nextIPID() uint16 {
	e.ipID++
	if e.ipID == 0 {
		e.ipID = 1
	}
	return e.ipID
}

func (e *Engine) emitV4(pkt []byte) {
	e.stats.PacketsV4Out++
	e.cfg.OnV4(append([]byte(nil), pkt...))
}

func (e *Engine) emitV6(pkt []byte) {
	e.stats.PacketsV6Out++
	e.cfg.OnV6(append([]byte(nil), pkt...))
}

func (e *Engine) drop() { e.stats.Drops++ }
