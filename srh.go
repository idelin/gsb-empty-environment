package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

const (
	// SRHRoutingType is the IPv6 Routing Header type allocated to the SRH.
	SRHRoutingType = 4

	// HMACTLVType is the RFC 8754 HMAC TLV type.
	HMACTLVType = 5

	// HMACSHA256Size is the size in bytes of an HMAC-SHA256 digest.
	HMACSHA256Size = sha256.Size

	// maxExtensionHeaders bounds extension-header walking on malformed input.
	maxExtensionHeaders = 32
)

var (
	// ErrMalformedPacket reports a truncated or structurally invalid IPv6
	// extension header, SRH, or SRH TLV.
	ErrMalformedPacket = errors.New("netsec: malformed SRH packet")

	// ErrNotSRH reports that a routing header is not an SRH.
	ErrNotSRH = errors.New("netsec: routing header is not an SRH")
)

// TLV is an SRH type-length-value object.  Pad1 TLVs have Type 0 and no
// Value.  Value aliases the packet buffer passed to ParseSRH.
type TLV struct {
	Type  uint8
	Value []byte
}

// HMACTLV is the RFC 8754 HMAC TLV (type 5).
//
// Reserved is the 16-bit field following Length.  RFC 8754 assigns its
// high bit as the D bit and requires the remaining bits to be zero on
// transmission.  This admission profile requires the whole field to be zero
// (the reduced-segment-list D bit is not supported) and always encodes two
// zero bytes in the HMAC input.
//
// HMAC aliases the packet buffer passed to ParseSRH.
type HMACTLV struct {
	Reserved uint16
	KeyID    uint32
	HMAC     []byte
}

// SRH is a parsed IPv6 Segment Routing Header.
type SRH struct {
	NextHeader   uint8
	HdrExtLen    uint8
	RoutingType  uint8
	SegmentsLeft uint8
	LastEntry    uint8
	Flags        uint8
	Tag          uint16

	// Segments contains LastEntry+1 entries.
	Segments []netip.Addr

	// TLVs contains the SRH TLV objects in wire order, including the HMAC TLV
	// when present.
	TLVs []TLV

	// HMAC is nil when the SRH has no HMAC TLV.
	HMAC *HMACTLV

	segmentBytes []byte
}

// ParseSRH parses an SRH beginning at b.  b may include bytes following the
// SRH; Hdr Ext Len determines how much of b belongs to the SRH.
//
// ParseSRH validates the SRH length, segment-list bounds, and TLV framing.
// It does not make an admission decision.
func ParseSRH(b []byte) (*SRH, error) {
	if len(b) < 8 {
		return nil, fmt.Errorf("%w: SRH shorter than 8 bytes", ErrMalformedPacket)
	}

	totalLen := (int(b[1]) + 1) * 8
	if len(b) < totalLen {
		return nil, fmt.Errorf("%w: SRH needs %d bytes, have %d", ErrMalformedPacket, totalLen, len(b))
	}
	if b[2] != SRHRoutingType {
		return nil, fmt.Errorf("%w: routing type %d", ErrNotSRH, b[2])
	}

	segmentCount := int(b[4]) + 1
	segmentEnd := 8 + segmentCount*16
	if segmentEnd > totalLen {
		return nil, fmt.Errorf("%w: segment list needs %d bytes, header has %d", ErrMalformedPacket, segmentEnd, totalLen)
	}

	srh := &SRH{
		NextHeader:   b[0],
		HdrExtLen:    b[1],
		RoutingType:  b[2],
		SegmentsLeft: b[3],
		LastEntry:    b[4],
		Flags:        b[5],
		Tag:          binary.BigEndian.Uint16(b[6:8]),
		Segments:     make([]netip.Addr, segmentCount),
		segmentBytes: b[8:segmentEnd],
	}

	for i := 0; i < segmentCount; i++ {
		var a [16]byte
		copy(a[:], b[8+i*16:8+(i+1)*16])
		srh.Segments[i] = netip.AddrFrom16(a)
	}

	hmacCount := 0
	for off := segmentEnd; off < totalLen; {
		typ := b[off]
		off++

		if typ == 0 {
			srh.TLVs = append(srh.TLVs, TLV{Type: 0})
			continue
		}

		if off >= totalLen {
			return nil, fmt.Errorf("%w: TLV %d has no length byte", ErrMalformedPacket, typ)
		}
		valueLen := int(b[off])
		off++
		if valueLen > totalLen-off {
			return nil, fmt.Errorf("%w: TLV %d length %d exceeds SRH", ErrMalformedPacket, typ, valueLen)
		}

		value := b[off : off+valueLen]
		if typ == HMACTLVType {
			hmacCount++
			if hmacCount > 1 {
				return nil, fmt.Errorf("%w: multiple HMAC TLVs", ErrMalformedPacket)
			}
			if len(value) < 6 {
				return nil, fmt.Errorf("%w: HMAC TLV shorter than 6 bytes", ErrMalformedPacket)
			}
			srh.HMAC = &HMACTLV{
				Reserved: binary.BigEndian.Uint16(value[0:2]),
				KeyID:    binary.BigEndian.Uint32(value[2:6]),
				HMAC:     value[6:],
			}
		}

		srh.TLVs = append(srh.TLVs, TLV{Type: typ, Value: value})
		off += valueLen
	}

	return srh, nil
}

// FindSRH walks the IPv6 extension-header chain in packet and returns the
// IPv6 source address and the first SRH it finds.  The returned SRH is nil
// when packet contains no SRH.
func FindSRH(packet []byte) (netip.Addr, *SRH, error) {
	if len(packet) < 40 {
		return netip.Addr{}, nil, fmt.Errorf("%w: packet shorter than an IPv6 header", ErrMalformedPacket)
	}
	if packet[0]>>4 != 6 {
		return netip.Addr{}, nil, fmt.Errorf("%w: not an IPv6 packet", ErrMalformedPacket)
	}

	var srcBytes [16]byte
	copy(srcBytes[:], packet[8:24])
	src := netip.AddrFrom16(srcBytes)

	nextHeader := packet[6]
	offset := 40

	for walked := 0; walked < maxExtensionHeaders; walked++ {
		switch nextHeader {
		case 59: // No Next Header
			return src, nil, nil

		case 0, 43, 60, 135: // Hop-by-Hop, Routing, Destination Options, Mobility
			if offset+8 > len(packet) {
				return netip.Addr{}, nil, fmt.Errorf("%w: truncated extension header", ErrMalformedPacket)
			}
			headerLen := (int(packet[offset+1]) + 1) * 8
			if offset+headerLen > len(packet) {
				return netip.Addr{}, nil, fmt.Errorf("%w: extension header length exceeds packet", ErrMalformedPacket)
			}

			if nextHeader == 43 && packet[offset+2] == SRHRoutingType {
				srh, err := ParseSRH(packet[offset : offset+headerLen])
				if err != nil {
					return netip.Addr{}, nil, err
				}
				return src, srh, nil
			}

			nextHeader = packet[offset]
			offset += headerLen

		case 44: // Fragment
			if offset+8 > len(packet) {
				return netip.Addr{}, nil, fmt.Errorf("%w: truncated fragment header", ErrMalformedPacket)
			}
			nextHeader = packet[offset]
			offset += 8

		case 51: // Authentication Header
			if offset+8 > len(packet) {
				return netip.Addr{}, nil, fmt.Errorf("%w: truncated authentication header", ErrMalformedPacket)
			}
			headerLen := (int(packet[offset+1]) + 2) * 4
			if offset+headerLen > len(packet) {
				return netip.Addr{}, nil, fmt.Errorf("%w: authentication header length exceeds packet", ErrMalformedPacket)
			}
			nextHeader = packet[offset]
			offset += headerLen

		case 50: // ESP: the remainder is encrypted and cannot contain a visible SRH
			return src, nil, nil

		default:
			return src, nil, nil
		}
	}

	return netip.Addr{}, nil, fmt.Errorf("%w: too many extension headers", ErrMalformedPacket)
}

// HMACInput returns the RFC 8754 HMAC-SHA256 input for this SRH:
//
//	IPv6 source address                       16 octets
//	SRH Last Entry                             1 octet
//	SRH Flags                                  1 octet
//	HMAC TLV Reserved, encoded as zero         2 octets
//	HMAC TLV Key ID                            4 octets
//	Segment List[0..LastEntry]                16*(LastEntry+1) octets
func (s *SRH) HMACInput(src netip.Addr) []byte {
	keyID := uint32(0)
	if s.HMAC != nil {
		keyID = s.HMAC.KeyID
	}

	src16 := src.As16()
	out := make([]byte, 0, 16+8+len(s.segmentBytes))
	out = append(out, src16[:]...)
	out = append(out, s.LastEntry, s.Flags, 0, 0)
	var id [4]byte
	binary.BigEndian.PutUint32(id[:], keyID)
	out = append(out, id[:]...)
	out = append(out, s.segmentBytes...)
	return out
}

// ComputeHMACSHA256 computes the HMAC over s.HMACInput(src).  The caller must
// ensure s.HMAC is non-nil so that the Key ID is available.
func (s *SRH) ComputeHMACSHA256(src netip.Addr, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(s.HMACInput(src))
	return mac.Sum(nil)
}

// VerifyHMACSHA256 verifies the HMAC TLV with key in constant time.
func (s *SRH) VerifyHMACSHA256(src netip.Addr, key []byte) bool {
	if s.HMAC == nil || len(s.HMAC.HMAC) != HMACSHA256Size {
		return false
	}
	return hmac.Equal(s.ComputeHMACSHA256(src, key), s.HMAC.HMAC)
}
