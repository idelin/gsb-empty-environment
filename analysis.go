package netsec

import (
	"fmt"
	"math"
	"strings"
)

// Reason codes reported for a dangerous range operation.
const (
	ReasonMixedOrder    = "mixed-byte-order"
	ReasonOrderConflict = "byte-order-conflict"
	ReasonOutOfBounds   = "out-of-bounds"
	ReasonNegativeRange = "negative-range"
	ReasonRangeOverflow = "range-overflow"
	ReasonRawOperand    = "raw-range-operand"
)

// Reason is one independent problem found at a range operation.
type Reason struct {
	Code    string
	Message string
}

// Source is one input to a dangerous range operation and its provenance.
type Source struct {
	Role  string
	Value Value
}

// Finding describes one dangerous range operation. A slice with both mixed
// byte order and an out-of-bounds range is still one finding, with multiple
// Reasons.
type Finding struct {
	Instr   int
	Pos     Position
	Reasons []Reason
	Sources []Source
}

// Codes returns the distinct reason codes in report order.
func (f Finding) Codes() []string {
	out := make([]string, 0, len(f.Reasons))
	seen := make(map[string]bool)
	for _, reason := range f.Reasons {
		if !seen[reason.Code] {
			seen[reason.Code] = true
			out = append(out, reason.Code)
		}
	}
	return out
}

func (f Finding) header() string {
	where := f.Pos.String()
	if where == "" {
		where = fmt.Sprintf("instruction %d", f.Instr)
	}
	codes := strings.Join(f.Codes(), ", ")
	if codes == "" {
		codes = "danger"
	}
	return fmt.Sprintf("dangerous range operation at %s: %s", where, codes)
}

// String renders the finding and the complete provenance tree of every input.
func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(f.header())
	for _, reason := range f.Reasons {
		fmt.Fprintf(&b, "\n  - %s: %s", reason.Code, reason.Message)
	}
	for _, src := range f.Sources {
		fmt.Fprintf(&b, "\n  %s [%s", src.Role, src.Value.Tag)
		if src.Value.HasConst {
			fmt.Fprintf(&b, ", known=%d", src.Value.Const)
		}
		if src.Value.HasBytes {
			fmt.Fprintf(&b, ", bytes=%d", src.Value.Bytes)
		}
		if src.Value.Overflow {
			b.WriteString(", overflow")
		}
		b.WriteString("]:")
		if src.Value.Trace == nil {
			b.WriteString("\n    <no provenance>")
			continue
		}
		for _, line := range src.Value.Trace.Lines() {
			fmt.Fprintf(&b, "\n    %s", line)
		}
	}
	return b.String()
}

// Report is the result of abstract interpretation.
type Report struct {
	Program  *Program
	Findings []Finding
	Values   map[int]Value
}

// String renders every dangerous range operation, one per finding.
func (r *Report) String() string {
	if r == nil || len(r.Findings) == 0 {
		return "no dangerous range operations"
	}
	parts := make([]string, len(r.Findings))
	for i := range r.Findings {
		parts[i] = r.Findings[i].String()
	}
	return strings.Join(parts, "\n\n")
}

// Analyze propagates byte-order labels through p and reports every dangerous
// range operation. A merge of BE and LE values is not resolved by picking one
// arm: it becomes TagConflict and stays dangerous until an explicit
// OpConvert.
func Analyze(p *Program) (*Report, error) {
	if p == nil {
		return nil, fmt.Errorf("nil program")
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}

	a := &analyzer{
		program: p,
		values:  make(map[int]Value, len(p.Params)+len(p.Instrs)),
	}
	for i, param := range p.Params {
		tag := param.Tag
		if tag == TagInvalid {
			tag = TagUnknown
		}
		trace := &Trace{Step: TraceStep{
			Inst: -1,
			Op:   OpParam,
			Tag:  tag,
			Text: fmt.Sprintf("v%d = param %s", i, param.Name),
			Pos:  param.Pos,
		}}
		a.values[i] = Value{
			Tag:      tag,
			HasConst: param.HasConst,
			Const:    param.Const,
			HasBytes: param.HasBytes,
			Bytes:    param.Bytes,
			Trace:    trace,
		}
	}

	report := &Report{Program: p}
	for i, in := range p.Instrs {
		value, err := a.eval(i, in)
		if err != nil {
			return nil, err
		}
		a.values[in.Dst] = value

		if in.Op == OpSlice {
			if finding, ok := a.checkSlice(i, in); ok {
				report.Findings = append(report.Findings, finding)
			}
		}
	}

	report.Values = make(map[int]Value, len(a.values))
	for id, value := range a.values {
		report.Values[id] = value
	}
	return report, nil
}

type analyzer struct {
	program *Program
	values  map[int]Value
}

func (a *analyzer) get(id int) (Value, error) {
	value, ok := a.values[id]
	if !ok {
		return Value{}, fmt.Errorf("operand v%d is undefined", id)
	}
	return value, nil
}

func (a *analyzer) parents(args []int) ([]*Trace, error) {
	out := make([]*Trace, len(args))
	for i, arg := range args {
		value, err := a.get(arg)
		if err != nil {
			return nil, err
		}
		out[i] = value.Trace
	}
	return out, nil
}

func (a *analyzer) trace(index int, in Instr, tag Tag, parents []*Trace, note string) *Trace {
	return &Trace{
		Step: TraceStep{
			Inst: index,
			Op:   in.Op,
			Tag:  tag,
			Text: in.Expr(),
			Pos:  in.Pos,
			Note: note,
		},
		Parents: parents,
	}
}

func (a *analyzer) eval(index int, in Instr) (Value, error) {
	switch in.Op {
	case OpConst:
		return Value{
			Tag:      TagConst,
			HasConst: true,
			Const:    in.Imm,
			Trace:    a.trace(index, in, TagConst, nil, ""),
		}, nil

	case OpReadByte:
		parents, err := a.parents(in.Args)
		if err != nil {
			return Value{}, err
		}
		return Value{
			Tag:      TagRaw,
			HasBytes: true,
			Bytes:    1,
			Trace:    a.trace(index, in, TagRaw, parents, ""),
		}, nil

	case OpReadBE, OpReadLE:
		parents, err := a.parents(in.Args)
		if err != nil {
			return Value{}, err
		}
		tag := TagBE
		orderName := "big-endian"
		if in.Op == OpReadLE {
			tag = TagLE
			orderName = "little-endian"
		}
		return Value{
			Tag:   tag,
			Trace: a.trace(index, in, tag, parents, fmt.Sprintf("%d-byte %s decode", in.Width, orderName)),
		}, nil

	case OpPack:
		inputs, parents, err := a.inputs(in.Args)
		if err != nil {
			return Value{}, err
		}
		tag := packTag(inputs, in.Order)
		value := Value{Tag: tag, Trace: a.trace(index, in, tag, parents, "assemble lanes")}
		if n, ok := packConst(inputs, in.Order); ok {
			value.HasConst = true
			value.Const = n
		}
		return value, nil

	case OpSwap:
		input, err := a.get(in.Args[0])
		if err != nil {
			return Value{}, err
		}
		tag := SwapTag(input.Tag)
		note := fmt.Sprintf("reverse %d bytes", in.Width)
		if input.Tag == TagConflict {
			note += "; byte swap does not resolve a merge conflict"
		}
		value := Value{
			Tag:      tag,
			HasConst: false,
			Overflow: input.Overflow,
			Trace:    a.trace(index, in, tag, []*Trace{input.Trace}, note),
		}
		if input.HasConst {
			value.HasConst = true
			value.Const = int64(swapWidth(uint64(input.Const), in.Width))
		}
		return value, nil

	case OpConvert:
		input, err := a.get(in.Args[0])
		if err != nil {
			return Value{}, err
		}
		target := in.Order.Tag()
		tag := ConvertTag(input.Tag, target)
		note := "explicit " + in.Order.String() + " conversion"
		if input.Tag == TagConflict {
			note += "; resolves merge conflict"
		}
		return Value{
			Tag:      tag,
			HasConst: input.HasConst,
			Const:    input.Const,
			HasBytes: input.HasBytes,
			Bytes:    input.Bytes,
			Overflow: input.Overflow,
			Trace:    a.trace(index, in, tag, []*Trace{input.Trace}, note),
		}, nil

	case OpAdd, OpSub, OpMul, OpAnd, OpOr, OpXor, OpShl, OpShr:
		return a.evalArithmetic(index, in)

	case OpSlice:
		inputs, parents, err := a.inputs(in.Args)
		if err != nil {
			return Value{}, err
		}
		value := Value{
			Tag:   TagRaw,
			Trace: a.trace(index, in, TagRaw, parents, "byte slice"),
		}
		length := inputs[2]
		if length.HasConst && length.Const >= 0 {
			value.HasBytes = true
			value.Bytes = length.Const
		}
		return value, nil

	case OpPhi:
		inputs, parents, err := a.inputs(in.Args)
		if err != nil {
			return Value{}, err
		}
		value := joinValues(inputs)
		note := "join"
		if value.Tag == TagConflict {
			note = "join produced a conflict; choose neither arm"
		}
		value.Trace = a.trace(index, in, value.Tag, parents, note)
		return value, nil

	case OpHost:
		inputs, parents, err := a.inputs(in.Args)
		if err != nil {
			return Value{}, err
		}
		tag := in.ResultTag
		if tag == TagInvalid {
			tag = JoinAll(valueTags(inputs)...)
			if tag == TagInvalid {
				tag = TagUnknown
			}
		}
		return Value{
			Tag:   tag,
			Trace: a.trace(index, in, tag, parents, "host call effect"),
		}, nil

	default:
		return Value{}, fmt.Errorf("%s: unsupported opcode %s", a.program.positionFor(in, index), in.Op)
	}
}

func (a *analyzer) inputs(args []int) ([]Value, []*Trace, error) {
	values := make([]Value, len(args))
	traces := make([]*Trace, len(args))
	for i, arg := range args {
		value, err := a.get(arg)
		if err != nil {
			return nil, nil, err
		}
		values[i] = value
		traces[i] = value.Trace
	}
	return values, traces, nil
}

func (a *analyzer) evalArithmetic(index int, in Instr) (Value, error) {
	left, err := a.get(in.Args[0])
	if err != nil {
		return Value{}, err
	}
	right, err := a.get(in.Args[1])
	if err != nil {
		return Value{}, err
	}
	parents := []*Trace{left.Trace, right.Trace}

	var tag Tag
	switch in.Op {
	case OpShl, OpShr:
		// A shift count is not part of the shifted integer's byte-order
		// representation. Only the left operand determines the result class.
		tag = left.Tag
	default:
		tag = binaryTag(left.Tag, right.Tag)
	}

	value := Value{
		Tag:      tag,
		Overflow: left.Overflow || right.Overflow,
		Trace:    a.trace(index, in, tag, parents, arithmeticNote(left, right, tag)),
	}
	if left.HasConst && right.HasConst {
		if n, ok := foldArithmetic(in.Op, left.Const, right.Const); ok {
			value.HasConst = true
			value.Const = n
		} else {
			value.Overflow = true
		}
	}
	return value, nil
}

func arithmeticNote(left, right Value, tag Tag) string {
	if tag == TagConflict && left.Tag != TagConflict && right.Tag != TagConflict {
		return fmt.Sprintf("mixed %s and %s arithmetic", left.Tag, right.Tag)
	}
	if tag == TagConflict {
		return "conflict propagated through arithmetic"
	}
	return ""
}

func binaryTag(a, b Tag) Tag {
	if a == TagUnknown || b == TagUnknown {
		return TagUnknown
	}
	if a == TagConflict || b == TagConflict {
		return TagConflict
	}
	if a == TagConst {
		return b
	}
	if b == TagConst {
		return a
	}
	if a == b {
		return a
	}
	return TagConflict
}

func valueTags(values []Value) []Tag {
	out := make([]Tag, len(values))
	for i, v := range values {
		out[i] = v.Tag
	}
	return out
}

func packTag(inputs []Value, order Order) Tag {
	target := order.Tag()
	if target == TagInvalid {
		return TagUnknown
	}
	for _, input := range inputs {
		switch input.Tag {
		case TagConst, TagRaw:
			// Raw lanes and constants do not impose an integer order.
		case TagUnknown:
			return TagUnknown
		case TagConflict:
			return TagConflict
		case TagBE, TagLE:
			if input.Tag != target {
				return TagConflict
			}
		default:
			return TagUnknown
		}
	}
	return target
}

func packConst(inputs []Value, order Order) (int64, bool) {
	var n uint64
	switch order {
	case OrderBig:
		for _, input := range inputs {
			if !input.HasConst || input.Const < 0 || input.Const > 255 {
				return 0, false
			}
			n = n<<8 | uint64(input.Const)
		}
	case OrderLittle:
		for i, input := range inputs {
			if !input.HasConst || input.Const < 0 || input.Const > 255 {
				return 0, false
			}
			n |= uint64(input.Const) << (8 * i)
		}
	default:
		return 0, false
	}
	if n > math.MaxInt64 {
		return 0, false
	}
	return int64(n), true
}

func swapWidth(n uint64, width int) uint64 {
	var out uint64
	for i := 0; i < width; i++ {
		out = out<<8 | (n & 0xff)
		n >>= 8
	}
	return out
}

func foldArithmetic(op Op, a, b int64) (int64, bool) {
	switch op {
	case OpAdd:
		if (b > 0 && a > math.MaxInt64-b) || (b < 0 && a < math.MinInt64-b) {
			return 0, false
		}
		return a + b, true
	case OpSub:
		if (b < 0 && a > math.MaxInt64+b) || (b > 0 && a < math.MinInt64+b) {
			return 0, false
		}
		return a - b, true
	case OpMul:
		if a == 0 || b == 0 {
			return 0, true
		}
		if a == math.MinInt64 && b == -1 || b == math.MinInt64 && a == -1 {
			return 0, false
		}
		n := a * b
		if n/b != a {
			return 0, false
		}
		return n, true
	case OpShl:
		if b < 0 || b >= 63 {
			return 0, false
		}
		if a != 0 && (a > math.MaxInt64>>uint(b) || a < math.MinInt64>>uint(b)) {
			return 0, false
		}
		return a << uint(b), true
	case OpShr:
		if b < 0 || b >= 63 {
			return 0, false
		}
		return a >> uint(b), true
	case OpAnd:
		return a & b, true
	case OpOr:
		return a | b, true
	case OpXor:
		return a ^ b, true
	default:
		return 0, true
	}
}

func (a *analyzer) checkSlice(index int, in Instr) (Finding, bool) {
	base, _ := a.get(in.Args[0])
	offset, _ := a.get(in.Args[1])
	length, _ := a.get(in.Args[2])

	var reasons []Reason
	addReason := func(code, message string) {
		for _, existing := range reasons {
			if existing.Code == code {
				return
			}
		}
		reasons = append(reasons, Reason{Code: code, Message: message})
	}

	switch {
	case offset.Tag == TagConflict || length.Tag == TagConflict:
		addReason(ReasonOrderConflict,
			"a merge produced conflicting byte orders and no explicit conversion resolved it")
	case offset.Tag == TagBE && length.Tag == TagLE:
		addReason(ReasonMixedOrder,
			fmt.Sprintf("host-order (%s) length is paired with a network-order (%s) offset", length.Tag, offset.Tag))
	case offset.Tag == TagLE && length.Tag == TagBE:
		addReason(ReasonMixedOrder,
			fmt.Sprintf("network-order (%s) length is paired with a host-order (%s) offset", length.Tag, offset.Tag))
	}
	if offset.Tag == TagRaw || length.Tag == TagRaw {
		addReason(ReasonRawOperand,
			"a slice offset or length is still raw bytes rather than a decoded integer")
	}
	if offset.Overflow || length.Overflow {
		addReason(ReasonRangeOverflow,
			"offset or length arithmetic overflowed during range computation")
	}
	if offset.HasConst && offset.Const < 0 {
		addReason(ReasonNegativeRange, fmt.Sprintf("offset %d is negative", offset.Const))
	}
	if length.HasConst && length.Const < 0 {
		addReason(ReasonNegativeRange, fmt.Sprintf("length %d is negative", length.Const))
	}

	if base.HasBytes && base.Bytes >= 0 {
		if offset.HasConst && length.HasConst && offset.Const >= 0 && length.Const >= 0 {
			if end, ok := safeEnd(offset.Const, length.Const); !ok {
				addReason(ReasonRangeOverflow,
					fmt.Sprintf("offset %d + length %d overflows int64", offset.Const, length.Const))
			} else if end > base.Bytes {
				addReason(ReasonOutOfBounds,
					fmt.Sprintf("range [%d,%d) exceeds buffer length %d", offset.Const, end, base.Bytes))
			}
		}
		if offset.HasConst && offset.Const > base.Bytes {
			addReason(ReasonOutOfBounds,
				fmt.Sprintf("offset %d exceeds buffer length %d", offset.Const, base.Bytes))
		}
		if length.HasConst && length.Const > base.Bytes {
			addReason(ReasonOutOfBounds,
				fmt.Sprintf("length %d exceeds buffer length %d", length.Const, base.Bytes))
		}
	}

	if len(reasons) == 0 {
		return Finding{}, false
	}
	return Finding{
		Instr:   index,
		Pos:     in.Pos,
		Reasons: reasons,
		Sources: []Source{
			{Role: "base", Value: cloneValue(base)},
			{Role: "offset", Value: cloneValue(offset)},
			{Role: "length", Value: cloneValue(length)},
		},
	}, true
}

func safeEnd(offset, length int64) (int64, bool) {
	if offset < 0 || length < 0 {
		return 0, true
	}
	if offset > math.MaxInt64-length {
		return 0, false
	}
	return offset + length, true
}
