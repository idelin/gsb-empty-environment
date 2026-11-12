package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies a contract violation.
type Kind string

const (
	// KindTimingContract is a callee timing-dependency bit fed by a secret
	// bit that the call site did not declare public.
	KindTimingContract Kind = "timing-contract"
	// KindSecretAddress is a secret bit that reaches a table index or a
	// summarized library address bit.  It is reported even if the bit was
	// declassified for timing, because a public mask cannot erase a secret
	// address dependency.
	KindSecretAddress Kind = "secret-address"
	// KindSecretBranch is a secret control-flow decision in the composed
	// function.
	KindSecretBranch Kind = "secret-branch"
)

// Source identifies the secret input bit at the start of a path.
type Source struct {
	Input string
	Bit   int
}

// Step is one bit-level operation on a path from a secret source to a sink.
type Step struct {
	Node string
	Op   string
	From int
	To   int
	Note string
}

// String renders a step.
func (s Step) String() string {
	var detail string
	switch {
	case s.From >= 0 && s.To >= 0 && s.From != s.To:
		detail = fmt.Sprintf("bit %d->%d", s.From, s.To)
	case s.From >= 0:
		detail = fmt.Sprintf("bit %d", s.From)
	}
	if s.Note != "" {
		if detail != "" {
			detail += ", "
		}
		detail += s.Note
	}
	if detail == "" {
		return fmt.Sprintf("%s(%s)", s.Op, s.Node)
	}
	return fmt.Sprintf("%s(%s)[%s]", s.Op, s.Node, detail)
}

// Finding is one violated bit-level contract path.
type Finding struct {
	Kind      Kind
	Program   string
	Sink      string
	Library   string
	Param     int // -1 for a composed-function lookup/branch
	Bit       int // sink bit (callee timing bit, index bit, or condition bit)
	Source    Source
	Path      []Step
	Note      string
	sinkIndex int
}

// String renders the finding with the full source-to-sink path.
func (f Finding) String() string {
	var b strings.Builder
	b.WriteString(string(f.Kind))
	b.WriteString(" at ")
	if f.Kind == KindTimingContract || f.Param >= 0 {
		fmt.Fprintf(&b, "call %q library %q param %d bit %d", f.Sink, f.Library, f.Param, f.Bit)
	} else {
		fmt.Fprintf(&b, "%q bit %d", f.Sink, f.Bit)
	}
	fmt.Fprintf(&b, " <- secret %q bit %d: %s[%d]", f.Source.Input, f.Source.Bit, f.Source.Input, f.Source.Bit)
	for _, step := range f.Path {
		b.WriteString(" -> ")
		b.WriteString(step.String())
	}
	if f.Note != "" {
		b.WriteString(" (")
		b.WriteString(f.Note)
		b.WriteByte(')')
	}
	return b.String()
}

// BitDependency is one bit-level dependency of a node's result.
type BitDependency struct {
	Bit    int
	Source Source
	Public bool
	Path   []Step
}

// Report is the result of analyzing one composed function.
type Report struct {
	Program  string
	Findings []Finding

	nodes  []Node
	values []value
}

// BitDependencies returns the per-bit provenance of a node result.  Public is
// true when the dependency has been declassified for ordinary timing checks;
// address checks ignore that flag.
func (r *Report) BitDependencies(node string) ([]BitDependency, error) {
	index := -1
	for i := range r.nodes {
		if r.nodes[i].Name == node {
			index = i
			break
		}
	}
	if index < 0 {
		return nil, fmt.Errorf("unknown node %q", node)
	}
	var out []BitDependency
	for bit := 0; bit < r.values[index].width; bit++ {
		for src, dep := range r.values[index].bits[bit].deps {
			out = append(out, BitDependency{
				Bit:    bit,
				Source: Source{Input: src.Input, Bit: src.Bit},
				Public: dep.public,
				Path:   flattenTrace(dep.tr),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bit != out[j].Bit {
			return out[i].Bit < out[j].Bit
		}
		if out[i].Source.Input != out[j].Source.Input {
			return out[i].Source.Input < out[j].Source.Input
		}
		return out[i].Source.Bit < out[j].Source.Bit
	})
	return out, nil
}

// Analyze checks a composed function against the supplied library summaries.
func Analyze(program *Program, libraries Libraries) (*Report, error) {
	built, err := program.Build()
	if err != nil {
		return nil, err
	}
	state := &analysis{
		program:   built,
		libraries: libraries,
		values:    make([]value, len(built.Nodes)),
		nextID:    1,
		findingAt: make(map[findingKey]int),
	}
	if err := state.validate(); err != nil {
		return nil, err
	}
	for i := range built.Nodes {
		result, err := state.analyzeNode(i, built.Nodes[i])
		if err != nil {
			return nil, err
		}
		state.values[i] = result
	}
	sort.SliceStable(state.findings, func(i, j int) bool {
		left, right := state.findings[i], state.findings[j]
		if left.sinkIndex != right.sinkIndex {
			return left.sinkIndex < right.sinkIndex
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		if left.Param != right.Param {
			return left.Param < right.Param
		}
		if left.Bit != right.Bit {
			return left.Bit < right.Bit
		}
		if left.Source.Input != right.Source.Input {
			return left.Source.Input < right.Source.Input
		}
		return left.Source.Bit < right.Source.Bit
	})
	return &Report{
		Program:  built.Name,
		Findings: state.findings,
		nodes:    built.Nodes,
		values:   state.values,
	}, nil
}

// Check is the convenience form of Analyze that returns only findings.
func Check(program *Program, libraries Libraries) ([]Finding, error) {
	report, err := Analyze(program, libraries)
	if err != nil {
		return nil, err
	}
	return report.Findings, nil
}

type findingKey struct {
	kind     Kind
	sink     string
	param    int
	bit      int
	sourceIn string
	sourceBt int
}

type analysis struct {
	program   *Program
	libraries Libraries
	values    []value
	nextID    int
	findings  []Finding
	findingAt map[findingKey]int
}

func (a *analysis) newValue(width int) value {
	v := newValue(width, a.nextID)
	a.nextID++
	return v
}

func (a *analysis) validate() error {
	widths := make([]int, len(a.program.Nodes))
	for i := range a.program.Nodes {
		node := &a.program.Nodes[i]
		switch node.Op {
		case OpSecretInput, OpPublicInput, OpConst:
			if err := checkWidth(node.Name, node.Width); err != nil {
				return err
			}
			if len(node.Args) != 0 {
				return fmt.Errorf("node %q: %s takes no arguments", node.Name, node.Op)
			}
		case OpAnd, OpOr, OpXor, OpAdd, OpSub, OpMul:
			if err := checkWidth(node.Name, node.Width); err != nil {
				return err
			}
			if len(node.Args) != 2 {
				return fmt.Errorf("node %q: %s takes two arguments", node.Name, node.Op)
			}
		case OpNot, OpShl, OpShr, OpPublicMask:
			if err := checkWidth(node.Name, node.Width); err != nil {
				return err
			}
			if len(node.Args) != 1 {
				return fmt.Errorf("node %q: %s takes one argument", node.Name, node.Op)
			}
		case OpSelect:
			if err := checkWidth(node.Name, node.Width); err != nil {
				return err
			}
			if len(node.Args) != 3 {
				return fmt.Errorf("node %q: select takes condition, true value, and false value", node.Name)
			}
		case OpLookup:
			if err := checkWidth(node.Name, node.Width); err != nil {
				return err
			}
			if len(node.Args) != 1 && len(node.Args) != 2 {
				return fmt.Errorf("node %q: lookup takes an index, optionally preceded by a table value", node.Name)
			}
		case OpBranch:
			if len(node.Args) != 1 {
				return fmt.Errorf("node %q: branch takes one condition", node.Name)
			}
		case OpCall:
			if node.Call == nil {
				return fmt.Errorf("node %q: call has no CallSpec", node.Name)
			}
			summary, err := a.libraries.lookup(node.Call.Library)
			if err != nil {
				return fmt.Errorf("node %q: %w", node.Name, err)
			}
			if len(summary.Params) != len(node.Args) {
				return fmt.Errorf("node %q: library %q has %d parameters, call has %d arguments",
					node.Name, summary.Name, len(summary.Params), len(node.Args))
			}
			for param, arg := range node.Args {
				info := summary.Params[param]
				argWidth := widths[arg]
				timing := info.TimingBits.With(maskAt(node.Call.TimingBits, param))
				address := info.AddressBits.With(maskAt(node.Call.AddressBits, param))
				declared := timing.With(address, info.PublicBits, maskAt(node.Call.AllowedPublic, param))
				if declared.Without(widthMask(argWidth)) != 0 {
					return fmt.Errorf("node %q: library %q parameter %d declares bits outside width %d",
						node.Name, summary.Name, param, argWidth)
				}
				if param < len(node.Call.AllowedPublic) && node.Call.AllowedPublic[param].Without(widthMask(argWidth)) != 0 {
					return fmt.Errorf("node %q: call-site allowed-public mask for parameter %d declares bits outside width %d",
						node.Name, param, argWidth)
				}
			}
			if summary.ResultWidth != 0 {
				if err := checkWidth(node.Name+" result", summary.ResultWidth); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("node %q: unknown op %d", node.Name, node.Op)
		}
		for _, arg := range node.Args {
			if arg < 0 || arg >= i {
				return fmt.Errorf("node %q: arg index %d is not an earlier node", node.Name, arg)
			}
			if a.program.Nodes[arg].Op == OpBranch {
				return fmt.Errorf("node %q: branch node %q does not produce a value", node.Name, a.program.Nodes[arg].Name)
			}
		}
		switch node.Op {
		case OpBranch:
			widths[i] = 0
		case OpCall:
			summary, _ := a.libraries.lookup(node.Call.Library)
			width := summary.ResultWidth
			if width == 0 {
				width = node.Width
			}
			if width == 0 {
				for _, arg := range node.Args {
					if widths[arg] > width {
						width = widths[arg]
					}
				}
			}
			if err := checkWidth(node.Name+" result", width); err != nil {
				return err
			}
			widths[i] = width
		default:
			widths[i] = node.Width
		}
	}
	for i := range a.program.Nodes {
		node := a.program.Nodes[i]
		switch node.Op {
		case OpAnd, OpOr, OpXor, OpAdd, OpSub, OpMul:
			if widths[node.Args[0]] != widths[i] || widths[node.Args[1]] != widths[i] {
				return fmt.Errorf("node %q: operand widths must equal %d", node.Name, node.Width)
			}
		case OpNot, OpShl, OpShr, OpPublicMask:
			if widths[node.Args[0]] != widths[i] {
				return fmt.Errorf("node %q: operand width must equal %d", node.Name, node.Width)
			}
		case OpSelect:
			if widths[node.Args[1]] != widths[i] || widths[node.Args[2]] != widths[i] {
				return fmt.Errorf("node %q: select branch widths must equal %d", node.Name, node.Width)
			}
		}
	}
	return nil
}

func (a *analysis) analyzeNode(index int, node Node) (value, error) {
	switch node.Op {
	case OpSecretInput:
		v := a.newValue(node.Width)
		for bit := 0; bit < node.Width; bit++ {
			v.mergeBit(bit, source{Input: node.Name, Bit: bit}, bitDep{})
		}
		return v, nil
	case OpPublicInput:
		v := a.newValue(node.Width)
		return v, nil
	case OpConst:
		v := a.newValue(node.Width)
		setKnownValue(&v, Mask(node.Value)&widthMask(node.Width))
		return v, nil
	case OpAnd, OpOr, OpXor:
		return a.bitwise(node, a.values[node.Args[0]], a.values[node.Args[1]]), nil
	case OpNot:
		return a.not(node, a.values[node.Args[0]]), nil
	case OpShl, OpShr:
		return a.shift(node, a.values[node.Args[0]]), nil
	case OpAdd, OpSub:
		return a.arithmetic(node, a.values[node.Args[0]], a.values[node.Args[1]]), nil
	case OpMul:
		return a.multiply(node, a.values[node.Args[0]], a.values[node.Args[1]]), nil
	case OpSelect:
		return a.selectValue(node, a.values[node.Args[0]], a.values[node.Args[1]], a.values[node.Args[2]]), nil
	case OpPublicMask:
		return a.publicMask(node, a.values[node.Args[0]]), nil
	case OpLookup:
		return a.lookup(node, index), nil
	case OpBranch:
		a.branch(node, a.values[node.Args[0]])
		v := newValue(0, a.nextID)
		a.nextID++
		return v, nil
	case OpCall:
		return a.call(node, index)
	default:
		return value{}, fmt.Errorf("node %q: unsupported op %s", node.Name, node.Op)
	}
}

func (a *analysis) bitwise(node Node, x, y value) value {
	if node.Op == OpXor && x.id == y.id {
		out := a.newValue(node.Width)
		for bit := 0; bit < node.Width; bit++ {
			out.setKnown(bit, 0)
		}
		return out
	}
	out := a.newValue(node.Width)
	for bit := 0; bit < node.Width; bit++ {
		xKnown, xOK := x.knownBit(bit)
		yKnown, yOK := y.knownBit(bit)
		step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit}

		switch node.Op {
		case OpAnd:
			switch {
			case (xOK && xKnown == 0) || (yOK && yKnown == 0):
				out.setKnown(bit, 0)
			case xOK && yOK:
				out.setKnown(bit, xKnown&yKnown)
			case xOK && xKnown == 1:
				out.mergeFromBit(bit, y.bits[bit], step, false)
			case yOK && yKnown == 1:
				out.mergeFromBit(bit, x.bits[bit], step, false)
			default:
				out.mergeFromBit(bit, x.bits[bit], step, false)
				out.mergeFromBit(bit, y.bits[bit], step, false)
			}
		case OpOr:
			switch {
			case (xOK && xKnown == 1) || (yOK && yKnown == 1):
				out.setKnown(bit, 1)
			case xOK && yOK:
				out.setKnown(bit, xKnown|yKnown)
			case xOK && xKnown == 0:
				out.mergeFromBit(bit, y.bits[bit], step, false)
			case yOK && yKnown == 0:
				out.mergeFromBit(bit, x.bits[bit], step, false)
			default:
				out.mergeFromBit(bit, x.bits[bit], step, false)
				out.mergeFromBit(bit, y.bits[bit], step, false)
			}
		case OpXor:
			switch {
			case xOK && yOK:
				out.setKnown(bit, xKnown^yKnown)
			case xOK && xKnown == 0:
				out.mergeFromBit(bit, y.bits[bit], step, false)
				out.mergeKnownFrom(bit, y)
			case yOK && yKnown == 0:
				out.mergeFromBit(bit, x.bits[bit], step, false)
				out.mergeKnownFrom(bit, x)
			case xOK && xKnown == 1:
				out.mergeFromBit(bit, y.bits[bit], step, false)
				if yKnown, ok := y.knownBit(bit); ok {
					out.setKnown(bit, yKnown^1)
				}
			case yOK && yKnown == 1:
				out.mergeFromBit(bit, x.bits[bit], step, false)
				if xKnown, ok := x.knownBit(bit); ok {
					out.setKnown(bit, xKnown^1)
				}
			default:
				out.mergeFromBit(bit, x.bits[bit], step, false)
				out.mergeFromBit(bit, y.bits[bit], step, false)
			}
		}
	}
	return out
}

func (v *value) mergeKnownFrom(bit int, from value) {
	if known, ok := from.knownBit(bit); ok {
		v.setKnown(bit, known)
	}
}

func (a *analysis) not(node Node, x value) value {
	out := a.newValue(node.Width)
	for bit := 0; bit < node.Width; bit++ {
		step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit}
		out.mergeFromBit(bit, x.bits[bit], step, false)
		if known, ok := x.knownBit(bit); ok {
			out.setKnown(bit, known^1)
		}
	}
	return out
}

func (a *analysis) shift(node Node, x value) value {
	out := a.newValue(node.Width)
	if node.Shift >= MaxWidth {
		for bit := 0; bit < node.Width; bit++ {
			out.setKnown(bit, 0)
		}
		return out
	}
	shift := int(node.Shift)
	if node.Op == OpShl {
		for to := 0; to < node.Width; to++ {
			from := to - shift
			if from < 0 {
				out.setKnown(to, 0)
				continue
			}
			step := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to}
			out.mergeFromBit(to, x.bits[from], step, false)
			if known, ok := x.knownBit(from); ok {
				out.setKnown(to, known)
			}
		}
		return out
	}
	for to := 0; to < node.Width; to++ {
		from := to + shift
		if from >= node.Width {
			out.setKnown(to, 0)
			continue
		}
		step := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to}
		out.mergeFromBit(to, x.bits[from], step, false)
		if known, ok := x.knownBit(from); ok {
			out.setKnown(to, known)
		}
	}
	return out
}

func (a *analysis) arithmetic(node Node, x, y value) value {
	out := a.newValue(node.Width)
	xKnown, xIsKnown := x.knownValue()
	yKnown, yIsKnown := y.knownValue()
	if xIsKnown && yIsKnown {
		var result uint64
		if node.Op == OpAdd {
			result = uint64(xKnown) + uint64(yKnown)
		} else {
			result = uint64(xKnown) - uint64(yKnown)
		}
		setKnownValue(&out, Mask(result)&widthMask(node.Width))
		return out
	}
	if node.Op == OpAdd {
		if xIsKnown && xKnown == 0 {
			return a.copyWithStep(node, y)
		}
		if yIsKnown && yKnown == 0 {
			return a.copyWithStep(node, x)
		}
	}
	if node.Op == OpSub && yIsKnown && yKnown == 0 {
		return a.copyWithStep(node, x)
	}
	for to := 0; to < node.Width; to++ {
		for from := 0; from <= to; from++ {
			stepX := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to, Note: "carry chain"}
			stepY := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to, Note: "carry chain"}
			out.mergeFromBit(to, x.bits[from], stepX, false)
			out.mergeFromBit(to, y.bits[from], stepY, false)
		}
	}
	return out
}

func (a *analysis) multiply(node Node, x, y value) value {
	out := a.newValue(node.Width)
	xKnown, xIsKnown := x.knownValue()
	yKnown, yIsKnown := y.knownValue()
	if xIsKnown && yIsKnown {
		setKnownValue(&out, Mask(uint64(xKnown)*uint64(yKnown))&widthMask(node.Width))
		return out
	}
	if (xIsKnown && xKnown == 0) || (yIsKnown && yKnown == 0) {
		for bit := 0; bit < node.Width; bit++ {
			out.setKnown(bit, 0)
		}
		return out
	}
	if xIsKnown && xKnown == 1 {
		return a.copyWithStep(node, y)
	}
	if yIsKnown && yKnown == 1 {
		return a.copyWithStep(node, x)
	}
	for to := 0; to < node.Width; to++ {
		for from := 0; from <= to; from++ {
			stepX := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to, Note: "multiply/carry"}
			stepY := Step{Node: node.Name, Op: node.Op.String(), From: from, To: to, Note: "multiply/carry"}
			out.mergeFromBit(to, x.bits[from], stepX, false)
			out.mergeFromBit(to, y.bits[from], stepY, false)
		}
	}
	return out
}

func (a *analysis) copyWithStep(node Node, from value) value {
	out := a.newValue(node.Width)
	for bit := 0; bit < node.Width; bit++ {
		step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit}
		out.mergeFromBit(bit, from.bits[bit], step, false)
		if known, ok := from.knownBit(bit); ok {
			out.setKnown(bit, known)
		}
	}
	return out
}

func (a *analysis) selectValue(node Node, cond, whenTrue, whenFalse value) value {
	out := a.newValue(node.Width)
	condKnown, condIsKnown := cond.knownValue()

	if condIsKnown {
		chosen := whenFalse
		if condKnown != 0 {
			chosen = whenTrue
		}
		for bit := 0; bit < node.Width; bit++ {
			step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit, Note: "constant condition"}
			out.mergeFromBit(bit, chosen.bits[bit], step, false)
			if known, ok := chosen.knownBit(bit); ok {
				out.setKnown(bit, known)
			}
		}
		return out
	}

	sameValue := whenTrue.id == whenFalse.id
	for bit := 0; bit < node.Width; bit++ {
		trueKnown, trueOK := whenTrue.knownBit(bit)
		falseKnown, falseOK := whenFalse.knownBit(bit)
		if trueOK && falseOK && trueKnown == falseKnown {
			out.setKnown(bit, trueKnown)
			continue
		}
		step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit, Note: "branch value"}
		out.mergeFromBit(bit, whenTrue.bits[bit], step, false)
		out.mergeFromBit(bit, whenFalse.bits[bit], step, false)
		if sameValue {
			continue
		}
		for condBit := 0; condBit < cond.width; condBit++ {
			condStep := Step{Node: node.Name, Op: node.Op.String(), From: condBit, To: bit, Note: "condition"}
			out.mergeFromBit(bit, cond.bits[condBit], condStep, false)
		}
	}
	return out
}

func (a *analysis) publicMask(node Node, x value) value {
	out := a.newValue(node.Width)
	for bit := 0; bit < node.Width; bit++ {
		if !node.Public.Has(bit) {
			out.setKnown(bit, 0)
			continue
		}
		step := Step{Node: node.Name, Op: node.Op.String(), From: bit, To: bit, Note: "declassified for timing"}
		out.mergeFromBit(bit, x.bits[bit], step, true)
		if known, ok := x.knownBit(bit); ok {
			out.setKnown(bit, known)
		}
	}
	return out
}

func (a *analysis) lookup(node Node, index int) value {
	indexValue := a.values[node.Args[len(node.Args)-1]]
	out := a.newValue(node.Width)
	for bit := 0; bit < node.Width; bit++ {
		if len(node.Args) == 2 {
			table := a.values[node.Args[0]]
			for tableBit := 0; tableBit < table.width; tableBit++ {
				step := Step{Node: node.Name, Op: node.Op.String(), From: tableBit, To: bit, Note: "table contents"}
				out.mergeFromBit(bit, table.bits[tableBit], step, false)
			}
		}
		for indexBit := 0; indexBit < indexValue.width; indexBit++ {
			step := Step{Node: node.Name, Op: node.Op.String(), From: indexBit, To: bit, Note: "index"}
			out.mergeFromBit(bit, indexValue.bits[indexBit], step, false)
		}
	}

	for indexBit := 0; indexBit < indexValue.width; indexBit++ {
		if node.AllowPublicAddress && node.Public.Has(indexBit) {
			continue
		}
		for src, dep := range indexValue.bits[indexBit].deps {
			path := flattenTrace(dep.tr)
			path = append(path, Step{
				Node: node.Name,
				Op:   "address",
				From: indexBit,
				To:   indexBit,
				Note: "table index",
			})
			a.addFinding(Finding{
				Kind:   KindSecretAddress,
				Sink:   node.Name,
				Param:  -1,
				Bit:    indexBit,
				Source: Source{Input: src.Input, Bit: src.Bit},
				Path:   path,
				Note:   "public masks do not clear address dependencies",
			}, index)
		}
	}
	return out
}

func (a *analysis) branch(node Node, cond value) {
	for bit := 0; bit < cond.width; bit++ {
		for src, dep := range cond.bits[bit].deps {
			if dep.public {
				continue
			}
			path := flattenTrace(dep.tr)
			path = append(path, Step{Node: node.Name, Op: "branch", From: bit, To: bit, Note: "control flow"})
			a.addFinding(Finding{
				Kind:   KindSecretBranch,
				Sink:   node.Name,
				Param:  -1,
				Bit:    bit,
				Source: Source{Input: src.Input, Bit: src.Bit},
				Path:   path,
			}, node.index)
		}
	}
}

func (a *analysis) call(node Node, index int) (value, error) {
	summary, err := a.libraries.lookup(node.Call.Library)
	if err != nil {
		return value{}, fmt.Errorf("node %q: %w", node.Name, err)
	}
	if len(summary.Params) != len(node.Args) {
		return value{}, fmt.Errorf("node %q: library %q has %d parameters, call has %d arguments",
			node.Name, summary.Name, len(summary.Params), len(node.Args))
	}

	for param, argIndex := range node.Args {
		arg := a.values[argIndex]
		info := summary.Params[param]
		timing := info.TimingBits.With(maskAt(node.Call.TimingBits, param))
		address := info.AddressBits.With(maskAt(node.Call.AddressBits, param))
		declared := timing.With(address, info.PublicBits)
		if declared.Without(widthMask(arg.width)) != 0 {
			return value{}, fmt.Errorf("node %q: library %q parameter %d declares bits outside width %d",
				node.Name, summary.Name, param, arg.width)
		}
		allowed := info.PublicBits
		if param < len(node.Call.AllowedPublic) {
			allowed = allowed.With(node.Call.AllowedPublic[param])
		}

		for _, bit := range timing.Without(address).Positions() {
			if allowed.Has(bit) {
				continue
			}
			for src, dep := range arg.bits[bit].deps {
				if dep.public {
					continue
				}
				path := flattenTrace(dep.tr)
				path = append(path, Step{
					Node: node.Name,
					Op:   "call-timing",
					From: bit,
					To:   bit,
					Note: fmt.Sprintf("parameter %d", param),
				})
				a.addFinding(Finding{
					Kind:    KindTimingContract,
					Library: summary.Name,
					Sink:    node.Name,
					Param:   param,
					Bit:     bit,
					Source:  Source{Input: src.Input, Bit: src.Bit},
					Path:    path,
				}, index)
			}
		}

		for _, bit := range address.Positions() {
			if node.Call.AllowPublicAddress && allowed.Has(bit) {
				continue
			}
			for src, dep := range arg.bits[bit].deps {
				path := flattenTrace(dep.tr)
				path = append(path, Step{
					Node: node.Name,
					Op:   "call-address",
					From: bit,
					To:   bit,
					Note: fmt.Sprintf("parameter %d", param),
				})
				a.addFinding(Finding{
					Kind:    KindSecretAddress,
					Library: summary.Name,
					Sink:    node.Name,
					Param:   param,
					Bit:     bit,
					Source:  Source{Input: src.Input, Bit: src.Bit},
					Path:    path,
					Note:    "public masks do not clear address dependencies",
				}, index)
			}
		}
	}

	resultWidth := summary.ResultWidth
	if resultWidth == 0 {
		resultWidth = node.Width
	}
	if resultWidth == 0 {
		for _, argIndex := range node.Args {
			if a.values[argIndex].width > resultWidth {
				resultWidth = a.values[argIndex].width
			}
		}
	}
	if err := checkWidth(node.Name+" result", resultWidth); err != nil {
		return value{}, err
	}
	out := a.newValue(resultWidth)
	for param, argIndex := range node.Args {
		arg := a.values[argIndex]
		info := summary.Params[param]
		allowed := info.PublicBits.With(maskAt(node.Call.AllowedPublic, param))
		for from := 0; from < arg.width; from++ {
			for to := 0; to < resultWidth; to++ {
				step := Step{Node: node.Name, Op: "call-result", From: from, To: to, Note: fmt.Sprintf("parameter %d", param)}
				out.mergeFromBit(to, arg.bits[from], step, allowed.Has(from))
			}
		}
	}
	return out, nil
}

func (a *analysis) addFinding(finding Finding, sinkIndex int) {
	finding.Program = a.program.Name
	finding.sinkIndex = sinkIndex
	key := findingKey{
		kind:     finding.Kind,
		sink:     finding.Sink,
		param:    finding.Param,
		bit:      finding.Bit,
		sourceIn: finding.Source.Input,
		sourceBt: finding.Source.Bit,
	}
	if existing, ok := a.findingAt[key]; ok {
		if len(finding.Path) < len(a.findings[existing].Path) {
			a.findings[existing] = finding
		}
		return
	}
	a.findingAt[key] = len(a.findings)
	a.findings = append(a.findings, finding)
}
