package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 安全约束常量：限制嵌套深度、栈大小与 memo 大小，防止资源耗尽。
const (
	maxDepth     = 32
	maxStackSize = 10000
	maxMemoSize  = 10000
)

// Tuple 表示 pickle 中的元组值（不可变，元素类型为 interface{}）。
type Tuple []interface{}

// 解码错误。
var (
	ErrUnsupportedOpcode   = errors.New("netsec: unsupported pickle opcode")
	ErrUnsupportedProtocol = errors.New("netsec: unsupported pickle protocol version")
	ErrMalformed           = errors.New("netsec: malformed pickle stream")
	ErrDepthLimit          = errors.New("netsec: nesting depth exceeds 32")
	ErrStackLimit          = errors.New("netsec: stack exceeds 10000 entries")
	ErrMemoLimit           = errors.New("netsec: memo exceeds 10000 entries")
	ErrUndefinedMemo       = errors.New("netsec: memo reference is not defined")
	ErrTrailingData        = errors.New("netsec: trailing bytes after STOP")
)

// plist 是解码期间使用的可变列表；通过指针共享以支持 memo 别名。
type plist []interface{}

type markType struct{}

var mark = markType{}

type decoder struct {
	data  []byte
	pos   int
	stack []interface{}
	memo  map[int]interface{}
}

// Unmarshal 安全地解码 pickle 字节流（仅协议 2 至 5 的纯数据操作码）。
// 返回值类型为：nil（None）、int64、string、[]interface{}（列表）、
// map[interface{}]interface{}（字典）或 Tuple（元组）。
func Unmarshal(data []byte) (interface{}, error) {
	d := &decoder{data: data, memo: make(map[int]interface{})}
	v, err := d.run()
	if err != nil {
		return nil, err
	}
	return materialize(v, 0)
}

func (d *decoder) readByte() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, fmt.Errorf("%w: unexpected end of stream", ErrMalformed)
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

func (d *decoder) readN(n uint64) ([]byte, error) {
	if n > uint64(len(d.data)-d.pos) {
		return nil, fmt.Errorf("%w: unexpected end of stream", ErrMalformed)
	}
	b := d.data[d.pos : d.pos+int(n)]
	d.pos += int(n)
	return b, nil
}

func (d *decoder) push(v interface{}) error {
	if len(d.stack) >= maxStackSize {
		return ErrStackLimit
	}
	d.stack = append(d.stack, v)
	return nil
}

func (d *decoder) pop() (interface{}, error) {
	if len(d.stack) == 0 {
		return nil, fmt.Errorf("%w: stack underflow", ErrMalformed)
	}
	v := d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
	return v, nil
}

func (d *decoder) peek() (interface{}, error) {
	if len(d.stack) == 0 {
		return nil, fmt.Errorf("%w: stack underflow", ErrMalformed)
	}
	return d.stack[len(d.stack)-1], nil
}

// popMark 弹出栈顶直到最近的 MARK，返回 MARK 之上的元素（保持原顺序）。
func (d *decoder) popMark() ([]interface{}, error) {
	for i := len(d.stack) - 1; i >= 0; i-- {
		if d.stack[i] == mark {
			items := make([]interface{}, len(d.stack)-i-1)
			copy(items, d.stack[i+1:])
			d.stack = d.stack[:i]
			return items, nil
		}
	}
	return nil, fmt.Errorf("%w: MARK not found", ErrMalformed)
}

func (d *decoder) memoPut(idx int) error {
	top, err := d.peek()
	if err != nil {
		return err
	}
	if _, ok := d.memo[idx]; !ok && len(d.memo) >= maxMemoSize {
		return ErrMemoLimit
	}
	d.memo[idx] = top
	return nil
}

func checkDictKey(k interface{}) error {
	switch k.(type) {
	case nil, string, int64:
		return nil
	default:
		return fmt.Errorf("%w: unhashable dict key type %T", ErrMalformed, k)
	}
}

func (d *decoder) run() (interface{}, error) {
	for {
		op, err := d.readByte()
		if err != nil {
			return nil, err
		}
		switch op {
		case 0x80: // PROTO
			v, err := d.readByte()
			if err != nil {
				return nil, err
			}
			if v < 2 || v > 5 {
				return nil, fmt.Errorf("%w: %d", ErrUnsupportedProtocol, v)
			}
		case 0x95: // FRAME
			b, err := d.readN(8)
			if err != nil {
				return nil, err
			}
			if n := binary.LittleEndian.Uint64(b); n > uint64(len(d.data)-d.pos) {
				return nil, fmt.Errorf("%w: frame length %d exceeds remaining data", ErrMalformed, n)
			}
		case '(': // MARK
			if err := d.push(mark); err != nil {
				return nil, err
			}
		case '.': // STOP
			if len(d.stack) == 0 {
				return nil, fmt.Errorf("%w: empty stack at STOP", ErrMalformed)
			}
			if d.pos != len(d.data) {
				return nil, ErrTrailingData
			}
			return d.stack[len(d.stack)-1], nil
		case 'N': // NONE
			if err := d.push(nil); err != nil {
				return nil, err
			}
		case 'K': // BININT1
			b, err := d.readByte()
			if err != nil {
				return nil, err
			}
			if err := d.push(int64(b)); err != nil {
				return nil, err
			}
		case 'M': // BININT2
			b, err := d.readN(2)
			if err != nil {
				return nil, err
			}
			if err := d.push(int64(binary.LittleEndian.Uint16(b))); err != nil {
				return nil, err
			}
		case 'J': // BININT
			b, err := d.readN(4)
			if err != nil {
				return nil, err
			}
			if err := d.push(int64(int32(binary.LittleEndian.Uint32(b)))); err != nil {
				return nil, err
			}
		case 'X': // BINUNICODE
			n, err := d.readN(4)
			if err != nil {
				return nil, err
			}
			b, err := d.readN(uint64(binary.LittleEndian.Uint32(n)))
			if err != nil {
				return nil, err
			}
			if err := d.push(string(b)); err != nil {
				return nil, err
			}
		case 0x8c: // SHORT_BINUNICODE
			n, err := d.readByte()
			if err != nil {
				return nil, err
			}
			b, err := d.readN(uint64(n))
			if err != nil {
				return nil, err
			}
			if err := d.push(string(b)); err != nil {
				return nil, err
			}
		case ']': // EMPTY_LIST
			if err := d.push(&plist{}); err != nil {
				return nil, err
			}
		case '}': // EMPTY_DICT
			if err := d.push(map[interface{}]interface{}{}); err != nil {
				return nil, err
			}
		case ')': // EMPTY_TUPLE
			if err := d.push(Tuple{}); err != nil {
				return nil, err
			}
		case 'a': // APPEND
			v, err := d.pop()
			if err != nil {
				return nil, err
			}
			top, err := d.peek()
			if err != nil {
				return nil, err
			}
			lst, ok := top.(*plist)
			if !ok {
				return nil, fmt.Errorf("%w: APPEND target is not a list", ErrMalformed)
			}
			*lst = append(*lst, v)
		case 'e': // APPENDS
			items, err := d.popMark()
			if err != nil {
				return nil, err
			}
			top, err := d.peek()
			if err != nil {
				return nil, err
			}
			lst, ok := top.(*plist)
			if !ok {
				return nil, fmt.Errorf("%w: APPENDS target is not a list", ErrMalformed)
			}
			*lst = append(*lst, items...)
		case 's': // SETITEM
			v, err := d.pop()
			if err != nil {
				return nil, err
			}
			k, err := d.pop()
			if err != nil {
				return nil, err
			}
			top, err := d.peek()
			if err != nil {
				return nil, err
			}
			dict, ok := top.(map[interface{}]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: SETITEM target is not a dict", ErrMalformed)
			}
			if err := checkDictKey(k); err != nil {
				return nil, err
			}
			dict[k] = v
		case 'u': // SETITEMS
			items, err := d.popMark()
			if err != nil {
				return nil, err
			}
			if len(items)%2 != 0 {
				return nil, fmt.Errorf("%w: SETITEMS requires key/value pairs", ErrMalformed)
			}
			top, err := d.peek()
			if err != nil {
				return nil, err
			}
			dict, ok := top.(map[interface{}]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: SETITEMS target is not a dict", ErrMalformed)
			}
			for i := 0; i < len(items); i += 2 {
				if err := checkDictKey(items[i]); err != nil {
					return nil, err
				}
				dict[items[i]] = items[i+1]
			}
		case 't': // TUPLE
			items, err := d.popMark()
			if err != nil {
				return nil, err
			}
			if err := d.push(Tuple(items)); err != nil {
				return nil, err
			}
		case 'q': // BINPUT
			b, err := d.readByte()
			if err != nil {
				return nil, err
			}
			if err := d.memoPut(int(b)); err != nil {
				return nil, err
			}
		case 'h': // BINGET
			b, err := d.readByte()
			if err != nil {
				return nil, err
			}
			v, ok := d.memo[int(b)]
			if !ok {
				return nil, fmt.Errorf("%w: index %d", ErrUndefinedMemo, b)
			}
			if err := d.push(v); err != nil {
				return nil, err
			}
		case 0x94: // MEMOIZE
			if err := d.memoPut(len(d.memo)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("%w: 0x%02x", ErrUnsupportedOpcode, op)
		}
	}
}

// materialize 把内部可变表示转换为不可变的导出类型，同时强制深度上限。
// 循环引用会因深度超限而被拒绝，不会导致无限递归。
func materialize(v interface{}, depth int) (interface{}, error) {
	switch t := v.(type) {
	case *plist:
		if depth >= maxDepth {
			return nil, ErrDepthLimit
		}
		out := make([]interface{}, len(*t))
		for i, e := range *t {
			m, err := materialize(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = m
		}
		return out, nil
	case Tuple:
		if depth >= maxDepth {
			return nil, ErrDepthLimit
		}
		out := make(Tuple, len(t))
		for i, e := range t {
			m, err := materialize(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = m
		}
		return out, nil
	case map[interface{}]interface{}:
		if depth >= maxDepth {
			return nil, ErrDepthLimit
		}
		out := make(map[interface{}]interface{}, len(t))
		for k, e := range t {
			m, err := materialize(e, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = m
		}
		return out, nil
	default:
		return v, nil
	}
}
