package bitlayout

import (
	"math"
	"sort"
	"strconv"
)

// MaxSchemaDepth bounds the nesting of arrays (directly or through union
// cases). Deeper schemas are rejected at compile time.
const MaxSchemaDepth = 8

type ftype int

const (
	tUint ftype = iota
	tInt
	tArray
	tUnion
	tAlign
	tChecksum
)

type cfield struct {
	name   string
	typ    ftype
	bits   int
	signed bool
	enum   []uint64

	lengthFrom string
	maxLen     uint64
	elem       []cfield

	tagFrom   string
	cases     map[uint64][]cfield
	caseOrder []uint64

	multiple int

	from, to string

	maxBits uint64
}

// Plan is a compiled, immutable layout. It is safe for concurrent use by
// any number of encoders and decoder sessions.
type Plan struct {
	Name      string
	BitOrder  BitOrder
	ByteOrder ByteOrder
	fields    []cfield
	ops       []op
	maxBits   uint64
}

// MaxBits is an upper bound on the encoded size of any record, in bits.
func (p *Plan) MaxBits() uint64 { return p.maxBits }

// NumOps is the number of instructions in the decode program.
func (p *Plan) NumOps() int { return len(p.ops) }

// NumFields is the number of top-level fields.
func (p *Plan) NumFields() int { return len(p.fields) }

// Compile validates a schema and produces a reusable Plan. It rejects
// forward length references, dependency cycles, duplicate field names,
// undefined union tags and unbounded allocations.
func Compile(s *Schema) (*Plan, error) {
	p := &Plan{Name: s.Name}
	if p.Name == "" {
		p.Name = "record"
	}
	switch s.BitOrder {
	case "", "msb":
		p.BitOrder = MSBFirst
	case "lsb":
		p.BitOrder = LSBFirst
	default:
		return nil, schemaErr(p.Name, "unknown bitOrder %q", s.BitOrder)
	}
	switch s.ByteOrder {
	case "", "big":
		p.ByteOrder = BigEndian
	case "little":
		p.ByteOrder = LittleEndian
	default:
		return nil, schemaErr(p.Name, "unknown byteOrder %q", s.ByteOrder)
	}
	if len(s.Fields) == 0 {
		return nil, schemaErr(p.Name, "schema has no fields")
	}
	fields, err := compileStruct(s.Fields, p.Name, 1)
	if err != nil {
		return nil, err
	}
	total := uint64(0)
	for i := range fields {
		if fields[i].maxBits > math.MaxUint64-total {
			return nil, schemaErr(p.Name, "record size is unbounded")
		}
		total += fields[i].maxBits
	}
	p.fields = fields
	p.maxBits = total
	p.ops = emitProgram(fields)
	return p, nil
}

func compileStruct(specs []FieldSpec, path string, depth int) ([]cfield, error) {
	if depth > MaxSchemaDepth {
		return nil, schemaErr(path, "nesting depth exceeds %d", MaxSchemaDepth)
	}
	if len(specs) == 0 {
		return nil, schemaErr(path, "empty field group")
	}
	fields := make([]cfield, 0, len(specs))
	prev := map[string]cfield{}
	for i := range specs {
		spec := &specs[i]
		fpath := path + "." + spec.Name
		if spec.Name == "" {
			return nil, schemaErr(path, "field with empty name")
		}
		if _, dup := prev[spec.Name]; dup {
			return nil, schemaErr(fpath, "duplicate field name %q", spec.Name)
		}
		order := make([]string, 0, len(fields))
		for j := range fields {
			order = append(order, fields[j].name)
		}
		f, err := compileField(spec, fpath, depth, prev, order)
		if err != nil {
			return nil, err
		}
		fields = append(fields, *f)
		prev[spec.Name] = *f
	}
	// Every reference resolved against prev points to an earlier field, so
	// the dependency graph is acyclic by construction; verify anyway.
	if err := checkAcyclic(fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func fieldDeps(f *cfield) []string {
	var deps []string
	switch f.typ {
	case tArray:
		deps = append(deps, f.lengthFrom)
	case tUnion:
		deps = append(deps, f.tagFrom)
	case tChecksum:
		deps = append(deps, f.from, f.to)
	}
	return deps
}

func checkAcyclic(fields []cfield) error {
	index := map[string]int{}
	for i := range fields {
		index[fields[i].name] = i
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var visit func(name string) error
	visit = func(name string) error {
		switch color[name] {
		case gray:
			return schemaErr(name, "cyclic dependency involving %q", name)
		case black:
			return nil
		}
		color[name] = gray
		i, ok := index[name]
		if ok {
			for _, dep := range fieldDeps(&fields[i]) {
				if _, known := index[dep]; known {
					if err := visit(dep); err != nil {
						return err
					}
				}
			}
		}
		color[name] = black
		return nil
	}
	for i := range fields {
		if err := visit(fields[i].name); err != nil {
			return err
		}
	}
	return nil
}

// compileField compiles one field. prev holds only the fields defined
// earlier at the same level, so every reference resolves backwards: forward
// references fail lookup here, and because edges always point backwards a
// dependency cycle cannot be constructed.
func compileField(spec *FieldSpec, path string, depth int, prev map[string]cfield, order []string) (*cfield, error) {
	f := &cfield{name: spec.Name}
	switch spec.Type {
	case "uint":
		f.typ = tUint
		if spec.Bits < 1 || spec.Bits > 64 {
			return nil, schemaErr(path, "uint bits %d out of range 1..64", spec.Bits)
		}
		f.bits = spec.Bits
		for _, v := range spec.Enum {
			if spec.Bits < 64 && v > mask64(spec.Bits) {
				return nil, schemaErr(path, "enum value %d does not fit %d bits", v, spec.Bits)
			}
			f.enum = append(f.enum, v)
		}
		f.maxBits = uint64(spec.Bits)

	case "int":
		f.typ = tInt
		f.signed = true
		if spec.Bits < 1 || spec.Bits > 64 {
			return nil, schemaErr(path, "int bits %d out of range 1..64", spec.Bits)
		}
		f.bits = spec.Bits
		f.maxBits = uint64(spec.Bits)

	case "array":
		f.typ = tArray
		if spec.LengthFrom == "" {
			return nil, schemaErr(path, "array requires lengthFrom")
		}
		src, ok := prev[spec.LengthFrom]
		if !ok {
			return nil, schemaErr(path, "lengthFrom %q is not a previously defined field", spec.LengthFrom)
		}
		if src.typ != tUint {
			return nil, schemaErr(path, "lengthFrom %q is not a uint field", spec.LengthFrom)
		}
		f.lengthFrom = spec.LengthFrom
		if spec.Element == nil || len(spec.Element.Fields) == 0 {
			return nil, schemaErr(path, "array requires a non-empty element")
		}
		elem, err := compileStruct(spec.Element.Fields, path+"[]", depth+1)
		if err != nil {
			return nil, err
		}
		f.elem = elem
		elemMax := uint64(0)
		for i := range elem {
			if elem[i].maxBits > math.MaxUint64-elemMax {
				return nil, schemaErr(path, "element size is unbounded")
			}
			elemMax += elem[i].maxBits
		}
		if elemMax == 0 {
			return nil, schemaErr(path, "array element has no bits")
		}
		maxCount := uint64(math.MaxUint64)
		if src.bits < 64 {
			maxCount = mask64(src.bits)
		}
		f.maxLen = maxCount
		if spec.MaxLength > 0 && spec.MaxLength < maxCount {
			f.maxLen = spec.MaxLength
		}
		if f.maxLen > math.MaxUint64/elemMax {
			return nil, schemaErr(path,
				"unbounded allocation: up to %d elements of %d bits overflow the size budget",
				f.maxLen, elemMax)
		}
		f.maxBits = f.maxLen * elemMax

	case "union":
		f.typ = tUnion
		if spec.TagFrom == "" {
			return nil, schemaErr(path, "union requires tagFrom")
		}
		src, ok := prev[spec.TagFrom]
		if !ok {
			return nil, schemaErr(path, "tagFrom %q is not a previously defined field", spec.TagFrom)
		}
		if src.typ != tUint {
			return nil, schemaErr(path, "tagFrom %q is not a uint field", spec.TagFrom)
		}
		f.tagFrom = spec.TagFrom
		if len(spec.Cases) == 0 {
			return nil, schemaErr(path, "union requires at least one case")
		}
		f.cases = map[uint64][]cfield{}
		for key, cs := range spec.Cases {
			tag, err := strconv.ParseUint(key, 0, 64)
			if err != nil {
				return nil, schemaErr(path, "invalid case tag %q", key)
			}
			if src.bits < 64 && tag > mask64(src.bits) {
				return nil, schemaErr(path, "case tag %d does not fit %d-bit tag field", tag, src.bits)
			}
			if _, dup := f.cases[tag]; dup {
				return nil, schemaErr(path, "duplicate case tag %d", tag)
			}
			if cs == nil || len(cs.Fields) == 0 {
				return nil, schemaErr(path, "case %d has no fields", tag)
			}
			caseFields, err := compileStruct(cs.Fields, path+"."+key, depth+1)
			if err != nil {
				return nil, err
			}
			f.cases[tag] = caseFields
			f.caseOrder = append(f.caseOrder, tag)
		}
		sort.Slice(f.caseOrder, func(i, j int) bool { return f.caseOrder[i] < f.caseOrder[j] })
		if len(src.enum) > 0 {
			for _, v := range src.enum {
				if _, ok := f.cases[v]; !ok {
					return nil, schemaErr(path, "missing case for declared tag %d", v)
				}
			}
			for _, tag := range f.caseOrder {
				if !containsUint(src.enum, tag) {
					return nil, schemaErr(path, "undefined tag %d: not in enum of %q", tag, spec.TagFrom)
				}
			}
		}
		for _, tag := range f.caseOrder {
			m := uint64(0)
			for i := range f.cases[tag] {
				if f.cases[tag][i].maxBits > math.MaxUint64-m {
					return nil, schemaErr(path, "case size is unbounded")
				}
				m += f.cases[tag][i].maxBits
			}
			if m > f.maxBits {
				f.maxBits = m
			}
		}

	case "align":
		f.typ = tAlign
		if spec.Multiple < 1 || spec.Multiple > 512 {
			return nil, schemaErr(path, "align multiple %d out of range 1..512", spec.Multiple)
		}
		f.multiple = spec.Multiple
		f.maxBits = uint64(spec.Multiple - 1)

	case "checksum":
		f.typ = tChecksum
		algo := spec.Algo
		if algo == "" {
			algo = "sum"
		}
		if algo != "sum" {
			return nil, schemaErr(path, "unknown checksum algo %q", spec.Algo)
		}
		if spec.Bits < 1 || spec.Bits > 64 {
			return nil, schemaErr(path, "checksum bits %d out of range 1..64", spec.Bits)
		}
		f.bits = spec.Bits
		if spec.From == "" || spec.To == "" {
			return nil, schemaErr(path, "checksum requires from and to")
		}
		if _, ok := prev[spec.From]; !ok {
			return nil, schemaErr(path, "checksum from %q is not a previously defined field", spec.From)
		}
		if _, ok := prev[spec.To]; !ok {
			return nil, schemaErr(path, "checksum to %q is not a previously defined field", spec.To)
		}
		fromIdx, toIdx := -1, -1
		for i, name := range order {
			if name == spec.From {
				fromIdx = i
			}
			if name == spec.To {
				toIdx = i
			}
		}
		if fromIdx > toIdx {
			return nil, schemaErr(path, "checksum range [%q, %q] is empty or inverted", spec.From, spec.To)
		}
		f.from = spec.From
		f.to = spec.To
		f.maxBits = uint64(spec.Bits)

	default:
		return nil, schemaErr(path, "unknown field type %q", spec.Type)
	}
	return f, nil
}

func containsUint(xs []uint64, v uint64) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
