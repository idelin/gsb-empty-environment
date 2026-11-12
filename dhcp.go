package netsec

import "errors"

// BOOTP/DHCP fixed-layout constants (RFC 2131 §2).
const (
	bootpMinLen      = 240
	magicCookieLen   = 4
	mainOptionsStart = 240
	snameStart       = 44
	snameLen         = 64
	fileStart        = 108
	fileLen          = 128

	hlenOffset = 2
	xidOffset  = 4
	ciaddrOff  = 12
	yiaddrOff  = 16
	siaddrOff  = 20
	giaddrOff  = 24
	chaddrOff  = 28
	chaddrMax  = 16
)

// magicCookie is the DHCP options magic cookie "DHCP" defined in RFC 2131.
var magicCookie = [4]byte{99, 130, 83, 99}

// DHCP option codes used by this package.
const (
	OptPad      = 0
	OptSubnet   = 1
	OptRouter   = 3
	OptDNS      = 6
	OptOverload = 52
	OptMsgType  = 53
	OptServerID = 54
	OptLease    = 51
	OptEnd      = 255
)

// Overload option bit values (RFC 2131 §9.3).
const (
	overloadFileBit  = 1
	overloadSnameBit = 2
)

// Sentinel errors. Callers use errors.Is to classify parse failures.
var (
	ErrPacketTooShort = errors.New("netsec: DHCP packet shorter than fixed BOOTP header")
	ErrBadCookie      = errors.New("netsec: invalid DHCP magic cookie")
	ErrTruncated      = errors.New("netsec: option length runs past the end of its option area")
	ErrOverloadValue  = errors.New("netsec: invalid or duplicated DHCP option 52 (overload)")
	ErrNestedOverload = errors.New("netsec: option 52 (overload) is only valid in the main option area")
	ErrOptionLength   = errors.New("netsec: concatenated option fails fixed-length validation")
	ErrBadMessageType = errors.New("netsec: DHCP message type option has an invalid value")
	ErrBadHardwareLen = errors.New("netsec: hardware address length is invalid for the chaddr field")
)

// Options is the single normalized option map shared by the inspection layer
// and the business layer. Repeated occurrences of an option have their values
// concatenated in parsing order (main area, then file area, then sname area).
// Every stored value is an independent copy; later mutation of the source
// packet cannot change the normalized result.
type Options map[uint8][]byte

// Binding is the per-lease binding information handed to the business layer.
// It is only produced after every structural and fixed-length check passes.
type Binding struct {
	HardwareAddr []byte
	XID          []byte
	ClientIP     []byte
	YourIP       []byte
	ServerIP     []byte
	RelayIP      []byte
	MessageType  uint8
	Options      Options
}
