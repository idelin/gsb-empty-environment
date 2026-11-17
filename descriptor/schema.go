package descriptor

import (
	"fmt"
	"math"
	"sort"
)

// Address is a byte address in the device address space.
type Address uint64

// Access is a set of permissions or the kind of a single operation.
type Access uint8

const (
	AccessRead Access = 1 << iota
	AccessWrite
)

const (
	// AccessAll grants both reads and writes.
	AccessAll = AccessRead | AccessWrite
)

func (a Access) String() string {
	switch a {
	case AccessRead:
		return "read"
	case AccessWrite:
		return "write"
	case AccessAll:
		return "read-write"
	case 0:
		return "none"
	default:
		return fmt.Sprintf("access(%d)", uint8(a))
	}
}

// Region is an authorized byte range. Allow is a set of Access bits.
type Region struct {
	Start Address
	Size  uint64
	Allow Access
}

// RegionSet is a static authorization policy. Overlapping and adjacent
// regions are allowed; a request is permitted when the union of regions with
// the required permission covers the whole requested span.
type RegionSet struct {
	regions []Region
}

// NewRegionSet validates and copies regions into an authorization policy.
func NewRegionSet(regions ...Region) (*RegionSet, error) {
	copied := append([]Region(nil), regions...)
	for i, r := range copied {
		if r.Size == 0 {
			return nil, fmt.Errorf("%w: region %d has zero size", ErrSchema, i)
		}
		if r.Allow == 0 || r.Allow&^AccessAll != 0 {
			return nil, fmt.Errorf("%w: region %d has invalid permissions %d", ErrSchema, i, r.Allow)
		}
		if r.Size > math.MaxUint64-uint64(r.Start) {
			return nil, fmt.Errorf("%w: region %d overflows address space", ErrSchema, i)
		}
	}
	sort.Slice(copied, func(i, j int) bool {
		if copied[i].Start != copied[j].Start {
			return copied[i].Start < copied[j].Start
		}
		return copied[i].Size < copied[j].Size
	})
	return &RegionSet{regions: copied}, nil
}

// Regions returns a copy of the configured regions.
func (r *RegionSet) Regions() []Region {
	if r == nil {
		return nil
	}
	return append([]Region(nil), r.regions...)
}

// Authorize implements Authorizer.
func (r *RegionSet) Authorize(addr Address, size uint64, access Access) error {
	if access == 0 || size == 0 {
		return fmt.Errorf("%w: invalid request (address %#x size %d access %s)",
			ErrUnauthorized, uint64(addr), size, access)
	}
	if size > math.MaxUint64-uint64(addr) {
		return fmt.Errorf("%w: access at %#x size %d overflows address space",
			ErrUnauthorized, uint64(addr), size)
	}
	if r == nil {
		return fmt.Errorf("%w: no regions configured", ErrUnauthorized)
	}
	start := uint64(addr)
	end := start + size
	covered := start
	for _, region := range r.regions {
		if region.Allow&access != access {
			continue
		}
		regionStart := uint64(region.Start)
		regionEnd := uint64(region.Start) + region.Size
		if regionEnd <= covered {
			continue
		}
		if regionStart > covered {
			break
		}
		covered = regionEnd
		if covered >= end {
			return nil
		}
	}
	return fmt.Errorf("%w: %s at %#x size %d", ErrUnauthorized, access, uint64(addr), size)
}

// Authorizer decides whether a device access is permitted. Authorize must
// accept the whole span [addr, addr+size) for the requested access and must
// not perform the access itself.
type Authorizer interface {
	Authorize(addr Address, size uint64, access Access) error
}

// AuthorizerFunc adapts a function to Authorizer.
type AuthorizerFunc func(addr Address, size uint64, access Access) error

func (f AuthorizerFunc) Authorize(addr Address, size uint64, access Access) error {
	if f == nil {
		return fmt.Errorf("%w: nil authorizer", ErrNil)
	}
	return f(addr, size, access)
}

// FieldKind describes how an instruction field is interpreted.
type FieldKind uint8

const (
	// FieldIgnored is not an address and is not validated.
	FieldIgnored FieldKind = iota
	// FieldRead is a direct read address.
	FieldRead
	// FieldWrite is a direct write address.
	FieldWrite
	// FieldIndirect points at another descriptor table.
	FieldIndirect
)

func (k FieldKind) String() string {
	switch k {
	case FieldIgnored:
		return "ignored"
	case FieldRead:
		return "read"
	case FieldWrite:
		return "write"
	case FieldIndirect:
		return "indirect"
	default:
		return fmt.Sprintf("field-kind(%d)", uint8(k))
	}
}

// AddressSemantics maps the raw bits of an address field to a byte address.
//
// The transformation is:
//
//	value := raw
//	if Mask != 0 { value &= Mask }
//	value >>= Shift
//	value *= Scale      // Scale == 0 means 1
//	value += Bias
//
// This covers the common cases of page-number fields, scaled offsets, bit
// fields, and fixed address biases without allowing arbitrary code in the
// untrusted data path.
type AddressSemantics struct {
	Mask  uint64
	Shift uint
	Scale uint64
	Bias  int64
}

func (s AddressSemantics) resolve(raw uint64) (Address, error) {
	value := raw
	if s.Mask != 0 {
		value &= s.Mask
	}
	if s.Shift >= 64 {
		if value != 0 {
			return 0, ErrOverflow
		}
		value = 0
	} else {
		value >>= s.Shift
	}
	scale := s.Scale
	if scale == 0 {
		scale = 1
	}
	if value != 0 && scale > math.MaxUint64/value {
		return 0, ErrOverflow
	}
	value *= scale
	if s.Bias >= 0 {
		bias := uint64(s.Bias)
		if value > math.MaxUint64-bias {
			return 0, ErrOverflow
		}
		value += bias
	} else {
		// Compute the magnitude without overflowing on math.MinInt64.
		bias := uint64(-(s.Bias + 1)) + 1
		if value < bias {
			return 0, ErrOverflow
		}
		value -= bias
	}
	return Address(value), nil
}

// AddressField describes one field of an instruction pattern.
type AddressField struct {
	Name string
	// BitOffset is the least-significant-bit-first offset from the start of
	// the entry. Bit 0 is the LSB of entry byte 0.
	BitOffset uint
	BitWidth  uint
	Kind      FieldKind
	// AccessSize is the number of bytes touched by a read or write. When it
	// is zero, the byte width of the field is used.
	AccessSize uint64
	Semantics  AddressSemantics
	// Table is required for FieldIndirect. Schema tables may be recursive.
	Table *Table
}

func (f AddressField) accessSize() uint64 {
	if f.AccessSize != 0 {
		return f.AccessSize
	}
	return uint64((f.BitWidth + 7) / 8)
}

// OpcodeField locates the opcode inside an entry. Bit numbering is the same
// as AddressField.
type OpcodeField struct {
	BitOffset uint
	BitWidth  uint
}

// Pattern is one allowed instruction shape. A pattern matches when
//
//	extractedOpcode & Mask == Value
//
// A zero Mask means "all bits of the opcode field". Value is the opcode.
type Pattern struct {
	Name   string
	Value  uint64
	Mask   uint64
	Fields []AddressField
}

// Format is the fixed-size instruction format of a descriptor table.
type Format struct {
	EntrySize uint64
	Opcode    OpcodeField
	Patterns  []Pattern
}

// CountMode selects how the number of entries in an indirect table is found.
type CountMode uint8

const (
	// CountFixed uses CountSpec.Value as the entry count.
	CountFixed CountMode = iota
	// CountField reads the entry count from a scalar field at the table base.
	CountField
	// CountBytesField reads the byte length of the entries area from a scalar
	// field at the table base. A length that is not a multiple of EntrySize is
	// an invalid trailing entry.
	CountBytesField
)

// ScalarField is a small unsigned integer in a table header.
type ScalarField struct {
	ByteOffset uint64
	ByteSize   uint64
	BigEndian  bool
}

func (f ScalarField) read(data []byte) (uint64, error) {
	if f.ByteSize != 1 && f.ByteSize != 2 && f.ByteSize != 4 && f.ByteSize != 8 {
		return 0, fmt.Errorf("%w: scalar field size must be 1, 2, 4, or 8", ErrSchema)
	}
	if f.ByteOffset > uint64(len(data)) || f.ByteSize > uint64(len(data))-f.ByteOffset {
		return 0, fmt.Errorf("%w: scalar field at %#x size %d exceeds %d-byte header",
			ErrBadDescriptor, f.ByteOffset, f.ByteSize, len(data))
	}
	b := data[f.ByteOffset : f.ByteOffset+f.ByteSize]
	var value uint64
	if f.BigEndian {
		for _, v := range b {
			value = value<<8 | uint64(v)
		}
	} else {
		for i := len(b) - 1; i >= 0; i-- {
			value = value<<8 | uint64(b[i])
		}
	}
	return value, nil
}

// CountSpec describes the entry count of an indirect table.
type CountSpec struct {
	Mode  CountMode
	Value uint64
	Field *ScalarField
}

// Table describes an indirectly referenced descriptor table.
type Table struct {
	Format *Format
	// HeaderSize is the number of bytes before the first entry.
	HeaderSize uint64
	Count      CountSpec
	// MaxEntries optionally tightens the validator-wide entry limit for this
	// table. Zero means no table-specific limit.
	MaxEntries uint64
}

// Request describes a top-level descriptor table.
type Request struct {
	Address Address
	Length  uint64
	Format  *Format
}

// Descriptor is an alias for Request.
type Descriptor = Request

func extractBits(buf []byte, offset, width uint) (uint64, error) {
	if width == 0 || width > 64 {
		return 0, fmt.Errorf("%w: bit width must be in [1, 64]", ErrSchema)
	}
	if offset > uint(math.MaxUint32) || width > uint(math.MaxUint32) ||
		uint64(offset)+uint64(width) > uint64(len(buf))*8 {
		return 0, fmt.Errorf("%w: bit field [%d,%d) exceeds %d-byte entry",
			ErrBadDescriptor, offset, offset+width, len(buf))
	}
	var value uint64
	for i := uint(0); i < width; i++ {
		bit := offset + i
		if buf[bit/8]&(1<<(bit%8)) != 0 {
			value |= uint64(1) << i
		}
	}
	return value, nil
}

func patternMask(p Pattern, opcodeBits uint) uint64 {
	if p.Mask != 0 {
		return p.Mask
	}
	if opcodeBits >= 64 {
		return math.MaxUint64
	}
	return (uint64(1) << opcodeBits) - 1
}

type schemaWalker struct {
	formats map[*Format]bool
	tables  map[*Table]bool
}

func validateFormat(f *Format) error {
	w := &schemaWalker{
		formats: make(map[*Format]bool),
		tables:  make(map[*Table]bool),
	}
	return w.format(f)
}

func (w *schemaWalker) format(f *Format) error {
	if f == nil {
		return fmt.Errorf("%w: nil format", ErrSchema)
	}
	if w.formats[f] {
		return nil
	}
	w.formats[f] = true
	if f.EntrySize == 0 {
		return fmt.Errorf("%w: entry size must be positive", ErrSchema)
	}
	if f.EntrySize > math.MaxUint64/8 {
		return fmt.Errorf("%w: entry size %d is too large", ErrSchema, f.EntrySize)
	}
	if f.Opcode.BitWidth == 0 || f.Opcode.BitWidth > 64 {
		return fmt.Errorf("%w: opcode width must be in [1, 64]", ErrSchema)
	}
	if f.Opcode.BitOffset > uint(math.MaxUint32) ||
		uint64(f.Opcode.BitOffset)+uint64(f.Opcode.BitWidth) > f.EntrySize*8 {
		return fmt.Errorf("%w: opcode lies outside %d-byte entry", ErrSchema, f.EntrySize)
	}
	if len(f.Patterns) == 0 {
		return fmt.Errorf("%w: format has no instruction patterns", ErrSchema)
	}
	for i := range f.Patterns {
		p := &f.Patterns[i]
		name := p.Name
		if name == "" {
			name = fmt.Sprintf("pattern-%d", i)
		}
		mask := patternMask(*p, f.Opcode.BitWidth)
		if p.Mask != 0 && mask&^patternMask(Pattern{}, f.Opcode.BitWidth) != 0 {
			return fmt.Errorf("%w: pattern %q mask has bits outside the opcode field", ErrSchema, name)
		}
		if p.Value&^mask != 0 {
			return fmt.Errorf("%w: pattern %q value %#x does not fit mask %#x", ErrSchema, name, p.Value, mask)
		}
		for j := range p.Fields {
			if err := w.field(&p.Fields[j], f); err != nil {
				return fmt.Errorf("pattern %q field %q: %w", name, p.Fields[j].Name, err)
			}
		}
	}
	return nil
}

func (w *schemaWalker) field(f *AddressField, format *Format) error {
	if f.Name == "" {
		return fmt.Errorf("%w: address field has no name", ErrSchema)
	}
	if f.BitWidth == 0 || f.BitWidth > 64 {
		return fmt.Errorf("%w: bit width must be in [1, 64]", ErrSchema)
	}
	if f.BitOffset > uint(math.MaxUint32) ||
		uint64(f.BitOffset)+uint64(f.BitWidth) > format.EntrySize*8 {
		return fmt.Errorf("%w: field lies outside %d-byte entry", ErrSchema, format.EntrySize)
	}
	switch f.Kind {
	case FieldIgnored:
		return nil
	case FieldRead, FieldWrite:
		if f.Table != nil {
			return fmt.Errorf("%w: direct address field must not have a table", ErrSchema)
		}
	case FieldIndirect:
		if f.Table == nil {
			return fmt.Errorf("%w: indirect address field needs a table schema", ErrSchema)
		}
		if err := w.table(f.Table); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unknown field kind %d", ErrSchema, f.Kind)
	}
	if f.Semantics.Shift >= 64 {
		return fmt.Errorf("%w: address shift must be less than 64", ErrSchema)
	}
	return nil
}

func (w *schemaWalker) table(t *Table) error {
	if t == nil {
		return fmt.Errorf("%w: nil table schema", ErrSchema)
	}
	if w.tables[t] {
		return nil
	}
	w.tables[t] = true
	if err := w.format(t.Format); err != nil {
		return err
	}
	if t.Count.Mode != CountFixed && t.Count.Mode != CountField && t.Count.Mode != CountBytesField {
		return fmt.Errorf("%w: unknown count mode %d", ErrSchema, t.Count.Mode)
	}
	if t.Count.Mode != CountFixed {
		if t.Count.Field == nil {
			return fmt.Errorf("%w: count mode requires a scalar field", ErrSchema)
		}
		sf := t.Count.Field
		if sf.ByteSize != 1 && sf.ByteSize != 2 && sf.ByteSize != 4 && sf.ByteSize != 8 {
			return fmt.Errorf("%w: scalar field size must be 1, 2, 4, or 8", ErrSchema)
		}
		if sf.ByteOffset > math.MaxUint64-sf.ByteSize || sf.ByteOffset+sf.ByteSize > t.HeaderSize {
			return fmt.Errorf("%w: count field lies outside table header", ErrSchema)
		}
	}
	return nil
}
