package netsec

import "fmt"

// Kind is a BTF type kind. The numeric values match the BTF specification so
// callers can build a Type directly from parsed BTF.
type Kind uint8

const (
	KindVoid      Kind = 0
	KindUnknown        = KindVoid
	KindInt       Kind = 1
	KindPtr       Kind = 2
	KindArray     Kind = 3
	KindStruct    Kind = 4
	KindUnion     Kind = 5
	KindEnum      Kind = 6
	KindFwd       Kind = 7
	KindTypedef   Kind = 8
	KindVolatile  Kind = 9
	KindConst     Kind = 10
	KindRestrict  Kind = 11
	KindFunc      Kind = 12
	KindFuncProto Kind = 13
	KindVar       Kind = 14
	KindDatasec   Kind = 15
	KindFloat     Kind = 16
	KindDeclTag   Kind = 17
	KindTypeTag   Kind = 18
	KindEnum64    Kind = 19
)

func (k Kind) String() string {
	switch k {
	case KindVoid:
		return "void"
	case KindInt:
		return "int"
	case KindPtr:
		return "ptr"
	case KindArray:
		return "array"
	case KindStruct:
		return "struct"
	case KindUnion:
		return "union"
	case KindEnum:
		return "enum"
	case KindFwd:
		return "fwd"
	case KindTypedef:
		return "typedef"
	case KindVolatile:
		return "volatile"
	case KindConst:
		return "const"
	case KindRestrict:
		return "restrict"
	case KindFunc:
		return "func"
	case KindFuncProto:
		return "func_proto"
	case KindVar:
		return "var"
	case KindDatasec:
		return "datasec"
	case KindFloat:
		return "float"
	case KindDeclTag:
		return "decl_tag"
	case KindTypeTag:
		return "type_tag"
	case KindEnum64:
		return "enum64"
	default:
		return fmt.Sprintf("kind(%d)", uint8(k))
	}
}

// IntEncoding describes the signedness and special integer flavours stored in
// a BTF integer type.
type IntEncoding uint8

const (
	IntSigned IntEncoding = 1 << iota
	IntChar
	IntBool
)

// Member is one field of a struct or union. BitOffset is the absolute bit
// offset inside the containing type. BitSize is zero for ordinary members and
// the bit-field width for bit-field members.
type Member struct {
	Name      string
	TypeID    uint32
	BitOffset uint32
	BitSize   uint32
}

// Type is the subset of a BTF type needed to validate CO-RE field
// relocations. Size is measured in bytes. TypeID is the referenced type for
// pointers, typedefs and qualifiers; ElemTypeID is the element type for an
// array. IntBits defaults to Size*8 when omitted.
type Type struct {
	Name        string
	Kind        Kind
	Size        uint32
	TypeID      uint32
	ElemTypeID  uint32
	Nelems      uint32
	Members     []Member
	IntBits     uint32
	IntEncoding IntEncoding
	EnumSigned  bool
}

// BTF is an index of caller-supplied BTF types. Type IDs are map keys; there
// is no requirement that the map contain an ID zero entry. PointerSize is the
// target pointer width in bytes and defaults to eight.
type BTF struct {
	Types       map[uint32]Type
	PointerSize uint32
}

// NewBTF returns an empty BTF index with the common 64-bit pointer width.
func NewBTF() *BTF {
	return &BTF{Types: make(map[uint32]Type), PointerSize: 8}
}

// Add stores a type under id.
func (b *BTF) Add(id uint32, typ Type) {
	if b.Types == nil {
		b.Types = make(map[uint32]Type)
	}
	b.Types[id] = typ
}

// Type returns the type stored under id.
func (b *BTF) Type(id uint32) (Type, bool) {
	if b == nil || b.Types == nil {
		return Type{}, false
	}
	typ, ok := b.Types[id]
	return typ, ok
}

func (b *BTF) pointerSize() (uint32, error) {
	if b == nil {
		return 0, fmt.Errorf("%w: nil BTF", ErrInvalidBTF)
	}
	switch b.PointerSize {
	case 0:
		return 8, nil
	case 4, 8:
		return b.PointerSize, nil
	default:
		return 0, fmt.Errorf("%w: invalid pointer size %d", ErrInvalidBTF, b.PointerSize)
	}
}

// SizeOf returns the byte size of a BTF type, resolving typedefs, qualifiers,
// arrays and pointers. It rejects cycles and incomplete types.
func (b *BTF) SizeOf(id uint32) (uint32, error) {
	return b.sizeOf(id, make(map[uint32]bool), 0)
}

func (b *BTF) sizeOf(id uint32, active map[uint32]bool, depth int) (uint32, error) {
	if depth > 64 {
		return 0, fmt.Errorf("%w: type graph is too deep", ErrInvalidBTF)
	}
	if id == 0 {
		return 0, nil
	}
	typ, ok := b.Type(id)
	if !ok {
		return 0, fmt.Errorf("%w: type ID %d", ErrUnknownType, id)
	}
	if active[id] {
		return 0, fmt.Errorf("%w: cycle through type ID %d", ErrInvalidBTF, id)
	}
	active[id] = true
	defer delete(active, id)

	switch typ.Kind {
	case KindVoid:
		return 0, nil
	case KindInt:
		if typ.Size != 0 {
			return typ.Size, nil
		}
		bits := typ.IntBits
		if bits == 0 {
			return 0, fmt.Errorf("%w: int type ID %d has no size", ErrInvalidBTF, id)
		}
		return (bits + 7) / 8, nil
	case KindStruct, KindUnion, KindEnum, KindEnum64, KindFloat:
		if typ.Size == 0 && (typ.Kind == KindEnum || typ.Kind == KindEnum64 || typ.Kind == KindFloat) {
			return 0, fmt.Errorf("%w: %s type ID %d has no size", ErrInvalidBTF, typ.Kind, id)
		}
		return typ.Size, nil
	case KindPtr:
		return b.pointerSize()
	case KindArray:
		if typ.ElemTypeID == 0 {
			return 0, fmt.Errorf("%w: array type ID %d has no element type", ErrInvalidBTF, id)
		}
		elem, err := b.sizeOf(typ.ElemTypeID, active, depth+1)
		if err != nil {
			return 0, err
		}
		if typ.Nelems != 0 && elem == 0 {
			return 0, fmt.Errorf("%w: array type ID %d has zero-sized element", ErrInvalidBTF, id)
		}
		if typ.Nelems == 0 {
			return 0, nil
		}
		const maxUint32 = ^uint32(0)
		if typ.Nelems > maxUint32/elem {
			return 0, fmt.Errorf("%w: array type ID %d overflows", ErrInvalidBTF, id)
		}
		return elem * typ.Nelems, nil
	case KindTypedef, KindVolatile, KindConst, KindRestrict, KindTypeTag:
		if typ.TypeID == 0 {
			return 0, fmt.Errorf("%w: %s type ID %d has no referenced type", ErrInvalidBTF, typ.Kind, id)
		}
		return b.sizeOf(typ.TypeID, active, depth+1)
	default:
		return 0, fmt.Errorf("%w: %s type ID %d has no byte size", ErrInvalidBTF, typ.Kind, id)
	}
}

func (b *BTF) peelModifiers(id uint32) (uint32, Type, error) {
	for depth := 0; depth < 64; depth++ {
		typ, ok := b.Type(id)
		if !ok {
			return 0, Type{}, fmt.Errorf("%w: type ID %d", ErrUnknownType, id)
		}
		switch typ.Kind {
		case KindTypedef, KindVolatile, KindConst, KindRestrict, KindTypeTag:
			if typ.TypeID == 0 {
				return 0, Type{}, fmt.Errorf("%w: %s type ID %d has no referenced type", ErrInvalidBTF, typ.Kind, id)
			}
			id = typ.TypeID
		default:
			return id, typ, nil
		}
	}
	return 0, Type{}, fmt.Errorf("%w: modifier chain is too deep", ErrInvalidBTF)
}

func effectiveIntBits(typ Type) uint32 {
	if typ.IntBits != 0 {
		return typ.IntBits
	}
	return typ.Size * 8
}

// typesCompatible reports whether a target type is a safe replacement for a
// local type. Transparent typedefs and qualifiers are peeled. Integer kinds,
// encodings and bit widths must match exactly, which is what prevents a u8
// target field from satisfying a u64 access. Composite target types must be
// at least as large as their local counterparts.
func typesCompatible(local *BTF, localID uint32, target *BTF, targetID uint32) error {
	return typesCompatibleDepth(local, localID, target, targetID, 0)
}

func typesCompatibleDepth(local *BTF, localID uint32, target *BTF, targetID uint32, depth int) error {
	if depth > 32 {
		return fmt.Errorf("%w: type nesting is too deep", ErrTypeIncompatible)
	}
	_, lt, err := local.peelModifiers(localID)
	if err != nil {
		return err
	}
	_, tt, err := target.peelModifiers(targetID)
	if err != nil {
		return err
	}
	if lt.Kind != tt.Kind {
		return fmt.Errorf("%w: local kind %s, target kind %s", ErrTypeIncompatible, lt.Kind, tt.Kind)
	}

	switch lt.Kind {
	case KindInt:
		if lt.IntEncoding != tt.IntEncoding {
			return fmt.Errorf("%w: integer encoding differs", ErrTypeIncompatible)
		}
		if effectiveIntBits(lt) != effectiveIntBits(tt) {
			return fmt.Errorf("%w: integer width %d != %d", ErrTypeIncompatible, effectiveIntBits(lt), effectiveIntBits(tt))
		}
		return nil
	case KindEnum, KindEnum64:
		if lt.Size != tt.Size || lt.EnumSigned != tt.EnumSigned {
			return fmt.Errorf("%w: %s size or signedness differs", ErrTypeIncompatible, lt.Kind)
		}
		return nil
	case KindFloat:
		if lt.Size != tt.Size {
			return fmt.Errorf("%w: float size %d != %d", ErrTypeIncompatible, lt.Size, tt.Size)
		}
		return nil
	case KindPtr:
		lp, err := local.pointerSize()
		if err != nil {
			return err
		}
		tp, err := target.pointerSize()
		if err != nil {
			return err
		}
		if lp != tp {
			return fmt.Errorf("%w: pointer size %d != %d", ErrTypeIncompatible, lp, tp)
		}
		return nil
	case KindArray:
		if lt.Nelems != tt.Nelems {
			return fmt.Errorf("%w: array elements %d != %d", ErrTypeIncompatible, lt.Nelems, tt.Nelems)
		}
		if lt.ElemTypeID == 0 || tt.ElemTypeID == 0 {
			return fmt.Errorf("%w: array has no element type", ErrTypeIncompatible)
		}
		return typesCompatibleDepth(local, lt.ElemTypeID, target, tt.ElemTypeID, depth+1)
	case KindStruct, KindUnion:
		lsize, err := local.SizeOf(localID)
		if err != nil {
			return err
		}
		tsize, err := target.SizeOf(targetID)
		if err != nil {
			return err
		}
		if tsize < lsize {
			return fmt.Errorf("%w: target %s size %d < local size %d", ErrTypeIncompatible, lt.Kind, tsize, lsize)
		}
		return nil
	case KindFwd:
		if lt.Name != "" && tt.Name != "" && lt.Name != tt.Name {
			return fmt.Errorf("%w: forward declaration %q != %q", ErrTypeIncompatible, lt.Name, tt.Name)
		}
		return nil
	case KindVoid:
		return nil
	default:
		return fmt.Errorf("%w: types of kind %s are not fields", ErrTypeIncompatible, lt.Kind)
	}
}

// sameKind reports the weaker compatibility needed by relocations whose
// result is a kind-dependent property rather than a fixed-size access.
func sameKind(local *BTF, localID uint32, target *BTF, targetID uint32) error {
	_, lt, err := local.peelModifiers(localID)
	if err != nil {
		return err
	}
	_, tt, err := target.peelModifiers(targetID)
	if err != nil {
		return err
	}
	if lt.Kind != tt.Kind {
		return fmt.Errorf("%w: local kind %s, target kind %s", ErrTypeIncompatible, lt.Kind, tt.Kind)
	}
	return nil
}
