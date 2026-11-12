package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// NTLM message types from [MS-NLMP].
const (
	MessageTypeNegotiate    uint32 = 1
	MessageTypeChallenge    uint32 = 2
	MessageTypeAuthenticate uint32 = 3
)

// Negotiate flags relevant to this package.
const (
	NegotiateFlagSign    uint32 = 0x00000010
	NegotiateFlagSeal    uint32 = 0x00000020
	NegotiateFlagVersion uint32 = 0x02000000
)

// AV_PAIR identifiers from [MS-NLMP] section 2.2.2.1.
const (
	MsvAvIDEnd             uint16 = 0
	MsvAvIDNbComputerName  uint16 = 1
	MsvAvIDNbDomainName    uint16 = 2
	MsvAvIDDNSComputerName uint16 = 3
	MsvAvIDDNSDomainName   uint16 = 4
	MsvAvIDDNSTreeName     uint16 = 5
	MsvAvIDFlags           uint16 = 6
	MsvAvIDTimestamp       uint16 = 7
	MsvAvIDSingleHost      uint16 = 8
	MsvAvIDTargetName      uint16 = 9
	MsvAvIDChannelBindings uint16 = 10
)

// MsvAvFlags bits from [MS-NLMP] section 2.2.2.1.
const (
	MsvAvFlagAccountConstrained uint32 = 0x00000001
	MsvAvFlagMICPresent         uint32 = 0x00000002
	MsvAvFlagSPNUNTrusted       uint32 = 0x00000004
	MsvAvFlagSPNUntrusted       uint32 = MsvAvFlagSPNUNTrusted
)

// Common aliases for the AV_PAIR identifiers.
const (
	MsvAvNbComputerName  = MsvAvIDNbComputerName
	MsvAvNbDomainName    = MsvAvIDNbDomainName
	MsvAvDNSComputerName = MsvAvIDDNSComputerName
	MsvAvDNSDomainName   = MsvAvIDDNSDomainName
	MsvAvDNSTreeName     = MsvAvIDDNSTreeName
	MsvAvFlags           = MsvAvIDFlags
	MsvAvTimestamp       = MsvAvIDTimestamp
	MsvAvSingleHost      = MsvAvIDSingleHost
	MsvAvTargetName      = MsvAvIDTargetName
	MsvAvChannelBindings = MsvAvIDChannelBindings
)

const ntlmSignature = "NTLMSSP\x00"

var (
	// ErrMalformedMessage indicates a message is truncated or has an
	// out-of-bounds security buffer.
	ErrMalformedMessage = errors.New("netsec: malformed NTLM message")
	// ErrUnsupportedMessage indicates an unexpected NTLM message type.
	ErrUnsupportedMessage = errors.New("netsec: unsupported NTLM message")
	// ErrMICMissing indicates that the challenge required a MIC but the
	// AUTHENTICATE message did not contain one.
	ErrMICMissing = errors.New("netsec: AUTHENTICATE MIC missing")
	// ErrMICMismatch indicates that the supplied MIC is not the HMAC-MD5
	// of the three messages under the SessionBaseKey.
	ErrMICMismatch = errors.New("netsec: AUTHENTICATE MIC mismatch")
	// ErrSPNMissing indicates that no target SPN was present in the
	// CHALLENGE or AUTHENTICATE target info.
	ErrSPNMissing = errors.New("netsec: target SPN missing")
	// ErrSPNMismatch indicates that the target SPN does not identify
	// the service receiving the authentication.
	ErrSPNMismatch = errors.New("netsec: target SPN mismatch")
	// ErrInvalidConfig indicates incomplete validator configuration.
	ErrInvalidConfig = errors.New("netsec: invalid validator configuration")
)

// ValidationError provides a stable sentinel error plus context.
type ValidationError struct {
	Kind   error
	Detail string
}

func (e *ValidationError) Error() string {
	if e.Detail == "" {
		return e.Kind.Error()
	}
	return e.Kind.Error() + ": " + e.Detail
}

func (e *ValidationError) Unwrap() error { return e.Kind }

func validationError(kind error, format string, args ...any) error {
	return &ValidationError{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

// SecurityBuffer is an NTLM length/max-length/offset tuple.
type SecurityBuffer struct {
	Length uint16
	MaxLen uint16
	Offset uint32
}

func readSecurityBuffer(raw []byte, off int) (SecurityBuffer, error) {
	if off < 0 || off+8 > len(raw) {
		return SecurityBuffer{}, validationError(ErrMalformedMessage, "security buffer at %d is out of bounds", off)
	}
	return SecurityBuffer{
		Length: binary.LittleEndian.Uint16(raw[off : off+2]),
		MaxLen: binary.LittleEndian.Uint16(raw[off+2 : off+4]),
		Offset: binary.LittleEndian.Uint32(raw[off+4 : off+8]),
	}, nil
}

func (b SecurityBuffer) bytes(raw []byte) ([]byte, error) {
	if b.Length == 0 {
		return nil, nil
	}
	if int(b.Length) > len(raw) {
		return nil, validationError(ErrMalformedMessage, "security buffer length %d exceeds message length %d", b.Length, len(raw))
	}
	end := uint64(b.Offset) + uint64(b.Length)
	if end > uint64(len(raw)) {
		return nil, validationError(ErrMalformedMessage, "security buffer [%d,%d) is out of bounds", b.Offset, end)
	}
	return raw[int(b.Offset):int(end)], nil
}

// AVPair is one MsvAv* pair from the NTLM target information list.
type AVPair struct {
	ID    uint16
	Value []byte
}

// AVList is a parsed list of AV_PAIR values.
type AVList []AVPair

// TargetInfo is an alias for AVList, matching the protocol terminology.
type TargetInfo = AVList

// ParseAVPairs parses a target information buffer. A terminating
// AvId=0/AvLen=0 pair is accepted but not required.
func ParseAVPairs(data []byte) (AVList, error) {
	var pairs AVList
	off := 0
	for off < len(data) {
		if off+4 > len(data) {
			for ; off < len(data); off++ {
				if data[off] != 0 {
					return nil, validationError(ErrMalformedMessage, "truncated AV_PAIR header")
				}
			}
			break
		}
		id := binary.LittleEndian.Uint16(data[off : off+2])
		length := int(binary.LittleEndian.Uint16(data[off+2 : off+4]))
		off += 4
		if id == MsvAvIDEnd {
			if length != 0 {
				return nil, validationError(ErrMalformedMessage, "AV_PAIR end marker has non-zero length %d", length)
			}
			for ; off < len(data); off++ {
				if data[off] != 0 {
					return nil, validationError(ErrMalformedMessage, "non-zero data after AV_PAIR end marker")
				}
			}
			return pairs, nil
		}
		if length > len(data)-off {
			return nil, validationError(ErrMalformedMessage, "AV_PAIR %d value is truncated", id)
		}
		pairs = append(pairs, AVPair{ID: id, Value: data[off : off+length]})
		off += length
	}
	return pairs, nil
}

// ParseTargetInfo is an alias for ParseAVPairs.
func ParseTargetInfo(data []byte) (TargetInfo, error) {
	return ParseAVPairs(data)
}

// Values returns every value with the requested AV identifier.
func (l AVList) Values(id uint16) [][]byte {
	var values [][]byte
	for _, pair := range l {
		if pair.ID == id {
			values = append(values, pair.Value)
		}
	}
	return values
}

// Get is an alias for First.
func (l AVList) Get(id uint16) ([]byte, bool) {
	return l.First(id)
}

// First returns the first value with the requested AV identifier.
func (l AVList) First(id uint16) ([]byte, bool) {
	for _, pair := range l {
		if pair.ID == id {
			return pair.Value, true
		}
	}
	return nil, false
}

// Has reports whether an AV identifier is present.
func (l AVList) Has(id uint16) bool {
	_, ok := l.First(id)
	return ok
}

// MsvAvFlags returns the bitwise OR of all MsvAvFlags values. The
// protocol requires each value to be exactly four bytes.
func (l AVList) MsvAvFlags() (uint32, error) {
	var flags uint32
	for _, pair := range l {
		if pair.ID != MsvAvIDFlags {
			continue
		}
		if len(pair.Value) != 4 {
			return 0, validationError(ErrMalformedMessage, "MsvAvFlags has length %d, want 4", len(pair.Value))
		}
		flags |= binary.LittleEndian.Uint32(pair.Value)
	}
	return flags, nil
}

// HasMICFlag reports whether any MsvAvFlags value advertises a MIC.
func (l AVList) HasMICFlag() bool {
	flags, err := l.MsvAvFlags()
	return err == nil && flags&MsvAvFlagMICPresent != 0
}

// HasTimestamp reports whether MsvAvTimestamp is present.
func (l AVList) HasTimestamp() bool { return l.Has(MsvAvIDTimestamp) }

// TargetName returns the first MsvAvTargetName value.
func (l AVList) TargetName() (string, bool) {
	value, ok := l.First(MsvAvIDTargetName)
	if !ok {
		return "", false
	}
	name, err := decodeAVString(value)
	if err != nil || name == "" {
		return "", false
	}
	return name, true
}

// TargetNames returns all MsvAvTargetName values. Having more than one
// is unusual, but every value must be checked so an appended pair
// cannot weaken validation.
func (l AVList) TargetNames() ([]string, error) {
	values := l.Values(MsvAvIDTargetName)
	names := make([]string, 0, len(values))
	for _, value := range values {
		name, err := decodeAVString(value)
		if err != nil {
			return nil, validationError(ErrMalformedMessage, "MsvAvTargetName: %v", err)
		}
		names = append(names, name)
	}
	return names, nil
}

func decodeAVString(value []byte) (string, error) {
	if len(value) == 0 {
		return "", nil
	}

	// AV_PAIR strings are UTF-16LE. A few non-Windows clients emit
	// plain ASCII/UTF-8 for the target name, so accept that form only
	// when it is unambiguously printable ASCII.
	if bytes.IndexByte(value, 0) < 0 && utf8.Valid(value) {
		printable := true
		for _, b := range value {
			if b < 0x20 || b > 0x7e {
				printable = false
				break
			}
		}
		if printable {
			return string(value), nil
		}
	}
	if len(value)%2 != 0 {
		return "", fmt.Errorf("UTF-16LE value has odd length %d", len(value))
	}
	units := make([]uint16, 0, len(value)/2)
	for i := 0; i < len(value); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(value[i:i+2]))
	}
	for len(units) > 0 && units[len(units)-1] == 0 {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units)), nil
}

func checkMessage(raw []byte, wantType uint32) error {
	if len(raw) < 12 {
		return validationError(ErrMalformedMessage, "message is only %d bytes", len(raw))
	}
	if string(raw[:8]) != ntlmSignature {
		return validationError(ErrMalformedMessage, "missing NTLMSSP signature")
	}
	gotType := binary.LittleEndian.Uint32(raw[8:12])
	if gotType != wantType {
		return validationError(ErrUnsupportedMessage, "message type %d, want %d", gotType, wantType)
	}
	return nil
}

func isZero(data []byte) bool {
	for _, b := range data {
		if b != 0 {
			return false
		}
	}
	return true
}

func trimNUL(s string) string {
	return strings.TrimRight(s, "\x00")
}
