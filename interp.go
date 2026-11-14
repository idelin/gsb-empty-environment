package netsec

import (
	"fmt"
	"math"
)

// Concrete is a concrete value used by the small IR interpreter. A value is
// either a byte sequence (IsBytes) or an unsigned integer with an order label.
type Concrete struct {
	IsBytes bool
	Data    []byte
	Num     uint64
	Label   Tag
}

// RawData constructs a raw byte-sequence value.
func RawData(data []byte) Concrete {
	return Concrete{IsBytes: true, Data: data, Label: TagRaw}
}

// Scalar constructs an integer value with the given order label.
func Scalar(n uint64, label Tag) Concrete {
	return Concrete{Num: n, Label: label}
}

// Integer returns the scalar payload, or an error for a byte sequence.
func (c Concrete) Integer() (uint64, error) {
	if c.IsBytes {
		return 0, fmt.Errorf("value is a raw byte sequence")
	}
	return c.Num, nil
}

// ByteSlice returns the byte payload, or an error for a scalar.
func (c Concrete) ByteSlice() ([]byte, error) {
	if !c.IsBytes {
		return nil, fmt.Errorf("value is a scalar")
	}
	return c.Data, nil
}

func (c Concrete) nonNegativeInt() (int64, error) {
	n, err := c.Integer()
	if err != nil {
		return 0, err
	}
	if n > math.MaxInt64 {
		return 0, fmt.Errorf("integer %d is too large for a host index", n)
	}
	return int64(n), nil
}

// Execution is the concrete result of a linear IR execution.
type Execution struct {
	Program *Program
	Values  map[int]Concrete
}

// Get looks up a register value.
func (e *Execution) Get(id int) (Concrete, error) {
	value, ok := e.Values[id]
	if !ok {
		return Concrete{}, fmt.Errorf("register v%d was not defined", id)
	}
	return value, nil
}

// Interpret executes a linear IR program. OpPhi and OpHost are rejected because
// they require control-flow or host-call choices; the byte-order samples used
// by the analyzer are straight-line programs.
func Interpret(p *Program, params []Concrete) (*Execution, error) {
	if p == nil {
		return nil, fmt.Errorf("nil program")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if len(params) != len(p.Params) {
		return nil, fmt.Errorf("program has %d parameters, got %d values", len(p.Params), len(params))
	}

	values := make(map[int]Concrete, len(p.Params)+len(p.Instrs))
	for i, param := range params {
		values[i] = param
	}

	for i, in := range p.Instrs {
		value, err := interpretInstr(values, in)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.positionFor(in, i), err)
		}
		values[in.Dst] = value
	}
	return &Execution{Program: p, Values: values}, nil
}

func interpretInstr(values map[int]Concrete, in Instr) (Concrete, error) {
	get := func(id int) (Concrete, error) {
		value, ok := values[id]
		if !ok {
			return Concrete{}, fmt.Errorf("operand v%d is undefined", id)
		}
		return value, nil
	}

	switch in.Op {
	case OpConst:
		return Scalar(uint64(in.Imm), TagConst), nil

	case OpReadByte:
		base, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		offset, err := get(in.Args[1])
		if err != nil {
			return Concrete{}, err
		}
		data, err := base.ByteSlice()
		if err != nil {
			return Concrete{}, fmt.Errorf("read_byte base: %w", err)
		}
		off, err := offset.nonNegativeInt()
		if err != nil {
			return Concrete{}, fmt.Errorf("read_byte offset: %w", err)
		}
		if off >= int64(len(data)) {
			return Concrete{}, fmt.Errorf("read_byte offset %d out of range for %d bytes", off, len(data))
		}
		return RawData([]byte{data[off]}), nil

	case OpReadBE, OpReadLE:
		base, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		offset, err := get(in.Args[1])
		if err != nil {
			return Concrete{}, err
		}
		data, err := base.ByteSlice()
		if err != nil {
			return Concrete{}, fmt.Errorf("%s base: %w", in.Op, err)
		}
		off, err := offset.nonNegativeInt()
		if err != nil {
			return Concrete{}, fmt.Errorf("%s offset: %w", in.Op, err)
		}
		if off > int64(len(data))-int64(in.Width) || off < 0 {
			return Concrete{}, fmt.Errorf("%s range [%d,%d) out of bounds for %d bytes",
				in.Op, off, off+int64(in.Width), len(data))
		}
		raw := data[off : off+int64(in.Width)]
		if in.Op == OpReadBE {
			return Scalar(decodeBytes(raw, OrderBig), TagBE), nil
		}
		return Scalar(decodeBytes(raw, OrderLittle), TagLE), nil

	case OpPack:
		return interpretPack(values, in)

	case OpSwap:
		input, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		n, err := input.Integer()
		if err != nil {
			return Concrete{}, fmt.Errorf("swap: %w", err)
		}
		return Scalar(swapWidth(n, in.Width), SwapTag(input.Label)), nil

	case OpConvert:
		input, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		target := in.Order.Tag()
		// A raw byte sequence is decoded with the target order. A scalar is
		// relabeled without mutating its host integer value; physical byte
		// reversal is OpSwap's job.
		if input.IsBytes {
			if len(input.Data) == 0 {
				return Concrete{}, fmt.Errorf("convert cannot decode an empty byte sequence")
			}
			return Scalar(decodeBytes(input.Data, in.Order), target), nil
		}
		return Scalar(input.Num, target), nil

	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpShl, OpShr:
		left, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		right, err := get(in.Args[1])
		if err != nil {
			return Concrete{}, err
		}
		a, err := left.Integer()
		if err != nil {
			return Concrete{}, fmt.Errorf("%s left: %w", in.Op, err)
		}
		b, err := right.Integer()
		if err != nil {
			return Concrete{}, fmt.Errorf("%s right: %w", in.Op, err)
		}
		var n uint64
		tag := binaryTag(left.Label, right.Label)
		switch in.Op {
		case OpAdd:
			n = a + b
		case OpSub:
			n = a - b
		case OpMul:
			n = a * b
		case OpAnd:
			n = a & b
		case OpOr:
			n = a | b
		case OpXor:
			n = a ^ b
		case OpShl:
			n = a << (b & 63)
			tag = left.Label
		case OpShr:
			n = a >> (b & 63)
			tag = left.Label
		default:
			return Concrete{}, fmt.Errorf("unsupported arithmetic op %s", in.Op)
		}
		return Scalar(n, tag), nil

	case OpSlice:
		base, err := get(in.Args[0])
		if err != nil {
			return Concrete{}, err
		}
		offset, err := get(in.Args[1])
		if err != nil {
			return Concrete{}, err
		}
		length, err := get(in.Args[2])
		if err != nil {
			return Concrete{}, err
		}
		data, err := base.ByteSlice()
		if err != nil {
			return Concrete{}, fmt.Errorf("slice base: %w", err)
		}
		off, err := offset.nonNegativeInt()
		if err != nil {
			return Concrete{}, fmt.Errorf("slice offset: %w", err)
		}
		size, err := length.nonNegativeInt()
		if err != nil {
			return Concrete{}, fmt.Errorf("slice length: %w", err)
		}
		end, ok := safeEnd(off, size)
		if !ok || end > int64(len(data)) {
			return Concrete{}, fmt.Errorf("slice range [%d,%d) out of bounds for %d bytes", off, end, len(data))
		}
		return RawData(data[off:end]), nil

	case OpPhi:
		return Concrete{}, fmt.Errorf("phi needs a control-flow predecessor choice; it is not executable as a straight-line instruction")

	case OpHost:
		return Concrete{}, fmt.Errorf("host call %q has no concrete implementation", in.Callee)

	default:
		return Concrete{}, fmt.Errorf("unsupported opcode %s", in.Op)
	}
}

func interpretPack(values map[int]Concrete, in Instr) (Concrete, error) {
	var n uint64
	lanes := len(in.Args)
	switch in.Order {
	case OrderBig:
		for _, id := range in.Args {
			value, ok := values[id]
			if !ok {
				return Concrete{}, fmt.Errorf("operand v%d is undefined", id)
			}
			byteValue, err := laneByte(value)
			if err != nil {
				return Concrete{}, err
			}
			n = n<<8 | uint64(byteValue)
		}
	case OrderLittle:
		for i, id := range in.Args {
			value, ok := values[id]
			if !ok {
				return Concrete{}, fmt.Errorf("operand v%d is undefined", id)
			}
			byteValue, err := laneByte(value)
			if err != nil {
				return Concrete{}, err
			}
			n |= uint64(byteValue) << (8 * i)
		}
	default:
		return Concrete{}, fmt.Errorf("pack has no byte order")
	}
	if lanes > 8 {
		return Concrete{}, fmt.Errorf("pack has %d lanes, at most 8 are supported", lanes)
	}
	return Scalar(n, in.Order.Tag()), nil
}

func laneByte(value Concrete) (byte, error) {
	if value.IsBytes {
		if len(value.Data) != 1 {
			return 0, fmt.Errorf("pack lane is %d bytes, want one", len(value.Data))
		}
		return value.Data[0], nil
	}
	if value.Num > 0xff {
		return 0, fmt.Errorf("pack lane %d does not fit in a byte", value.Num)
	}
	return byte(value.Num), nil
}

func decodeBytes(raw []byte, order Order) uint64 {
	var n uint64
	switch order {
	case OrderBig:
		for _, b := range raw {
			n = n<<8 | uint64(b)
		}
	case OrderLittle:
		for i := len(raw) - 1; i >= 0; i-- {
			n = n<<8 | uint64(raw[i])
		}
	}
	return n
}
