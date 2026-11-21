// Package pdbind implements IA_PD prefix delegation bindings for a DHCPv6
// server. It is meant to be invoked by the packet processing layer once a
// message has been parsed.
//
// Security properties enforced by this package:
//
//   - A delegation is bound to the tuple (client DUID, IAID). A client can
//     never touch a prefix delegated to another DUID/IAID.
//   - A prefix hint in a Solicit/Request is honoured only when the prefix is
//     inside the configured pool and is not already delegated to somebody
//     else. Otherwise the hint is ignored and a free prefix is allocated.
//   - The prefixes carried in Renew, Rebind and Release messages must match
//     the stored binding exactly, otherwise the answer is NoBinding and no
//     state changes.
//   - Relayed messages are accepted only when the relay-forward chain depth
//     does not exceed the configured limit (default 32) and the link address
//     of the innermost relay is valid and, when configured, belongs to an
//     allowed link range.
//   - Routing table changes are staged together with the binding change and
//     are applied only once the caller has confirmed that the Reply is being
//     sent (Decision.Commit). Releasing a binding removes its route.
package pdbind

import (
	"encoding/hex"
	"errors"
	"net/netip"
	"time"
)

// DefaultMaxRelayDepth is the maximum number of nested relay-forward messages
// accepted when Config.MaxRelayDepth is left zero.
const DefaultMaxRelayDepth = 32

// DHCPv6 message types (RFC 8415 section 7.3).
type MsgType uint8

const (
	MsgSolicit     MsgType = 1
	MsgAdvertise   MsgType = 2
	MsgRequest     MsgType = 3
	MsgConfirm     MsgType = 4
	MsgRenew       MsgType = 5
	MsgRebind      MsgType = 6
	MsgReply       MsgType = 7
	MsgRelease     MsgType = 8
	MsgDecline     MsgType = 9
	MsgReconfigure MsgType = 10
	MsgInfoRequest MsgType = 11
	MsgRelayForw   MsgType = 12
	MsgRelayRepl   MsgType = 13
)

// DHCPv6 status codes (RFC 8415 section 21.13). Only the codes relevant to
// prefix delegation are modelled.
type StatusCode uint16

const (
	StatusSuccess       StatusCode = 0
	StatusUnspecFail    StatusCode = 1
	StatusNoBinding     StatusCode = 3
	StatusNoPrefixAvail StatusCode = 6
)

// Errors returned by Manager.Process and the wire parsers. They describe
// protocol-level failures that make a message unprocessable, as opposed to
// StatusCode values which are encoded in a DHCPv6 Reply.
var (
	// ErrRelayDepth is returned when the relay-forward chain is deeper than
	// the configured limit.
	ErrRelayDepth = errors.New("pdbind: relay-forward chain depth exceeds limit")
	// ErrInvalidLink is returned when a relay hop carries an unusable link or
	// peer address.
	ErrInvalidLink = errors.New("pdbind: invalid relay link or peer address")
	// ErrLinkNotAllowed is returned when the innermost link address is not
	// part of any configured allowed link range.
	ErrLinkNotAllowed = errors.New("pdbind: relay link address is not allowed")
	// ErrMalformedMessage is returned when a packet cannot be parsed.
	ErrMalformedMessage = errors.New("pdbind: malformed DHCPv6 message")
	// ErrRelayMessage is returned when a relay message is passed to a parser
	// that expects a client message.
	ErrRelayMessage = errors.New("pdbind: relay message passed to client message parser")
	// ErrNoDUID is returned when the client message carries no client DUID.
	ErrNoDUID = errors.New("pdbind: message has no client DUID")
	// ErrUnsupportedMessage is returned for message types this package does
	// not process.
	ErrUnsupportedMessage = errors.New("pdbind: unsupported message type")
)

// PrefixLease represents an IAPREFIX option: the delegated prefix together
// with its preferred and valid lifetimes in seconds.
type PrefixLease struct {
	Prefix       netip.Prefix
	PreferredLft uint32
	ValidLft     uint32
}

// IAPD represents an IA_PD option received from (or answered to) a client.
// Hint is the IAPREFIX option nested in the IA_PD. On input it is the
// client-provided hint or the prefix being renewed/released; on output it is
// the delegated lease. It may be nil when the IA_PD contains no IAPREFIX.
type IAPD struct {
	IAID uint32
	Hint *PrefixLease
}

// ClientMessage is the parsed content of a DHCPv6 client message relevant to
// prefix delegation.
type ClientMessage struct {
	Type     MsgType
	DUID     []byte
	IAPDs    []IAPD
	PeerAddr netip.Addr
}

// RelayHop describes one relay-forward envelope, ordered from the relay
// closest to the client (index 0) to the outermost relay.
type RelayHop struct {
	HopCount uint8
	LinkAddr netip.Addr
	PeerAddr netip.Addr
}

// BindingKey uniquely identifies a delegation: the client DUID (hex encoded)
// together with the IAID.
type BindingKey struct {
	DUIDHex string
	IAID    uint32
}

// MakeKey builds the BindingKey for a client DUID and IAID.
func MakeKey(duid []byte, iaid uint32) BindingKey {
	return BindingKey{DUIDHex: hex.EncodeToString(duid), IAID: iaid}
}

// String returns "duid-hex/iaid".
func (k BindingKey) String() string {
	return k.DUIDHex + "/" + itoa(k.IAID)
}

// Binding is a stored prefix delegation.
type Binding struct {
	Key          BindingKey
	DUID         []byte
	IAID         uint32
	Prefix       netip.Prefix
	LinkAddr     netip.Addr
	NextHop      netip.Addr
	PreferredLft uint32
	ValidLft     uint32
	PreferredAt  time.Time
	ValidUntil   time.Time
}

// Lease returns the PrefixLease representing this binding.
func (b *Binding) Lease() *PrefixLease {
	return &PrefixLease{
		Prefix:       b.Prefix,
		PreferredLft: b.PreferredLft,
		ValidLft:     b.ValidLft,
	}
}

// Config configures a Manager.
type Config struct {
	// Pool is the aggregate prefix pool from which delegations are carved.
	Pool PrefixPool
	// AllowedLinks restricts the innermost relay link-address to the given
	// ranges. When empty, any valid non-unspecified link address is accepted.
	AllowedLinks []netip.Prefix
	// MaxRelayDepth limits the relay-forward chain depth. Zero means
	// DefaultMaxRelayDepth.
	MaxRelayDepth int
	// PreferredLft and ValidLft are delegated lifetimes in seconds. Zero
	// selects sensible defaults.
	PreferredLft uint32
	ValidLft     uint32
	// Now is the clock used for lifetime computations. It is overridable in
	// tests; when nil time.Now is used.
	Now func() time.Time
}
