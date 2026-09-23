package blockpack

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Hard limits for the supported serialization ranges. Every length field in
// the wire format is validated against these limits and against the remaining
// input bytes before any allocation happens.
const (
	MaxTypeLen = 256
	MaxDataLen = 1 << 20
	MaxRefs    = 4096
	MaxObjects = 1 << 22
	MaxBlocks  = 1 << 22
)

// MaxObjectEnc is the largest possible encoded object.
const MaxObjectEnc = 12 + MaxTypeLen + MaxDataLen + 4*MaxRefs

var (
	errTruncated = errors.New("blockpack: truncated input")
	errLength    = errors.New("blockpack: length field out of supported range")
)

// Object is a typed payload that may reference other objects by index.
type Object struct {
	Type string
	Data []byte
	Refs []uint32
}

// EncodeObject serializes an object into the canonical length-prefixed
// binary form:
//
//	u32 len(Type) | Type | u32 len(Data) | Data | u32 len(Refs) | u32 Refs...
//
// The encoding is plain data only: decoding never resolves types and never
// executes host code.
func EncodeObject(o Object) ([]byte, error) {
	if len(o.Type) > MaxTypeLen {
		return nil, fmt.Errorf("blockpack: type too long: %d", len(o.Type))
	}
	if len(o.Data) > MaxDataLen {
		return nil, fmt.Errorf("blockpack: data too large: %d", len(o.Data))
	}
	if len(o.Refs) > MaxRefs {
		return nil, fmt.Errorf("blockpack: too many refs: %d", len(o.Refs))
	}
	buf := make([]byte, 0, 12+len(o.Type)+len(o.Data)+4*len(o.Refs))
	buf = append(buf, u32(uint32(len(o.Type)))...)
	buf = append(buf, o.Type...)
	buf = append(buf, u32(uint32(len(o.Data)))...)
	buf = append(buf, o.Data...)
	buf = append(buf, u32(uint32(len(o.Refs)))...)
	for _, r := range o.Refs {
		buf = append(buf, u32(r)...)
	}
	return buf, nil
}

// DecodeObject decodes one object from the front of b and returns the rest.
// Each length field is checked against the hard limits and the remaining
// input before the corresponding allocation is made.
func DecodeObject(b []byte) (Object, []byte, error) {
	var o Object

	typeLen, rest, err := takeU32(b)
	if err != nil {
		return o, nil, err
	}
	if typeLen > MaxTypeLen || uint32(len(rest)) < typeLen {
		return o, nil, errLength
	}
	o.Type = string(rest[:typeLen])
	rest = rest[typeLen:]

	dataLen, rest, err := takeU32(rest)
	if err != nil {
		return o, nil, err
	}
	if dataLen > MaxDataLen || uint32(len(rest)) < dataLen {
		return o, nil, errLength
	}
	o.Data = append([]byte(nil), rest[:dataLen]...)
	rest = rest[dataLen:]

	nRefs, rest, err := takeU32(rest)
	if err != nil {
		return o, nil, err
	}
	if nRefs > MaxRefs || uint32(len(rest)) < nRefs*4 {
		return o, nil, errLength
	}
	o.Refs = make([]uint32, 0, nRefs)
	for i := uint32(0); i < nRefs; i++ {
		o.Refs = append(o.Refs, binary.LittleEndian.Uint32(rest))
		rest = rest[4:]
	}
	return o, rest, nil
}

func takeU32(b []byte) (uint32, []byte, error) {
	if len(b) < 4 {
		return 0, nil, errTruncated
	}
	return binary.LittleEndian.Uint32(b), b[4:], nil
}

// objectDigest commits to the full canonical encoding, which includes the
// object's outgoing references.
func objectDigest(enc []byte) Digest {
	return hashParts([]byte("obj\x00"), enc)
}
