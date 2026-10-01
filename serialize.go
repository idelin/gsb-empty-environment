package moc

// 受限二进制序列化:仅支持封闭类型集合
//   nil、bool、int64(含各宽度有符号整型)、float64/float32、string、
//   []byte、[]interface{}、map[string]interface{}
// 解码只依据 1 字节类型标签构造上述 Go 值:不解析类型名、不调用
// 反射构造、不执行任何宿主代码。所有变长字段都是 uint32 长度前缀,
// 解码时先验证长度不超过剩余输入,再切片或分配,杜绝长度炸弹。

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

const (
	tagNil byte = iota
	tagFalse
	tagTrue
	tagInt
	tagFloat
	tagString
	tagBytes
	tagArray
	tagMap
)

// maxDepth 限制嵌套深度,防止畸形输入导致栈耗尽。
const maxDepth = 64

// Marshal 将受限类型集合编码为字节流。map 按键排序,输出确定。
func Marshal(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, v, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encodeValue(buf *bytes.Buffer, v interface{}, depth int) error {
	if depth > maxDepth {
		return errors.New("moc: 嵌套深度超限")
	}
	switch t := v.(type) {
	case nil:
		buf.WriteByte(tagNil)
	case bool:
		if t {
			buf.WriteByte(tagTrue)
		} else {
			buf.WriteByte(tagFalse)
		}
	case int:
		encodeInt(buf, int64(t))
	case int8:
		encodeInt(buf, int64(t))
	case int16:
		encodeInt(buf, int64(t))
	case int32:
		encodeInt(buf, int64(t))
	case int64:
		encodeInt(buf, t)
	case float32:
		encodeFloat(buf, float64(t))
	case float64:
		encodeFloat(buf, t)
	case string:
		buf.WriteByte(tagString)
		writeUint32(buf, uint32(len(t)))
		buf.WriteString(t)
	case []byte:
		buf.WriteByte(tagBytes)
		writeUint32(buf, uint32(len(t)))
		buf.Write(t)
	case []interface{}:
		buf.WriteByte(tagArray)
		writeUint32(buf, uint32(len(t)))
		for _, e := range t {
			if err := encodeValue(buf, e, depth+1); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		buf.WriteByte(tagMap)
		writeUint32(buf, uint32(len(t)))
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := encodeValue(buf, k, depth+1); err != nil {
				return err
			}
			if err := encodeValue(buf, t[k], depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("moc: 不支持的类型 %T", v)
	}
	return nil
}

func encodeInt(buf *bytes.Buffer, n int64) {
	buf.WriteByte(tagInt)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(n))
	buf.Write(b[:])
}

func encodeFloat(buf *bytes.Buffer, f float64) {
	buf.WriteByte(tagFloat)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(f))
	buf.Write(b[:])
}

func writeUint32(buf *bytes.Buffer, n uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	buf.Write(b[:])
}

// Unmarshal 解码受限类型集合。输入必须恰好消耗完毕。
func Unmarshal(data []byte) (interface{}, error) {
	d := &decoder{data: data}
	v, err := d.decodeValue(0)
	if err != nil {
		return nil, err
	}
	if d.pos != len(d.data) {
		return nil, errors.New("moc: 输入尾部存在多余字节")
	}
	return v, nil
}

type decoder struct {
	data []byte
	pos  int
}

var errTruncated = errors.New("moc: 输入截断或声明长度越界")

// take 先验证 n 不超过剩余输入,再前进游标。n 来自不可信输入,
// 通过校验后必然 <= len(data),转换为 int 不会溢出。
func (d *decoder) take(n uint32) ([]byte, error) {
	if uint64(n) > uint64(len(d.data)-d.pos) {
		return nil, errTruncated
	}
	b := d.data[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

func (d *decoder) decodeValue(depth int) (interface{}, error) {
	if depth > maxDepth {
		return nil, errors.New("moc: 嵌套深度超限")
	}
	tag, err := d.take(1)
	if err != nil {
		return nil, err
	}
	switch tag[0] {
	case tagNil:
		return nil, nil
	case tagFalse:
		return false, nil
	case tagTrue:
		return true, nil
	case tagInt:
		b, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return int64(binary.BigEndian.Uint64(b)), nil
	case tagFloat:
		b, err := d.take(8)
		if err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.BigEndian.Uint64(b)), nil
	case tagString:
		b, err := d.takeLenPrefixed()
		if err != nil {
			return nil, err
		}
		return string(b), nil
	case tagBytes:
		b, err := d.takeLenPrefixed()
		if err != nil {
			return nil, err
		}
		out := make([]byte, len(b))
		copy(out, b)
		return out, nil
	case tagArray:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		// 每个元素至少占 1 字节(类型标签),故 n 不可能超过剩余字节数。
		// 先验证上界再分配,拒绝长度炸弹。
		if uint64(n) > uint64(len(d.data)-d.pos) {
			return nil, errTruncated
		}
		arr := make([]interface{}, 0, int(n))
		for i := uint32(0); i < n; i++ {
			e, err := d.decodeValue(depth + 1)
			if err != nil {
				return nil, err
			}
			arr = append(arr, e)
		}
		return arr, nil
	case tagMap:
		n, err := d.readUint32()
		if err != nil {
			return nil, err
		}
		// 每个条目至少占 2 字节(key 标签 + value 标签)。
		if uint64(n) > uint64(len(d.data)-d.pos)/2 {
			return nil, errTruncated
		}
		m := make(map[string]interface{}, int(n))
		for i := uint32(0); i < n; i++ {
			k, err := d.decodeValue(depth + 1)
			if err != nil {
				return nil, err
			}
			ks, ok := k.(string)
			if !ok {
				return nil, errors.New("moc: map 键必须是 string")
			}
			v, err := d.decodeValue(depth + 1)
			if err != nil {
				return nil, err
			}
			m[ks] = v
		}
		return m, nil
	default:
		return nil, fmt.Errorf("moc: 未知类型标签 0x%02x", tag[0])
	}
}

func (d *decoder) readUint32() (uint32, error) {
	b, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b), nil
}

func (d *decoder) takeLenPrefixed() ([]byte, error) {
	n, err := d.readUint32()
	if err != nil {
		return nil, err
	}
	return d.take(n)
}
