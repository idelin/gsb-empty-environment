package netsec

import "fmt"

// Kind identifies an SSZ type.
type Kind uint8

const (
	KindBasic Kind = iota + 1
	KindVector
	KindList
	KindBitvector
	KindBitlist
	KindContainer
	KindUnion
)

// Type describes an SSZ type used as the schema to validate wire bytes.
// FixedSize is 0 for variable-size types. MaxSize bounds the wire length.
type Type interface {
	Kind() Kind
	FixedSize() uint64
	MaxSize() uint64
	valid() error
}

// BasicType covers bool, uint8, uint16, uint32, uint64 and uint256.
type BasicType struct {
	size   uint64
	isBool bool
}

// Kind implements Type.
func (t *BasicType) Kind() Kind { return KindBasic }

// FixedSize implements Type.
func (t *BasicType) FixedSize() uint64 { return t.size }

// MaxSize implements Type.
func (t *BasicType) MaxSize() uint64 { return t.size }

func (t *BasicType) valid() error {
	if t == nil {
		return fmt.Errorf("ssz: nil basic type")
	}
	switch t.size {
	case 1, 2, 4, 8, 32:
		return nil
	default:
		return fmt.Errorf("ssz: invalid basic type size %d", t.size)
	}
}

// Predeclared basic types.
var (
	BoolType    = &BasicType{size: 1, isBool: true}
	Uint8Type   = &BasicType{size: 1}
	Uint16Type  = &BasicType{size: 2}
	Uint32Type  = &BasicType{size: 4}
	Uint64Type  = &BasicType{size: 8}
	Uint256Type = &BasicType{size: 32}
)

// VectorType is a fixed-length homogeneous collection.
type VectorType struct {
	Elem     Type
	Length   uint64
	elemSize uint64
}

// NewVectorType constructs a vector of length elements of elem.
func NewVectorType(elem Type, length uint64) (*VectorType, error) {
	if length == 0 {
		return nil, fmt.Errorf("ssz: vector length must be non-zero")
	}
	if elem == nil {
		return nil, fmt.Errorf("ssz: vector element type is nil")
	}
	if err := elem.valid(); err != nil {
		return nil, err
	}
	t := &VectorType{Elem: elem, Length: length, elemSize: elem.FixedSize()}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustVectorType is like NewVectorType but panics on an invalid schema.
func MustVectorType(elem Type, length uint64) *VectorType {
	t, err := NewVectorType(elem, length)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *VectorType) Kind() Kind { return KindVector }

// FixedSize implements Type.
func (t *VectorType) FixedSize() uint64 {
	if t.elemSize == 0 {
		return 0
	}
	return t.elemSize * t.Length
}

// MaxSize implements Type.
func (t *VectorType) MaxSize() uint64 {
	total := t.Elem.MaxSize() * t.Length
	if t.elemSize == 0 {
		if mulOverflows(4, t.Length) || addOverflows(total, 4*t.Length) {
			return ^uint64(0)
		}
		total += 4 * t.Length // offset table prefixes the variable elements.
	}
	return total
}

func (t *VectorType) valid() error {
	if t == nil || t.Elem == nil {
		return fmt.Errorf("ssz: nil vector type")
	}
	if t.Length == 0 {
		return fmt.Errorf("ssz: vector length must be non-zero")
	}
	if err := t.Elem.valid(); err != nil {
		return err
	}
	if mulOverflows(t.Elem.MaxSize(), t.Length) {
		return fmt.Errorf("ssz: vector max size overflows uint64")
	}
	return nil
}

// ListType is a variable-length homogeneous collection bounded by MaxLength.
type ListType struct {
	Elem      Type
	MaxLength uint64
	elemSize  uint64
}

// NewListType constructs a list whose length must not exceed maxLength.
func NewListType(elem Type, maxLength uint64) (*ListType, error) {
	if elem == nil {
		return nil, fmt.Errorf("ssz: list element type is nil")
	}
	if maxLength == 0 {
		return nil, fmt.Errorf("ssz: list max length must be non-zero")
	}
	if err := elem.valid(); err != nil {
		return nil, err
	}
	t := &ListType{Elem: elem, MaxLength: maxLength, elemSize: elem.FixedSize()}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustListType is like NewListType but panics on an invalid schema.
func MustListType(elem Type, maxLength uint64) *ListType {
	t, err := NewListType(elem, maxLength)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *ListType) Kind() Kind { return KindList }

// FixedSize implements Type; lists are always variable-size.
func (t *ListType) FixedSize() uint64 { return 0 }

// MaxSize implements Type.
func (t *ListType) MaxSize() uint64 {
	total := t.Elem.MaxSize() * t.MaxLength
	if t.elemSize == 0 {
		if mulOverflows(4, t.MaxLength) || addOverflows(total, 4*t.MaxLength) {
			return ^uint64(0)
		}
		total += 4 * t.MaxLength // each variable element needs an offset slot.
	}
	return total
}

func (t *ListType) valid() error {
	if t == nil || t.Elem == nil {
		return fmt.Errorf("ssz: nil list type")
	}
	if t.MaxLength == 0 {
		return fmt.Errorf("ssz: list max length must be non-zero")
	}
	if err := t.Elem.valid(); err != nil {
		return err
	}
	if mulOverflows(t.Elem.MaxSize(), t.MaxLength) {
		return fmt.Errorf("ssz: list max size overflows uint64")
	}
	return nil
}

// BitvectorType is a fixed-length bit sequence with Length bits.
type BitvectorType struct {
	Length uint64
}

// NewBitvectorType constructs a bitvector of length bits.
func NewBitvectorType(length uint64) (*BitvectorType, error) {
	if length == 0 {
		return nil, fmt.Errorf("ssz: bitvector length must be non-zero")
	}
	t := &BitvectorType{Length: length}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustBitvectorType is like NewBitvectorType but panics on an invalid schema.
func MustBitvectorType(length uint64) *BitvectorType {
	t, err := NewBitvectorType(length)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *BitvectorType) Kind() Kind { return KindBitvector }

// FixedSize implements Type.
func (t *BitvectorType) FixedSize() uint64 { return (t.Length + 7) / 8 }

// MaxSize implements Type.
func (t *BitvectorType) MaxSize() uint64 { return t.FixedSize() }

func (t *BitvectorType) valid() error {
	if t == nil || t.Length == 0 {
		return fmt.Errorf("ssz: invalid bitvector length")
	}
	return nil
}

// BitlistType is a variable-length bit sequence with at most MaxLength bits.
// Every serialized bitlist carries a terminal delimiter bit set to 1.
type BitlistType struct {
	MaxLength uint64
}

// NewBitlistType constructs a bitlist bounded by maxLength bits.
func NewBitlistType(maxLength uint64) (*BitlistType, error) {
	if maxLength == 0 {
		return nil, fmt.Errorf("ssz: bitlist max length must be non-zero")
	}
	t := &BitlistType{MaxLength: maxLength}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustBitlistType is like NewBitlistType but panics on an invalid schema.
func MustBitlistType(maxLength uint64) *BitlistType {
	t, err := NewBitlistType(maxLength)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *BitlistType) Kind() Kind { return KindBitlist }

// FixedSize implements Type; bitlists are variable-size.
func (t *BitlistType) FixedSize() uint64 { return 0 }

// MaxSize implements Type; an extra byte may hold the delimiter.
func (t *BitlistType) MaxSize() uint64 { return t.MaxLength/8 + 1 }

func (t *BitlistType) valid() error {
	if t == nil || t.MaxLength == 0 {
		return fmt.Errorf("ssz: invalid bitlist max length")
	}
	return nil
}

// Field is a named container member.
type Field struct {
	Name string
	Type Type
}

// ContainerType is an ordered, fixed set of fields.
type ContainerType struct {
	Fields    []Field
	fixedSize uint64
	index     map[string]int
}

// NewContainerType constructs a container from the given fields.
func NewContainerType(fields []Field) (*ContainerType, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("ssz: container must have at least one field")
	}
	index := make(map[string]int, len(fields))
	var fixedSize uint64
	for i, f := range fields {
		if f.Type == nil {
			return nil, fmt.Errorf("ssz: container field %q has nil type", f.Name)
		}
		if err := f.Type.valid(); err != nil {
			return nil, fmt.Errorf("ssz: container field %q: %w", f.Name, err)
		}
		if f.Name != "" {
			if _, exists := index[f.Name]; exists {
				return nil, fmt.Errorf("ssz: duplicate container field %q", f.Name)
			}
			index[f.Name] = i
		}
		if fs := f.Type.FixedSize(); fs != 0 {
			if addOverflows(fixedSize, fs) {
				return nil, fmt.Errorf("ssz: container fixed size overflows uint64")
			}
			fixedSize += fs
		} else {
			if addOverflows(fixedSize, 4) {
				return nil, fmt.Errorf("ssz: container fixed size overflows uint64")
			}
			fixedSize += 4
		}
	}
	t := &ContainerType{Fields: fields, fixedSize: fixedSize, index: index}
	if err := t.valid(); err != nil {
		return nil, err
	}
	return t, nil
}

// MustContainerType is like NewContainerType but panics on an invalid schema.
func MustContainerType(fields []Field) *ContainerType {
	t, err := NewContainerType(fields)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *ContainerType) Kind() Kind { return KindContainer }

// FixedSize implements Type; non-zero only when every field is fixed-size.
func (t *ContainerType) FixedSize() uint64 {
	for _, f := range t.Fields {
		if f.Type.FixedSize() == 0 {
			return 0
		}
	}
	return t.fixedSize
}

// MaxSize implements Type.
func (t *ContainerType) MaxSize() uint64 {
	var total uint64
	for _, f := range t.Fields {
		if f.Type.FixedSize() == 0 {
			if addOverflows(total, 4) {
				return ^uint64(0)
			}
			total += 4 // offset slot in the fixed part
		}
		if addOverflows(total, f.Type.MaxSize()) {
			return ^uint64(0)
		}
		total += f.Type.MaxSize()
	}
	return total
}

// FieldIndex returns the positional index of a named field and whether it exists.
func (t *ContainerType) FieldIndex(name string) (int, bool) {
	i, ok := t.index[name]
	return i, ok
}

func (t *ContainerType) valid() error {
	if t == nil || len(t.Fields) == 0 {
		return fmt.Errorf("ssz: invalid container type")
	}
	for i, f := range t.Fields {
		if f.Type == nil {
			return fmt.Errorf("ssz: container field %d has nil type", i)
		}
		if err := f.Type.valid(); err != nil {
			return err
		}
	}
	return nil
}

// UnionType is a tagged choice over one of its member types. The wire form is
// a one-byte selector followed by the member encoding, prefixed by a relative
// offset when the selected member is variable-size.
type UnionType struct {
	Options []Type
}

// NewUnionType constructs a union over the given member types.
func NewUnionType(options []Type) (*UnionType, error) {
	if len(options) == 0 {
		return nil, fmt.Errorf("ssz: union must have at least one option")
	}
	if len(options) > 255 {
		return nil, fmt.Errorf("ssz: union may have at most 255 options")
	}
	for i, opt := range options {
		if opt == nil {
			return nil, fmt.Errorf("ssz: union option %d is nil", i)
		}
		if err := opt.valid(); err != nil {
			return nil, fmt.Errorf("ssz: union option %d: %w", i, err)
		}
	}
	return &UnionType{Options: options}, nil
}

// MustUnionType is like NewUnionType but panics on an invalid schema.
func MustUnionType(options []Type) *UnionType {
	t, err := NewUnionType(options)
	if err != nil {
		panic(err)
	}
	return t
}

// Kind implements Type.
func (t *UnionType) Kind() Kind { return KindUnion }

// FixedSize implements Type; unions are always variable-size.
func (t *UnionType) FixedSize() uint64 { return 0 }

// MaxSize implements Type (selector byte, optional offset and largest member).
func (t *UnionType) MaxSize() uint64 {
	var max uint64
	for _, opt := range t.Options {
		if opt.MaxSize() > max {
			max = opt.MaxSize()
		}
	}
	return max + 1 + 4
}

func (t *UnionType) valid() error {
	if t == nil || len(t.Options) == 0 || len(t.Options) > 255 {
		return fmt.Errorf("ssz: invalid union type")
	}
	for i, opt := range t.Options {
		if opt == nil {
			return fmt.Errorf("ssz: union option %d is nil", i)
		}
		if err := opt.valid(); err != nil {
			return err
		}
	}
	return nil
}

func mulOverflows(a, b uint64) bool {
	if a == 0 || b == 0 {
		return false
	}
	return a > ^uint64(0)/b
}

func addOverflows(a, b uint64) bool { return a > ^uint64(0)-b }
