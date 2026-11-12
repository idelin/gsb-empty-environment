package netsec

import "sort"

type preparedProgram struct {
	funcs    []Function
	byName   map[string]int
	edgeRefs []edgeRef
	adj      [][]int
	entry    int
}

type edgeRef struct {
	from  int
	to    int
	index int
	delta int64
}

// Validate checks function names, frame sizes, edges, guards, and targets.
func (p Program) Validate() error {
	_, err := prepare(p)
	return err
}

func prepare(p Program) (*preparedProgram, error) {
	if p.Entry == "" {
		return nil, validationError("entry function is empty")
	}
	source := make([]Function, 0, len(p.Functions)+len(p.Funcs))
	source = append(source, p.Functions...)
	source = append(source, p.Funcs...)
	prepared := &preparedProgram{
		funcs:  make([]Function, 0, len(source)),
		byName: make(map[string]int, len(source)),
		adj:    make([][]int, len(source)),
	}
	for i, fn := range source {
		if fn.Name == "" {
			return nil, validationError("function %d has an empty name", i)
		}
		frameSize := fn.FrameSize
		if fn.FrameSize < 0 {
			return nil, validationError("function %q has a negative frame size", fn.Name)
		}
		if fn.Frame < 0 {
			return nil, validationError("function %q has a negative frame size", fn.Name)
		}
		if frameSize == 0 {
			frameSize = fn.Frame
		} else if fn.Frame != 0 && fn.Frame != frameSize {
			return nil, validationError("function %q has conflicting Frame and FrameSize", fn.Name)
		}
		if _, exists := prepared.byName[fn.Name]; exists {
			return nil, validationError("duplicate function %q", fn.Name)
		}
		calls, err := normalizeCalls(fn)
		if err != nil {
			return nil, err
		}
		prepared.funcs = append(prepared.funcs, Function{
			Name:      fn.Name,
			FrameSize: frameSize,
			Calls:     calls,
		})
		prepared.byName[fn.Name] = i
	}
	if _, ok := prepared.byName[p.Entry]; !ok {
		return nil, validationError("entry function %q is not defined", p.Entry)
	}
	prepared.entry = prepared.byName[p.Entry]

	refs := make([]edgeRef, 0)
	for i := range prepared.funcs {
		fn := &prepared.funcs[i]
		for j := range fn.Calls {
			call := &fn.Calls[j]
			to, ok := prepared.byName[call.Target]
			if !ok {
				return nil, validationError("function %q calls undefined function %q", fn.Name, call.Target)
			}
			refs = append(refs, edgeRef{
				from:  i,
				to:    to,
				index: j,
				delta: call.Delta,
			})
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].from != refs[j].from {
			return refs[i].from < refs[j].from
		}
		return refs[i].index < refs[j].index
	})
	prepared.edgeRefs = refs
	for i, ref := range refs {
		prepared.adj[ref.from] = append(prepared.adj[ref.from], i)
	}
	return prepared, nil
}

func normalizeCalls(fn Function) ([]Edge, error) {
	calls := make([]Edge, len(fn.Calls))
	for j, call := range fn.Calls {
		target := call.Target
		if target == "" {
			target = call.To
		} else if call.To != "" && call.To != target {
			return nil, validationError("function %q has conflicting Target and To at edge %d", fn.Name, j)
		}
		kind := call.Kind
		if call.Tail {
			if kind == CallNormal {
				kind = CallTail
			} else if kind != CallTail {
				return nil, validationError("function %q has an invalid call kind at edge %d", fn.Name, j)
			}
		}
		if kind != CallNormal && kind != CallTail {
			return nil, validationError("function %q has an invalid call kind at edge %d", fn.Name, j)
		}
		guard := call.Guard
		if guard.Op == OpAlways && call.Cond.Op != OpAlways {
			guard = call.Cond
		} else if guard.Op != OpAlways && call.Cond.Op != OpAlways && guard != call.Cond {
			return nil, validationError("function %q has conflicting Guard and Cond at edge %d", fn.Name, j)
		}
		if guard.Op > OpNotEqual {
			return nil, validationError("function %q has an invalid guard at edge %d", fn.Name, j)
		}
		delta := call.Delta
		if call.Decrement != 0 {
			derived := -call.Decrement
			if delta == 0 {
				delta = derived
			} else if delta != derived {
				return nil, validationError("function %q has conflicting Delta and Decrement at edge %d", fn.Name, j)
			}
		}
		calls[j] = Edge{
			Target: target,
			Kind:   kind,
			Guard:  guard,
			Delta:  delta,
			Label:  call.Label,
		}
	}
	return calls, nil
}
