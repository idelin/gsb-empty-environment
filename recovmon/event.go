package recovmon

// EventKind enumerates the parser observations a Monitor understands.
type EventKind int

const (
	// EventBegin marks the start of a (possibly nested) field at Span.
	EventBegin EventKind = iota + 1
	// EventEnd marks the end of the named field at Span.End.
	EventEnd
	// EventConsume records bytes the parser has successfully read.
	EventConsume
	// EventError records a parse failure at the given byte range.
	EventError
	// EventRecovery records the parser jumping forward after an error.
	EventRecovery
)

// Event is a single parser observation.
//
// Span is mandatory for every kind. Name and Path are used by begin/end
// events to describe field nesting; Path is the full dotted field path.
// Target gives the resume offset for a recovery event.
type Event struct {
	Kind   EventKind
	Span   Interval
	Name   string
	Path   string
	Target int64
}

// Begin marks the start of a field spanning [start,end).
// path is the full dotted nesting path; name may be left empty when it
// equals the final path component.
func Begin(path string, start, end int64) Event {
	return Event{Kind: EventBegin, Span: Interval{Start: start, End: end}, Path: path, Name: leafName(path)}
}

// End closes the named field; span.End is the observed end offset.
func End(path string, span Interval) Event {
	return Event{Kind: EventEnd, Span: span, Path: path, Name: leafName(path)}
}

// Consume records successful reading of [start,end).
func Consume(start, end int64) Event {
	return Event{Kind: EventConsume, Span: Interval{Start: start, End: end}}
}

// Error records a parser failure covering [start,end).
func Error(start, end int64) Event {
	return Event{Kind: EventError, Span: Interval{Start: start, End: end}}
}

// Recovery records a resume at target, discarding [discardStart,target).
func Recovery(discardStart, target int64) Event {
	return Event{Kind: EventRecovery, Span: Interval{Start: discardStart, End: target}, Target: target}
}

func leafName(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '.' {
			return path[i+1:]
		}
	}
	return path
}
