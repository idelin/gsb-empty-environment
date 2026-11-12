package netsec

import "net/netip"

// SOME/IP-SD entry type values (AUTOSAR FO PRS SOME/IP Service Discovery).
const (
	EntryFindService  uint8 = 0x00
	EntryOfferService uint8 = 0x01
	EntrySubscribe    uint8 = 0x06
	EntrySubscribeAck uint8 = 0x07
)

// SOME/IP-SD option type values.
const (
	OptionIPv4Endpoint   uint8 = 0x04
	OptionIPv6Endpoint   uint8 = 0x05
	OptionIPv4Multicast  uint8 = 0x14
	OptionIPv6Multicast  uint8 = 0x15
	OptionIPv4SDEndpoint uint8 = 0x24
	OptionIPv6SDEndpoint uint8 = 0x26
)

// L4 transport protocol numbers carried inside endpoint options.
const (
	L4UDP uint8 = 0x11
	L4TCP uint8 = 0x06
)

// ServiceKey identifies a concrete service instance by service ID, instance ID
// and major version. This is the tuple the gateway configuration is keyed on.
type ServiceKey struct {
	ServiceID    uint16
	InstanceID   uint16
	MajorVersion uint8
}

// Entry is one 16-byte SOME/IP-SD entry in decoded form.
type Entry struct {
	Type         uint8
	ServiceID    uint16
	InstanceID   uint16
	MajorVersion uint8
	MinorVersion uint32
	// TTL is the 24-bit time-to-live in seconds. A value of 0 turns an Offer
	// into a StopOffer and a Subscribe into a StopSubscribe.
	TTL uint32
	// Index1/Num1 describe the first option run, Index2/Num2 the second one.
	Index1, Num1 uint8
	Index2, Num2 uint8
}

// IsStopOffer reports whether an OfferService entry carries TTL 0.
func (e Entry) IsStopOffer() bool {
	return e.Type == EntryOfferService && e.TTL == 0
}

// Option is a decoded SOME/IP-SD option. Address/Port/L4Proto are only
// populated for address-bearing endpoint and multicast options; Raw always
// retains the original on-wire option bytes (excluding the 2-byte length
// prefix) for option types this package does not interpret.
type Option struct {
	Type    uint8
	Address netip.Addr
	Port    uint16
	L4Proto uint8
	Raw     []byte
}

// Message is a parsed SOME/IP-SD notification message.
type Message struct {
	SessionID  uint16
	ClientID   uint16
	ReturnCode uint8
	Reboot     bool
	Unicast    bool
	Entries    []Entry
	Options    []Option
}
