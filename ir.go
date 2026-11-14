package netsec

import (
	"fmt"
	"strings"
)

// Op is an IR operation.
type Op uint8

const (
	OpInvalid Op = iota

	// OpParam is used only in provenance traces for the implicit argument
	// registers 0..len(Program.Params)-1.
	OpParam
	OpConst

	// Byte reads. OpReadByte produces a raw one-byte value. OpReadBE and
	// OpReadLE decode Width bytes as an integer in the requested order.
	OpReadByte
	OpReadBE
	OpReadLE

	// OpPack assembles integer lanes using Instr.Order. The first argument is
	// the most significant lane for OrderBig and the least significant for
	// OrderLittle.
	OpPack
	// OpSwap reverses Width bytes.
	OpSwap
	// OpConvert is an explicit order conversion/normalization. At the type
	// level it relabels a scalar; OpSwap performs the physical byte reversal.
	OpConvert

	OpAdd
	OpSub
	OpMul
	OpShl
	OpShr
	OpAnd
	OpOr
	OpXor

	// OpSlice computes base[offset:offset+length]. It is the range operation
	// checked by the analyzer.
	OpSlice
	// OpPhi merges values reaching a control-flow join.
	OpPhi
	// OpHost models a call with a declared result effect.
	OpHost
)

func (o Op) String() string {
	switch o {
	case OpParam:
		return "param"
	case OpConst:
		return "const"
	case OpReadByte:
		return "read_byte"
	case OpReadBE:
		return "read_be"
	case OpReadLE:
		return "read_le"
	case OpPack:
		return "pack"
	case OpSwap:
		return "swap"
	case OpConvert:
		return "convert"
	case OpAdd:
		return "add"
	case OpSub:
		return "sub"
	case OpMul:
		return "mul"
	case OpShl:
		return "shl"
	case OpShr:
		return "shr"
	case OpAnd:
		return "and"
	case OpOr:
		return "or"
	case OpXor:
		return "xor"
	case OpSlice:
		return "slice"
	case OpPhi:
		return "phi"
	case OpHost:
		return "host"
	case OpInvalid:
		return "invalid"
	default:
		return fmt.Sprintf("op(%d)", uint8(o))
	}
}

// Param describes an implicit IR argument register.
type Param struct {
	Name     string
	Tag      Tag
	HasConst bool
	Const    int64
	HasBytes bool
	Bytes    int64
	Pos      Position
}

// Instr is one SSA instruction. Registers 0..len(Program.Params)-1 are the
// implicit parameters; every instruction defines a new register in Dst.
//
// The IR is deliberately small and linear. OpPhi represents a value reaching a
// join from multiple alternative paths; Validate requires its inputs to be
// defined textually earlier.
type Instr struct {
	Op        Op
	Dst       int
	Args      []int
	Imm       int64
	Width     int
	Order     Order
	Pos       Position
	Note      string
	Callee    string
	ResultTag Tag
}

// Program is a sequence of SSA instructions over implicit parameters.
type Program struct {
	Name   string
	Params []Param
	Instrs []Instr
}

// Expr renders the instruction as a provenance expression.
func (in Instr) Expr() string {
	dst := "?"
	if in.Dst >= 0 {
		dst = fmt.Sprintf("v%d", in.Dst)
	}
	args := make([]string, len(in.Args))
	for i, a := range in.Args {
		args[i] = fmt.Sprintf("v%d", a)
	}
	argList := strings.Join(args, ", ")

	var rhs string
	switch in.Op {
	case OpConst:
		rhs = fmt.Sprintf("const %d", in.Imm)
	case OpReadByte:
		rhs = fmt.Sprintf("read_byte(%s)", argList)
	case OpReadBE:
		rhs = fmt.Sprintf("read_be%d(%s)", in.Width, argList)
	case OpReadLE:
		rhs = fmt.Sprintf("read_le%d(%s)", in.Width, argList)
	case OpPack:
		rhs = fmt.Sprintf("pack_%s(%s)", in.Order, argList)
	case OpSwap:
		rhs = fmt.Sprintf("swap%d(%s)", in.Width, argList)
	case OpConvert:
		rhs = fmt.Sprintf("convert_%s(%s)", in.Order, argList)
	case OpSlice:
		rhs = fmt.Sprintf("slice(%s)", argList)
	case OpPhi:
		rhs = fmt.Sprintf("phi(%s)", argList)
	case OpHost:
		callee := in.Callee
		if callee == "" {
			callee = "<unknown>"
		}
		rhs = fmt.Sprintf("host[%s](%s)", callee, argList)
	default:
		rhs = fmt.Sprintf("%s(%s)", in.Op, argList)
	}
	if in.Note != "" {
		rhs += " ; " + in.Note
	}
	return dst + " = " + rhs
}

// Format is an alias for Expr.
func (in Instr) Format() string {
	return in.Expr()
}

func validWidth(width int) bool {
	return width == 2 || width == 4 || width == 8
}

func (p *Program) positionFor(in Instr, inst int) string {
	pos := in.Pos.String()
	if pos == "" {
		pos = p.Name
	}
	if pos == "" {
		return fmt.Sprintf("instruction %d", inst)
	}
	return fmt.Sprintf("instruction %d (%s)", inst, pos)
}

// Validate checks register definitions, operand availability, and opcode
// arity/width constraints.
func (p *Program) Validate() error {
	defined := make(map[int]bool, len(p.Params)+len(p.Instrs))
	for i := range p.Params {
		defined[i] = true
	}

	for i, in := range p.Instrs {
		fail := func(format string, args ...any) error {
			return fmt.Errorf("%s: %s", p.positionFor(in, i), fmt.Sprintf(format, args...))
		}
		if in.Dst < 0 {
			return fail("negative destination register v%d", in.Dst)
		}
		if in.Dst < len(p.Params) {
			return fail("destination v%d redefines an implicit parameter", in.Dst)
		}
		if defined[in.Dst] {
			return fail("destination v%d is already defined", in.Dst)
		}
		for _, arg := range in.Args {
			if arg < 0 || !defined[arg] {
				return fail("operand v%d is not defined before use", arg)
			}
		}

		switch in.Op {
		case OpConst:
			if len(in.Args) != 0 {
				return fail("const takes no operands")
			}
		case OpReadByte:
			if len(in.Args) != 2 {
				return fail("read_byte takes base and offset")
			}
			if in.Width != 0 && in.Width != 1 {
				return fail("read_byte width must be 0 or 1")
			}
		case OpReadBE, OpReadLE:
			if len(in.Args) != 2 {
				return fail("%s takes base and offset", in.Op)
			}
			if !validWidth(in.Width) {
				return fail("%s width must be 2, 4, or 8", in.Op)
			}
		case OpPack:
			if len(in.Args) == 0 {
				return fail("pack needs at least one lane")
			}
			if len(in.Args) > 8 {
				return fail("pack supports at most 8 lanes")
			}
			if in.Order != OrderBig && in.Order != OrderLittle {
				return fail("pack requires a big- or little-endian order")
			}
			if in.Width != 0 && in.Width != len(in.Args) {
				return fail("pack width %d does not match %d lanes", in.Width, len(in.Args))
			}
		case OpSwap:
			if len(in.Args) != 1 {
				return fail("swap takes one operand")
			}
			if !validWidth(in.Width) {
				return fail("swap width must be 2, 4, or 8")
			}
		case OpConvert:
			if len(in.Args) != 1 {
				return fail("convert takes one operand")
			}
			if in.Order != OrderBig && in.Order != OrderLittle {
				return fail("convert requires a big- or little-endian target")
			}
		case OpAdd, OpSub, OpMul, OpShl, OpShr, OpAnd, OpOr, OpXor:
			if len(in.Args) != 2 {
				return fail("%s takes two operands", in.Op)
			}
		case OpSlice:
			if len(in.Args) != 3 {
				return fail("slice takes base, offset, and length")
			}
		case OpPhi:
			if len(in.Args) == 0 {
				return fail("phi needs at least one input")
			}
		case OpHost:
			if in.Callee == "" {
				return fail("host call needs a callee")
			}
			if in.ResultTag != TagInvalid && in.ResultTag != TagRaw && in.ResultTag != TagBE &&
				in.ResultTag != TagLE && in.ResultTag != TagConst && in.ResultTag != TagUnknown &&
				in.ResultTag != TagConflict {
				return fail("host call has invalid result tag %s", in.ResultTag)
			}
		default:
			return fail("unknown opcode %s", in.Op)
		}
		defined[in.Dst] = true
	}
	return nil
}

func (p *Program) String() string {
	var b strings.Builder
	name := p.Name
	if name == "" {
		name = "program"
	}
	fmt.Fprintf(&b, "%s {\n", name)
	for i, param := range p.Params {
		fmt.Fprintf(&b, "  v%d = param %s [%s", i, param.Name, param.Tag)
		if param.HasConst {
			fmt.Fprintf(&b, " known=%d", param.Const)
		}
		if param.HasBytes {
			fmt.Fprintf(&b, " bytes=%d", param.Bytes)
		}
		fmt.Fprintf(&b, "]\n")
	}
	for _, in := range p.Instrs {
		fmt.Fprintf(&b, "  %s\n", in.Expr())
	}
	b.WriteString("}")
	return b.String()
}
