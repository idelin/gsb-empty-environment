package bootpolicy

import "strings"

// LoadEvent is one concrete child component being loaded by an optional
// parent. It is the unit of admission: every edge of a boot input, and every
// root component, is evaluated as a separate event.
type LoadEvent struct {
	BootTime uint64
	Child    Component
	Parent   *Component
}

func (p ParentScope) matches(parent *Component) bool {
	if parent == nil {
		return false
	}
	return parent.SignerID == p.SignerID && parent.Label == p.Label
}

// selectorMatches reports whether the common selector of e matches ev. The
// exempt-only fields (root, exact digest, expiry) are intentionally excluded.
func (e Entry) selectorMatches(ev LoadEvent) bool {
	c := ev.Child
	if c.SignerID != e.SignerID {
		return false
	}
	if !strings.HasPrefix(c.Label, e.LabelPrefix) {
		return false
	}
	if c.Generation < e.GenLo || c.Generation > e.GenHi {
		return false
	}
	if e.Location != "" && e.Location != c.Location {
		return false
	}
	if e.Parent != nil && !e.Parent.matches(ev.Parent) {
		return false
	}
	return true
}

// Matches reports whether the full entry matches ev, including for exempt
// entries the trusted root, exact image digest and lifetime checks.
func (e Entry) Matches(ev LoadEvent) bool {
	if !e.selectorMatches(ev) {
		return false
	}
	if e.Kind == KindExempt {
		if e.RootID != "" && e.RootID != ev.Child.RootID {
			return false
		}
		if !e.Digest.equal(ev.Child.Digest) {
			return false
		}
		if e.NotAfter != 0 && ev.BootTime > e.NotAfter {
			return false
		}
	}
	return true
}

// EventAdmit decides one load event. The event is rejected only when at least
// one deny entry matches. A matching, still-valid exemption for the exact
// image digest rescues the event from matching deny entries. Exemptions never
// override one another and never transfer to other images because each event
// is evaluated on its own.
func (p *Policy) EventAdmit(ev LoadEvent) bool {
	denied := false
	for i := range p.Entries {
		e := &p.Entries[i]
		if !e.Matches(ev) {
			continue
		}
		if e.Kind == KindDeny {
			denied = true
		}
	}
	// An exemption can only rescue when it is itself valid for this exact
	// event; the selector and exempt-only checks above enforce that.
	if denied {
		for i := range p.Entries {
			e := &p.Entries[i]
			if e.Kind == KindExempt && e.Matches(ev) {
				return true
			}
		}
	}
	return !denied
}

// Admit evaluates every load event implied by a boot input and admits the
// boot only when all of them are admitted. Components with no incoming edge
// are treated as root components and evaluated with a nil parent. A component
// reached through several parents is evaluated once per parent, so a
// parent-scoped exemption cannot be shared across parent chains.
func (p *Policy) Admit(in BootInput) (bool, error) {
	if err := p.Validate(); err != nil {
		return false, err
	}
	if err := validateInput(in); err != nil {
		return false, err
	}
	events, err := eventsOf(in)
	if err != nil {
		return false, err
	}
	for _, ev := range events {
		if !p.EventAdmit(ev) {
			return false, nil
		}
	}
	return true, nil
}

func validateInput(in BootInput) error {
	n := len(in.Components)
	if n == 0 || n > MaxComponents {
		return ErrInvalidInput
	}
	for i := range in.Components {
		c := &in.Components[i]
		if c.SignerID == "" || c.Label == "" || c.Digest.Algorithm == "" || len(c.Digest.Hash) == 0 {
			return ErrInvalidInput
		}
	}
	hasIncoming := make([]bool, n)
	for _, edge := range in.Edges {
		if edge.ParentIndex < 0 || edge.ParentIndex >= n ||
			edge.ChildIndex < 0 || edge.ChildIndex >= n ||
			edge.ParentIndex == edge.ChildIndex {
			return ErrInvalidInput
		}
		hasIncoming[edge.ChildIndex] = true
	}
	return nil
}

func eventsOf(in BootInput) ([]LoadEvent, error) {
	n := len(in.Components)
	hasIncoming := make([]bool, n)
	events := make([]LoadEvent, 0, len(in.Edges)+n)
	for i := range in.Edges {
		edge := &in.Edges[i]
		hasIncoming[edge.ChildIndex] = true
		parent := in.Components[edge.ParentIndex]
		events = append(events, LoadEvent{
			BootTime: in.BootTime,
			Child:    in.Components[edge.ChildIndex],
			Parent:   &parent,
		})
	}
	for i := range in.Components {
		if !hasIncoming[i] {
			events = append(events, LoadEvent{
				BootTime: in.BootTime,
				Child:    in.Components[i],
			})
		}
	}
	return events, nil
}
