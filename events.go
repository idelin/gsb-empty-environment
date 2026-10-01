// Package streamdec implements an incremental, event-driven decoder for a
// small length-prefixed data format (see doc.go). The decoder never executes
// host code: the format is pure data and decoding is a finite state machine
// over bytes.
package streamdec

// EventKind identifies the type of a decoded Event.
type EventKind int

const (
	EventObjectStart EventKind = iota // '{'
	EventObjectEnd                    // '}'
	EventFieldStart                   // a field name was decoded; Name is set
	EventFieldEnd                     // the value of the current field is complete
	EventString                       // a string value; Str is set
	EventInt                          // an integer value; Int is set
	EventBool                         // a boolean value; Bool is set
	EventNull                         // a null value
)

// Event is a single decoding event. Events for one top-level object are
// published as a batch, starting with EventObjectStart and ending with
// EventObjectEnd, only after the object has been decoded in full.
type Event struct {
	Kind EventKind
	Name string // EventFieldStart: field name
	Str  string // EventString: string value
	Int  int64  // EventInt: integer value
	Bool bool   // EventBool: boolean value
}

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
	case EventString:
		return "String"
	case EventInt:
		return "Int"
	case EventBool:
		return "Bool"
	case EventNull:
		return "Null"
	}
	return "Unknown"
}
