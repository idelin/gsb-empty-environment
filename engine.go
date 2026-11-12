package netsec

import (
	"fmt"
	"strconv"
)

// frame is one instantiation of a function or role template.
type frame struct {
	prefix string
	// params maps key parameter names to concrete argument terms.
	params map[string]Term
	// epochs maps epoch parameter names to concrete session labels.
	epochs map[string]string
	// locals maps local ids to their prefixed, instantiated identifiers.
	locals map[string]string
}

type engine struct {
	script *Script
	errs   []error
	ops    []Operation
	opSeen map[string]struct{}
}

// instantiate expands every template invocation in the script into concrete
// operations. Expansion depth is bounded by the explicit role depth and a
// fixed guard against accidental cyclic helper calls.
func (e *engine) instantiate() ([]Operation, []error) {
	for _, op := range e.script.TopLevel {
		switch op.Kind {
		case kindCall:
			e.invokeFunc(op.ID, op.Key.(Literal).Key, op.Payload, nil, map[string]string{})
		case kindRole:
			depth, err := strconv.Atoi(op.Session)
			if err != nil {
				e.errs = append(e.errs, fmt.Errorf("netsec: role call %q has bad depth: %w", op.ID, err))
				continue
			}
			e.invokeRole(op.ID, op.Key.(Literal).Key, depth, op.Payload)
		default:
			e.ops = append(e.ops, op)
		}
	}
	return e.ops, e.errs
}

func (e *engine) emit(op Operation) {
	if _, ok := e.opSeen[op.ID]; ok {
		e.errs = append(e.errs, fmt.Errorf("netsec: internal duplicate operation id %q", op.ID))
		return
	}
	e.opSeen[op.ID] = struct{}{}
	e.ops = append(e.ops, op)
}

func (e *engine) resolve(t Term, f *frame) Term {
	switch v := t.(type) {
	case Param:
		arg, ok := f.params[v.Name]
		if !ok {
			e.errs = append(e.errs, fmt.Errorf("netsec: unbound parameter %q", v.Name))
			return v
		}
		return arg
	case Local:
		if id, ok := f.locals[v.ID]; ok {
			return Local{ID: id}
		}
		e.errs = append(e.errs, fmt.Errorf("netsec: unbound local key %q", v.ID))
		return v
	default:
		return t
	}
}

func (e *engine) resolveAll(ts []Term, f *frame) []Term {
	out := make([]Term, 0, len(ts))
	for _, t := range ts {
		out = append(out, e.resolve(t, f))
	}
	return out
}

func (e *engine) invokeFunc(prefix, name string, args []Term, stack []string, epochs map[string]string) {
	fn, ok := e.script.Funcs[name]
	if !ok {
		e.errs = append(e.errs, fmt.Errorf("netsec: unknown function %q", name))
		return
	}
	for _, on := range stack {
		if on == name {
			e.errs = append(e.errs, fmt.Errorf("netsec: cyclic helper call through %q", name))
			return
		}
	}
	params := make(map[string]Term, len(fn.KeyParams))
	for i, p := range fn.KeyParams {
		params[p] = args[i]
	}
	frameEpochs := make(map[string]string, len(fn.EpochParams))
	for _, ep := range fn.EpochParams {
		if val, ok := epochs[ep]; ok {
			frameEpochs[ep] = val
		}
	}
	e.runBody(prefix, fn, &frame{
		prefix: prefix, params: params, epochs: frameEpochs,
		locals: map[string]string{},
	}, append(stack, name))
}

func (e *engine) invokeRole(prefix, name string, depth int, args []Term) {
	role, ok := e.script.Roles[name]
	if !ok {
		e.errs = append(e.errs, fmt.Errorf("netsec: unknown role %q", name))
		return
	}
	for frameIdx := 0; frameIdx < depth; frameIdx++ {
		fp := fmt.Sprintf("%s.f%d", prefix, frameIdx)
		params := make(map[string]Term, len(role.KeyParams))
		for i, p := range role.KeyParams {
			params[p] = args[i]
		}
		epochs := map[string]string{role.Epoch: strconv.Itoa(frameIdx)}
		for _, ep := range role.EpochParams {
			if ep == role.Epoch {
				continue
			}
			epochs[ep] = strconv.Itoa(frameIdx)
		}
		f := &frame{prefix: fp, params: params, epochs: epochs, locals: map[string]string{}}
		for idx, n := range role.Body {
			if r, ok := n.(Recurse); ok {
				if r.Epoch != role.Epoch {
					e.errs = append(e.errs, fmt.Errorf("netsec: role %q recurses on non-role epoch %q", name, r.Epoch))
				}
				if frameIdx == depth-1 {
					continue
				}
				if idx != len(role.Body)-1 {
					e.errs = append(e.errs, fmt.Errorf("netsec: role %q Recurse must be the final node", name))
				}
				break
			}
			e.runNode(fp, idx, n, f, nil)
		}
	}
}

func (e *engine) runBody(prefix string, fn *Func, f *frame, stack []string) {
	for i, n := range fn.Body {
		if _, ok := n.(Recurse); ok {
			e.errs = append(e.errs, fmt.Errorf("netsec: Recurse is only legal in role %q", fn.Name))
			continue
		}
		e.runNode(prefix, i, n, f, stack)
	}
}

func (e *engine) runNode(prefix string, idx int, n Node, f *frame, stack []string) {
	switch v := n.(type) {
	case NewKey:
		localID := prefix + ".key." + v.LocalID
		f.locals[v.LocalID] = localID
		session := ""
		if v.Epoch != "" {
			session = f.epochs[v.Epoch]
		}
		e.emit(Operation{
			ID:      fmt.Sprintf("%s.n%d.new.%s", prefix, idx, v.LocalID),
			Kind:    KindNewKey,
			Key:     Local{ID: localID},
			Owner:   v.Owner,
			Purpose: v.Purpose,
			Session: session,
		})
	case Encrypt:
		e.emit(Operation{
			ID: fmt.Sprintf("%s.n%d.enc", prefix, idx), Kind: KindEncrypt,
			Key: e.resolve(v.Key, f), Payload: e.resolveAll(v.Payload, f),
		})
	case Sign:
		e.emit(Operation{
			ID: fmt.Sprintf("%s.n%d.sign", prefix, idx), Kind: KindSign,
			Key: e.resolve(v.Key, f), Payload: e.resolveAll(v.Payload, f),
		})
	case Derive:
		e.emit(Operation{
			ID: fmt.Sprintf("%s.n%d.derive", prefix, idx), Kind: KindDerive,
			Key: e.resolve(v.Key, f), Payload: e.resolveAll(v.Payload, f),
		})
	case BindKey:
		e.emit(Operation{
			ID: fmt.Sprintf("%s.n%d.bind", prefix, idx), Kind: KindBindKey,
			Key: e.resolve(v.Target, f), Payload: []Term{e.resolve(v.Source, f)},
		})
	case Call:
		args := make([]Term, len(v.Args))
		for i, a := range v.Args {
			args[i] = e.resolve(a, f)
		}
		e.invokeFunc(prefix+".call."+v.Func, v.Func, args, stack, f.epochs)
	case Recurse:
		// Handled directly by invokeRole.
	}
}
