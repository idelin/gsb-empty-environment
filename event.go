package netsec

import "fmt"

// EventType names the six page events in the sandbox model.
type EventType string

const (
	EventMap     EventType = "Map"
	EventWrite   EventType = "Write"
	EventProtect EventType = "Protect"
	EventExecute EventType = "Execute"
	EventUnmap   EventType = "Unmap"
	EventRemap   EventType = "Remap"
)

// MapEvent maps pageID to a fresh physical page.
type MapEvent struct {
	PageID PageID
	Source any
	Perm   Perm
	Data   []byte
}

// WriteEvent writes bytes into a page.
type WriteEvent struct {
	PageID PageID
	Offset int
	Data   []byte
}

// ProtectEvent changes a virtual page's permissions.
type ProtectEvent struct {
	PageID PageID
	Perm   Perm
}

// ExecuteEvent requests execution of a byte range.
type ExecuteEvent struct {
	PageID PageID
	Offset int
	Length int
}

// UnmapEvent removes a virtual page.
type UnmapEvent struct {
	PageID PageID
}

// RemapEvent creates an independent virtual generation over an existing
// physical page. Set UsePhysical to target PhysicalID instead of Target.
type RemapEvent struct {
	PageID      PageID
	Target      PageID
	PhysicalID  PhysicalID
	UsePhysical bool
	Perm        Perm
	HasPerm     bool
}

// Short aliases matching the event names used in the model description.
type (
	Map     = MapEvent
	Write   = WriteEvent
	Protect = ProtectEvent
	Execute = ExecuteEvent
	Unmap   = UnmapEvent
	Remap   = RemapEvent
)

// Event is a single tagged event usable with Apply.
type Event struct {
	Kind EventType

	PageID PageID
	Source any
	Perm   Perm
	Offset int
	Data   []byte
	Length int

	Target      PageID
	PhysicalID  PhysicalID
	UsePhysical bool
	HasPerm     bool
}

// Apply applies a MapEvent, WriteEvent, ProtectEvent, ExecuteEvent,
// UnmapEvent, RemapEvent, or tagged Event. Execute returns its execution
// identity; the other events return a nil execution.
func (t *Tracker) Apply(event any) (*Execution, error) {
	switch ev := event.(type) {
	case MapEvent:
		if ev.Data != nil {
			return nil, t.MapBytes(ev.PageID, ev.Source, ev.Data, ev.Perm)
		}
		return nil, t.Map(ev.PageID, ev.Source, ev.Perm)
	case *MapEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil MapEvent")
		}
		return t.Apply(*ev)
	case WriteEvent:
		return nil, t.Write(ev.PageID, ev.Offset, ev.Data)
	case *WriteEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil WriteEvent")
		}
		return t.Apply(*ev)
	case ProtectEvent:
		return nil, t.Protect(ev.PageID, ev.Perm)
	case *ProtectEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil ProtectEvent")
		}
		return t.Apply(*ev)
	case ExecuteEvent:
		return t.Execute(ev.PageID, ev.Offset, ev.Length)
	case *ExecuteEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil ExecuteEvent")
		}
		return t.Apply(*ev)
	case UnmapEvent:
		return nil, t.Unmap(ev.PageID)
	case *UnmapEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil UnmapEvent")
		}
		return t.Apply(*ev)
	case RemapEvent:
		if ev.UsePhysical {
			if ev.HasPerm {
				return nil, t.RemapPhysical(ev.PageID, ev.PhysicalID, ev.Perm)
			}
			return nil, t.RemapPhysical(ev.PageID, ev.PhysicalID)
		}
		if ev.HasPerm {
			return nil, t.Remap(ev.PageID, ev.Target, ev.Perm)
		}
		return nil, t.Remap(ev.PageID, ev.Target)
	case *RemapEvent:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil RemapEvent")
		}
		return t.Apply(*ev)
	case Event:
		return t.applyTagged(ev)
	case *Event:
		if ev == nil {
			return nil, fmt.Errorf("netsec: nil Event")
		}
		return t.applyTagged(*ev)
	default:
		return nil, fmt.Errorf("netsec: unsupported event %T", event)
	}
}

// ApplyEvent is an alias for Apply with a tagged Event.
func (t *Tracker) ApplyEvent(event Event) (*Execution, error) {
	return t.applyTagged(event)
}

func (t *Tracker) applyTagged(ev Event) (*Execution, error) {
	switch ev.Kind {
	case EventMap:
		if ev.Data != nil {
			return nil, t.MapBytes(ev.PageID, ev.Source, ev.Data, ev.Perm)
		}
		return nil, t.Map(ev.PageID, ev.Source, ev.Perm)
	case EventWrite:
		return nil, t.Write(ev.PageID, ev.Offset, ev.Data)
	case EventProtect:
		return nil, t.Protect(ev.PageID, ev.Perm)
	case EventExecute:
		return t.Execute(ev.PageID, ev.Offset, ev.Length)
	case EventUnmap:
		return nil, t.Unmap(ev.PageID)
	case EventRemap:
		if ev.UsePhysical {
			if ev.HasPerm {
				return nil, t.RemapPhysical(ev.PageID, ev.PhysicalID, ev.Perm)
			}
			return nil, t.RemapPhysical(ev.PageID, ev.PhysicalID)
		}
		if ev.HasPerm {
			return nil, t.Remap(ev.PageID, ev.Target, ev.Perm)
		}
		return nil, t.Remap(ev.PageID, ev.Target)
	default:
		return nil, fmt.Errorf("netsec: unsupported event kind %q", ev.Kind)
	}
}
