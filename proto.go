package netsec

import (
	"errors"
	"fmt"
)

// WireType is a protobuf wire type.
type WireType uint8

const (
	WireVarint     WireType = 0
	WireFixed64    WireType = 1
	WireBytes      WireType = 2
	WireStartGroup WireType = 3
	WireEndGroup   WireType = 4
	WireFixed32    WireType = 5
)

const maxProtobufFieldNumber = (1 << 29) - 1

var errVarintOverflow = errors.New("protobuf varint overflows uint64")

type rawField struct {
	Number  int32
	Type    WireType
	Varint  uint64
	Fixed32 uint32
	Fixed64 uint64
	Bytes   []byte
	Raw     []byte
}

func readVarint(data []byte, off *int) (uint64, error) {
	var value uint64
	for i := 0; i < 10; i++ {
		if *off >= len(data) {
			return 0, errors.New("truncated protobuf varint")
		}
		b := data[*off]
		*off = *off + 1
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, errVarintOverflow
			}
			return value | uint64(b)<<uint(7*i), nil
		}
		value |= uint64(b&0x7f) << uint(7*i)
	}
	return 0, errVarintOverflow
}

func skipGroup(data []byte, off *int, groupNumber int32) error {
	for *off < len(data) {
		tag, err := readVarint(data, off)
		if err != nil {
			return err
		}
		number := int32(tag >> 3)
		wireType := WireType(tag & 7)
		if number == 0 || number > maxProtobufFieldNumber {
			return fmt.Errorf("invalid protobuf field number %d", number)
		}
		if wireType == WireEndGroup {
			if number != groupNumber {
				return fmt.Errorf("protobuf group %d closed by group %d", groupNumber, number)
			}
			return nil
		}
		if err := skipValue(data, off, number, wireType); err != nil {
			return err
		}
	}
	return fmt.Errorf("unterminated protobuf group %d", groupNumber)
}

func skipValue(data []byte, off *int, number int32, wireType WireType) error {
	switch wireType {
	case WireVarint:
		_, err := readVarint(data, off)
		return err
	case WireFixed64:
		if len(data)-*off < 8 {
			return errors.New("truncated protobuf fixed64")
		}
		*off += 8
		return nil
	case WireBytes:
		length, err := readVarint(data, off)
		if err != nil {
			return err
		}
		if length > uint64(len(data)-*off) {
			return errors.New("truncated protobuf bytes field")
		}
		*off += int(length)
		return nil
	case WireFixed32:
		if len(data)-*off < 4 {
			return errors.New("truncated protobuf fixed32")
		}
		*off += 4
		return nil
	case WireStartGroup:
		return skipGroup(data, off, number)
	case WireEndGroup:
		return fmt.Errorf("unexpected protobuf end-group for field %d", number)
	default:
		return fmt.Errorf("invalid protobuf wire type %d", wireType)
	}
}

func parseFields(data []byte) ([]rawField, error) {
	var fields []rawField
	off := 0
	for off < len(data) {
		start := off
		tag, err := readVarint(data, &off)
		if err != nil {
			return nil, err
		}
		number := int32(tag >> 3)
		wireType := WireType(tag & 7)
		if number == 0 || number > maxProtobufFieldNumber {
			return nil, fmt.Errorf("invalid protobuf field number %d", number)
		}

		field := rawField{Number: number, Type: wireType}
		switch wireType {
		case WireVarint:
			field.Varint, err = readVarint(data, &off)
			if err != nil {
				return nil, err
			}
		case WireFixed64:
			if len(data)-off < 8 {
				return nil, errors.New("truncated protobuf fixed64")
			}
			field.Fixed64 = uint64(data[off]) |
				uint64(data[off+1])<<8 |
				uint64(data[off+2])<<16 |
				uint64(data[off+3])<<24 |
				uint64(data[off+4])<<32 |
				uint64(data[off+5])<<40 |
				uint64(data[off+6])<<48 |
				uint64(data[off+7])<<56
			off += 8
		case WireBytes:
			length, err := readVarint(data, &off)
			if err != nil {
				return nil, err
			}
			if length > uint64(len(data)-off) {
				return nil, errors.New("truncated protobuf bytes field")
			}
			field.Bytes = data[off : off+int(length)]
			off += int(length)
		case WireStartGroup:
			if err := skipGroup(data, &off, number); err != nil {
				return nil, err
			}
		case WireFixed32:
			if len(data)-off < 4 {
				return nil, errors.New("truncated protobuf fixed32")
			}
			field.Fixed32 = uint32(data[off]) |
				uint32(data[off+1])<<8 |
				uint32(data[off+2])<<16 |
				uint32(data[off+3])<<24
			off += 4
		case WireEndGroup:
			return nil, fmt.Errorf("unexpected protobuf end-group for field %d", number)
		default:
			return nil, fmt.Errorf("invalid protobuf wire type %d", wireType)
		}
		field.Raw = data[start:off]
		fields = append(fields, field)
	}
	return fields, nil
}

func appendVarint(dst []byte, value uint64) []byte {
	for value >= 0x80 {
		dst = append(dst, byte(value&0x7f)|0x80)
		value >>= 7
	}
	return append(dst, byte(value))
}

func appendTag(dst []byte, number int32, wireType WireType) []byte {
	return appendVarint(dst, uint64(number)<<3|uint64(wireType))
}

func appendVarintField(dst []byte, number int32, value uint64) []byte {
	dst = appendTag(dst, number, WireVarint)
	return appendVarint(dst, value)
}

func appendBytesField(dst []byte, number int32, value []byte) []byte {
	dst = appendTag(dst, number, WireBytes)
	dst = appendVarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func appendStringField(dst []byte, number int32, value string) []byte {
	return appendBytesField(dst, number, []byte(value))
}
