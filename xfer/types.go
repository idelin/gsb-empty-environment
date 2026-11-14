// Package xfer provides admission-controlled DNS zone transfer (AXFR/IXFR)
// for authoritative DNS servers. It decides, per zone, source address, TSIG
// key, transport protocol and request type, whether a transfer is answered.
// The default posture is deny: anything not explicitly allowed is refused.
package xfer

import (
	"errors"
	"fmt"
	"net/netip"
)

// RequestType is the kind of zone transfer being requested.
type RequestType uint8

const (
	TypeAXFR RequestType = iota
	TypeIXFR
)

func (t RequestType) String() string {
	switch t {
	case TypeAXFR:
		return "AXFR"
	case TypeIXFR:
		return "IXFR"
	default:
		return "UNKNOWN"
	}
}

// Protocol is the transport the request arrived on.
type Protocol uint8

const (
	ProtoUDP Protocol = iota
	ProtoTCP
)

func (p Protocol) String() string {
	switch p {
	case ProtoUDP:
		return "UDP"
	case ProtoTCP:
		return "TCP"
	default:
		return "UNKNOWN"
	}
}

// Record is a single DNS resource record in presentation-friendly form.
type Record struct {
	Name  string
	Type  string
	TTL   uint32
	Value string
}

// TSIG carries the transaction signature attached to a request.
type TSIG struct {
	KeyID      string
	Generation uint32
	TimeSigned int64
	Fudge      int64
	MAC        []byte
}

// Request describes one incoming zone-transfer query.
type Request struct {
	Zone         string
	Type         RequestType
	Proto        Protocol
	Source       netip.Addr
	TSIG         *TSIG
	ClientSerial uint32 // IXFR only: serial the client already holds
}

// DenyError is returned for every refused transfer. It deliberately carries
// no record data so unauthorized queriers learn nothing.
type DenyError struct {
	Reason string
}

func (e *DenyError) Error() string { return "zone transfer refused: " + e.Reason }

// IsDeny reports whether err is an admission denial.
func IsDeny(err error) bool {
	var de *DenyError
	return errors.As(err, &de)
}

// ErrZoneChanged aborts a streaming transfer when the zone is modified
// mid-transfer, so old and new versions are never mixed in one response.
var ErrZoneChanged = errors.New("zone changed during transfer, aborted")

func denyf(format string, args ...any) error {
	return &DenyError{Reason: fmt.Sprintf(format, args...)}
}
