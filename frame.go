package safeframe

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
)

const (
	Magic          = 0x5346524d // "SFRM"
	Version1       = 1
	HeaderLength   = 32
	MaxPayloadSize = 1 << 20
)

const (
	TypeBytes uint8 = iota
	TypeString
	TypeMessage
)

const FlagRedacted uint8 = 0x01

var (
	ErrTruncated     = errors.New("safeframe: incomplete frame")
	ErrBadMagic      = errors.New("safeframe: bad magic")
	ErrBadVersion    = errors.New("safeframe: unsupported version")
	ErrBadReserved   = errors.New("safeframe: reserved header bytes are not zero")
	ErrPayloadLimit  = errors.New("safeframe: declared frame exceeds payload limit")
	ErrBadLength     = errors.New("safeframe: invalid header or field length")
	ErrBadType       = errors.New("safeframe: unknown structured field type")
	ErrChecksum      = errors.New("safeframe: checksum mismatch")
	ErrBadName       = errors.New("safeframe: invalid field name")
	ErrBadUTF8       = errors.New("safeframe: string field is not valid UTF-8")
	ErrStructTooDeep = errors.New("safeframe: structured payload nesting too deep")
)

// Root is the top-level structured payload.
type Root []*Field

type Field struct {
	Name  string
	Type  uint8
	Bytes []byte
	Text  string
	Root  Root
	Extra []byte
}

type Frame struct {
	Version       uint8
	Flags         uint8
	Sequence      uint64
	TimestampNano uint64
	Root          Root
}

var ieeeTable = crc32.MakeTable(crc32.IEEE)

func (f Frame) Marshal() ([]byte, error) {
	version := f.Version
	if version == 0 {
		version = Version1
	}
	if version != Version1 {
		return nil, ErrBadVersion
	}

	payload, err := marshalFields(f.Root, 0)
	if err != nil {
		return nil, err
	}
	if len(payload) > MaxPayloadSize {
		return nil, ErrPayloadLimit
	}
	return encodeFrame(version, f, payload)
}

// MarshalWithPayloadLimit serializes a frame with a caller-defined payload
// allowance. The declared payload length is always recomputed.
func (f Frame) MarshalWithPayloadLimit(maxPayload int) ([]byte, error) {
	maxPayload = normalizePayloadLimit(maxPayload)
	version := f.Version
	if version == 0 {
		version = Version1
	}
	if version != Version1 {
		return nil, ErrBadVersion
	}

	payload, err := marshalFieldsWithLimit(f.Root, 0, maxPayload)
	if err != nil {
		return nil, err
	}
	return encodeFrame(version, f, payload)
}

func encodeFrame(version uint8, f Frame, payload []byte) ([]byte, error) {

	buf := make([]byte, HeaderLength+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], Magic)
	buf[4] = version
	buf[5] = f.Flags
	binary.BigEndian.PutUint32(buf[8:12], uint32(len(payload)))
	binary.BigEndian.PutUint64(buf[12:20], f.Sequence)
	binary.BigEndian.PutUint64(buf[20:28], f.TimestampNano)
	copy(buf[HeaderLength:], payload)

	protected := make([]byte, 0, len(buf)-4+len(payload))
	protected = append(protected, buf[:28]...)
	protected = append(protected, payload...)
	sum := crc32.Checksum(protected, ieeeTable)
	binary.BigEndian.PutUint32(buf[28:32], sum)
	return buf, nil
}

func marshalFields(fields Root, depth int) ([]byte, error) {
	return marshalFieldsWithLimit(fields, depth, MaxPayloadSize)
}

func marshalFieldsWithLimit(fields Root, depth int, maxPayload int) ([]byte, error) {
	if depth > 64 {
		return nil, ErrStructTooDeep
	}

	var parts [][]byte
	total := 0
	for _, field := range fields {
		if field == nil || !validName(field.Name) {
			return nil, ErrBadName
		}

		var value []byte
		valueLen := 0
		switch field.Type {
		case TypeBytes:
			valueLen = len(field.Bytes)
		case TypeString:
			if !isValidUTF8(field.Text) {
				return nil, ErrBadUTF8
			}
			valueLen = len(field.Text)
		case TypeMessage:
			nested, err := marshalFieldsWithLimit(field.Root, depth+1, maxPayload)
			if err != nil {
				return nil, err
			}
			value = nested
			valueLen = len(value)
		default:
			if len(field.Extra) > 0x7fffffff {
				return nil, ErrBadLength
			}
			valueLen = len(field.Extra)
		}

		if len(field.Name) > 0xffff || valueLen > 0x7fffffff {
			return nil, ErrBadLength
		}
		if total+7+len(field.Name)+valueLen > maxPayload {
			return nil, ErrPayloadLimit
		}

		if field.Type == TypeBytes {
			value = append([]byte(nil), field.Bytes...)
		} else if field.Type == TypeString {
			value = []byte(field.Text)
		} else if field.Type != TypeMessage {
			value = append([]byte(nil), field.Extra...)
		}

		entry := make([]byte, 7+len(field.Name)+valueLen)
		binary.BigEndian.PutUint16(entry[0:2], uint16(len(field.Name)))
		copy(entry[2:2+len(field.Name)], field.Name)
		entry[2+len(field.Name)] = field.Type
		binary.BigEndian.PutUint32(entry[3+len(field.Name):7+len(field.Name)], uint32(valueLen))
		copy(entry[7+len(field.Name):], value)
		parts = append(parts, entry)
		total += len(entry)
		if total > maxPayload {
			return nil, ErrPayloadLimit
		}
	}

	out := make([]byte, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out, nil
}

// DecodeFrame parses one complete wire frame and verifies its checksum.
func DecodeFrame(wire []byte) (*Frame, error) {
	if len(wire) < HeaderLength {
		return nil, ErrTruncated
	}
	if binary.BigEndian.Uint32(wire[0:4]) != Magic {
		return nil, ErrBadMagic
	}
	if wire[4] != Version1 {
		return nil, ErrBadVersion
	}
	if wire[6] != 0 || wire[7] != 0 {
		return nil, ErrBadReserved
	}

	declared := int(binary.BigEndian.Uint32(wire[8:12]))
	if declared > MaxPayloadSize {
		return nil, ErrPayloadLimit
	}
	if len(wire) != HeaderLength+declared {
		return nil, ErrBadLength
	}

	expected := binary.BigEndian.Uint32(wire[28:32])
	protected := make([]byte, 0, len(wire)-4)
	protected = append(protected, wire[:28]...)
	protected = append(protected, wire[32:]...)
	actual := crc32.Checksum(protected, ieeeTable)
	if expected != actual {
		return nil, ErrChecksum
	}

	root, rootConsumed, err := unmarshalFields(wire[HeaderLength:], 0)
	if err != nil {
		return nil, err
	}
	if rootConsumed != declared {
		return nil, ErrBadLength
	}

	return &Frame{
		Version:       wire[4],
		Flags:         wire[5],
		Sequence:      binary.BigEndian.Uint64(wire[12:20]),
		TimestampNano: binary.BigEndian.Uint64(wire[20:28]),
		Root:          root,
	}, nil
}

func unmarshalFields(data []byte, depth int) (Root, int, error) {
	if depth > 64 {
		return nil, 0, ErrStructTooDeep
	}

	var fields Root
	consumed := 0
	for consumed < len(data) {
		if len(data)-consumed < 7 {
			return nil, 0, ErrBadLength
		}

		nameLen := int(binary.BigEndian.Uint16(data[consumed : consumed+2]))
		entryStart := consumed
		consumed += 2
		if len(data)-consumed < nameLen+5 {
			return nil, 0, ErrBadLength
		}

		name := string(data[consumed : consumed+nameLen])
		if !validName(name) {
			return nil, 0, ErrBadName
		}
		consumed += nameLen

		typ := data[consumed]
		consumed++

		valueLen := int(binary.BigEndian.Uint32(data[consumed : consumed+4]))
		consumed += 4
		if valueLen < 0 || valueLen > len(data)-consumed {
			return nil, 0, ErrBadLength
		}

		value := data[consumed : consumed+valueLen]
		field := &Field{Name: name, Type: typ}

		switch typ {
		case TypeBytes:
			field.Bytes = append([]byte(nil), value...)
		case TypeString:
			if !isValidUTF8Bytes(value) {
				return nil, 0, ErrBadUTF8
			}
			field.Text = string(value)
		case TypeMessage:
			child, childConsumed, err := unmarshalFields(value, depth+1)
			if err != nil {
				return nil, 0, err
			}
			if childConsumed != len(value) {
				return nil, 0, ErrBadLength
			}
			field.Root = child
		default:
			field.Extra = append([]byte(nil), value...)
		}

		fields = append(fields, field)
		consumed += valueLen
		if consumed < entryStart {
			return nil, 0, ErrBadLength
		}
	}
	return fields, consumed, nil
}

func validName(name string) bool {
	if name == "" || !isValidUTF8(name) {
		return false
	}
	for _, r := range name {
		if r == '.' || r < 0x20 {
			return false
		}
	}
	return true
}

func isValidUTF8(value string) bool {
	return isValidUTF8Bytes([]byte(value))
}

func isValidUTF8Bytes(value []byte) bool {
	return bytes.Equal([]byte(string(value)), value)
}

// ReadFrame reads one complete frame without trusting declared lengths before
// enforcing maxPayload. A clean EOF is represented by io.EOF.
func ReadFrame(r io.Reader, maxPayload int) (*Frame, int, error) {
	maxPayload = normalizePayloadLimit(maxPayload)

	header := make([]byte, HeaderLength)
	n, err := io.ReadFull(r, header)
	if err == io.ErrUnexpectedEOF {
		return nil, n, ErrTruncated
	}
	if err != nil {
		return nil, n, err
	}

	if _, err := inspectHeader(header, maxPayload); err != nil {
		return nil, n, err
	}

	declared := int(binary.BigEndian.Uint32(header[8:12]))

	payload := make([]byte, declared)
	m, err := io.ReadFull(r, payload)
	n += m
	if err == io.ErrUnexpectedEOF {
		return nil, n, ErrTruncated
	}
	if err != nil {
		return nil, n, err
	}

	frame, err := DecodeFrame(append(header, payload...))
	if err != nil {
		return nil, n, err
	}
	return frame, n, nil
}
