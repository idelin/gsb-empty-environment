package bridge

import (
	"encoding/binary"
	"fmt"
	"sort"
)

// RawField is an undecoded field kept as raw bytes (unknown fields and
// extension blobs), so nothing is lost in transit.
type RawField struct {
	ID   uint32
	Type FieldType
	Data []byte
}

// Message is the decoded in-memory form of a frame payload.
type Message struct {
	Values  map[uint32]Value
	Unknown []RawField
}

func NewMessage() *Message { return &Message{Values: map[uint32]Value{}} }

func appendUvarint(dst []byte, v uint64) []byte {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], v)
	return append(dst, b[:n]...)
}

func appendRawField(dst []byte, rf RawField) []byte {
	dst = appendUvarint(dst, uint64(rf.ID))
	dst = append(dst, byte(rf.Type))
	dst = appendUvarint(dst, uint64(len(rf.Data)))
	return append(dst, rf.Data...)
}

func valuePayload(v Value) []byte {
	switch v.Type {
	case TyInt:
		var b [binary.MaxVarintLen64]byte
		n := binary.PutVarint(b[:], v.Int)
		return append([]byte(nil), b[:n]...)
	case TyBool:
		if v.Bool {
			return []byte{1}
		}
		return []byte{0}
	case TyString:
		return []byte(v.Str)
	case TyBytes:
		return v.Bytes
	}
	return nil
}

func decodeValue(t FieldType, raw []byte) (Value, error) {
	switch t {
	case TyInt:
		x, n := binary.Varint(raw)
		if n <= 0 || n != len(raw) {
			return Value{}, fmt.Errorf("%w: malformed int payload", ErrBadData)
		}
		return IntVal(x), nil
	case TyBool:
		if len(raw) != 1 || raw[0] > 1 {
			return Value{}, fmt.Errorf("%w: malformed bool payload", ErrBadData)
		}
		return BoolVal(raw[0] == 1), nil
	case TyString:
		return StrVal(string(raw)), nil
	case TyBytes:
		return BytesVal(append([]byte(nil), raw...)), nil
	}
	return Value{}, fmt.Errorf("%w: unknown field type %d", ErrBadData, t)
}

// DecodeMessage parses a complete frame payload under a schema. Unknown
// fields are preserved verbatim in Unknown. Truncated or mistyped fields
// are bad data, not "need more data": the frame boundary is already known.
func DecodeMessage(s *Schema, payload []byte) (*Message, error) {
	m := NewMessage()
	for len(payload) > 0 {
		id, n := binary.Uvarint(payload)
		if n <= 0 {
			return nil, fmt.Errorf("%w: malformed field id", ErrBadData)
		}
		payload = payload[n:]
		if len(payload) < 1 {
			return nil, fmt.Errorf("%w: missing field type", ErrBadData)
		}
		t := FieldType(payload[0])
		payload = payload[1:]
		l, n := binary.Uvarint(payload)
		if n <= 0 {
			return nil, fmt.Errorf("%w: malformed field length", ErrBadData)
		}
		payload = payload[n:]
		if l > uint64(len(payload)) {
			return nil, fmt.Errorf("%w: field %d truncated", ErrBadData, id)
		}
		raw := payload[:l]
		payload = payload[l:]
		fid := uint32(id)
		if f, ok := s.Fields[fid]; ok {
			if f.Type != t {
				return nil, fmt.Errorf("%w: field %d has wire type %s, schema says %s", ErrBadData, fid, t, f.Type)
			}
			v, err := decodeValue(t, raw)
			if err != nil {
				return nil, err
			}
			m.Values[fid] = v
		} else {
			m.Unknown = append(m.Unknown, RawField{ID: fid, Type: t, Data: append([]byte(nil), raw...)})
		}
	}
	for _, f := range s.Fields {
		if f.Required {
			if _, ok := m.Values[f.ID]; !ok {
				return nil, fmt.Errorf("%w: missing required field %d (%s)", ErrBadData, f.ID, f.Name)
			}
		}
	}
	return m, nil
}

// Encode serializes a message deterministically: known values sorted by
// field ID, then unknown raw fields in their original order.
func (m *Message) Encode() []byte {
	var out []byte
	ids := make([]uint32, 0, len(m.Values))
	for id := range m.Values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		v := m.Values[id]
		out = appendRawField(out, RawField{ID: id, Type: v.Type, Data: valuePayload(v)})
	}
	for _, u := range m.Unknown {
		out = appendRawField(out, u)
	}
	return out
}
