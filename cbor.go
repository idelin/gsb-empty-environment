package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"sort"
)

// Minimal deterministic CBOR (RFC 8949) encoder. Only the value shapes that
// can appear in an mdoc response's data elements, IssuerSignedItem and
// DeviceAuthentication structure are supported. Integers use the shortest
// representation, map keys are emitted in canonical (length then byte) order
// and floats use the shortest round-trippable width.

const (
	cborUint    byte = 0 << 5
	cborNegint  byte = 1 << 5
	cborBytes   byte = 2 << 5
	cborText    byte = 3 << 5
	cborArray   byte = 4 << 5
	cborMap     byte = 5 << 5
	cborTag     byte = 6 << 5
	cborSimple  byte = 7 << 5
	cborFalse   byte = cborSimple | 20
	cborTrue    byte = cborSimple | 21
	cborNull    byte = cborSimple | 22
	cborFloat64 byte = cborSimple | 27

	cborTagEncodedCBOR uint64 = 24
)

// cborEncode deterministically encodes value into buf.
func cborEncode(buf *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buf.WriteByte(cborNull)
	case bool:
		if v {
			buf.WriteByte(cborTrue)
		} else {
			buf.WriteByte(cborFalse)
		}
	case int:
		encodeInt(buf, int64(v))
	case int64:
		encodeInt(buf, v)
	case uint64:
		encodeHead(buf, cborUint, v)
	case uint:
		encodeHead(buf, cborUint, uint64(v))
	case string:
		encodeHead(buf, cborText, uint64(len(v)))
		buf.WriteString(v)
	case []byte:
		encodeByteSlice(buf, v)
	case float64:
		encodeFloat(buf, v)
	case float32:
		encodeFloat(buf, float64(v))
	case []any:
		encodeHead(buf, cborArray, uint64(len(v)))
		for _, item := range v {
			if err := cborEncode(buf, item); err != nil {
				return err
			}
		}
	case map[string]any:
		if err := encodeStringMap(buf, v); err != nil {
			return err
		}
	case map[int]any:
		if err := encodeIntKeyedMap(buf, v); err != nil {
			return err
		}
	case map[uint64]any:
		if err := encodeUintKeyedMap(buf, v); err != nil {
			return err
		}
	case map[string]map[string]any:
		if err := encodeDeviceNameSpacesMap(buf, v); err != nil {
			return err
		}
	case map[any]any:
		if err := encodeAnyKeyedMap(buf, v); err != nil {
			return err
		}
	default:
		return errors.New("netsec: unsupported CBOR value type")
	}
	return nil
}

// encodeByteSlice emits a CBOR byte string. A nil slice is distinct from an
// empty one and encodes as null; an empty slice encodes as 0x40.
func encodeByteSlice(buf *bytes.Buffer, v []byte) {
	if v == nil {
		buf.WriteByte(cborNull)
		return
	}
	encodeHead(buf, cborBytes, uint64(len(v)))
	buf.Write(v)
}

func encodeAnyKeyedMap(buf *bytes.Buffer, m map[any]any) error {
	type entry struct {
		key []byte
		val any
	}
	entries := make([]entry, 0, len(m))
	for k, val := range m {
		kb := &bytes.Buffer{}
		if err := cborEncode(kb, k); err != nil {
			return err
		}
		entries = append(entries, entry{key: kb.Bytes(), val: val})
	}
	sort.Slice(entries, func(i, j int) bool {
		return cborKeyLess(entries[i].key, entries[j].key)
	})
	encodeHead(buf, cborMap, uint64(len(entries)))
	for _, e := range entries {
		buf.Write(e.key)
		if err := cborEncode(buf, e.val); err != nil {
			return err
		}
	}
	return nil
}

func encodeDeviceNameSpacesMap(buf *bytes.Buffer, m map[string]map[string]any) error {
	nsKeys := make([]string, 0, len(m))
	for ns := range m {
		nsKeys = append(nsKeys, ns)
	}
	sort.Strings(nsKeys)

	encodeHead(buf, cborMap, uint64(len(nsKeys)))
	for _, ns := range nsKeys {
		encodeHead(buf, cborText, uint64(len(ns)))
		buf.WriteString(ns)
		if err := encodeStringMap(buf, m[ns]); err != nil {
			return err
		}
	}
	return nil
}

func encodeInt(buf *bytes.Buffer, value int64) {
	if value >= 0 {
		encodeHead(buf, cborUint, uint64(value))
		return
	}
	encodeHead(buf, cborNegint, uint64(-value)-1)
}

// encodeHead writes a CBOR head byte plus argument for the given major type.
func encodeHead(buf *bytes.Buffer, major byte, n uint64) {
	switch {
	case n < 24:
		buf.WriteByte(major | byte(n))
	case n <= math.MaxUint8:
		buf.WriteByte(major | 24)
		buf.WriteByte(byte(n))
	case n <= math.MaxUint16:
		buf.WriteByte(major | 25)
		var scratch [2]byte
		binary.BigEndian.PutUint16(scratch[:], uint16(n))
		buf.Write(scratch[:])
	case n <= math.MaxUint32:
		buf.WriteByte(major | 26)
		var scratch [4]byte
		binary.BigEndian.PutUint32(scratch[:], uint32(n))
		buf.Write(scratch[:])
	default:
		buf.WriteByte(major | 27)
		var scratch [8]byte
		binary.BigEndian.PutUint64(scratch[:], n)
		buf.Write(scratch[:])
	}
}

func encodeStringMap(buf *bytes.Buffer, m map[string]any) error {
	type entry struct {
		key []byte
		val any
	}
	entries := make([]entry, 0, len(m))
	for k, val := range m {
		kb := &bytes.Buffer{}
		encodeHead(kb, cborText, uint64(len(k)))
		kb.WriteString(k)
		entries = append(entries, entry{key: kb.Bytes(), val: val})
	}
	sort.Slice(entries, func(i, j int) bool {
		return cborKeyLess(entries[i].key, entries[j].key)
	})
	encodeHead(buf, cborMap, uint64(len(entries)))
	for _, e := range entries {
		buf.Write(e.key)
		if err := cborEncode(buf, e.val); err != nil {
			return err
		}
	}
	return nil
}

func encodeIntKeyedMap(buf *bytes.Buffer, m map[int]any) error {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	encodeHead(buf, cborMap, uint64(len(keys)))
	for _, k := range keys {
		encodeInt(buf, int64(k))
		if err := cborEncode(buf, m[k]); err != nil {
			return err
		}
	}
	return nil
}

func encodeUintKeyedMap(buf *bytes.Buffer, m map[uint64]any) error {
	keys := make([]uint64, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	encodeHead(buf, cborMap, uint64(len(keys)))
	for _, k := range keys {
		encodeHead(buf, cborUint, k)
		if err := cborEncode(buf, m[k]); err != nil {
			return err
		}
	}
	return nil
}

// cborKeyLess implements the canonical CBOR key ordering: shorter encoding
// first, then lexicographic byte order.
func cborKeyLess(a, b []byte) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return bytes.Compare(a, b) < 0
}

func encodeFloat(buf *bytes.Buffer, f float64) {
	switch {
	case f == 0:
		// Canonical zero is positive 0.0; preserve an explicit negative zero.
		if math.Signbit(f) {
			buf.Write([]byte{0xf9, 0x80, 0x00})
			return
		}
		buf.Write([]byte{0xf9, 0x00, 0x00})
	case math.IsNaN(f):
		buf.Write([]byte{0xf9, 0x7e, 0x00})
	case math.IsInf(f, 1):
		buf.Write([]byte{0xf9, 0x7c, 0x00})
	case math.IsInf(f, -1):
		buf.Write([]byte{0xf9, 0xfc, 0x00})
	default:
		if f16, ok := toFloat16(f); ok {
			buf.Write([]byte{0xf9, byte(f16 >> 8), byte(f16)})
			return
		}
		if float64(float32(f)) == f {
			buf.WriteByte(cborSimple | 26)
			var scratch [4]byte
			binary.BigEndian.PutUint32(scratch[:], math.Float32bits(float32(f)))
			buf.Write(scratch[:])
			return
		}
		buf.WriteByte(cborFloat64)
		var scratch [8]byte
		binary.BigEndian.PutUint64(scratch[:], math.Float64bits(f))
		buf.Write(scratch[:])
	}
}

// toFloat16 returns the IEEE 754 binary16 encoding of f when f round-trips
// through binary16 without changing value.
func toFloat16(f float64) (uint16, bool) {
	bits := math.Float64bits(f)
	sign := uint16(bits>>63) << 15
	exp := int((bits>>52)&0x7ff) - 1023
	mant := bits & ((1 << 52) - 1)

	switch {
	case exp > 15:
		return 0, false
	case exp >= -14:
		halfExp := uint16(exp + 15)
		halfMant := uint16(mant >> 42)
		encoded := sign | halfExp<<10 | halfMant
		if math.Float64frombits(float16ToBits(encoded)) == f {
			return encoded, true
		}
		return 0, false
	case exp >= -24:
		// Subnormal binary16 values.
		shift := uint(-14 - exp)
		halfMant := uint16((mant | (1 << 52)) >> (42 + shift))
		encoded := sign | halfMant
		if math.Float64frombits(float16ToBits(encoded)) == f {
			return encoded, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// float16ToBits converts a binary16 bit pattern into a binary64 bit pattern.
func float16ToBits(h uint16) uint64 {
	sign := uint64(h&0x8000) << 48
	exp := h & 0x7c00
	mant := uint64(h & 0x03ff)
	switch exp {
	case 0x7c00:
		return sign | 0x7ff0000000000000 | mant<<40
	case 0:
		if mant == 0 {
			return sign
		}
		for mant&0x0400 == 0 {
			mant <<= 1
			exp -= 0x0400
		}
		exp += 0x0400
		mant &= 0x03ff
	}
	return sign | uint64((exp>>10)+(1023-15))<<52 | mant<<42
}

// encodeIssuerSignedItem emits tag 24 wrapping the deterministic encoding of
// {0: digestID, 1: random, 2: elementIdentifier, 3: elementValue}, which is
// the byte string whose digest appears in the MSO valueDigests.
func encodeIssuerSignedItem(item IssuerSignedItem) ([]byte, error) {
	inner := &bytes.Buffer{}
	encodeHead(inner, cborMap, 4)

	encodeHead(inner, cborUint, 0)
	encodeHead(inner, cborUint, item.DigestID)

	encodeHead(inner, cborUint, 1)
	encodeHead(inner, cborBytes, uint64(len(item.Random)))
	inner.Write(item.Random)

	encodeHead(inner, cborUint, 2)
	encodeHead(inner, cborText, uint64(len(item.ElementIdentifier)))
	inner.WriteString(item.ElementIdentifier)

	encodeHead(inner, cborUint, 3)
	if err := cborEncode(inner, item.ElementValue); err != nil {
		return nil, err
	}

	out := &bytes.Buffer{}
	encodeHead(out, cborTag, cborTagEncodedCBOR)
	encodeHead(out, cborBytes, uint64(inner.Len()))
	out.Write(inner.Bytes())
	return out.Bytes(), nil
}

// encodeDeviceNameSpaces canonical-encodes a deviceNameSpaces map.
func encodeDeviceNameSpaces(nameSpaces map[string]map[string]any) ([]byte, error) {
	buf := &bytes.Buffer{}
	nsKeys := make([]string, 0, len(nameSpaces))
	for ns := range nameSpaces {
		nsKeys = append(nsKeys, ns)
	}
	sort.Strings(nsKeys)

	encodeHead(buf, cborMap, uint64(len(nsKeys)))
	for _, ns := range nsKeys {
		elements := nameSpaces[ns]
		encodeHead(buf, cborText, uint64(len(ns)))
		buf.WriteString(ns)

		elKeys := make([]string, 0, len(elements))
		for k := range elements {
			elKeys = append(elKeys, k)
		}
		sort.Strings(elKeys)
		encodeHead(buf, cborMap, uint64(len(elKeys)))
		for _, k := range elKeys {
			encodeHead(buf, cborText, uint64(len(k)))
			buf.WriteString(k)
			if err := cborEncode(buf, elements[k]); err != nil {
				return nil, err
			}
		}
	}
	return buf.Bytes(), nil
}

// encodeDeviceAuthentication emits the deterministic DeviceAuthentication
// array defined in ISO/IEC 18013-5:
//
//	["DeviceAuthentication", SessionTranscript, DocType, DeviceNameSpacesBytes]
//
// sessionTranscript is the verifier's full on-the-wire SessionTranscript
// encoding (the tag 24 byte string) and is embedded verbatim. A nil
// deviceNameSpacesBytes encodes the null placeholder used when no device
// namespaces are present.
func encodeDeviceAuthentication(sessionTranscript []byte, docType string, deviceNameSpacesBytes []byte) ([]byte, error) {
	buf := &bytes.Buffer{}
	encodeHead(buf, cborArray, 4)

	const deviceAuthentication = "DeviceAuthentication"
	encodeHead(buf, cborText, uint64(len(deviceAuthentication)))
	buf.WriteString(deviceAuthentication)

	buf.Write(sessionTranscript)

	encodeHead(buf, cborText, uint64(len(docType)))
	buf.WriteString(docType)

	if deviceNameSpacesBytes == nil {
		buf.WriteByte(cborNull)
	} else {
		encodeHead(buf, cborBytes, uint64(len(deviceNameSpacesBytes)))
		buf.Write(deviceNameSpacesBytes)
	}
	return buf.Bytes(), nil
}
