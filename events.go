package streamdec

import "fmt"

// EventKind identifies the type of a decode event.
type EventKind int

const (
	EventObjectStart EventKind = iota // '{' encountered
	EventObjectEnd                    // matching '}' encountered
	EventFieldStart                   // object key parsed, value follows
	EventFieldEnd                     // value for the current field is complete
	EventValue                        // scalar value (string, float64, bool, nil)
)

func (k EventKind) String() string {
	switch k {
	case EventObjectStart:
		return "ObjectStart"
	case EventObjectEnd:
		return "ObjectEnd"
	case EventFieldStart:
		return "FieldStart"
	case EventFieldEnd:
		return "FieldEnd"
	case EventValue:
		return "Value"
	default:
		return "Unknown"
	}
}

// Event is a single decode event. Events belonging to one object are
// buffered internally and only published once the object is complete.
//
// Field is set on FieldStart events and on Value/ObjectStart events that
// appear as a field value. Offset is the absolute byte offset in the
// stream where the corresponding token starts (for FieldEnd/ObjectEnd:
// where it ends).
type Event struct {
	Kind   EventKind
	Field  string
	Value  any // string, float64, bool or nil for EventValue
	Offset int64
}

func (e Event) String() string {
	switch e.Kind {
	case EventFieldStart:
		return fmt.Sprintf("[%6d] FieldStart   %q", e.Offset, e.Field)
	case EventFieldEnd:
		return fmt.Sprintf("[%6d] FieldEnd     %q", e.Offset, e.Field)
	case EventValue:
		if e.Field != "" {
			return fmt.Sprintf("[%6d] Value        %q = %#v", e.Offset, e.Field, e.Value)
		}
		return fmt.Sprintf("[%6d] Value        %#v", e.Offset, e.Value)
	case EventObjectStart:
		if e.Field != "" {
			return fmt.Sprintf("[%6d] ObjectStart  %q", e.Offset, e.Field)
		}
		return fmt.Sprintf("[%6d] ObjectStart", e.Offset)
	case EventObjectEnd:
		return fmt.Sprintf("[%6d] ObjectEnd", e.Offset)
	default:
		return fmt.Sprintf("[%6d] %s", e.Offset, e.Kind)
	}
}
