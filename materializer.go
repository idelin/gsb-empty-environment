package minijit

import "fmt"

// Materializer turns virtual objects into real heap objects. It implements
// the mandatory two-phase protocol:
//
//  1. allocate one empty heap instance per virtual id and register it in the
//     virtualID -> heapRef map (so cycles and shared references resolve);
//  2. walk every virtual object again and fill its fields, resolving scalar
//     slots, machine slots and virtual references through the map.
type Materializer struct {
	slots    []Value
	virtuals []VirtualObject
	refs     map[int]*Object
}

// NewMaterializer builds a materializer over a machine frame snapshot.
func NewMaterializer(mf *MachineFrame) *Materializer {
	return &Materializer{
		slots:    mf.Slots,
		virtuals: mf.Virtuals,
		refs:     map[int]*Object{},
	}
}

// Materialize runs both phases and returns the virtualID -> heapRef map.
// It is idempotent: repeated calls return the same heap identities.
func (m *Materializer) Materialize() (map[int]*Object, error) {
	if len(m.refs) == len(m.virtuals) && len(m.virtuals) > 0 {
		return m.refs, nil
	}

	// Phase 1: allocate empty shells first. No field is read here, so
	// A.next = B / B.next = A cannot recurse infinitely.
	for idx := range m.virtuals {
		vo := &m.virtuals[idx]
		if _, ok := m.refs[vo.ID]; ok {
			continue
		}
		m.refs[vo.ID] = NewObject(vo.Class)
	}

	// Phase 2: fill fields, resolving every reference through the map.
	for idx := range m.virtuals {
		vo := &m.virtuals[idx]
		target := m.refs[vo.ID]
		for _, fi := range vo.Fields {
			v, err := m.resolve(fi.Value)
			if err != nil {
				return nil, fmt.Errorf("minijit: materialize field %q of vobj %d: %w", fi.Name, vo.ID, err)
			}
			target.SetField(fi.Name, v)
		}
	}
	return m.refs, nil
}

// Ref materializes on demand and returns the heap object for one virtual id.
func (m *Materializer) Ref(id int) (*Object, error) {
	if _, err := m.Materialize(); err != nil {
		return nil, err
	}
	o, ok := m.refs[id]
	if !ok {
		return nil, fmt.Errorf("minijit: unknown virtual object id %d", id)
	}
	return o, nil
}

// Resolve converts an arbitrary Input into a concrete Value, materializing
// referenced virtual objects as needed.
func (m *Materializer) Resolve(in Input) (Value, error) {
	return m.resolve(in)
}

func (m *Materializer) resolve(in Input) (Value, error) {
	switch in.kind {
	case inputLiteral:
		return in.lit, nil
	case inputSlot:
		if in.slot < 0 || in.slot >= len(m.slots) {
			return NilValue(), fmt.Errorf("machine slot %d out of range", in.slot)
		}
		return m.slots[in.slot], nil
	case inputVirtual:
		o, err := m.Ref(in.virt)
		if err != nil {
			return NilValue(), err
		}
		return ObjectValue(o), nil
	default:
		return NilValue(), fmt.Errorf("unknown input kind %d", in.kind)
	}
}
