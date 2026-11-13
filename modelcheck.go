package netsec

// System is the small-step interface the model checker drives. Both Host
// (reviewed) and ReviewHost (pending review) implement it.
type System interface {
	AddProcess(id uint32, lvl IntegrityLevel) ProcessRef
	AddObject(id ObjectID, typ ObjectType)
	OpenHandle(proc ProcessRef, obj ObjectID, access Access, inherit bool) (HandleRef, error)
	CloseHandle(proc ProcessRef, ref HandleRef) error
	RestartProcess(id uint32)
	Duplicate(req DuplicateRequest) (HandleRef, error)
	IssueBrokerCapability(src, tgt ProcessRef, typeName string) (*Capability, error)
	ProcRef(id uint32) (ProcessRef, bool)
	HandleGeneration(procID uint32, slot uint32) (uint32, bool)
	HasEffectiveAccess(procID uint32, bit Access) bool
	StateKey() string
	CloneSystem() System
}

// State is a clonable, keyable model state for breadth-first search.
type State[S any] interface {
	Clone() S
	Key() string
}

// Action is one small-step transition. Do mutates the state in place (it
// receives a fresh clone); Violation reports whether the state breaks the
// safety property under test.
type Action[S any] struct {
	Name      string
	Do        func(s S)
	Violation func(s S) bool
}

// BFS explores the state space breadth-first and returns the shortest action
// trace that reaches a violating state, or nil if none is reachable within
// maxDepth. Because the search is breadth-first, the returned trace is
// guaranteed to be minimal.
func BFS[S State[S]](initial S, actions []Action[S], maxDepth int) (trace []string, found bool) {
	for _, a := range actions {
		if a.Violation(initial) {
			return nil, true
		}
	}
	type node struct {
		state S
		trace []string
	}
	seen := map[string]struct{}{initial.Key(): {}}
	queue := []node{{state: initial}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if len(n.trace) >= maxDepth {
			continue
		}
		for _, a := range actions {
			next := n.state.Clone()
			a.Do(next)
			tr := make([]string, 0, len(n.trace)+1)
			tr = append(tr, n.trace...)
			tr = append(tr, a.Name)
			if a.Violation(next) {
				return tr, true
			}
			k := next.Key()
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			queue = append(queue, node{state: next, trace: tr})
		}
	}
	return nil, false
}
