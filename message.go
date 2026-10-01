package wire

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// UnknownField 是解码时不认识、但原样保留以便转存（透传）的字段。
// Raw 含完整 TLV（tag + 长度前缀 + 负载），重新编码时逐字节写回。
type UnknownField struct {
	Num int
	Raw []byte
}

// Diagnostics 记录一次解码的版本与兼容性信息。
type Diagnostics struct {
	DataVersion      uint32   // 数据写入方的 schema 版本
	SchemaVersion    uint32   // 本机 schema 版本
	NewerData        bool     // 数据由更新版本写入
	UnknownFieldNums []int    // 本机不认识、已透传保留的字段编号
	Notes            []string // 人类可读的诊断说明
}

func (d Diagnostics) String() string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "data-version=%d schema-version=%d", d.DataVersion, d.SchemaVersion)
	if d.NewerData {
		b.WriteString(" (data is NEWER than local schema)")
	}
	if len(d.UnknownFieldNums) > 0 {
		fmt.Fprintf(&b, " unknown-fields-preserved=%v", d.UnknownFieldNums)
	}
	for _, n := range d.Notes {
		fmt.Fprintf(&b, "\n  note: %s", n)
	}
	return b.String()
}

// Message 是按某个 Schema 解码/构建的一条消息。
type Message struct {
	schema  *Schema
	values  map[int]any
	unknown []UnknownField
	Diag    Diagnostics
}

func NewMessage(s *Schema) *Message {
	return &Message{schema: s, values: make(map[int]any)}
}

// Set 设置字段值，Go 类型必须与 schema 声明一致。
func (m *Message) Set(num int, v any) error {
	def, ok := m.schema.fields[num]
	if !ok {
		return fmt.Errorf("field %d is not defined in schema %q", num, m.schema.Name)
	}
	switch def.Type {
	case TUint64:
		if _, ok := v.(uint64); !ok {
			return fmt.Errorf("field %d: want uint64, got %T", num, v)
		}
	case TInt64:
		if _, ok := v.(int64); !ok {
			return fmt.Errorf("field %d: want int64, got %T", num, v)
		}
	case TBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("field %d: want bool, got %T", num, v)
		}
	case TString:
		if _, ok := v.(string); !ok {
			return fmt.Errorf("field %d: want string, got %T", num, v)
		}
	case TBytes:
		if _, ok := v.([]byte); !ok {
			return fmt.Errorf("field %d: want []byte, got %T", num, v)
		}
	}
	m.values[num] = v
	return nil
}

// Get 返回字段值；未知（透传）字段不在其中。
func (m *Message) Get(num int) (any, bool) {
	v, ok := m.values[num]
	return v, ok
}

func (m *Message) GetUint64(num int) uint64 { v, _ := m.values[num].(uint64); return v }
func (m *Message) GetInt64(num int) int64   { v, _ := m.values[num].(int64); return v }
func (m *Message) GetBool(num int) bool     { v, _ := m.values[num].(bool); return v }
func (m *Message) GetString(num int) string { v, _ := m.values[num].(string); return v }

// UnknownFields 返回被透传保留的未知字段（只读视图）。
func (m *Message) UnknownFields() []UnknownField { return m.unknown }

// Encode 序列化消息：帧头(含版本) + 已知字段(按编号升序) + 未知字段原样写回。
// 旧程序转存时，新版本写入的未知字段不会丢失。
func (m *Message) Encode() ([]byte, error) {
	buf := append([]byte(nil), magic...)
	buf = binary.AppendUvarint(buf, uint64(m.schema.Version))

	nums := make([]int, 0, len(m.values))
	for num := range m.values {
		nums = append(nums, num)
	}
	sort.Ints(nums)
	for _, num := range nums {
		def := m.schema.fields[num]
		buf = appendTag(buf, num, def.Type.wireType())
		var err error
		buf, err = appendValue(buf, def, m.values[num])
		if err != nil {
			return nil, err
		}
	}
	for _, uf := range m.unknown {
		if _, nowKnown := m.schema.fields[uf.Num]; nowKnown {
			continue // 防御：编号已被 schema 认识时不重复写原始字节
		}
		buf = append(buf, uf.Raw...)
	}
	return buf, nil
}

func appendValue(buf []byte, def FieldDef, v any) ([]byte, error) {
	switch def.Type {
	case TUint64:
		return binary.AppendUvarint(buf, v.(uint64)), nil
	case TInt64:
		return binary.AppendUvarint(buf, zigzagEncode(v.(int64))), nil
	case TBool:
		if v.(bool) {
			return append(buf, 1), nil
		}
		return append(buf, 0), nil
	case TString:
		s := v.(string)
		buf = binary.AppendUvarint(buf, uint64(len(s)))
		return append(buf, s...), nil
	case TBytes:
		b := v.([]byte)
		buf = binary.AppendUvarint(buf, uint64(len(b)))
		return append(buf, b...), nil
	}
	return nil, fmt.Errorf("field %d: unsupported type %d", def.Num, int(def.Type))
}

// Decode 按 schema 解码 data。流程：验帧头与版本 -> 逐字段 TLV 解析 ->
// 必填校验。任何长度前缀都先与剩余输入和 MaxFieldLen 比较，再分配。
func Decode(s *Schema, data []byte) (*Message, error) {
	if len(data) < len(magic)+1 {
		return nil, errors.New("input too short for frame header")
	}
	if !bytes.Equal(data[:len(magic)], magic) {
		return nil, errors.New("bad magic: not a stablewire frame")
	}
	rest := data[len(magic):]
	ver, n := binary.Uvarint(rest)
	if n <= 0 {
		return nil, errors.New("corrupt version field in frame header")
	}
	rest = rest[n:]

	m := NewMessage(s)
	m.Diag.DataVersion = uint32(ver)
	m.Diag.SchemaVersion = s.Version
	if ver > uint64(s.Version) {
		m.Diag.NewerData = true
		m.Diag.Notes = append(m.Diag.Notes,
			fmt.Sprintf("data written by newer schema v%d; local schema is v%d; unknown fields will be preserved on re-save",
				ver, s.Version))
	}

	seenWire := make(map[int]int) // 字段号 -> 已见线类型，用于冲突检测
	for len(rest) > 0 {
		start := rest
		tag, n := binary.Uvarint(rest)
		if n <= 0 {
			return nil, fmt.Errorf("corrupt tag at offset %d", len(data)-len(rest))
		}
		rest = rest[n:]
		num := int(tag >> 3)
		wt := int(tag & 7)
		if num == 0 {
			return nil, errors.New("field number 0 is invalid")
		}
		if prev, ok := seenWire[num]; ok && prev != wt {
			return nil, fmt.Errorf("field %d: conflicting wire types %d and %d in stream; rejected", num, prev, wt)
		}
		seenWire[num] = wt

		def, known := s.fields[num]
		if known && def.Type.wireType() != wt {
			return nil, fmt.Errorf("field %d: wire type %d does not match schema type %s", num, wt, def.Type)
		}

		switch wt {
		case wtVarint:
			x, n := binary.Uvarint(rest)
			if n <= 0 {
				return nil, fmt.Errorf("field %d: truncated varint", num)
			}
			rest = rest[n:]
			if known {
				m.values[num] = varintValue(def, x)
			} else {
				m.keepUnknown(num, start, rest)
			}
		case wtBytes:
			l, n := binary.Uvarint(rest)
			if n <= 0 {
				return nil, fmt.Errorf("field %d: truncated length prefix", num)
			}
			rest = rest[n:]
			// 先验证长度，再分配：上限检查 + 剩余输入检查。
			if l > MaxFieldLen {
				return nil, fmt.Errorf("field %d: declared length %d exceeds limit %d", num, l, MaxFieldLen)
			}
			if l > uint64(len(rest)) {
				return nil, fmt.Errorf("field %d: declared length %d exceeds remaining input %d", num, l, len(rest))
			}
			payload := rest[:l]
			rest = rest[l:]
			if known {
				if def.Type == TString {
					m.values[num] = string(payload)
				} else {
					m.values[num] = append([]byte(nil), payload...)
				}
			} else {
				m.keepUnknown(num, start, rest)
			}
		default:
			return nil, fmt.Errorf("field %d: unsupported wire type %d", num, wt)
		}
	}

	var missing []int
	for num, def := range s.fields {
		if def.Required {
			if _, ok := m.values[num]; !ok {
				missing = append(missing, num)
			}
		}
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		return nil, &MissingRequiredError{Nums: missing}
	}
	return m, nil
}

func varintValue(def FieldDef, x uint64) any {
	switch def.Type {
	case TInt64:
		return zigzagDecode(x)
	case TBool:
		return x != 0
	default:
		return x
	}
}

func (m *Message) keepUnknown(num int, start, rest []byte) {
	raw := append([]byte(nil), start[:len(start)-len(rest)]...)
	m.unknown = append(m.unknown, UnknownField{Num: num, Raw: raw})
	m.Diag.UnknownFieldNums = append(m.Diag.UnknownFieldNums, num)
}
