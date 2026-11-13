package netsec

import "fmt"

// PolicyContext is the information a table policy may inspect. Subject is the
// current effective subject (invoker or definer). OriginSubject is the subject
// that started the statement and never changes.
type PolicyContext struct {
	Subject          string
	EffectiveSubject string
	OriginSubject    string
	Table            string
	Event            Event
	RowID            RowID
	Row              Row
	Depth            int
	StatementID      string
}

// Policy decides whether Subject may perform Event on Table/RowID with Row.
type Policy interface {
	Allows(PolicyContext) bool
}

// PolicyFunc adapts a function to Policy.
type PolicyFunc func(PolicyContext) bool

// Allows implements Policy.
func (f PolicyFunc) Allows(c PolicyContext) bool {
	if f == nil {
		return false
	}
	return f(c)
}

// SimplePolicy adapts a subject/event/table function to Policy.
func SimplePolicy(fn func(subject string, event Event, table string) bool) Policy {
	return PolicyFunc(func(c PolicyContext) bool {
		if fn == nil {
			return false
		}
		return fn(c.Subject, c.Event, c.Table)
	})
}

// RowPolicy adapts a subject/event/table/row function to Policy.
func RowPolicy(fn func(subject string, event Event, table string, row Row) bool) Policy {
	return PolicyFunc(func(c PolicyContext) bool {
		if fn == nil {
			return false
		}
		return fn(c.Subject, c.Event, c.Table, c.Row)
	})
}

// EventSet is a set of DML events.
type EventSet map[Event]bool

// Events constructs an EventSet.
func Events(events ...Event) EventSet {
	set := make(EventSet, len(events))
	for _, e := range events {
		set[normaliseEvent(e)] = true
	}
	return set
}

// AllEvents returns a set containing Insert, Update and Delete.
func AllEvents() EventSet {
	return Events(EventInsert, EventUpdate, EventDelete)
}

// WriteEvents is an alias for AllEvents.
func WriteEvents() EventSet { return AllEvents() }

// Has reports whether the set contains event. EventAny in the set matches any
// event.
func (s EventSet) Has(event Event) bool {
	if s == nil {
		return false
	}
	if s[EventAny] {
		return true
	}
	return s[normaliseEvent(event)]
}

// Clone copies the set.
func (s EventSet) Clone() EventSet {
	out := make(EventSet, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

// SubjectPolicy maps subjects to permitted DML events. A "*" entry is used as
// a fallback for subjects without their own entry. Table-level scoping is done
// by assigning this policy to a specific Table.
type SubjectPolicy map[string]EventSet

// Allows implements Policy.
func (p SubjectPolicy) Allows(c PolicyContext) bool {
	if p == nil {
		return false
	}
	events, ok := p[c.Subject]
	if !ok {
		events, ok = p["*"]
	}
	if !ok {
		return false
	}
	return events.Has(c.Event)
}

// ExactSubjectPolicy maps a subject to its allowed events and does not fall
// back to a wildcard subject.
type ExactSubjectPolicy map[string]EventSet

// Allows implements Policy.
func (p ExactSubjectPolicy) Allows(c PolicyContext) bool {
	if p == nil {
		return false
	}
	events, ok := p[c.Subject]
	return ok && events.Has(c.Event)
}

// AllowAll permits every write.
var AllowAll Policy = PolicyFunc(func(PolicyContext) bool { return true })

// DenyAll denies every write.
var DenyAll Policy = PolicyFunc(func(PolicyContext) bool { return false })

// OwnerPolicy permits exactly the named subject. It is a convenient building
// block for definer-owned tables.
func OwnerPolicy(subject string) Policy {
	return PolicyFunc(func(c PolicyContext) bool { return c.Subject == subject })
}

// SubjectsPolicy permits any subject in subjects for the events in events.
func SubjectsPolicy(events EventSet, subjects ...string) Policy {
	allowed := make(map[string]bool, len(subjects))
	for _, s := range subjects {
		allowed[s] = true
	}
	return PolicyFunc(func(c PolicyContext) bool {
		return allowed[c.Subject] && events.Has(c.Event)
	})
}

// ValidatePolicy is a convenience check for policies that implement optional
// validation hooks.
func ValidatePolicy(p Policy) error {
	switch v := p.(type) {
	case nil:
		return nil
	case SubjectPolicy:
		for s, set := range v {
			if s == "" {
				return fmt.Errorf("netsec: empty subject in policy")
			}
			for e := range set {
				if normaliseEvent(e) == "" {
					return fmt.Errorf("netsec: invalid event in policy")
				}
			}
		}
	}
	return nil
}
