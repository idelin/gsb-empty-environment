package rbcast

// Membership tracks the valid subscriber set and each member's generation.
// The generation is incremented on every rejoin, which invalidates any acks
// issued before the previous leave: a member must never reuse its pre-leave
// ack epoch.
type Membership struct {
	generation map[string]uint64
	valid      map[string]bool
}

func NewMembership() *Membership {
	return &Membership{
		generation: make(map[string]uint64),
		valid:      make(map[string]bool),
	}
}

// Join adds (or re-adds) a member. First join gets generation 1; every
// rejoin bumps the generation so stale acks are rejected.
func (m *Membership) Join(id string) uint64 {
	m.generation[id]++
	m.valid[id] = true
	return m.generation[id]
}

// Leave removes a member from the valid set. Its generation is preserved so
// that late-arriving acks from the pre-leave epoch remain recognisable and
// rejected.
func (m *Membership) Leave(id string) {
	m.valid[id] = false
}

// IsValid reports whether the member is currently subscribed.
func (m *Membership) IsValid(id string) bool { return m.valid[id] }

// Generation returns the member's current generation (0 if never joined).
func (m *Membership) Generation(id string) uint64 { return m.generation[id] }

// ValidMembers lists the currently valid subscriber IDs.
func (m *Membership) ValidMembers() []string {
	out := make([]string, 0, len(m.valid))
	for id, v := range m.valid {
		if v {
			out = append(out, id)
		}
	}
	return out
}

// AcceptAck validates an ack against the current membership state: the
// sender must be a valid subscriber and the ack's generation must equal the
// member's current generation. Acks from a pre-leave epoch are rejected.
func (m *Membership) AcceptAck(a Ack) bool {
	if !m.valid[a.MemberID] {
		return false
	}
	return m.generation[a.MemberID] == a.Generation
}
