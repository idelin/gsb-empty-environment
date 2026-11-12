package netsec

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// 字段类型标识。低信任写入方可以扩展类型，但读取方只接受这里定义的类型。
const (
	FieldBytes  uint8 = 1 // 任意不透明字节
	FieldString uint8 = 2 // UTF-8 文本
	FieldUint64 uint8 = 3 // 定长 8 字节无符号整数（小端）
)

// 单个字段的帧头长度：3 字节 ID + 1 字节类型 + 4 字节载荷长度。
const fieldHeaderSize = 8

// Field 是记录中的一个变长字段。
type Field struct {
	ID   uint32 // 取值范围 1..2^24-1
	Type uint8
	Data []byte
}

// Record 是从共享页读出的一条完整记录。
type Record struct {
	Fields []Field
}

// Encoded 将记录编码为紧凑帧：
//
//	uint32 little-endian 字段数量
//	对每个字段：uint24 ID | uint8 类型 | uint32 载荷长度 | 载荷
func (r Record) Encoded() ([]byte, error) {
	if len(r.Fields) > 0xffffffff {
		return nil, fmt.Errorf("netsec: too many fields: %d", len(r.Fields))
	}

	var size = 4
	for _, f := range r.Fields {
		if f.ID == 0 || f.ID > 0xffffff {
			return nil, fmt.Errorf("netsec: invalid field id %d", f.ID)
		}
		if len(f.Data) > 0xffffffff {
			return nil, fmt.Errorf("netsec: field %d payload too large", f.ID)
		}
		size += fieldHeaderSize + len(f.Data)
	}

	buf := make([]byte, size)
	le.PutUint32(buf[:4], uint32(len(r.Fields)))
	pos := 4
	for _, f := range r.Fields {
		buf[pos] = byte(f.ID)
		buf[pos+1] = byte(f.ID >> 8)
		buf[pos+2] = byte(f.ID >> 16)
		buf[pos+3] = f.Type
		le.PutUint32(buf[pos+4:pos+8], uint32(len(f.Data)))
		pos += fieldHeaderSize
		pos += copy(buf[pos:], f.Data)
	}
	return buf, nil
}

// ParseRecord 严格解析记录帧，任何截断、越界或尾随字节都返回 ErrMalformed。
func ParseRecord(payload []byte) (*Record, error) {
	// 零长度载荷表示一条零字段的空记录（页面初始状态）。
	if len(payload) == 0 {
		return &Record{}, nil
	}
	if len(payload) < 4 {
		return nil, fmt.Errorf("%w: record truncated before field count", ErrMalformed)
	}
	count := le.Uint32(payload[:4])

	rec := &Record{Fields: make([]Field, 0, count)}
	pos := 4
	for i := uint32(0); i < count; i++ {
		if len(payload)-pos < fieldHeaderSize {
			return nil, fmt.Errorf("%w: field %d header truncated", ErrMalformed, i)
		}
		id := uint32(payload[pos]) | uint32(payload[pos+1])<<8 | uint32(payload[pos+2])<<16
		typ := payload[pos+3]
		dataLen := le.Uint32(payload[pos+4 : pos+8])
		pos += fieldHeaderSize

		if id == 0 {
			return nil, fmt.Errorf("%w: field %d has zero id", ErrMalformed, i)
		}
		if !validFieldType(typ) {
			return nil, fmt.Errorf("%w: field %d has unknown type %d", ErrMalformed, i, typ)
		}
		if uint64(pos)+uint64(dataLen) > uint64(len(payload)) {
			return nil, fmt.Errorf("%w: field %d payload out of bounds", ErrMalformed, i)
		}
		if err := validateField(typ, payload[pos:pos+int(dataLen)]); err != nil {
			return nil, err
		}

		data := make([]byte, dataLen)
		copy(data, payload[pos:pos+int(dataLen)])
		rec.Fields = append(rec.Fields, Field{ID: id, Type: typ, Data: data})
		pos += int(dataLen)
	}

	if pos != len(payload) {
		return nil, fmt.Errorf("%w: %d trailing bytes after record", ErrMalformed, len(payload)-pos)
	}
	return rec, nil
}

func validFieldType(typ uint8) bool {
	switch typ {
	case FieldBytes, FieldString, FieldUint64:
		return true
	default:
		return false
	}
}

func validateField(typ uint8, data []byte) error {
	switch typ {
	case FieldBytes, FieldString:
		return nil
	case FieldUint64:
		if len(data) != 8 {
			return fmt.Errorf("%w: uint64 field has %d bytes", ErrMalformed, len(data))
		}
		return nil
	default:
		return fmt.Errorf("%w: unknown field type %d", ErrMalformed, typ)
	}
}

// digestPayload 计算记录载荷的摘要。
func digestPayload(payload []byte) [digestSize]byte {
	return sha256.Sum256(payload)
}

// ErrMalformed 表示共享页中的长度/字段结构本身不合法（稳定复现的确定错误）。
var ErrMalformed = errors.New("netsec: malformed shared page record")
