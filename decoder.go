package wiretypes

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
)

// Mode is the negotiated set of type IDs that may be instantiated while
// decoding one message.
type Mode map[TypeID]bool

// Allow reports whether id was negotiated.
func (m Mode) Allow(id TypeID) bool { return m != nil && m[id] }

// Budget caps the resources a single message may consume. The zero value of
// any field means that resource is unlimited.
type Budget struct {
	MaxObjects int
	MaxFields  int
	MaxRefs    int
	MaxDepth   int
}

// Callback is the business hook. Check runs after the full graph has been
// built and validated but before any delivery; if any Check fails, no Receive
// is ever called for that message. Receive is then called in reference-table
// order. Both methods must be safe for concurrent use across messages.
type Callback interface {
	Check(obj any, t TypeID, id RefID) error
	Receive(obj any, t TypeID, id RefID)
}

// NopCallback is a callback that never rejects and discards deliveries.
var NopCallback Callback = FuncCallback{}

// FuncCallback adapts optional functions into a Callback.
type FuncCallback struct {
	CheckFn   func(obj any, t TypeID, id RefID) error
	ReceiveFn func(obj any, t TypeID, id RefID)
}

func (f FuncCallback) Check(obj any, t TypeID, id RefID) error {
	if f.CheckFn == nil {
		return nil
	}
	return f.CheckFn(obj, t, id)
}

func (f FuncCallback) Receive(obj any, t TypeID, id RefID) {
	if f.ReceiveFn != nil {
		f.ReceiveFn(obj, t, id)
	}
}

// Decoder decodes messages against a swappable registry under a fixed mode and
// budget. It is safe for concurrent use.
type Decoder struct {
	registry *Registry
	mode     Mode
	budget   Budget
}

// NewDecoder builds a decoder. The mode and budget are fixed for the decoder's
// lifetime; registry hot swaps are honored only by later Decode calls.
func NewDecoder(r *Registry, mode Mode, budget Budget) (*Decoder, error) {
	if r == nil {
		return nil, fmt.Errorf("%w: nil registry", ErrRegistry)
	}
	return &Decoder{registry: r, mode: mode, budget: budget}, nil
}

// Decode validates and constructs one message. It binds to the registry
// snapshot published at call entry, so a concurrent Swap cannot affect this
// message. On success the constructed root object is returned. Any failure
// leaves no external side effect: factories only run after full validation,
// and deliveries only run after every Check succeeds.
func (d *Decoder) Decode(msg *Message, cb Callback) (any, error) {
	snap := d.registry.Snapshot()
	if snap == nil {
		return nil, fmt.Errorf("%w: registry has no snapshot", ErrRegistry)
	}
	if cb == nil {
		cb = NopCallback
	}

	entries, _, _, err := d.validate(snap, msg)
	if err != nil {
		return nil, err
	}

	instances := make(map[RefID]reflect.Value, len(entries))
	for _, e := range entries {
		instances[e.obj.ID] = reflect.ValueOf(e.ct.newFunc())
	}

	for _, e := range entries {
		rv := instances[e.obj.ID]
		for _, name := range sortedKeys(e.obj.Fields) {
			raw := e.obj.Fields[name]
			cf := e.ct.fieldIndex[name]
			target := rv.Elem().FieldByIndex(cf.index)
			if err := assignValue(snap, e.obj.ID, name, target, cf.spec, raw, instances); err != nil {
				return nil, err
			}
		}
	}

	for _, e := range entries {
		if err := cb.Check(instances[e.obj.ID].Interface(), e.ct.id, e.obj.ID); err != nil {
			return nil, decodeError("check", e.obj.ID, "", ErrRejected, "%v", err)
		}
	}
	for _, e := range entries {
		cb.Receive(instances[e.obj.ID].Interface(), e.ct.id, e.obj.ID)
	}
	return instances[msg.Root].Interface(), nil
}

type tableEntry struct {
	obj *RawObject
	ct  *compiledType
}

func (d *Decoder) validate(snap *Snapshot, msg *Message) ([]tableEntry, int, int, error) {
	if msg == nil || len(msg.Objects) == 0 {
		return nil, 0, 0, &Error{Op: "validate", Err: fmt.Errorf("%w: no objects", ErrEmptyMessage)}
	}
	if d.budget.MaxObjects > 0 && len(msg.Objects) > d.budget.MaxObjects {
		return nil, 0, 0, decodeError("validate", "", "", ErrBudgetExceeded,
			"object count %d exceeds limit %d", len(msg.Objects), d.budget.MaxObjects)
	}

	byID := make(map[RefID]*RawObject, len(msg.Objects))
	entries := make([]tableEntry, 0, len(msg.Objects))
	for i := range msg.Objects {
		obj := &msg.Objects[i]
		if obj.ID == "" {
			return nil, 0, 0, decodeError("validate", "", "", ErrRegistry, "object at index %d has empty id", i)
		}
		if _, dup := byID[obj.ID]; dup {
			return nil, 0, 0, decodeError("validate", obj.ID, "", ErrDuplicateID, "id appears more than once")
		}
		byID[obj.ID] = obj
		ct := snap.Lookup(obj.Type)
		if ct == nil {
			return nil, 0, 0, decodeError("validate", obj.ID, "", ErrUnknownType, "type %q", obj.Type)
		}
		if !d.mode.Allow(obj.Type) {
			return nil, 0, 0, decodeError("validate", obj.ID, "", ErrTypeNotNegotiated, "type %q", obj.Type)
		}
		entries = append(entries, tableEntry{obj: obj, ct: ct})
	}

	root := byID[msg.Root]
	if root == nil {
		return nil, 0, 0, decodeError("validate", msg.Root, "", ErrUnknownRoot, "root not present in table")
	}

	var fieldCount, refCount int
	var errs []error
	for _, e := range entries {
		for _, name := range sortedKeys(e.obj.Fields) {
			fieldCount++
			cf, ok := e.ct.fieldIndex[name]
			if !ok {
				errs = append(errs, decodeError("validate", e.obj.ID, name, ErrUnknownField, "field not declared by type %q", e.ct.id))
				continue
			}
			depth, refs, verrs := checkValue(snap, byID, e, cf.spec, e.obj.Fields[name], name)
			errs = append(errs, verrs...)
			if d.budget.MaxDepth > 0 && depth > d.budget.MaxDepth {
				errs = append(errs, decodeError("validate", e.obj.ID, name, ErrBudgetExceeded,
					"nesting depth %d exceeds limit %d", depth, d.budget.MaxDepth))
			}
			refCount += refs
		}
		for _, cf := range e.ct.fields {
			if !cf.spec.Nullable {
				if _, present := e.obj.Fields[cf.spec.Name]; !present {
					errs = append(errs, decodeError("validate", e.obj.ID, cf.spec.Name, ErrMissingField, "required field absent"))
				}
			}
		}
	}
	if d.budget.MaxFields > 0 && fieldCount > d.budget.MaxFields {
		errs = append(errs, decodeError("validate", "", "", ErrBudgetExceeded,
			"field count %d exceeds limit %d", fieldCount, d.budget.MaxFields))
	}
	if d.budget.MaxRefs > 0 && refCount > d.budget.MaxRefs {
		errs = append(errs, decodeError("validate", "", "", ErrBudgetExceeded,
			"reference count %d exceeds limit %d", refCount, d.budget.MaxRefs))
	}
	if len(errs) > 0 {
		return nil, 0, 0, errors.Join(errs...)
	}
	return entries, fieldCount, refCount, nil
}

// checkValue validates one raw value against its spec, recursively validating
// lists and references. It returns the nesting depth of the value, the number
// of references it contains and all errors found.
func checkValue(snap *Snapshot, byID map[RefID]*RawObject, e tableEntry, fs FieldSpec, raw RawValue, path string) (int, int, []error) {
	if raw.Kind == KindNull {
		if !fs.Nullable {
			return 1, 0, []error{decodeError("validate", e.obj.ID, path, ErrNullNotAllowed, "field is not nullable")}
		}
		return 1, 0, nil
	}
	if raw.Kind != fs.Kind {
		return 1, 0, []error{decodeError("validate", e.obj.ID, path, ErrTypeMismatch,
			"wire kind %d does not match declared kind %d", raw.Kind, fs.Kind)}
	}
	switch fs.Kind {
	case KindBool, KindInt, KindString:
		return 1, 0, nil
	case KindRef:
		target := byID[raw.Ref]
		if target == nil {
			return 1, 1, []error{decodeError("validate", e.obj.ID, path, ErrUnknownRef, "reference %q", raw.Ref)}
		}
		if !snap.isKindOf(target.Type, fs.RefType) {
			return 1, 1, []error{decodeError("validate", e.obj.ID, path, ErrTypeMismatch,
				"referenced type %q is not a %q", target.Type, fs.RefType)}
		}
		targetCT := snap.Lookup(target.Type)
		fieldCT := snap.Lookup(fs.RefType)
		if !targetCT.prototype.AssignableTo(fieldCT.prototype) {
			return 1, 1, []error{decodeError("validate", e.obj.ID, path, ErrTypeMismatch,
				"go type of %q is not assignable to reference field", target.Type)}
		}
		return 1, 1, nil
	case KindList:
		maxDepth := 1
		refs := 0
		var errs []error
		for i, elem := range raw.List {
			d, r, elemErrs := checkValue(snap, byID, e, *fs.Elem, elem, fmt.Sprintf("%s[%d]", path, i))
			if d+1 > maxDepth {
				maxDepth = d + 1
			}
			refs += r
			errs = append(errs, elemErrs...)
		}
		return maxDepth, refs, errs
	default:
		return 1, 0, []error{decodeError("validate", e.obj.ID, path, ErrTypeMismatch, "unsupported kind")}
	}
}

func assignValue(snap *Snapshot, owner RefID, path string, target reflect.Value, fs FieldSpec, raw RawValue, instances map[RefID]reflect.Value) error {
	if raw.Kind == KindNull {
		target.Set(reflect.Zero(target.Type()))
		return nil
	}
	switch fs.Kind {
	case KindBool:
		if target.Kind() == reflect.Ptr {
			v := reflect.New(target.Type().Elem())
			v.Elem().SetBool(raw.Bool)
			target.Set(v)
		} else {
			target.SetBool(raw.Bool)
		}
	case KindInt:
		if target.Kind() == reflect.Ptr {
			v := reflect.New(target.Type().Elem())
			v.Elem().SetInt(raw.Int)
			target.Set(v)
		} else {
			target.SetInt(raw.Int)
		}
	case KindString:
		if target.Kind() == reflect.Ptr {
			v := reflect.New(target.Type().Elem())
			v.Elem().SetString(raw.Str)
			target.Set(v)
		} else {
			target.SetString(raw.Str)
		}
	case KindRef:
		inst, ok := instances[raw.Ref]
		if !ok {
			return decodeError("link", owner, path, ErrUnknownRef, "reference %q", raw.Ref)
		}
		target.Set(inst)
	case KindList:
		slice := reflect.MakeSlice(target.Type(), len(raw.List), len(raw.List))
		for i, elem := range raw.List {
			if err := assignValue(snap, owner, fmt.Sprintf("%s[%d]", path, i), slice.Index(i), *fs.Elem, elem, instances); err != nil {
				return err
			}
		}
		target.Set(slice)
	}
	return nil
}

func sortedKeys(m map[string]RawValue) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
