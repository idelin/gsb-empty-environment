package netsec

import (
	"encoding"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sync"
)

const (
	// DefaultMaxDepth is the default maximum number of nested JSON containers
	// (objects/arrays, and resolved references) accepted by Decode.
	DefaultMaxDepth = 32

	// DefaultMaxBytes is the default maximum encoded payload size accepted by
	// Decode.
	DefaultMaxBytes = 4 << 20

	maxTypeIDLength = 128
)

// TypeID is the exact, case-sensitive identifier embedded in a payload.
type TypeID string

// IntRange restricts an integer field to an inclusive range.
type IntRange struct {
	Min int64
	Max int64
}

// LenRange restricts a string, slice, or array field by its length. Min/Max
// are inclusive. The zero value for Max means no upper bound.
type LenRange struct {
	Min int
	Max int
}

// FieldSpec explicitly allowlists one wire field of a registered type.
//
// Name is the exact JSON key as it appears on the wire. Required fields must
// be present; optional fields may be absent (their Go zero value is used).
// IntRange and LenRange are applied after decoding. Cross-field consistency
// belongs in TypeSpec.Validate.
type FieldSpec struct {
	Name     string
	Required bool
	IntRange *IntRange
	LenRange *LenRange
}

// TypeSpec describes one concrete, allowlisted type.
//
// New must return a non-nil pointer to the desired struct type. Decode checks
// that every invocation returns that exact type before constructing it.
// Fields is the complete allowlist for the type: fields absent from this list
// are rejected if present on the wire. Validate, when non-nil, runs after the
// value has been decoded and basic field constraints have passed.
type TypeSpec struct {
	ID       TypeID
	Version  uint32
	New      func() any
	Fields   []FieldSpec
	Validate func(any) error
}

// Option configures a Registry.
type Option func(*Registry)

// WithMaxDepth sets the maximum permitted nesting/reference depth.
func WithMaxDepth(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.maxDepth = n
		}
	}
}

// WithMaxBytes sets the maximum accepted encoded payload size.
func WithMaxBytes(n int) Option {
	return func(r *Registry) {
		if n > 0 {
			r.maxBytes = n
		}
	}
}

type compiledField struct {
	spec  FieldSpec
	index int
	typ   reflect.Type
}

type compiledSpec struct {
	id       TypeID
	version  uint32
	newFn    func() any
	typ      reflect.Type // struct type
	fields   map[string]*compiledField
	order    []string
	validate func(any) error
}

// Registry holds the allowlisted type and field definitions used by Decode.
// It is safe for concurrent use after registration.
type Registry struct {
	mu       sync.RWMutex
	specs    map[TypeID]*compiledSpec
	byType   map[reflect.Type]*compiledSpec
	maxDepth int
	maxBytes int
}

// Decoder is an alias for Registry, for callers that prefer decoder-centric
// naming.
type Decoder = Registry

// NewRegistry creates an empty allowlist registry.
func NewRegistry(opts ...Option) *Registry {
	r := &Registry{
		specs:    make(map[TypeID]*compiledSpec),
		byType:   make(map[reflect.Type]*compiledSpec),
		maxDepth: DefaultMaxDepth,
		maxBytes: DefaultMaxBytes,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// NewDecoder creates an empty allowlist decoder. It is an alias for
// NewRegistry.
func NewDecoder(opts ...Option) *Decoder {
	return NewRegistry(opts...)
}

// Register adds an allowlisted concrete type. Registrations are immutable for
// the lifetime of the call; duplicate type identifiers and duplicate Go types
// are rejected.
func (r *Registry) Register(spec TypeSpec) error {
	if r == nil {
		return fmt.Errorf("%w: nil registry", ErrInvalidSpec)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.specs == nil {
		r.specs = make(map[TypeID]*compiledSpec)
	}
	if r.byType == nil {
		r.byType = make(map[reflect.Type]*compiledSpec)
	}

	if err := validateTypeID(spec.ID); err != nil {
		return err
	}
	if _, exists := r.specs[spec.ID]; exists {
		return fmt.Errorf("%w: type identifier %q already registered", ErrDuplicateType, spec.ID)
	}
	if spec.New == nil {
		return fmt.Errorf("%w: type %q: New is nil", ErrInvalidSpec, spec.ID)
	}

	target, err := invokeFactory(spec.New)
	if err != nil {
		return fmt.Errorf("%w: type %q: %v", ErrInvalidSpec, spec.ID, err)
	}
	if target == nil {
		return fmt.Errorf("%w: type %q: New returned nil", ErrInvalidSpec, spec.ID)
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("%w: type %q: New must return a non-nil pointer to a struct", ErrInvalidSpec, spec.ID)
	}
	structType := rv.Type().Elem()
	if structType.Kind() != reflect.Struct {
		return fmt.Errorf("%w: type %q: New must return a pointer to a struct", ErrInvalidSpec, spec.ID)
	}

	if existing, exists := r.byType[structType]; exists {
		return fmt.Errorf("%w: Go type %v already registered as %q", ErrDuplicateType, structType, existing.id)
	}

	cs := &compiledSpec{
		id:       spec.ID,
		version:  spec.Version,
		newFn:    spec.New,
		typ:      structType,
		fields:   make(map[string]*compiledField, len(spec.Fields)),
		order:    make([]string, 0, len(spec.Fields)),
		validate: spec.Validate,
	}

	typeFields := jsonFieldMap(structType)
	for _, field := range spec.Fields {
		if err := validateFieldSpec(field); err != nil {
			return fmt.Errorf("%w: type %q: %v", ErrInvalidSpec, spec.ID, err)
		}
		if _, dup := cs.fields[field.Name]; dup {
			return fmt.Errorf("%w: type %q: field %q registered twice", ErrDuplicateType, spec.ID, field.Name)
		}

		goField, ok := typeFields[field.Name]
		if !ok {
			return fmt.Errorf("%w: type %q: field %q does not exist as an exported JSON field", ErrInvalidSpec, spec.ID, field.Name)
		}
		if err := checkFieldType(goField.typ, make(map[reflect.Type]bool)); err != nil {
			return fmt.Errorf("%w: type %q field %q: %v", err, spec.ID, field.Name, err)
		}
		if err := validateRangeTypes(field, goField.typ); err != nil {
			return fmt.Errorf("%w: type %q field %q: %v", ErrInvalidSpec, spec.ID, field.Name, err)
		}

		cs.fields[field.Name] = &compiledField{
			spec:  field,
			index: goField.index,
			typ:   goField.typ,
		}
		cs.order = append(cs.order, field.Name)
	}

	r.specs[cs.id] = cs
	r.byType[structType] = cs
	return nil
}

// RegisterType is a typed convenience wrapper around Register. T must be a
// struct type; the factory always constructs a fresh *T.
func RegisterType[T any](r *Registry, id TypeID, version uint32, fields []FieldSpec, validate func(*T) error) error {
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		return fmt.Errorf("%w: %q must be a struct type, got %v", ErrInvalidSpec, id, typ)
	}

	spec := TypeSpec{
		ID:      id,
		Version: version,
		New: func() any {
			return new(T)
		},
		Fields: fields,
	}
	if validate != nil {
		spec.Validate = func(v any) error {
			typed, ok := v.(*T)
			if !ok {
				return fmt.Errorf("expected %T, got %T", (*T)(nil), v)
			}
			return validate(typed)
		}
	}
	return r.Register(spec)
}

// Has reports whether the exact type identifier and version are registered.
func (r *Registry) Has(id TypeID, version uint32) bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	cs, ok := r.specs[id]
	return ok && cs.version == version
}

func (r *Registry) lookup(id TypeID) *compiledSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.specs[id]
}

func (r *Registry) specForType(typ reflect.Type) *compiledSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.byType[typ]
}

func (r *Registry) limits() (int, int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	maxDepth, maxBytes := r.maxDepth, r.maxBytes
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return maxDepth, maxBytes
}

func invokeFactory(factory func() any) (value any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			value = nil
			err = fmt.Errorf("type factory panicked: %v", recovered)
		}
	}()
	return factory(), nil
}

var typeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

func validateTypeID(id TypeID) error {
	s := string(id)
	if s == "" {
		return fmt.Errorf("%w: empty type identifier", ErrInvalidSpec)
	}
	if len(s) > maxTypeIDLength {
		return fmt.Errorf("%w: type identifier is longer than %d bytes", ErrInvalidSpec, maxTypeIDLength)
	}
	if !typeIDPattern.MatchString(s) {
		return fmt.Errorf("%w: type identifier %q contains unsupported characters", ErrInvalidSpec, s)
	}
	return nil
}

func validateFieldSpec(field FieldSpec) error {
	if field.Name == "" {
		return fmt.Errorf("field name is empty")
	}
	if field.Name == "$ref" {
		return fmt.Errorf("field name %q is reserved", field.Name)
	}
	if field.IntRange != nil && field.IntRange.Min > field.IntRange.Max {
		return fmt.Errorf("field %q has inverted integer range", field.Name)
	}
	if field.LenRange != nil {
		if field.LenRange.Min < 0 || field.LenRange.Max < 0 {
			return fmt.Errorf("field %q has negative length range", field.Name)
		}
		if field.LenRange.Max != 0 && field.LenRange.Min > field.LenRange.Max {
			return fmt.Errorf("field %q has inverted length range", field.Name)
		}
	}
	return nil
}

type jsonField struct {
	index int
	typ   reflect.Type
}

func jsonFieldMap(structType reflect.Type) map[string]jsonField {
	out := make(map[string]jsonField, structType.NumField())
	for i := 0; i < structType.NumField(); i++ {
		sf := structType.Field(i)
		if sf.PkgPath != "" || sf.Anonymous {
			continue
		}
		name := sf.Name
		tag, hasTag := sf.Tag.Lookup("json")
		if hasTag {
			parts := splitJSONTag(tag)
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}
		if _, dup := out[name]; dup {
			continue
		}
		out[name] = jsonField{index: i, typ: sf.Type}
	}
	return out
}

func splitJSONTag(tag string) []string {
	out := make([]string, 0, 2)
	start := 0
	for i := 0; i <= len(tag); i++ {
		if i == len(tag) || tag[i] == ',' {
			out = append(out, tag[start:i])
			start = i + 1
		}
	}
	if len(out) > 0 && len(out[0]) > 0 && out[0][0] == '-' {
		out[0] = "-"
	}
	return out
}

var (
	jsonUnmarshalerType  = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType  = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
	binaryUnmarshalerTyp = reflect.TypeOf((*encoding.BinaryUnmarshaler)(nil)).Elem()
)

func checkFieldType(t reflect.Type, seen map[reflect.Type]bool) error {
	if t == nil {
		return fmt.Errorf("nil type")
	}
	if seen[t] {
		return nil
	}
	seen[t] = true

	if implementsUnmarshaler(t) {
		return fmt.Errorf("%w: %v implements UnmarshalJSON/TextUnmarshaler", ErrUnsupportedFieldType, t)
	}

	switch t.Kind() {
	case reflect.Pointer:
		return checkFieldType(t.Elem(), seen)
	case reflect.Bool,
		reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64,
		reflect.Struct:
		return nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		return checkFieldType(t.Elem(), seen)
	case reflect.Interface, reflect.Map, reflect.Func, reflect.Chan, reflect.UnsafePointer, reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("%w: %v", ErrUnsupportedFieldType, t)
	default:
		return fmt.Errorf("%w: %v", ErrUnsupportedFieldType, t)
	}
}

func implementsUnmarshaler(t reflect.Type) bool {
	if t.Implements(jsonUnmarshalerType) ||
		t.Implements(textUnmarshalerType) ||
		t.Implements(binaryUnmarshalerTyp) {
		return true
	}
	if t.Kind() != reflect.Pointer {
		pt := reflect.PointerTo(t)
		return pt.Implements(jsonUnmarshalerType) ||
			pt.Implements(textUnmarshalerType) ||
			pt.Implements(binaryUnmarshalerTyp)
	}
	return false
}

func validateRangeTypes(field FieldSpec, typ reflect.Type) error {
	base := typ
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}
	if field.IntRange != nil {
		switch base.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		default:
			return fmt.Errorf("integer range requires an integer field, got %v", typ)
		}
	}
	if field.LenRange != nil {
		switch base.Kind() {
		case reflect.String, reflect.Slice, reflect.Array:
		default:
			return fmt.Errorf("length range requires a string/slice/array field, got %v", typ)
		}
	}
	return nil
}
