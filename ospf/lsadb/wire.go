package lsadb

import (
	"encoding/binary"
	"fmt"
)

// Marshal encodes an LSA into its OSPFv2 wire representation and computes the
// LS checksum. The returned slice is also stored on the LSA so later
// admission processing can re-verify it.
func (l *LSA) Marshal() ([]byte, error) {
	if l.LSType == 0 {
		return nil, fmt.Errorf("%w: missing LS type", ErrMalformedLSA)
	}
	totalLength := HeaderLength + len(l.Body)
	if totalLength > 0xffff {
		return nil, fmt.Errorf("%w: LSA length %d overflows 16 bits", ErrMalformedLSA, totalLength)
	}

	raw := make([]byte, totalLength)
	binary.BigEndian.PutUint16(raw[0:2], l.LSAGE)
	raw[2] = l.Options
	raw[3] = l.LSType
	binary.BigEndian.PutUint32(raw[4:8], uint32(l.LinkStateID))
	binary.BigEndian.PutUint32(raw[8:12], uint32(l.AdvertisingRouter))
	binary.BigEndian.PutUint32(raw[12:16], seqHex(l.SeqNum))
	// checksum field at 16:18 stays zero while computed.
	binary.BigEndian.PutUint16(raw[18:20], uint16(totalLength))
	copy(raw[HeaderLength:], l.Body)

	l.Checksum = computeChecksum(raw)
	binary.BigEndian.PutUint16(raw[checksumOffset:checksumOffset+2], l.Checksum)
	l.Length = uint16(totalLength)
	l.raw = raw
	return raw, nil
}

// ParseLSA decodes one OSPFv2 LSA from raw. It enforces the header length,
// the advertised length, the reserved sequence number and the Fletcher
// checksum. The returned LSA retains raw for re-verification.
func ParseLSA(raw []byte) (*LSA, error) {
	if len(raw) < HeaderLength {
		return nil, fmt.Errorf("%w: %d bytes shorter than %d-byte header",
			ErrMalformedLSA, len(raw), HeaderLength)
	}

	advertised := int(binary.BigEndian.Uint16(raw[18:20]))
	if advertised < HeaderLength || advertised > len(raw) {
		return nil, fmt.Errorf("%w: advertised length %d inconsistent with %d received bytes",
			ErrMalformedLSA, advertised, len(raw))
	}

	l := &LSA{
		LSAGE:             binary.BigEndian.Uint16(raw[0:2]),
		Options:           raw[2],
		LSType:            raw[3],
		LinkStateID:       RouterID(binary.BigEndian.Uint32(raw[4:8])),
		AdvertisingRouter: RouterID(binary.BigEndian.Uint32(raw[8:12])),
		SeqNum:            int32(binary.BigEndian.Uint32(raw[12:16])),
		Checksum:          binary.BigEndian.Uint16(raw[16:18]),
		Length:            binary.BigEndian.Uint16(raw[18:20]),
		raw:               append([]byte(nil), raw[:advertised]...),
	}
	body := make([]byte, advertised-HeaderLength)
	copy(body, raw[HeaderLength:advertised])
	l.Body = body

	if l.SeqNum == reservedSequenceNumber {
		return nil, fmt.Errorf("%w: LSA from %s", ErrReservedSequence, l.AdvertisingRouter)
	}
	if !verifyChecksum(l.raw) {
		return nil, fmt.Errorf("%w: LSA from %s type %d",
			ErrChecksum, l.AdvertisingRouter, l.LSType)
	}
	return l, nil
}
