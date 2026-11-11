package canon

// Message is one entry of the network message queue.
//
// From and To are fixed role/session labels rather than symbolic terms, so
// they are never renamed; only Payload is rewritten.
type Message struct {
	From    string
	To      string
	Payload Term
}

// RoleState holds the local variables of one role instance in one session.
type RoleState struct {
	Session string
	Role    string
	Vars    map[string]Term
}

// State is the full symbolic state handed over by the verification host.
type State struct {
	// Queue is the ordered network message queue.
	Queue []Message

	// Roles are the local role states. The slice order is irrelevant: role
	// states are always processed in a deterministic (session, role) order.
	Roles []RoleState

	// AttackerKnowledge is the set of terms the attacker has observed or
	// derived. Slice order is irrelevant; processing order is deterministic.
	AttackerKnowledge []Term
}

// Equal reports whether two states are structurally identical.
func StatesEqual(a, b *State) bool {
	if a == nil || b == nil {
		return a == b
	}
	if len(a.Queue) != len(b.Queue) ||
		len(a.Roles) != len(b.Roles) ||
		len(a.AttackerKnowledge) != len(b.AttackerKnowledge) {
		return false
	}
	for i := range a.Queue {
		m1, m2 := a.Queue[i], b.Queue[i]
		if m1.From != m2.From || m1.To != m2.To || !termsEqual(m1.Payload, m2.Payload) {
			return false
		}
	}
	for i := range a.Roles {
		r1, r2 := a.Roles[i], b.Roles[i]
		if r1.Session != r2.Session || r1.Role != r2.Role || len(r1.Vars) != len(r2.Vars) {
			return false
		}
		for k, v := range r1.Vars {
			if !termsEqual(v, r2.Vars[k]) {
				return false
			}
		}
	}
	for i := range a.AttackerKnowledge {
		if !termsEqual(a.AttackerKnowledge[i], b.AttackerKnowledge[i]) {
			return false
		}
	}
	return true
}

// cloneTerm returns an independent deep copy of t.
func cloneTerm(t Term) Term {
	switch x := t.(type) {
	case nil:
		return nil
	case Const:
		return Const{Name: x.Name}
	case Fresh:
		return Fresh{Session: x.Session, UID: x.UID}
	case Tuple:
		items := make([]Term, len(x.Items))
		for i, item := range x.Items {
			items[i] = cloneTerm(item)
		}
		return Tuple{Items: items}
	case SymEnc:
		return SymEnc{Plaintext: cloneTerm(x.Plaintext), Key: cloneTerm(x.Key)}
	case PKEEnc:
		return PKEEnc{Plaintext: cloneTerm(x.Plaintext), PubKey: cloneTerm(x.PubKey)}
	case FuncApp:
		args := make([]Term, len(x.Args))
		for i, arg := range x.Args {
			args[i] = cloneTerm(arg)
		}
		return FuncApp{Func: x.Func, Args: args}
	default:
		return nil
	}
}

// clone returns a deep copy so canonicalization never mutates its input.
func (s *State) clone() *State {
	if s == nil {
		return nil
	}
	out := &State{
		Queue:             make([]Message, len(s.Queue)),
		Roles:             make([]RoleState, len(s.Roles)),
		AttackerKnowledge: make([]Term, len(s.AttackerKnowledge)),
	}
	for i, msg := range s.Queue {
		out.Queue[i] = Message{From: msg.From, To: msg.To, Payload: cloneTerm(msg.Payload)}
	}
	for i, role := range s.Roles {
		vars := make(map[string]Term, len(role.Vars))
		for k, v := range role.Vars {
			vars[k] = cloneTerm(v)
		}
		out.Roles[i] = RoleState{Session: role.Session, Role: role.Role, Vars: vars}
	}
	for i, term := range s.AttackerKnowledge {
		out.AttackerKnowledge[i] = cloneTerm(term)
	}
	return out
}
