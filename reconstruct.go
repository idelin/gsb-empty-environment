package netsec

import (
	"fmt"
	"reflect"
)

// MaxVirtualObjects is the hard upper bound on virtual object definitions in
// one reconstruction.
const MaxVirtualObjects = 64

// MaxVirtuals is an alias for MaxVirtualObjects.
const MaxVirtuals = MaxVirtualObjects

// Reconstruct materializes the reachable part of an optimized frame and
// returns the interpreter frame plus a proof mapping each reachable virtual
// identifier to its single concrete object identifier.
//
// The algorithm is deliberately two-phase:
//
//  1. Validate the whole description, compute reachability from registers and
//     stack slots, then allocate exactly one object for every reachable
//     virtual identifier.
//  2. Fill fields and rewrite frame slots.  This phase performs no allocator
//     or other host callbacks.
//
// If validation or any allocation fails, Reconstruct returns (nil, nil, err)
// and releases every object allocated by the attempt.  Existing host objects
// are never released or modified.
func Reconstruct(in any, alloc Allocator) (*Frame, Proof, error) {
	rc, err := normalizeReconstruction(in)
	if err != nil {
		return nil, nil, err
	}
	plan, err := makePlan(rc)
	if err != nil {
		return nil, nil, err
	}
	adapter, err := newAllocAdapter(alloc)
	if err != nil {
		return nil, nil, err
	}

	frame := &Frame{
		Objects:    make(map[ObjectID]*Object, len(plan.order)),
		NewObjects: make([]*Object, 0, len(plan.order)),
	}
	proof := make(Proof, len(plan.order))

	if len(plan.order) == 0 {
		frame.Registers, err = resolveValues(rc.AllRegisters(), proof)
		if err != nil {
			return nil, nil, err
		}
		frame.Stack, err = resolveValues(rc.AllStack(), proof)
		if err != nil {
			return nil, nil, err
		}
		frame.Regs = frame.Registers
		frame.StackSlots = frame.Stack
		frame.Slots = frame.Stack
		return frame, proof, nil
	}

	allocations := make([]*Object, 0, len(plan.order))
	byVirtual := make(map[VirtualID]*Object, len(plan.order))
	byObject := make(map[ObjectID]*Object, len(plan.order))
	owner := make(map[ObjectID]VirtualID, len(plan.order))

	rollback := func(cause error) (*Frame, Proof, error) {
		if adapter.release == nil {
			return nil, nil, fmt.Errorf("%w: %v (rollback unavailable: %w)", ErrAllocationFailed, cause, ErrNoRelease)
		}
		for i := len(allocations) - 1; i >= 0; i-- {
			o := allocations[i]
			if o == nil {
				continue
			}
			if _, isExisting := plan.existing[o.ID]; isExisting {
				continue
			}
			adapter.release(o)
		}
		return nil, nil, cause
	}

	for _, def := range plan.order {
		obj, allocErr := adapter.allocate()
		if allocErr != nil {
			if obj != nil {
				if _, isExisting := plan.existing[obj.ID]; !isExisting {
					if prior, ok := byObject[obj.ID]; !ok || prior != obj {
						allocations = append(allocations, obj)
					}
				}
			}
			return rollback(fmt.Errorf("%w: virtual %d: %w", ErrAllocationFailed, def.ID, allocErr))
		}
		if obj == nil {
			return rollback(fmt.Errorf("%w: virtual %d: %w", ErrAllocationFailed, def.ID, ErrNilObject))
		}
		if _, isExisting := plan.existing[obj.ID]; isExisting {
			return rollback(fmt.Errorf("%w: virtual %d: object %d: %w",
				ErrAllocationFailed, def.ID, obj.ID, ErrObjectIDCollision))
		}
		if prior, duplicate := byObject[obj.ID]; duplicate {
			if prior != obj {
				allocations = append(allocations, obj)
			}
			return rollback(fmt.Errorf("%w: virtual %d and %d: object %d: %w",
				ErrAllocationFailed, owner[obj.ID], def.ID, obj.ID, ErrDuplicateObjectID))
		}

		allocations = append(allocations, obj)
		byObject[obj.ID] = obj
		owner[obj.ID] = def.ID
		byVirtual[def.ID] = obj
		proof[def.ID] = obj.ID
	}

	for _, def := range plan.order {
		if err := fillObject(byVirtual[def.ID], def, proof); err != nil {
			return rollback(err)
		}
	}

	frame.Registers, err = resolveValues(rc.AllRegisters(), proof)
	if err != nil {
		return rollback(err)
	}
	frame.Stack, err = resolveValues(rc.AllStack(), proof)
	if err != nil {
		return rollback(err)
	}
	frame.Regs = frame.Registers
	frame.StackSlots = frame.Stack
	frame.Slots = frame.Stack
	for _, obj := range allocations {
		frame.Objects[obj.ID] = obj
		frame.NewObjects = append(frame.NewObjects, obj)
	}
	if err := proof.Verify(); err != nil {
		return rollback(err)
	}
	return frame, proof, nil
}

// Rebuild is an alias for Reconstruct.
func Rebuild(in any, alloc Allocator) (*Frame, Proof, error) { return Reconstruct(in, alloc) }

// Materialize is an alias for Reconstruct.
func Materialize(in any, alloc Allocator) (*Frame, Proof, error) { return Reconstruct(in, alloc) }

// ReconstructFrame is an alias for Reconstruct.
func ReconstructFrame(in any, alloc Allocator) (*Frame, Proof, error) {
	return Reconstruct(in, alloc)
}

type reconstructionPlan struct {
	order     []VirtualObject
	reachable map[VirtualID]struct{}
	existing  map[ObjectID]struct{}
}

func makePlan(rc Reconstruction) (*reconstructionPlan, error) {
	defs := rc.AllObjects()
	if len(defs) > MaxVirtualObjects {
		return nil, fmt.Errorf("%w: got %d, limit %d", ErrTooManyVirtualObjects, len(defs), MaxVirtualObjects)
	}

	byID := make(map[VirtualID]VirtualObject, len(defs))
	for _, def := range defs {
		if _, duplicate := byID[def.ID]; duplicate {
			return nil, fmt.Errorf("%w: virtual %d", ErrDuplicateVirtual, def.ID)
		}
		byID[def.ID] = def
	}

	existing := make(map[ObjectID]struct{}, len(rc.Existing)+len(rc.ExistingIDs))
	for _, id := range rc.Existing {
		existing[id] = struct{}{}
	}
	for _, id := range rc.ExistingIDs {
		existing[id] = struct{}{}
	}

	for _, def := range defs {
		for name, value := range def.Fields {
			if name == "closed" && value.Kind != KindInt {
				return nil, fmt.Errorf("%w: virtual %d field %q", ErrInvalidClosedField, def.ID, name)
			}
			if err := validateValue(value, byID, existing); err != nil {
				return nil, fmt.Errorf("virtual %d field %q: %w", def.ID, name, err)
			}
		}
	}

	reachable := make(map[VirtualID]struct{}, len(defs))
	var queue []VirtualID
	addRoot := func(v Value) error {
		if err := validateValue(v, byID, existing); err != nil {
			return err
		}
		if v.Kind != KindVirtual {
			return nil
		}
		if _, seen := reachable[v.Virtual]; !seen {
			reachable[v.Virtual] = struct{}{}
			queue = append(queue, v.Virtual)
		}
		return nil
	}
	for i, v := range rc.AllRegisters() {
		if err := addRoot(v); err != nil {
			return nil, fmt.Errorf("register %d: %w", i, err)
		}
	}
	for i, v := range rc.AllStack() {
		if err := addRoot(v); err != nil {
			return nil, fmt.Errorf("stack slot %d: %w", i, err)
		}
	}

	for i := 0; i < len(queue); i++ {
		def := byID[queue[i]]
		for _, v := range def.Fields {
			if v.Kind != KindVirtual {
				continue
			}
			if _, seen := reachable[v.Virtual]; !seen {
				reachable[v.Virtual] = struct{}{}
				queue = append(queue, v.Virtual)
			}
		}
	}

	order := make([]VirtualObject, 0, len(reachable))
	for _, def := range defs {
		if _, ok := reachable[def.ID]; ok {
			order = append(order, def)
		}
	}
	return &reconstructionPlan{order: order, reachable: reachable, existing: existing}, nil
}

func validateValue(v Value, byID map[VirtualID]VirtualObject, existing map[ObjectID]struct{}) error {
	switch v.Kind {
	case KindInt:
		return nil
	case KindHeap:
		existing[v.Heap] = struct{}{}
		return nil
	case KindVirtual:
		if _, ok := byID[v.Virtual]; !ok {
			return fmt.Errorf("%w: virtual %d", ErrMissingVirtual, v.Virtual)
		}
		return nil
	default:
		return fmt.Errorf("%w: %d", ErrInvalidValueKind, v.Kind)
	}
}

func fillObject(obj *Object, def VirtualObject, proof Proof) error {
	if obj == nil {
		return ErrNilObject
	}
	if obj.Fields == nil {
		obj.Fields = make(map[string]Value, len(def.Fields)+1)
	}

	closed := def.Closed
	for name, raw := range def.Fields {
		value, err := resolveValue(raw, proof)
		if err != nil {
			return err
		}
		if name == "closed" {
			if value.Kind != KindInt {
				return fmt.Errorf("%w: virtual %d", ErrInvalidClosedField, def.ID)
			}
			if value.Int != 0 {
				closed = true
			}
			value = IntValue(boolInt(closed))
		}
		obj.Fields[name] = value
	}

	obj.Closed = closed
	obj.Fields["closed"] = IntValue(boolInt(closed))
	return nil
}

func resolveValues(values []Value, proof Proof) ([]Value, error) {
	if values == nil {
		return nil, nil
	}
	out := make([]Value, len(values))
	for i, v := range values {
		resolved, err := resolveValue(v, proof)
		if err != nil {
			return nil, fmt.Errorf("slot %d: %w", i, err)
		}
		out[i] = resolved
	}
	return out, nil
}

func resolveValue(v Value, proof Proof) (Value, error) {
	switch v.Kind {
	case KindInt, KindHeap:
		return v, nil
	case KindVirtual:
		id, ok := proof[v.Virtual]
		if !ok {
			return Value{}, fmt.Errorf("%w: virtual %d", ErrMissingVirtual, v.Virtual)
		}
		return HeapRef(id), nil
	default:
		return Value{}, fmt.Errorf("%w: %d", ErrInvalidValueKind, v.Kind)
	}
}

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func normalizeReconstruction(in any) (Reconstruction, error) {
	switch x := in.(type) {
	case Reconstruction:
		return x, nil
	case *Reconstruction:
		if x == nil {
			return Reconstruction{}, fmt.Errorf("%w: nil *Reconstruction", ErrInvalidReconstruction)
		}
		return *x, nil
	}
	if in == nil {
		return Reconstruction{}, fmt.Errorf("%w: nil input", ErrInvalidReconstruction)
	}

	rv := reflect.ValueOf(in)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return Reconstruction{}, fmt.Errorf("%w: nil input", ErrInvalidReconstruction)
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return Reconstruction{}, fmt.Errorf("%w: %T", ErrInvalidReconstruction, in)
	}

	var rc Reconstruction
	var found bool
	if f := structField(rv, "Registers", "Register", "Regs"); f.IsValid() {
		if f.Type() != reflect.TypeOf([]Value(nil)) {
			return Reconstruction{}, fmt.Errorf("%w: registers type %s", ErrInvalidReconstruction, f.Type())
		}
		rc.Registers = f.Interface().([]Value)
		found = true
	}
	if f := structField(rv, "Stack", "StackSlots", "Slots"); f.IsValid() {
		if f.Type() != reflect.TypeOf([]Value(nil)) {
			return Reconstruction{}, fmt.Errorf("%w: stack type %s", ErrInvalidReconstruction, f.Type())
		}
		rc.Stack = f.Interface().([]Value)
		found = true
	}
	for _, names := range [][]string{
		{"Objects", "VirtualObjects"},
		{"Virtuals"},
		{"Definitions"},
	} {
		if f := structField(rv, names...); f.IsValid() {
			if f.Type() != reflect.TypeOf([]VirtualObject(nil)) {
				return Reconstruction{}, fmt.Errorf("%w: objects type %s", ErrInvalidReconstruction, f.Type())
			}
			switch names[0] {
			case "Virtuals":
				rc.Virtuals = append(rc.Virtuals, f.Interface().([]VirtualObject)...)
			case "Definitions":
				rc.Definitions = append(rc.Definitions, f.Interface().([]VirtualObject)...)
			default:
				rc.Objects = append(rc.Objects, f.Interface().([]VirtualObject)...)
			}
			found = true
		}
	}
	if f := structField(rv, "Existing", "ExistingIDs"); f.IsValid() {
		ids, err := objectIDsFromReflect(f)
		if err != nil {
			return Reconstruction{}, err
		}
		rc.Existing = append(rc.Existing, ids...)
		found = true
	}
	if !found {
		return Reconstruction{}, fmt.Errorf("%w: %T", ErrInvalidReconstruction, in)
	}
	return rc, nil
}

func structField(v reflect.Value, names ...string) reflect.Value {
	for _, name := range names {
		f := v.FieldByName(name)
		if f.IsValid() && f.CanInterface() {
			return f
		}
	}
	return reflect.Value{}
}

func objectIDsFromReflect(v reflect.Value) ([]ObjectID, error) {
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, fmt.Errorf("%w: existing ids type %s", ErrInvalidReconstruction, v.Type())
	}
	out := make([]ObjectID, 0, v.Len())
	for i := 0; i < v.Len(); i++ {
		e := v.Index(i)
		if e.Kind() == reflect.Interface && !e.IsNil() {
			e = e.Elem()
		}
		switch e.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			out = append(out, ObjectID(e.Int()))
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			out = append(out, ObjectID(e.Uint()))
		default:
			return nil, fmt.Errorf("%w: existing id type %s", ErrInvalidReconstruction, e.Type())
		}
	}
	return out, nil
}
