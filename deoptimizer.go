package minijit

import "fmt"

// DeoptResult describes one deoptimization event.
type DeoptResult struct {
	Interpreter *Interpreter
	Reason      string
	Depth       int
}

// Deopt consumes deoptimization metadata and produces a ready-to-resume
// interpreter. Reconstruction happens strictly outer-to-inner so that the
// innermost inlined frame ends up on top of the interpreter stack.
func Deopt(prog *Program, meta *DeoptMetadata) (*DeoptResult, error) {
	if meta == nil || meta.Machine == nil || meta.Machine.Root == nil {
		return nil, fmt.Errorf("minijit: invalid deoptimization metadata")
	}
	mf := meta.Machine

	// Step 1: materialize the whole virtual object graph (two phases,
	// cycle-safe, identity preserving).
	mat := NewMaterializer(mf)
	if _, err := mat.Materialize(); err != nil {
		return nil, err
	}

	// Step 2: walk the FrameState tree outer -> inner and push one
	// independent interpreter frame per inlined layer.
	in := NewInterpreter(prog)
	depth := 0
	for fs := mf.Root; fs != nil; fs = fs.Inner {
		if fs.Function == nil {
			return nil, fmt.Errorf("minijit: frame state layer %d has no function", depth)
		}
		locals := make([]Value, fs.Function.NLocals)
		for i, inp := range fs.Locals {
			if i >= len(locals) {
				return nil, fmt.Errorf("minijit: too many locals for %s", fs.Function.Name)
			}
			v, err := mat.Resolve(inp)
			if err != nil {
				return nil, fmt.Errorf("minijit: rebuild local %d of %s: %w", i, fs.Function.Name, err)
			}
			locals[i] = v
		}
		stack := make([]Value, 0, len(fs.Stack))
		for _, inp := range fs.Stack {
			v, err := mat.Resolve(inp)
			if err != nil {
				return nil, fmt.Errorf("minijit: rebuild stack of %s: %w", fs.Function.Name, err)
			}
			stack = append(stack, v)
		}
		in.frames = append(in.frames, &Frame{
			fn:     fs.Function,
			pc:     fs.PC,
			locals: locals,
			stack:  stack,
		})
		depth++
	}

	return &DeoptResult{Interpreter: in, Reason: meta.Reason, Depth: depth}, nil
}
