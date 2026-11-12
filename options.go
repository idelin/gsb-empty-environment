// Package netsec decodes IPv4 header options for a boundary device that must
// reject source-routing options capable of changing the effective next hop,
// without classifying unrelated unknown options as source routes.
//
// Every decoded option keeps its original interval as a sub-slice of the input
// options region, together with its absolute offset, copied flag, option
// class, option number and declared length. LSRR (0x83 / 131) and SSRR
// (0x87 / 135) additionally validate that their pointer addresses a legal
// four-byte address slot.
package netsec

import (
	"errors"
	"fmt"
	"net/netip"
)

// Well-known IPv4 option type bytes (RFC 791).
const (
	OptionEndOfOptions      byte = 0   // single-byte list terminator and padding
	OptionNoOperation       byte = 1   // single-byte alignment filler
	OptionLooseSourceRoute  byte = 131 // 0x83, copied, control class, number 3
	OptionStrictSourceRoute byte = 135 // 0x87, copied, control class, number 7
)

// Sentinel errors reported through ParseError.Kind. Use errors.Is to classify.
var (
	// ErrTruncated means an option needs bytes beyond the options region.
	ErrTruncated = errors.New("netsec: option truncated by options region")
	// ErrLength means a declared length is structurally illegal.
	ErrLength = errors.New("netsec: illegal option length")
	// ErrPointer means a source-route pointer does not name a valid slot.
	ErrPointer = errors.New("netsec: source-route pointer outside valid address slots")
	// ErrPadding means a non-zero byte appears after EndOfOptions.
	ErrPadding = errors.New("netsec: non-zero padding after end-of-options")
	// ErrNotSourceRoute means DecodeSourceRoute got a non LSRR/SSRR option.
	ErrNotSourceRoute = errors.New("netsec: option is not a source route")
)

// ParseError locates the earliest rejection inside the options region.
// Offset is the zero-based byte offset of the offending option, except for
// ErrPadding, where it is the offset of the first non-zero padding byte.
type ParseError struct {
	Kind   error // one of the Err* sentinels
	Offset int   // earliest rejection position within the input
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("%s at offset %d", e.Kind.Error(), e.Offset)
}

// Unwrap exposes the Kind sentinel for errors.Is / errors.As.
func (e *ParseError) Unwrap() error { return e.Kind }

// Option is one decoded IPv4 option. Raw aliases the caller's input buffer
// instead of copying, so the original byte interval is always retained.
type Option struct {
	Type   byte   // full option type byte
	Class  uint8  // two-bit class, bits 6-5
	Number uint8  // five-bit option number, bits 4-0
	Copied bool   // copied flag, bit 7
	Offset int    // start offset within the options region
	Raw    []byte // original interval [Offset, Offset+Length)
}

// Length returns the declared option length in bytes, including type and
// length bytes. Single-byte options have length 1.
func (o Option) Length() int { return len(o.Raw) }

// IsSourceRoute reports whether the option is LSRR or SSRR.
func (o Option) IsSourceRoute() bool {
	return o.Type == OptionLooseSourceRoute || o.Type == OptionStrictSourceRoute
}

// SourceRoute is a decoded LSRR or SSRR option.
//
// Pointer is the one-based pointer relative to the option start. Route holds
// the four-byte address slots as sub-slices of the original input.
type SourceRoute struct {
	Option
	Pointer int
	Route   [][]byte
}

// Active reports whether a route entry remains for this hop. An active source
// route instructs the node to forward toward an address chosen by the sender,
// thereby changing the effective next hop. Pointer == len(Raw)+1 means the
// route is exhausted and must not influence forwarding.
func (s SourceRoute) Active() bool { return s.Pointer <= len(s.Raw) }

// NextHop returns the address currently named by Pointer when Active.
func (s SourceRoute) NextHop() (netip.Addr, bool) {
	if !s.Active() {
		return netip.Addr{}, false
	}
	// Pointer is one-based relative to the option start; validation already
	// proved it names the first byte of a four-byte address slot.
	idx := s.Pointer - 1
	var addr [4]byte
	copy(addr[:], s.Raw[idx:idx+4])
	return netip.AddrFrom4(addr), true
}

// Parse decodes an IPv4 options region (the bytes between the fixed header
// and the payload). It stops at the first malformed option and returns the
// options decoded before the rejection.
//
// EndOfOptions terminates the list; every byte after it must be zero. A
// region that ends exactly on an option boundary needs no EndOfOptions.
func Parse(options []byte) ([]Option, error) {
	var decoded []Option
	for off := 0; off < len(options); {
		typeByte := options[off]
		opt := Option{
			Type:   typeByte,
			Class:  (typeByte >> 5) & 0x03,
			Number: typeByte & 0x1f,
			Copied: typeByte&0x80 != 0,
			Offset: off,
		}

		if typeByte == OptionEndOfOptions {
			opt.Raw = options[off : off+1]
			decoded = append(decoded, opt)
			for pad := off + 1; pad < len(options); pad++ {
				if options[pad] != 0 {
					return decoded, &ParseError{Kind: ErrPadding, Offset: pad}
				}
			}
			return decoded, nil
		}

		if typeByte == OptionNoOperation {
			opt.Raw = options[off : off+1]
			decoded = append(decoded, opt)
			off++
			continue
		}

		// Every remaining option carries a length byte.
		if len(options)-off < 2 {
			return decoded, &ParseError{Kind: ErrTruncated, Offset: off}
		}
		length := int(options[off+1])
		if length < 2 {
			return decoded, &ParseError{Kind: ErrLength, Offset: off}
		}
		if length > len(options)-off {
			return decoded, &ParseError{Kind: ErrTruncated, Offset: off}
		}
		opt.Raw = options[off : off+length]

		if opt.IsSourceRoute() {
			if err := validateSourceRoute(opt); err != nil {
				return decoded, err
			}
		}

		decoded = append(decoded, opt)
		off += length
	}
	return decoded, nil
}

// validateSourceRoute enforces the LSRR/SSRR structure (RFC 791):
// at least type, length and pointer bytes; route data made of four-byte
// addresses; pointer equal to 4+4k (an address slot) or length+1 (exhausted).
// Structural length errors take precedence over pointer errors so that the
// earliest rejection kind is deterministic.
func validateSourceRoute(opt Option) error {
	length := len(opt.Raw)
	if length < 3 || (length-3)%4 != 0 {
		return &ParseError{Kind: ErrLength, Offset: opt.Offset}
	}
	pointer := int(opt.Raw[2])
	if pointer < 4 || pointer > length+1 || (pointer-4)%4 != 0 {
		return &ParseError{Kind: ErrPointer, Offset: opt.Offset}
	}
	return nil
}

// DecodeSourceRoute decodes an LSRR/SSRR option that Parse has already
// validated, exposing its pointer and address slots.
func DecodeSourceRoute(opt Option) (SourceRoute, error) {
	if !opt.IsSourceRoute() {
		return SourceRoute{}, &ParseError{Kind: ErrNotSourceRoute, Offset: opt.Offset}
	}
	if err := validateSourceRoute(opt); err != nil {
		return SourceRoute{}, err
	}
	route := SourceRoute{Option: opt, Pointer: int(opt.Raw[2])}
	for pos := 3; pos+4 <= len(opt.Raw); pos += 4 {
		route.Route = append(route.Route, opt.Raw[pos:pos+4])
	}
	return route, nil
}

// Reason classifies a Check verdict.
type Reason string

const (
	ReasonNone        Reason = ""             // structurally fine, no active route
	ReasonMalformed   Reason = "malformed"    // framing, length, pointer or padding error
	ReasonSourceRoute Reason = "source-route" // active LSRR/SSRR changes the next hop
)

// Verdict is the boundary-device decision for one options region.
type Verdict struct {
	Reject      bool
	Reason      Reason
	SourceRoute *SourceRoute // first active source route, when Reason is source-route
	Options     []Option
}

// Check parses the options region and decides whether it must be rejected.
// Unknown options are never treated as source routes: only a structurally
// valid, active LSRR/SSRR option yields ReasonSourceRoute.
func Check(options []byte) (Verdict, error) {
	parsed, err := Parse(options)
	verdict := Verdict{Options: parsed}
	if err != nil {
		verdict.Reject = true
		verdict.Reason = ReasonMalformed
		return verdict, err
	}
	for i := range parsed {
		if !parsed[i].IsSourceRoute() {
			continue
		}
		// Parse already validated every LSRR/SSRR, so this cannot fail.
		route, _ := DecodeSourceRoute(parsed[i])
		if route.Active() {
			verdict.Reject = true
			verdict.Reason = ReasonSourceRoute
			verdict.SourceRoute = &route
			return verdict, nil
		}
	}
	return verdict, nil
}

// FromIPv4Header extracts the options region from a raw IPv4 header using its
// internet header length field. The returned slice aliases header.
func FromIPv4Header(header []byte) ([]byte, error) {
	if len(header) < 20 {
		return nil, errors.New("netsec: IPv4 header shorter than 20 bytes")
	}
	if header[0]>>4 != 4 {
		return nil, errors.New("netsec: not an IPv4 header")
	}
	headerLength := int(header[0]&0x0f) * 4
	if headerLength < 20 || headerLength > len(header) {
		return nil, errors.New("netsec: invalid IPv4 internet header length")
	}
	return header[20:headerLength], nil
}
