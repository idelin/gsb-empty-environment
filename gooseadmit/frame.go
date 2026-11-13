package gooseadmit

import (
	"errors"
)

// EtherTypeGOOSE is the IEC 61850 GOOSE EtherType. The IEEE 802.1Q tag
// EtherType 0x8100 and the 802.1ad service tag 0x88A8 are transparently
// skipped so tagged and untagged frames are both handled.
const EtherTypeGOOSE uint16 = 0x88B8

const (
	ethHeaderLen    = 14
	vlanTagLen      = 4
	gooseMinLen     = ethHeaderLen + 2
	tagGoCBRef      = 0x80
	tagTimeTTL      = 0x81
	tagDataSet      = 0x82
	tagGoID         = 0x83
	tagT            = 0x84
	tagStNum        = 0x85
	tagSqNum        = 0x86
	tagSimulation   = 0x87
	tagConfRev      = 0x88
	tagNdsCom       = 0x89
	tagNumDatSetEnt = 0x8A
)

var (
	errFrameTooShort  = errors.New("gooseadmit: frame shorter than Ethernet header")
	errNotGOOSE       = errors.New("gooseadmit: EtherType is not 0x88B8 GOOSE")
	errTrailingVLAN   = errors.New("gooseadmit: malformed stacked VLAN tag")
	errBadOuterTLV    = errors.New("gooseadmit: GOOSE PDU is not a BER SEQUENCE")
	errTruncatedTLV   = errors.New("gooseadmit: truncated BER TLV")
	errBadLength      = errors.New("gooseadmit: invalid BER length")
	errBadInteger     = errors.New("gooseadmit: invalid BER INTEGER for 32-bit GOOSE field")
	errBadBoolean     = errors.New("gooseadmit: invalid BER BOOLEAN")
	errMissingField   = errors.New("gooseadmit: mandatory GOOSE field missing")
	errStringOverflow = errors.New("gooseadmit: GOOSE string field exceeds bounds")
)

// gooseFrame is the subset of a GOOSE PDU needed by the admission policy.
type gooseFrame struct {
	srcMAC     [6]byte
	gocbRef    string
	datSet     string
	goID       string
	ttl        uint32
	stNum      uint32
	sqNum      uint32
	simulation bool
}

// parseFrame parses a raw Ethernet frame captured at layer 2 up to the
// simulation flag, which is the last field the admission policy needs.
func parseFrame(raw []byte) (*gooseFrame, error) {
	if len(raw) < gooseMinLen {
		return nil, errFrameTooShort
	}

	off := 12
	etherType := uint16(raw[off])<<8 | uint16(raw[off+1])
	for etherType == 0x8100 || etherType == 0x88A8 {
		if len(raw) < off+vlanTagLen+2 {
			return nil, errTrailingVLAN
		}
		off += vlanTagLen
		etherType = uint16(raw[off])<<8 | uint16(raw[off+1])
	}
	if etherType != EtherTypeGOOSE {
		return nil, errNotGOOSE
	}

	var f gooseFrame
	copy(f.srcMAC[:], raw[6:12])

	pdu := raw[off+2:]
	content, _, err := readTLV(pdu, 0, 0x30)
	if err != nil {
		return nil, err
	}

	simSeen := false
	pos := 0
	for pos < len(content) {
		tag, val, next, err := readAnyTLV(content, pos)
		if err != nil {
			return nil, err
		}
		pos = next
		switch tag {
		case tagGoCBRef:
			f.gocbRef, err = readString(val)
		case tagTimeTTL:
			f.ttl, err = readUint32(val)
		case tagDataSet:
			f.datSet, err = readString(val)
		case tagGoID:
			f.goID, err = readString(val)
		case tagStNum:
			f.stNum, err = readUint32(val)
		case tagSqNum:
			f.sqNum, err = readUint32(val)
		case tagSimulation:
			f.simulation, err = readBool(val)
			simSeen = true
		}
		if err != nil {
			return nil, err
		}
		if simSeen {
			break
		}
	}

	if f.gocbRef == "" || f.datSet == "" || f.goID == "" || !simSeen {
		return nil, errMissingField
	}
	return &f, nil
}

// readTLV reads one BER TLV at pos, requiring the expected tag, and returns
// its value bytes plus the position just past the TLV.
func readTLV(buf []byte, pos int, want byte) (value []byte, next int, err error) {
	tag, value, next, err := readAnyTLV(buf, pos)
	if err != nil {
		return nil, 0, err
	}
	if tag != want {
		return nil, 0, errBadOuterTLV
	}
	return value, next, nil
}

// readAnyTLV decodes one BER TLV without interpreting the value.
func readAnyTLV(buf []byte, pos int) (tag byte, value []byte, next int, err error) {
	if pos >= len(buf) {
		return 0, nil, 0, errTruncatedTLV
	}
	tag = buf[pos]
	pos++
	if pos >= len(buf) {
		return 0, nil, 0, errTruncatedTLV
	}

	length := int(buf[pos])
	pos++
	if length&0x80 != 0 {
		numBytes := length & 0x7F
		if numBytes == 0 || numBytes > 4 {
			return 0, nil, 0, errBadLength
		}
		length = 0
		for i := 0; i < numBytes; i++ {
			if pos >= len(buf) {
				return 0, nil, 0, errTruncatedTLV
			}
			length = length<<8 | int(buf[pos])
			pos++
		}
		if length < 0 {
			return 0, nil, 0, errBadLength
		}
	}
	if pos+length > len(buf) {
		return 0, nil, 0, errTruncatedTLV
	}
	return tag, buf[pos : pos+length], pos + length, nil
}

// readUint32 decodes a non-negative BER INTEGER that fits in uint32.
// GOOSE stNum, sqNum and timeAllowedToLive are unsigned 32-bit quantities.
func readUint32(value []byte) (uint32, error) {
	if len(value) == 0 || len(value) > 5 {
		return 0, errBadInteger
	}
	var result uint64
	for _, b := range value {
		result = result<<8 | uint64(b)
		if result > 1<<32-1 {
			return 0, errBadInteger
		}
	}
	return uint32(result), nil
}

func readBool(value []byte) (bool, error) {
	if len(value) != 1 {
		return false, errBadBoolean
	}
	// BER BOOLEAN: FALSE is 0x00, any other octet is TRUE.
	return value[0] != 0x00, nil
}

func readString(value []byte) (string, error) {
	if len(value) > 1<<20 {
		return "", errStringOverflow
	}
	return string(value), nil
}
