// Package ndadmit parses and performs admission control on ICMPv6 Neighbor
// Discovery messages (RFC 4861, with constraints from RFC 4191 and RFC 8106).
//
// A network host receives ND messages from a shared link and must never let a
// malformed message drive the protocol state machine into an infinite loop or
// make it adopt an attacker-controlled identity. The Parse entry point is a
// single choke point that enforces:
//
//   - the IPv6 hop limit is exactly 255 (off-link senders cannot forge it);
//   - the ICMPv6 code is zero and the fixed message body is present;
//   - every option has a non-zero, in-bounds length, including unknown
//     options which are skipped solely according to their length field;
//   - security-critical, singleton options that name an identity (SLLA, TLLA,
//     MTU, Redirected Header) appear at most once;
//   - source-address/option relationships (e.g. an unspecified source, as used
//     by Duplicate Address Detection, must not carry a link-layer address);
//   - router advertisements and redirects originate from a link-local source.
//
// All option bytes are counted against the packet so that a long run of
// unknown options cannot exhaust a host's per-packet processing budget.
package ndadmit

import (
	"errors"
	"net/netip"
)

// HopLimit is the only acceptable IPv6 hop limit for ND messages.
const HopLimit uint8 = 255

// ICMPv6 ND message types (RFC 4861 section 4).
const (
	RouterSolicitation    = 133
	RouterAdvertisement   = 134
	NeighborSolicitation  = 135
	NeighborAdvertisement = 136
	Redirect              = 137
)

// Option types defined by RFC 4861, RFC 4191 and RFC 8106.
const (
	OptSourceLinkLayerAddr = 1
	OptTargetLinkLayerAddr = 2
	OptPrefixInformation   = 3
	OptRedirectedHeader    = 4
	OptMTU                 = 5
	OptRouteInformation    = 24
	OptRDNSS               = 25
	OptDNSSL               = 31
)

// optionUnit is the size, in bytes, of one option length unit.
const optionUnit = 8

// minFixed is the size of the fixed header per message type.
var minFixed = map[int]int{
	RouterSolicitation:    4,
	RouterAdvertisement:   16,
	NeighborSolicitation:  24,
	NeighborAdvertisement: 24,
	Redirect:              36,
}

// Sentinel errors. Every rejection returned by Parse wraps one of these so a
// caller can classify a packet with errors.Is.
var (
	// ErrHopLimit means the carrying packet did not arrive with a hop limit
	// of 255.
	ErrHopLimit = errors.New("ndadmit: hop limit must be 255")

	// ErrBadCode means the ICMPv6 code field was not zero.
	ErrBadCode = errors.New("ndadmit: ICMPv6 code must be zero")

	// ErrUnknownType means the ICMPv6 type is not one of the five ND types.
	ErrUnknownType = errors.New("ndadmit: unknown ICMPv6 type")

	// ErrTruncated means the packet ended before the fixed header, or before
	// an option claimed by its length field.
	ErrTruncated = errors.New("ndadmit: message truncated")

	// ErrZeroOption means an option carried a length of zero. Such an option
	// would make a naive parser advance by zero bytes forever.
	ErrZeroOption = errors.New("ndadmit: option length is zero")

	// ErrOptionLen means a recognized option's length is invalid for its
	// type, or a link-layer address does not have the expected size.
	ErrOptionLen = errors.New("ndadmit: invalid option length")

	// ErrDuplicateOption means a security-critical option that may name the
	// host's identity appeared more than once. Repeated prefix/route options
	// remain legal and are not rejected.
	ErrDuplicateOption = errors.New("ndadmit: duplicate security-critical option")

	// ErrBadFields means a fixed-header field or a type-specific constraint
	// (address scope, flags or source/option relationship) was violated.
	ErrBadFields = errors.New("ndadmit: invalid message field")

	// ErrChecksum means the ICMPv6 checksum did not verify.
	ErrChecksum = errors.New("ndadmit: ICMPv6 checksum mismatch")
)

// Config controls optional admission checks. The zero value applies the
// mandatory RFC 4861 checks with no checksum verification, matching a host
// whose kernel has already verified the ICMPv6 checksum before delivery.
type Config struct {
	// VerifyChecksum forces ICMPv6 checksum verification. A host whose stack
	// already validated the checksum can leave this false.
	VerifyChecksum bool

	// LinkLayerAddrLen, when positive, requires SLLA/TLLA address fields to
	// be exactly this many bytes (6 for Ethernet). Zero disables the size
	// check for non-Ethernet links.
	LinkLayerAddrLen int
}

// Prefix is a decoded Prefix Information option (RFC 4861 section 4.6.2).
type Prefix struct {
	Length            uint8
	OnLink            bool
	Autonomous        bool
	ValidLifetime     uint32
	PreferredLifetime uint32
	Prefix            netip.Addr
}

// RouteInfo is a decoded Route Information option (RFC 4191).
type RouteInfo struct {
	Prefix     netip.Addr
	PrefixLen  uint8
	Preference uint16
	Lifetime   uint32
}

// Message is an admitted ND message. All fields have passed validation; a
// host may consume them without re-checking framing constraints.
type Message struct {
	Type uint8
	Code uint8

	// Router advertisement fields.
	CurHopLimit      uint8
	Managed          bool
	Other            bool
	RouterLifetime   uint16
	ReachableTime    uint32
	RetransTimer     uint32
	RouterPreference uint16

	// Neighbor advertisement flags.
	RouterFlag bool
	Solicited  bool
	Override   bool

	Target      netip.Addr // NS / NA
	Destination netip.Addr // Redirect

	SLLA []byte
	TLLA []byte
	MTU  uint32
	// HasMTU reports whether a well-formed MTU option was present.
	HasMTU bool

	Prefixes []Prefix
	Routes   []RouteInfo

	// Unknown counts options whose type this implementation does not
	// understand. They were validated for framing and skipped.
	Unknown int
	// OptionCount is the total number of options, known and unknown, that
	// shared the packet's option budget.
	OptionCount int
}

// Parse admits an ND message using the default configuration.
func Parse(hopLimit uint8, src, dst netip.Addr, icmpPayload []byte) (*Message, error) {
	return ParseWith(Config{}, hopLimit, src, dst, icmpPayload)
}

// ParseWith admits an ND message under cfg. icmpPayload is the ICMPv6 packet
// starting at the type byte; src and dst are the IPv6 source and destination
// addresses of the carrying packet.
func ParseWith(cfg Config, hopLimit uint8, src, dst netip.Addr, icmpPayload []byte) (*Message, error) {
	if hopLimit != HopLimit {
		return nil, ErrHopLimit
	}
	if !src.Is6() || !dst.Is6() {
		return nil, ErrBadFields
	}
	if len(icmpPayload) < 4 {
		return nil, ErrTruncated
	}

	msgType := int(icmpPayload[0])
	code := icmpPayload[1]
	if code != 0 {
		return nil, ErrBadCode
	}
	fixedLen, ok := minFixed[msgType]
	if !ok {
		return nil, ErrUnknownType
	}
	if len(icmpPayload) < fixedLen {
		return nil, ErrTruncated
	}

	if cfg.VerifyChecksum {
		if !checksumValid(src, dst, icmpPayload) {
			return nil, ErrChecksum
		}
	}

	msg := &Message{Type: icmpPayload[0], Code: code}
	body := icmpPayload[4:fixedLen]

	switch msgType {
	case RouterAdvertisement:
		msg.CurHopLimit = body[0]
		flags := body[1]
		msg.Managed = flags&0x80 != 0
		msg.Other = flags&0x40 != 0
		msg.RouterPreference = uint16((flags >> 3) & 0x3)
		msg.RouterLifetime = be16(body[2:4])
		msg.ReachableTime = be32(body[4:8])
		msg.RetransTimer = be32(body[8:12])
		if msg.RouterPreference == 2 { // 0b10 is reserved by RFC 4191.
			return nil, ErrBadFields
		}
	case NeighborSolicitation:
		msg.Target = netip.AddrFrom16(be128(body[4:20]))
	case NeighborAdvertisement:
		flags := body[0]
		msg.RouterFlag = flags&0x80 != 0
		msg.Solicited = flags&0x40 != 0
		msg.Override = flags&0x20 != 0
		msg.Target = netip.AddrFrom16(be128(body[4:20]))
	case Redirect:
		msg.Target = netip.AddrFrom16(be128(body[0:16]))
		msg.Destination = netip.AddrFrom16(be128(body[16:32]))
	}

	seen := make(map[uint8]bool)
	if err := parseOptions(cfg, msg, icmpPayload[fixedLen:], seen); err != nil {
		return nil, err
	}
	if err := validateRelations(msgType, src, dst, msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// parseOptions walks every option. The loop can never make zero progress: a
// zero length is rejected before the cursor advances, and every other option
// consumes at least optionUnit bytes drawn from the same packet buffer.
func parseOptions(cfg Config, msg *Message, options []byte, seen map[uint8]bool) error {
	for len(options) > 0 {
		if len(options) < 2 {
			return ErrTruncated
		}
		optType := options[0]
		optLen := int(options[1])
		if optLen == 0 {
			return ErrZeroOption
		}
		optBytes := optLen * optionUnit
		if optBytes > len(options) {
			return ErrTruncated
		}
		// The whole option, known or unknown, draws from the one shared
		// packet budget, so unknown types cannot inflate processing cost.
		msg.OptionCount++
		if err := admitOption(cfg, msg, optType, options[:optBytes], seen); err != nil {
			return err
		}
		options = options[optBytes:]
	}
	return nil
}

// singletonOptions are identity-bearing options where accepting a second copy
// would let an attacker hide a legitimate value among attacker values.
var singletonOptions = map[uint8]bool{
	OptSourceLinkLayerAddr: true,
	OptTargetLinkLayerAddr: true,
	OptMTU:                 true,
	OptRedirectedHeader:    true,
}

// admitOption validates one option and records it on msg. Unknown options are
// counted and skipped: their framing was already checked by parseOptions.
func admitOption(cfg Config, msg *Message, optType uint8, opt []byte, seen map[uint8]bool) error {
	if singletonOptions[optType] && seen[optType] {
		return ErrDuplicateOption
	}

	switch optType {
	case OptSourceLinkLayerAddr, OptTargetLinkLayerAddr:
		llAddr := opt[2:]
		if cfg.LinkLayerAddrLen > 0 && len(llAddr) != cfg.LinkLayerAddrLen {
			return ErrOptionLen
		}
		copied := append([]byte(nil), llAddr...)
		if optType == OptSourceLinkLayerAddr {
			msg.SLLA = copied
		} else {
			msg.TLLA = copied
		}
		seen[optType] = true

	case OptPrefixInformation:
		if len(opt) != 32 {
			return ErrOptionLen
		}
		prefixLen := opt[2]
		if prefixLen > 128 {
			return ErrOptionLen
		}
		flags := opt[3]
		prefix := netip.AddrFrom16(be128(opt[16:32]))
		// A multicast prefix can never describe an on-link or autoconfigured
		// prefix, so reject it rather than installing bogus state.
		if !prefix.IsValid() || prefix.IsMulticast() {
			return ErrBadFields
		}
		msg.Prefixes = append(msg.Prefixes, Prefix{
			Length:            prefixLen,
			OnLink:            flags&0x80 != 0,
			Autonomous:        flags&0x40 != 0,
			ValidLifetime:     be32(opt[4:8]),
			PreferredLifetime: be32(opt[8:12]),
			Prefix:            prefix,
		})

	case OptRedirectedHeader:
		if len(opt) < 8 || opt[6] != 0 || opt[7] != 0 {
			return ErrOptionLen
		}
		seen[optType] = true

	case OptMTU:
		if len(opt) != 8 {
			return ErrOptionLen
		}
		if opt[2] != 0 || opt[3] != 0 {
			return ErrOptionLen
		}
		mtu := be32(opt[4:8])
		if mtu < 1280 { // RFC 8200 minimum IPv6 MTU.
			return ErrBadFields
		}
		msg.MTU = mtu
		msg.HasMTU = true
		seen[optType] = true

	case OptRouteInformation:
		prefixLen := opt[2]
		preference := uint16((opt[3] >> 3) & 0x3)
		if preference == 2 { // Reserved per RFC 4191.
			return ErrBadFields
		}
		switch len(opt) {
		case 8:
			if prefixLen != 0 {
				return ErrOptionLen
			}
		case 16:
			if prefixLen > 64 {
				return ErrOptionLen
			}
		case 32:
			if prefixLen > 128 {
				return ErrOptionLen
			}
		default:
			return ErrOptionLen
		}
		var prefix netip.Addr
		switch len(opt) {
		case 16:
			var raw [16]byte
			copy(raw[:8], opt[8:16])
			prefix = netip.AddrFrom16(raw)
		case 32:
			prefix = netip.AddrFrom16(be128(opt[16:32]))
		}
		msg.Routes = append(msg.Routes, RouteInfo{
			Prefix:     prefix,
			PrefixLen:  prefixLen,
			Preference: preference,
			Lifetime:   be32(opt[4:8]),
		})

	case OptRDNSS:
		if len(opt) < 24 || (len(opt)-8)%16 != 0 {
			return ErrOptionLen
		}
		// Framing alone is enough to keep later options parseable; the
		// addresses themselves are not identity-bearing for admission.

	case OptDNSSL:
		if len(opt) < 16 {
			return ErrOptionLen
		}

	default:
		// Unknown option: framing was already validated; skip by length. It
		// has already been charged to the shared OptionCount budget.
		msg.Unknown++
	}
	return nil
}

// validateRelations enforces the per-type constraints that only become
// checkable once the full message and its source/destination are known.
func validateRelations(msgType int, src, dst netip.Addr, msg *Message) error {
	if src.IsMulticast() {
		return ErrBadFields
	}

	switch msgType {
	case RouterSolicitation, NeighborSolicitation:
		// RFC 4861: with an unspecified source (Duplicate Address
		// Detection), a source link-layer address option must not appear,
		// because the sender has no usable address/identity yet.
		if src.IsUnspecified() && msg.SLLA != nil {
			return ErrBadFields
		}

	case RouterAdvertisement:
		// RFC 4861 section 6.1.2: router advertisements are sent from a
		// router's link-local address. A global/unspecified source is a
		// forged advertisement.
		if !src.IsLinkLocalUnicast() {
			return ErrBadFields
		}

	case NeighborAdvertisement:
		// RFC 4861 section 4.4: the Solicited flag must not be set when the
		// source is unspecified.
		if msg.Solicited && src.IsUnspecified() {
			return ErrBadFields
		}
		if msg.Target.IsMulticast() || msg.Target.IsUnspecified() {
			return ErrBadFields
		}
		// A TLLA on an advertisement sent to a multicast destination is
		// prohibited; it would be copied into every listener's cache.
		if dst.IsMulticast() && msg.TLLA != nil {
			return ErrBadFields
		}

	case Redirect:
		// Redirects, like advertisements, only come from a link-local router.
		if !src.IsLinkLocalUnicast() {
			return ErrBadFields
		}
		if msg.Target.IsMulticast() || msg.Destination.IsMulticast() {
			return ErrBadFields
		}
	}

	switch msgType {
	case NeighborSolicitation:
		if msg.Target.IsMulticast() || msg.Target.IsUnspecified() {
			return ErrBadFields
		}
	}
	return nil
}
