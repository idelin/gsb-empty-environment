package netsec

import "fmt"

// EventKind is one of the thread or updater events from the model.
type EventKind uint8

const (
	EventEnter EventKind = iota + 1
	EventLoadRoot
	EventDereference
	EventExit
	EventQuiescent
	EventReplaceRoot
	EventRetire
	EventTryAdvance
)

// IsThreadEvent reports whether the event acts on a particular thread.
func (k EventKind) IsThreadEvent() bool {
	switch k {
	case EventEnter, EventLoadRoot, EventDereference, EventExit, EventQuiescent:
		return true
	default:
		return false
	}
}

// IsUpdaterEvent reports whether the event is global.
func (k EventKind) IsUpdaterEvent() bool {
	switch k {
	case EventReplaceRoot, EventRetire, EventTryAdvance:
		return true
	default:
		return false
	}
}

func (k EventKind) String() string {
	switch k {
	case EventEnter:
		return "Enter"
	case EventLoadRoot:
		return "LoadRoot"
	case EventDereference:
		return "Dereference"
	case EventExit:
		return "Exit"
	case EventQuiescent:
		return "Quiescent"
	case EventReplaceRoot:
		return "ReplaceRoot"
	case EventRetire:
		return "Retire"
	case EventTryAdvance:
		return "TryAdvance"
	default:
		return fmt.Sprintf("EventKind(%d)", uint8(k))
	}
}

// Event is a tagged event. Object and Generation are used by ReplaceRoot and
// Retire. A Dereference may optionally name a held ObjectRef; when it does not,
// the thread's most recently loaded root is dereferenced.
type Event struct {
	Kind EventKind

	Object        ObjectID
	Generation    Generation
	HasObject     bool
	HasGeneration bool
}

// Thread events.

func Enter() Event       { return Event{Kind: EventEnter} }
func LoadRoot() Event    { return Event{Kind: EventLoadRoot} }
func Dereference() Event { return Event{Kind: EventDereference} }
func Exit() Event        { return Event{Kind: EventExit} }
func Quiescent() Event   { return Event{Kind: EventQuiescent} }

// DereferenceRef dereferences a specific held ObjectRef instead of the most
// recently loaded one.
func DereferenceRef(ref ObjectRef) Event {
	return Event{
		Kind:          EventDereference,
		Object:        ref.ID,
		Generation:    ref.Gen,
		HasObject:     true,
		HasGeneration: true,
	}
}

// Updater events.

// ReplaceRoot installs a new root at id. If the address has not been used, the
// first generation is allocated. If a previous generation has been reclaimed,
// the next generation is allocated (address reuse).
func ReplaceRoot(id ObjectID) Event {
	return Event{Kind: EventReplaceRoot, Object: id, HasObject: true}
}

// ReplaceRootGen installs an existing live generation as the root. It is
// mainly useful for replaying an explicitly recorded trace.
func ReplaceRootGen(ref ObjectRef) Event {
	return Event{
		Kind:          EventReplaceRoot,
		Object:        ref.ID,
		Generation:    ref.Gen,
		HasObject:     true,
		HasGeneration: true,
	}
}

// Retire retires the current root.
func Retire() Event {
	return Event{Kind: EventRetire}
}

// RetireObject retires the current generation of id if it is the root, or the
// latest generation of id otherwise.
func RetireObject(id ObjectID) Event {
	return Event{Kind: EventRetire, Object: id, HasObject: true}
}

// RetireGen retires the exact address/generation pair.
func RetireGen(ref ObjectRef) Event {
	return Event{
		Kind:          EventRetire,
		Object:        ref.ID,
		Generation:    ref.Gen,
		HasObject:     true,
		HasGeneration: true,
	}
}

// TryAdvance asks the global epoch to advance by one.
func TryAdvance() Event {
	return Event{Kind: EventTryAdvance}
}

func (e Event) String() string {
	switch e.Kind {
	case EventReplaceRoot:
		if e.HasGeneration {
			return fmt.Sprintf("ReplaceRoot(%d@%d)", uint32(e.Object), uint32(e.Generation))
		}
		return fmt.Sprintf("ReplaceRoot(%d)", uint32(e.Object))
	case EventRetire:
		if e.HasGeneration {
			return fmt.Sprintf("Retire(%d@%d)", uint32(e.Object), uint32(e.Generation))
		}
		if e.HasObject {
			return fmt.Sprintf("Retire(%d)", uint32(e.Object))
		}
		return "Retire(root)"
	case EventDereference:
		if e.HasGeneration {
			return fmt.Sprintf("Dereference(%d@%d)", uint32(e.Object), uint32(e.Generation))
		}
		return "Dereference"
	default:
		return e.Kind.String()
	}
}

func (e Event) validateFields() error {
	if !e.Kind.IsThreadEvent() && !e.Kind.IsUpdaterEvent() {
		return fmt.Errorf("unknown event kind %d", uint8(e.Kind))
	}
	if e.HasGeneration && !e.HasObject {
		return fmt.Errorf("event %s has a generation without an object", e.Kind)
	}
	switch e.Kind {
	case EventReplaceRoot, EventRetire:
		if e.Kind == EventReplaceRoot && !e.HasObject {
			return fmt.Errorf("ReplaceRoot requires an object ID")
		}
	case EventDereference:
		if e.HasObject != e.HasGeneration {
			return fmt.Errorf("Dereference requires an object ID and generation together")
		}
	case EventLoadRoot, EventEnter, EventExit, EventQuiescent, EventTryAdvance:
		if e.HasObject || e.HasGeneration {
			return fmt.Errorf("%s does not take an object reference", e.Kind)
		}
	}
	return nil
}
