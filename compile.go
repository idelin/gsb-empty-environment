package bitlayout

import (
	"fmt"
	"math/bits"
	"strconv"
)

// DefaultCompileMaxElements bounds the statically computable worst-case
// element count of any single array. A schema whose arrays could exceed
// it is rejected as an unbounded allocation.
const DefaultCompileMaxElements = 1 << 20

// CompileOptions tunes the compiler. The zero value is valid.
type CompileOptions struct {
	// MaxElements caps the worst-case element count of any one array.
	// Zero means DefaultCompileMaxElements.
	MaxElements uint64
}

type kind int

const (
	kindInt kind = iota
	kindStruct
	kindArray
	kindUnion
	kindPad
	kindChecksum
)

// node is the compiled form of a FieldDef. Plans are immutable after
// Compile returns and safe for concurrent use.
type node struct {
	kind kind
	name string
	path string // schema path, e.g. /body/samples

	// int / checksum
	bits   int
	signed bool

	// struct / union case
	fields []*node
	index  map[string]*node // sibling name -> node (nil for non-composites)

	// array
	lengthFrom *node // resolved earlier sibling, unsigned int
	elem       *node

	// union
	tagFrom *node
	cases   map[uint64]*node

	// pad
	align int

	// checksum
	algo   string
	fromIx int // index into the top-level field list
	toIx   int

	// static size bounds in bits (arrays use worst-case length)
	minBits int64
	maxBits int64
	// maxElems is the worst-case element count for arrays.
	maxElems uint64
}

// Plan is a compiled schema. It is immutable and safe for concurrent use
// by any number of encoders and decoders.
type Plan struct {
	Name      string
	BitOrder  BitOrder
	ByteOrder ByteOrder

	root     *node // anonymous top-level struct
	topNames []string

	// Record size bounds in bits, derived at compile time.
	MinBits int64
	MaxBits int64
}

// Compile parses and validates a schema document and returns a reusable
// plan. It rejects: forward length/tag references, cyclic dependencies,
// duplicate (overlapping) field names, undefined union tags, checksum
// ranges that do not precede the checksum field, and schemas whose
// worst-case allocation is unbounded or overflows 64-bit arithmetic.
func Compile(data []byte, opts *CompileOptions) (*Plan, error) {
	s, err := ParseSchema(data)
	if err != nil {
		return nil, err
	}
	var co CompileOptions
	if opts != nil {
		co = *opts
	}
	if co.MaxElements == 0 {
		co.MaxElements = DefaultCompileMaxElements
	}
	c := &compiler{maxElems: co.MaxElements}
	root, err := c.compileScope("", s.Fields)
	if err != nil {
		return nil, err
	}
	p := &Plan{
		Name:      s.Name,
		BitOrder:  s.BitOrder,
		ByteOrder: s.ByteOrder,
		root:      root,
		MinBits:   root.minBits,
		MaxBits:   root.maxBits,
	}
	for _, f := range root.fields {
		p.topNames = append(p.topNames, f.name)
	}
	if err := c.checkCycles(root); err != nil {
		return nil, err
	}
	return p, nil
}

type compiler struct {
	maxElems uint64
}

func (c *compiler) compileScope(parentPath string, defs []FieldDef) (*node, error) {
	n := &node{kind: kindStruct, path: parentPath, index: map[string]*node{}}
	all := map[string]bool{}
	for i := range defs {
		if defs[i].Name != "" {
			all[defs[i].Name] = true
		}
	}
	for i := range defs {
		child, err := c.compileNode(parentPath, &defs[i], n, all, false)
		if err != nil {
			return nil, err
		}
		if child.name != "" {
			if _, dup := n.index[child.name]; dup {
				return nil, fmt.Errorf("field overlap: duplicate name %q at %s", child.name, child.path)
			}
			n.index[child.name] = child
		}
		n.fields = append(n.fields, child)
		if addOverflow(n.minBits, child.minBits) || addOverflow(n.maxBits, child.maxBits) {
			return nil, fmt.Errorf("record size overflows 64 bits at %s", child.path)
		}
		n.minBits += child.minBits
		n.maxBits += child.maxBits
	}
	return n, nil
}

func (c *compiler) compileNode(parentPath string, d *FieldDef, scope *node, all map[string]bool, isElem bool) (*node, error) {
	path := parentPath + "/" + d.Name
	if d.Name == "" && d.Kind != "pad" {
		if isElem {
			path = parentPath + "[]"
		} else {
			path = parentPath + "/<" + d.Kind + ">"
		}
	}
	n := &node{name: d.Name, path: path}
	switch d.Kind {
	case "int":
		if d.Name == "" && !isElem {
			return nil, fmt.Errorf("%s: int field needs a name", path)
		}
		if d.Bits < 1 || d.Bits > 64 {
			return nil, fmt.Errorf("%s: bits must be in 1..64, got %d", path, d.Bits)
		}
		n.kind, n.bits, n.signed = kindInt, d.Bits, d.Signed
		n.minBits, n.maxBits = int64(d.Bits), int64(d.Bits)

	case "struct":
		if d.Name == "" && !isElem {
			return nil, fmt.Errorf("%s: struct field needs a name", path)
		}
		if len(d.Fields) == 0 {
			return nil, fmt.Errorf("%s: struct needs fields", path)
		}
		n.kind = kindStruct
		sub, err := c.compileScope(path, d.Fields)
		if err != nil {
			return nil, err
		}
		n.fields, n.index = sub.fields, sub.index
		n.minBits, n.maxBits = sub.minBits, sub.maxBits

	case "array":
		if d.Name == "" {
			return nil, fmt.Errorf("%s: array field needs a name", path)
		}
		if d.Element == nil {
			return nil, fmt.Errorf("%s: array needs an element", path)
		}
		if d.LengthFrom == "" {
			return nil, fmt.Errorf("%s: unbounded allocation: array needs lengthFrom", path)
		}
		n.kind = kindArray
		// Length must come from an earlier sibling: reject forward
		// references (and self references).
		for _, f := range scope.fields {
			if f.name == d.LengthFrom {
				n.lengthFrom = f
			}
		}
		if n.lengthFrom == nil {
			if all[d.LengthFrom] {
				return nil, fmt.Errorf("%s: forward length reference %q is not allowed", path, d.LengthFrom)
			}
			return nil, fmt.Errorf("%s: lengthFrom %q does not name an earlier sibling field", path, d.LengthFrom)
		}
		if n.lengthFrom.kind != kindInt || n.lengthFrom.signed {
			return nil, fmt.Errorf("%s: lengthFrom %q must be an unsigned int field", path, d.LengthFrom)
		}
		elem, err := c.compileNode(path, d.Element, &node{kind: kindStruct, index: map[string]*node{}}, nil, true)
		if err != nil {
			return nil, err
		}
		n.elem = elem
		if n.lengthFrom.bits == 64 {
			n.maxElems = ^uint64(0)
		} else {
			n.maxElems = uint64(1)<<n.lengthFrom.bits - 1
		}
		if n.maxElems > c.maxElems {
			return nil, fmt.Errorf("%s: unbounded allocation: up to %d elements (cap %d)", path, n.maxElems, c.maxElems)
		}
		hi, lo := bits.Mul64(n.maxElems, uint64(elem.maxBits))
		if hi != 0 || lo > uint64(1)<<62 {
			return nil, fmt.Errorf("%s: worst-case array size overflows 64 bits", path)
		}
		n.minBits = 0
		n.maxBits = int64(lo)

	case "union":
		if d.Name == "" {
			return nil, fmt.Errorf("%s: union field needs a name", path)
		}
		if d.TagFrom == "" {
			return nil, fmt.Errorf("%s: union needs tagFrom", path)
		}
		if len(d.Cases) == 0 {
			return nil, fmt.Errorf("%s: union needs at least one case", path)
		}
		n.kind = kindUnion
		for _, f := range scope.fields {
			if f.name == d.TagFrom {
				n.tagFrom = f
			}
		}
		if n.tagFrom == nil {
			return nil, fmt.Errorf("%s: tagFrom %q does not name an earlier sibling field", path, d.TagFrom)
		}
		if n.tagFrom.kind != kindInt || n.tagFrom.signed {
			return nil, fmt.Errorf("%s: tagFrom %q must be an unsigned int field", path, d.TagFrom)
		}
		n.cases = map[uint64]*node{}
		first := true
		for key, cd := range d.Cases {
			tag, err := strconv.ParseUint(key, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%s: undefined tag %q: not a decimal number", path, key)
			}
			if n.tagFrom.bits < 64 && tag >= uint64(1)<<n.tagFrom.bits {
				return nil, fmt.Errorf("%s: undefined tag %d: does not fit %d-bit tag field", path, tag, n.tagFrom.bits)
			}
			if _, dup := n.cases[tag]; dup {
				return nil, fmt.Errorf("%s: duplicate union case %d", path, tag)
			}
			if len(cd.Fields) == 0 {
				return nil, fmt.Errorf("%s: union case %d needs fields", path, tag)
			}
			sub, err := c.compileScope(fmt.Sprintf("%s[case=%d]", path, tag), cd.Fields)
			if err != nil {
				return nil, err
			}
			n.cases[tag] = sub
			if first || sub.minBits < n.minBits {
				n.minBits = sub.minBits
			}
			if first || sub.maxBits > n.maxBits {
				n.maxBits = sub.maxBits
			}
			first = false
		}

	case "pad":
		if d.Align < 1 || d.Align > 64 {
			return nil, fmt.Errorf("%s: pad align must be in 1..64, got %d", path, d.Align)
		}
		n.kind, n.align = kindPad, d.Align
		n.name = "" // pads are anonymous
		n.minBits, n.maxBits = 0, int64(d.Align-1)

	case "checksum":
		if d.Name == "" {
			return nil, fmt.Errorf("%s: checksum field needs a name", path)
		}
		if d.Bits < 1 || d.Bits > 32 {
			return nil, fmt.Errorf("%s: checksum bits must be in 1..32, got %d", path, d.Bits)
		}
		switch d.Algo {
		case "sum", "xor":
		case "crc8":
			if d.Bits != 8 {
				return nil, fmt.Errorf("%s: crc8 requires bits=8", path)
			}
		default:
			return nil, fmt.Errorf("%s: unknown checksum algo %q", path, d.Algo)
		}
		n.kind, n.bits, n.algo = kindChecksum, d.Bits, d.Algo
		if d.From == "" || d.To == "" {
			return nil, fmt.Errorf("%s: checksum needs from and to field names", path)
		}
		// Coverage must be a range of earlier top-level fields.
		n.fromIx, n.toIx = -1, -1
		for i, f := range scope.fields {
			if f.name == d.From && n.fromIx < 0 {
				n.fromIx = i
			}
			if f.name == d.To {
				n.toIx = i
			}
		}
		if n.fromIx < 0 || n.toIx < 0 {
			return nil, fmt.Errorf("%s: checksum range [%q,%q] must name earlier top-level fields", path, d.From, d.To)
		}
		if n.fromIx > n.toIx {
			return nil, fmt.Errorf("%s: checksum range is reversed (%q after %q)", path, d.From, d.To)
		}
		n.minBits, n.maxBits = int64(d.Bits), int64(d.Bits)

	default:
		return nil, fmt.Errorf("%s: unknown kind %q", path, d.Kind)
	}
	return n, nil
}

func addOverflow(a, b int64) bool {
	return b > 0 && a > (1<<62)-b
}

// checkCycles walks the dependency graph (length/tag/checksum edges).
// Backward-only references already make cycles impossible; this pass is a
// safety net so any future relaxation of the reference rules still cannot
// introduce a cyclic plan.
func (c *compiler) checkCycles(root *node) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[*node]int{}
	var visit func(n *node) error
	visit = func(n *node) error {
		switch color[n] {
		case gray:
			return fmt.Errorf("cyclic dependency involving %s", n.path)
		case black:
			return nil
		}
		color[n] = gray
		deps := []*node{n.lengthFrom, n.tagFrom, n.elem}
		deps = append(deps, n.fields...)
		for _, sub := range n.cases {
			deps = append(deps, sub)
		}
		for _, d := range deps {
			if d != nil {
				if err := visit(d); err != nil {
					return err
				}
			}
		}
		color[n] = black
		return nil
	}
	return visit(root)
}
