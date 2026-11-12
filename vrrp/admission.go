// Package vrrp implements admission control for inbound VRRPv3 (RFC 5798)
// advertisement packets on the IPv4 receive path of a routing daemon.
//
// An Admitter holds the local router's virtual-router configuration and
// decides whether a received advertisement may be processed. Every rejection
// is accounted per reason with an atomic counter, so receive-path workers can
// collect drop statistics without additional locking.
//
// The checks implement the receive requirements of RFC 5798 section 5.2.4:
// IP TTL 255, destination 224.0.0.18, matching VRID, a checksum computed
// over the IPv4 pseudo header plus the VRRP packet, matching virtual-router
// address list and advertisement interval. A priority-255 advertisement is
// additionally accepted only when its source address is one of the locally
// configured virtual addresses, i.e. it originates from the IP address owner.
package vrrp

import (
	"encoding/binary"
	"net/netip"
	"sync/atomic"
)

// Protocol constants from RFC 5798.
const (
	// ProtocolNumber is the IANA protocol number assigned to VRRP.
	ProtocolNumber = 112
	// Version3 is the VRRP protocol version implemented here.
	Version3 = 3
	// MulticastTTL is the only acceptable IP TTL for a VRRP advertisement.
	MulticastTTL = 255

	headerLen       = 8
	typeAdvertise   = 1
	offsetVRID      = 1
	offsetPriority  = 2
	offsetAddrCount = 3
	offsetMaxAdver  = 4
	offsetChecksum  = 6
	maxIntervalMask = 0x0fff

	// PriorityOwner is the priority used by an IP address owner.
	PriorityOwner = 255
	// PriorityZero signals that a master is relinquishing the virtual router.
	PriorityZero = 0
)

// MulticastAddrV4 is the IPv4 VRRP multicast destination address 224.0.0.18.
var MulticastAddrV4 = netip.AddrFrom4([4]byte{224, 0, 0, 18})

// Reason identifies why an advertisement was rejected by the Admitter.
type Reason int

const (
	// ReasonNone is returned for an accepted advertisement; it is not a drop.
	ReasonNone Reason = iota
	// ReasonShortPacket means the packet is shorter than the VRRP header.
	ReasonShortPacket
	// ReasonVersion means the VRRP version is not 3.
	ReasonVersion
	// ReasonPacketType means the packet type is not ADVERTISEMENT.
	ReasonPacketType
	// ReasonTTL means the IP TTL is not 255, indicating a remote sender.
	ReasonTTL
	// ReasonDestination means the IP destination is not 224.0.0.18.
	ReasonDestination
	// ReasonSourceAddress means the IP source is not a valid unicast IPv4 address.
	ReasonSourceAddress
	// ReasonVRID means the advertised VRID does not match local configuration.
	ReasonVRID
	// ReasonChecksum means the VRRP checksum (pseudo header included) is wrong.
	ReasonChecksum
	// ReasonAddressCount means the address count does not match the packet length.
	ReasonAddressCount
	// ReasonVirtualAddress means the advertised address list is invalid or
	// does not match the locally configured virtual addresses.
	ReasonVirtualAddress
	// ReasonMaxAdvertInterval means the advertisement interval differs from
	// the locally configured one.
	ReasonMaxAdvertInterval
	// ReasonOwnerSpoof means a priority-255 packet did not originate from a
	// configured virtual address (IP address owner).
	ReasonOwnerSpoof

	numReasons
)

// String returns a stable machine-readable name for the reason.
func (r Reason) String() string {
	switch r {
	case ReasonNone:
		return "none"
	case ReasonShortPacket:
		return "short_packet"
	case ReasonVersion:
		return "unsupported_version"
	case ReasonPacketType:
		return "bad_packet_type"
	case ReasonTTL:
		return "ttl_not_255"
	case ReasonDestination:
		return "bad_destination"
	case ReasonSourceAddress:
		return "bad_source_address"
	case ReasonVRID:
		return "vrid_mismatch"
	case ReasonChecksum:
		return "checksum_error"
	case ReasonAddressCount:
		return "address_count_error"
	case ReasonVirtualAddress:
		return "virtual_address_mismatch"
	case ReasonMaxAdvertInterval:
		return "interval_mismatch"
	case ReasonOwnerSpoof:
		return "owner_spoof"
	default:
		return "unknown"
	}
}

// Error makes Reason usable as an error on the receive path.
func (r Reason) Error() string { return "vrrp admission: " + r.String() }

// Config is the local virtual-router configuration used for admission.
type Config struct {
	// VRID is the local virtual router identifier, 1..255.
	VRID uint8
	// MaxAdvertisementInterval is the local Max Advertisement Interval in
	// centiseconds, 1..4095 (100 means one second).
	MaxAdvertisementInterval uint16
	// VirtualAddresses are the IPv4 addresses backed by this virtual router.
	// A priority-255 advertisement must originate from one of them.
	VirtualAddresses []netip.Addr
}

// Inbound carries the fields of a received IP packet that admission needs.
type Inbound struct {
	// TTL is the IPv4 header TTL (hop limit).
	TTL uint8
	// Source is the IPv4 source address.
	Source netip.Addr
	// Destination is the IPv4 destination address.
	Destination netip.Addr
	// VRRPPacket is the VRRP packet (IP payload), starting at the version byte.
	VRRPPacket []byte
}

// Advertisement is the parsed, validated result of an admitted packet.
type Advertisement struct {
	VRID                     uint8
	Priority                 uint8
	MaxAdvertisementInterval uint16
	VirtualAddresses         []netip.Addr
}

// Admitter performs VRRPv3 advertisement admission against one local
// configuration. It is safe for concurrent use.
type Admitter struct {
	vrid     uint8
	interval uint16
	vaddrs   []netip.Addr
	vaddrSet map[netip.Addr]struct{}

	dropCounts [numReasons]atomic.Uint64
}

// NewAdmitter validates the configuration and builds an Admitter. The
// configured addresses are copied so later caller mutation has no effect.
func NewAdmitter(cfg Config) (*Admitter, error) {
	if cfg.VRID == 0 {
		return nil, errConfig("VRID must be in 1..255")
	}
	if cfg.MaxAdvertisementInterval == 0 || cfg.MaxAdvertisementInterval > maxIntervalMask {
		return nil, errConfig("MaxAdvertisementInterval must be in 1..4095 centiseconds")
	}
	if len(cfg.VirtualAddresses) == 0 {
		return nil, errConfig("at least one virtual address is required")
	}

	vaddrs := make([]netip.Addr, 0, len(cfg.VirtualAddresses))
	vaddrSet := make(map[netip.Addr]struct{}, len(cfg.VirtualAddresses))
	for _, addr := range cfg.VirtualAddresses {
		if !addr.Is4() {
			return nil, errConfig("virtual addresses must be IPv4: " + addr.String())
		}
		if _, dup := vaddrSet[addr]; dup {
			return nil, errConfig("duplicate virtual address: " + addr.String())
		}
		vaddrSet[addr] = struct{}{}
		vaddrs = append(vaddrs, addr)
	}

	return &Admitter{
		vrid:     cfg.VRID,
		interval: cfg.MaxAdvertisementInterval,
		vaddrs:   vaddrs,
		vaddrSet: vaddrSet,
	}, nil
}

// Admit validates one inbound advertisement. On success it returns the parsed
// advertisement and ReasonNone; otherwise it returns nil, the rejection
// reason, and increments the corresponding drop counter.
func (a *Admitter) Admit(in Inbound) (*Advertisement, Reason) {
	pkt := in.VRRPPacket

	if len(pkt) < headerLen {
		return nil, a.drop(ReasonShortPacket)
	}
	if pkt[0]>>4 != Version3 {
		return nil, a.drop(ReasonVersion)
	}
	if pkt[0]&0x0f != typeAdvertise {
		return nil, a.drop(ReasonPacketType)
	}
	// RFC 5798 5.2.4: a TTL other than 255 means the packet crossed a router
	// and must be discarded; this blocks off-link senders.
	if in.TTL != MulticastTTL {
		return nil, a.drop(ReasonTTL)
	}
	if !in.Destination.Is4() || in.Destination != MulticastAddrV4 {
		return nil, a.drop(ReasonDestination)
	}
	if !in.Source.Is4() || !validUnicastV4(in.Source) {
		return nil, a.drop(ReasonSourceAddress)
	}

	vrid := pkt[offsetVRID]
	if vrid != a.vrid {
		return nil, a.drop(ReasonVRID)
	}

	// Checksum before deeper parsing: it covers the IPv4 pseudo header, so a
	// spoofed source or destination invalidates it as well.
	if checksumV4(pkt, in.Source, in.Destination) != binary.BigEndian.Uint16(pkt[offsetChecksum:offsetChecksum+2]) {
		return nil, a.drop(ReasonChecksum)
	}

	count := int(pkt[offsetAddrCount])
	if count == 0 || len(pkt) != headerLen+4*count {
		return nil, a.drop(ReasonAddressCount)
	}

	advertised := make([]netip.Addr, 0, count)
	off := headerLen
	for range count {
		var b [4]byte
		copy(b[:], pkt[off:off+4])
		addr := netip.AddrFrom4(b)
		if !validUnicastV4(addr) {
			return nil, a.drop(ReasonVirtualAddress)
		}
		advertised = append(advertised, addr)
		off += 4
	}
	// RFC 5798 5.2.4: the advertised address list must equal the local list.
	if !sameAddressSet(advertised, a.vaddrs) {
		return nil, a.drop(ReasonVirtualAddress)
	}

	maxAdver := binary.BigEndian.Uint16(pkt[offsetMaxAdver:offsetMaxAdver+2]) & maxIntervalMask
	if maxAdver != a.interval {
		return nil, a.drop(ReasonMaxAdvertInterval)
	}

	// Priority 255 is reserved to the IP address owner. A packet carrying it
	// is only accepted when the source is one of the configured virtual
	// addresses; otherwise it is an owner-spoofing attempt.
	priority := pkt[offsetPriority]
	if priority == PriorityOwner {
		if _, ok := a.vaddrSet[in.Source]; !ok {
			return nil, a.drop(ReasonOwnerSpoof)
		}
	}

	return &Advertisement{
		VRID:                     vrid,
		Priority:                 priority,
		MaxAdvertisementInterval: maxAdver,
		VirtualAddresses:         advertised,
	}, ReasonNone
}

// DropCount returns the number of packets rejected for the given reason.
func (a *Admitter) DropCount(r Reason) uint64 {
	if r <= ReasonNone || r >= numReasons {
		return 0
	}
	return a.dropCounts[r].Load()
}

// DropCounts returns a snapshot of all non-zero drop counters.
func (a *Admitter) DropCounts() map[Reason]uint64 {
	out := make(map[Reason]uint64)
	for r := Reason(1); r < numReasons; r++ {
		if n := a.dropCounts[r].Load(); n != 0 {
			out[r] = n
		}
	}
	return out
}

func (a *Admitter) drop(r Reason) Reason {
	a.dropCounts[r].Add(1)
	return r
}

// checksumV4 computes the VRRP checksum per RFC 5798 section 5.2.8: the
// 16-bit one's complement of the one's complement sum over the IPv4 pseudo
// header (source, destination, zero, protocol 112, VRRP length) followed by
// the VRRP packet. The embedded checksum field contributes zero.
func checksumV4(pkt []byte, src, dst netip.Addr) uint16 {
	s4 := src.As4()
	d4 := dst.As4()

	var sum uint32
	sum += uint32(s4[0])<<8 | uint32(s4[1])
	sum += uint32(s4[2])<<8 | uint32(s4[3])
	sum += uint32(d4[0])<<8 | uint32(d4[1])
	sum += uint32(d4[2])<<8 | uint32(d4[3])
	sum += uint32(ProtocolNumber)
	sum += uint32(len(pkt))

	for i := 0; i+1 < len(pkt); i += 2 {
		if i == offsetChecksum {
			continue // checksum field is treated as zero
		}
		sum += uint32(pkt[i])<<8 | uint32(pkt[i+1])
	}
	if len(pkt)%2 == 1 {
		sum += uint32(pkt[len(pkt)-1]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func validUnicastV4(addr netip.Addr) bool {
	if !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() {
		return false
	}
	// Reject limited broadcast and the reserved 240.0.0.0/4 range.
	return addr.As4()[0] < 240
}

func sameAddressSet(a, b []netip.Addr) bool {
	if len(a) != len(b) {
		return false
	}
	remaining := make(map[netip.Addr]int, len(b))
	for _, addr := range b {
		remaining[addr]++
	}
	for _, addr := range a {
		if remaining[addr] == 0 {
			return false
		}
		remaining[addr]--
	}
	return true
}

type configError string

func (e configError) Error() string { return "vrrp config: " + string(e) }

func errConfig(msg string) error { return configError(msg) }
