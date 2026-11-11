package dhcpsnoop

import (
	"fmt"
	"net"
	"time"
)

// Port identifies a physical or virtual switch port.
type Port int

// MAC is an EUI-48 hardware address, usable as a map key.
type MAC [6]byte

// IPv4 is an IPv4 address, usable as a map key.
type IPv4 [4]byte

// ParseMAC parses an EUI-48 MAC address such as "aa:bb:cc:dd:ee:01".
func ParseMAC(s string) (MAC, error) {
	hw, err := net.ParseMAC(s)
	if err != nil {
		return MAC{}, err
	}
	if len(hw) != 6 {
		return MAC{}, fmt.Errorf("dhcpsnoop: only EUI-48 MAC addresses are supported: %q", s)
	}
	var m MAC
	copy(m[:], hw)
	return m, nil
}

// MustMAC parses a MAC address or panics. It is intended for tests and
// static configuration.
func MustMAC(s string) MAC {
	m, err := ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

func (m MAC) String() string {
	return net.HardwareAddr(m[:]).String()
}

// IsZero reports whether the MAC is all zeroes.
func (m MAC) IsZero() bool { return m == MAC{} }

// ParseIPv4 parses a dotted-quad IPv4 address.
func ParseIPv4(s string) (IPv4, error) {
	ip := net.ParseIP(s)
	if ip == nil {
		return IPv4{}, fmt.Errorf("dhcpsnoop: invalid IP address: %q", s)
	}
	v4 := ip.To4()
	if v4 == nil {
		return IPv4{}, fmt.Errorf("dhcpsnoop: not an IPv4 address: %q", s)
	}
	var out IPv4
	copy(out[:], v4)
	return out, nil
}

// MustIPv4 parses an IPv4 address or panics.
func MustIPv4(s string) IPv4 {
	ip, err := ParseIPv4(s)
	if err != nil {
		panic(err)
	}
	return ip
}

func (ip IPv4) String() string {
	return net.IP(ip[:]).String()
}

// IsZero reports whether the address is 0.0.0.0.
func (ip IPv4) IsZero() bool { return ip == IPv4{} }

// DHCPType is the DHCP message type carried in option 53 (RFC 2132).
type DHCPType uint8

const (
	DHCPDiscover DHCPType = 1
	DHCPOffer    DHCPType = 2
	DHCPRequest  DHCPType = 3
	DHCPDecline  DHCPType = 4
	DHCPAck      DHCPType = 5
	DHCPNak      DHCPType = 6
	DHCPRelease  DHCPType = 7
	DHCPInform   DHCPType = 8
)

func (t DHCPType) String() string {
	switch t {
	case DHCPDiscover:
		return "DISCOVER"
	case DHCPOffer:
		return "OFFER"
	case DHCPRequest:
		return "REQUEST"
	case DHCPDecline:
		return "DECLINE"
	case DHCPAck:
		return "ACK"
	case DHCPNak:
		return "NAK"
	case DHCPRelease:
		return "RELEASE"
	case DHCPInform:
		return "INFORM"
	default:
		return fmt.Sprintf("DHCPType(%d)", uint8(t))
	}
}

// DHCPPacket is the subset of a DHCP message relevant to snooping.
type DHCPPacket struct {
	InPort Port          // ingress switch port
	XID    uint32        // transaction ID (bootp xid field)
	Type   DHCPType      // option 53 message type
	Chaddr MAC           // client hardware address field
	Ciaddr IPv4          // client IP address field (0 when unassigned)
	Yiaddr IPv4          // "your" IP address assigned by the server
	Lease  time.Duration // lease time from option 51 (0 when absent)
}

// ARPOp is the ARP operation code.
type ARPOp uint8

const (
	ARPRequest ARPOp = 1
	ARPReply   ARPOp = 2
)

// ARPPacket is the subset of an ARP message inspected by DAI.
type ARPPacket struct {
	InPort    Port
	Op        ARPOp
	SenderMAC MAC
	SenderIP  IPv4 // 0.0.0.0 means an ARP probe
	TargetIP  IPv4
}

// IPPacket is a non-ARP IPv4 packet checked by IP source guard.
type IPPacket struct {
	InPort Port
	SrcMAC MAC
	SrcIP  IPv4
}

// Binding is a DHCP snooping binding table entry.
type Binding struct {
	IP     IPv4
	MAC    MAC
	Port   Port
	Expiry time.Time
}

// ActiveAt reports whether the lease is still in force at time t.
func (b Binding) ActiveAt(t time.Time) bool { return t.Before(b.Expiry) }

// Action is the forwarding verdict for a packet.
type Action uint8

const (
	ActionAllow Action = iota
	ActionDrop
)

func (a Action) String() string {
	switch a {
	case ActionAllow:
		return "ALLOW"
	case ActionDrop:
		return "DROP"
	default:
		return fmt.Sprintf("Action(%d)", uint8(a))
	}
}

// Reason explains why a packet was allowed or dropped.
type Reason uint8

const (
	ReasonOK Reason = iota
	ReasonTrustedBypass
	ReasonArpProbe
	ReasonUntrustedServerReply
	ReasonInvalidMessage
	ReasonTransactionUnknown
	ReasonClientIDMismatch
	ReasonNoLease
	ReasonBindingConflict
	ReasonReleaseNoMatch
	ReasonNoBinding
	ReasonPortMismatch
	ReasonMacMismatch
)

func (r Reason) String() string {
	switch r {
	case ReasonOK:
		return "ok"
	case ReasonTrustedBypass:
		return "trusted-port-bypass"
	case ReasonArpProbe:
		return "arp-probe"
	case ReasonUntrustedServerReply:
		return "untrusted-server-reply"
	case ReasonInvalidMessage:
		return "invalid-message"
	case ReasonTransactionUnknown:
		return "transaction-unknown"
	case ReasonClientIDMismatch:
		return "client-id-mismatch"
	case ReasonNoLease:
		return "missing-lease"
	case ReasonBindingConflict:
		return "binding-conflict"
	case ReasonReleaseNoMatch:
		return "release-decline-no-match"
	case ReasonNoBinding:
		return "no-binding"
	case ReasonPortMismatch:
		return "port-mismatch"
	case ReasonMacMismatch:
		return "mac-mismatch"
	default:
		return fmt.Sprintf("Reason(%d)", uint8(r))
	}
}

// Result is the deterministic verdict for one packet.
type Result struct {
	Action Action
	Reason Reason
}

// Allowed reports whether the packet may be forwarded.
func (r Result) Allowed() bool { return r.Action == ActionAllow }

func allow(r Reason) Result { return Result{Action: ActionAllow, Reason: r} }
func drop(r Reason) Result  { return Result{Action: ActionDrop, Reason: r} }
