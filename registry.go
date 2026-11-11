package wiretypes

import (
	"fmt"
	"reflect"
	"sort"
	"sync/atomic"
)

// TypeSpec binds a stable TypeID to a factory and a structural description.
// Factory must be a pure, side-effect-free constructor returning a pointer to
// the Go struct that carries this type's fields.
type TypeSpec struct {
	ID     TypeID
	Base   TypeID
	New    func() any
	Fields []FieldSpec
}

// FieldSpec describes one field of a registered type.
type FieldSpec struct {
	// Name is the Go struct field name and the wire field name.
	Name string
	// Kind is the required wire value kind.
	Kind ValueKind
	// Elem describes list elements and is required exactly when Kind is KindList.
	Elem *FieldSpec
	// RefType is the required root type of referenced objects and is required
	// exactly when Kind (or a list element's kind) is KindRef.
	RefType TypeID
	// Nullable permits KindNull at this field.
	Nullable bool
}

type compiledField struct {
	spec  FieldSpec
	index []int
}

type compiledType struct {
	id         TypeID
	base       TypeID
	prototype  reflect.Type
	newFunc    func() any
	fields     []*compiledField
	fieldIndex map[string]*compiledField
}

// Snapshot is an immutable view of the registry as of one point in time.
// Decoders hold snapshots, never the live registry, so hot swap is invisible
// to messages already in flight.
type Snapshot struct {
	types map[TypeID]*compiledType
	order []TypeID
}

// Lookup returns the compiled type for id, or nil when it is absent.
func (s *Snapshot) Lookup(id TypeID) *compiledType {
	if s == nil {
		return nil
	}
	return s.types[id]
}

// IDs returns the registered type IDs in lexicographic order.
func (s *Snapshot) IDs() []TypeID {
	if s == nil {
		return nil
	}
	out := make([]TypeID, len(s.order))
	copy(out, s.order)
	return out
}

// Registry is a swappable catalog of negotiated types. The zero value is not
// usable; create one with NewRegistry.
type Registry struct {
	snap atomic.Pointer[Snapshot]
}

// NewRegistry builds a registry from specs, failing atomically on any invalid
// spec without leaving partial state.
func NewRegistry(specs []TypeSpec) (*Registry, error) {
	snap, err := compile(specs)
	if err != nil {
		return nil, err
	}
	r := &Registry{}
	r.snap.Store(snap)
	return r, nil
}

// Swap atomically replaces the whole catalog. On error the previously
// installed snapshot stays in effect.
func (r *Registry) Swap(specs []TypeSpec) error {
	snap, err := compile(specs)
	if err != nil {
		return err
	}
	r.snap.Store(snap)
	return nil
}

// Snapshot returns the registry snapshot currently published.
func (r *Registry) Snapshot() *Snapshot { return r.snap.Load() }

func compile(specs []TypeSpec) (*Snapshot, error) {
	if len(specs) == 0 {
		return nil, fmt.Errorf("%w: no types registered", ErrRegistry)
	}
	snap := &Snapshot{types: make(map[TypeID]*compiledType, len(specs))}
	for i := range specs {
		spec := &specs[i]
		if spec.ID == "" {
			return nil, fmt.Errorf("%w: type at index %d has empty id", ErrRegistry, i)
		}
		if _, dup := snap.types[spec.ID]; dup {
			return nil, fmt.Errorf("%w: duplicate type id %q", ErrRegistry, spec.ID)
		}
		if spec.New == nil {
			return nil, fmt.Errorf("%w: type %q has no factory", ErrRegistry, spec.ID)
		}
		proto := spec.New()
		if proto == nil {
			return nil, fmt.Errorf("%w: type %q factory returned nil", ErrRegistry, spec.ID)
		}
		t := reflect.TypeOf(proto)
		if t.Kind() != reflect.Ptr || t.Elem().Kind() != reflect.Struct {
			return nil, fmt.Errorf("%w: type %q factory must return pointer to struct", ErrRegistry, spec.ID)
		}
		ct := &compiledType{
			id:         spec.ID,
			base:       spec.Base,
			prototype:  t,
			newFunc:    spec.New,
			fieldIndex: make(map[string]*compiledField),
		}
		snap.types[spec.ID] = ct
	}

	for _, spec := range specs {
		ct := snap.types[spec.ID]
		if ct.base != "" {
			base := snap.types[ct.base]
			if base == nil {
				return nil, fmt.Errorf("%w: type %q extends unknown base %q", ErrRegistry, ct.id, ct.base)
			}
			if err := checkBaseChain(snap, ct.id, map[TypeID]bool{}); err != nil {
				return nil, err
			}
		}
		seen := map[string]bool{}
		for i := range spec.Fields {
			fs := &spec.Fields[i]
			if fs.Name == "" {
				return nil, fmt.Errorf("%w: type %q field %d has empty name", ErrRegistry, ct.id, i)
			}
			if seen[fs.Name] {
				return nil, fmt.Errorf("%w: type %q has duplicate field %q", ErrRegistry, ct.id, fs.Name)
			}
			seen[fs.Name] = true
			if err := validateFieldShape(snap, ct, fs); err != nil {
				return nil, err
			}
			structField, ok := ct.prototype.Elem().FieldByName(fs.Name)
			if !ok {
				return nil, fmt.Errorf("%w: type %q has no struct field %q", ErrRegistry, ct.id, fs.Name)
			}
			if err := checkFieldGoType(snap, structField.Type, fs); err != nil {
				return nil, fmt.Errorf("%w: type %q field %q: %v", ErrRegistry, ct.id, fs.Name, err)
			}
			cf := &compiledField{spec: *fs, index: structField.Index}
			ct.fields = append(ct.fields, cf)
			ct.fieldIndex[fs.Name] = cf
		}
	}

	snap.order = make([]TypeID, 0, len(snap.types))
	for id := range snap.types {
		snap.order = append(snap.order, id)
	}
	sort.Slice(snap.order, func(i, j int) bool { return snap.order[i] < snap.order[j] })
	return snap, nil
}

func checkBaseChain(snap *Snapshot, id TypeID, visiting map[TypeID]bool) error {
	if visiting[id] {
		return fmt.Errorf("%w: base cycle involving type %q", ErrRegistry, id)
	}
	ct := snap.types[id]
	if ct == nil {
		return fmt.Errorf("%w: unknown base type %q", ErrRegistry, id)
	}
	if ct.base == "" {
		return nil
	}
	visiting[id] = true
	defer delete(visiting, id)
	return checkBaseChain(snap, ct.base, visiting)
}

func validateFieldShape(snap *Snapshot, ct *compiledType, fs *FieldSpec) error {
	switch fs.Kind {
	case KindBool, KindInt, KindString:
		if fs.Elem != nil || fs.RefType != "" {
			return fmt.Errorf("%w: type %q scalar field %q carries composite descriptors", ErrRegistry, ct.id, fs.Name)
		}
	case KindRef:
		if fs.Elem != nil {
			return fmt.Errorf("%w: type %q ref field %q must not declare Elem", ErrRegistry, ct.id, fs.Name)
		}
		if fs.RefType == "" {
			return fmt.Errorf("%w: type %q ref field %q missing RefType", ErrRegistry, ct.id, fs.Name)
		}
		if snap.types[fs.RefType] == nil {
			return fmt.Errorf("%w: type %q ref field %q points at unknown type %q", ErrRegistry, ct.id, fs.Name, fs.RefType)
		}
	case KindList:
		if fs.Elem == nil {
			return fmt.Errorf("%w: type %q list field %q missing Elem", ErrRegistry, ct.id, fs.Name)
		}
		if fs.RefType != "" {
			return fmt.Errorf("%w: type %q list field %q must declare RefType on Elem", ErrRegistry, ct.id, fs.Name)
		}
		return validateFieldShape(snap, ct, fs.Elem)
	case KindNull:
		return fmt.Errorf("%w: type %q field %q cannot declare KindNull", ErrRegistry, ct.id, fs.Name)
	default:
		return fmt.Errorf("%w: type %q field %q has unknown kind", ErrRegistry, ct.id, fs.Name)
	}
	return nil
}

func checkFieldGoType(snap *Snapshot, t reflect.Type, fs *FieldSpec) error {
	switch fs.Kind {
	case KindBool:
		elem := t
		if fs.Nullable && t.Kind() == reflect.Ptr {
			elem = t.Elem()
		}
		if elem.Kind() != reflect.Bool {
			return fmt.Errorf("requires bool, got %s", t)
		}
	case KindInt:
		elem := t
		if fs.Nullable && t.Kind() == reflect.Ptr {
			elem = t.Elem()
		}
		if elem.Kind() != reflect.Int {
			return fmt.Errorf("requires int, got %s", t)
		}
	case KindString:
		elem := t
		if fs.Nullable && t.Kind() == reflect.Ptr {
			elem = t.Elem()
		}
		if elem.Kind() != reflect.String {
			return fmt.Errorf("requires string, got %s", t)
		}
	case KindRef:
		if t.Kind() != reflect.Ptr {
			return fmt.Errorf("reference requires pointer field, got %s", t)
		}
		target := snap.types[fs.RefType].prototype
		if !target.AssignableTo(t) {
			return fmt.Errorf("reference type %s is not assignable to field %s", target, t)
		}
	case KindList:
		if t.Kind() != reflect.Slice {
			return fmt.Errorf("list requires slice field, got %s", t)
		}
		return checkFieldGoType(snap, t.Elem(), fs.Elem)
	}
	return nil
}

// isKindOf reports whether id equals base or transitively extends base.
func (s *Snapshot) isKindOf(id, base TypeID) bool {
	for cur := id; cur != ""; {
		if cur == base {
			return true
		}
		ct := s.types[cur]
		if ct == nil {
			return false
		}
		cur = ct.base
	}
	return false
}
