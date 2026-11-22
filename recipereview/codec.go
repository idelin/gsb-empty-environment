package recipereview

import (
	"errors"
	"fmt"
)

// Assignment maps a recipe's field names to caller-supplied bounded strings.
type Assignment map[string][]byte

// ValidateAssignment checks that every declared field is present, within its
// length bounds and drawn from its alphabet.
func (r *Recipe) ValidateAssignment(in Assignment) error {
	for name, f := range r.Fields {
		v, ok := in[name]
		if !ok {
			return fmt.Errorf("recipereview: missing value for field %q", name)
		}
		if len(v) < f.MinLen || len(v) > f.MaxLen {
			return fmt.Errorf("recipereview: value for field %q has length %d outside [%d,%d]",
				name, len(v), f.MinLen, f.MaxLen)
		}
		for _, c := range v {
			if !f.Alphabet.Contains(c) {
				return fmt.Errorf("recipereview: value for field %q contains byte 0x%02x outside its alphabet",
					name, c)
			}
		}
	}
	return nil
}

// Encode turns a validated field assignment into the recipe info string by
// recursively evaluating the encoding expression.
func (r *Recipe) Encode(in Assignment) ([]byte, error) {
	if err := ValidateRecipe(r); err != nil {
		return nil, err
	}
	if err := r.ValidateAssignment(in); err != nil {
		return nil, err
	}
	out := make([]byte, 0, r.encodedSizeHint(in))
	var err error
	out, err = encodeExpr(r, r.Root, in, out)
	return out, err
}

func encodeExpr(r *Recipe, e Expr, in Assignment, out []byte) ([]byte, error) {
	switch n := e.(type) {
	case *ConstExpr:
		return append(out, n.Value...), nil
	case *RawExpr:
		return append(out, in[n.Field]...), nil
	case *PrefixExpr:
		v := in[n.Field]
		out = append(out, byte(len(v)))
		return append(out, v...), nil
	case *EscapeExpr:
		for _, c := range in[n.Field] {
			if c == n.Escape || c == n.Terminator {
				out = append(out, n.Escape)
			}
			out = append(out, c)
		}
		return append(out, n.Terminator), nil
	case *BranchExpr:
		if len(in[n.Field]) == 0 {
			return encodeExpr(r, n.IfEmpty, in, out)
		}
		return encodeExpr(r, n.IfNonEmpty, in, out)
	case *SeqExpr:
		var err error
		for _, part := range n.Parts {
			out, err = encodeExpr(r, part, in, out)
			if err != nil {
				return nil, err
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("recipereview: unknown expression type %T", e)
	}
}

func (r *Recipe) encodedSizeHint(in Assignment) int {
	total := 0
	var walk func(Expr)
	walk = func(e Expr) {
		switch n := e.(type) {
		case *ConstExpr:
			total += len(n.Value)
		case *RawExpr:
			total += len(in[n.Field])
		case *PrefixExpr:
			total += 1 + len(in[n.Field])
		case *EscapeExpr:
			v := in[n.Field]
			for _, c := range v {
				if c == n.Escape || c == n.Terminator {
					total++
				}
			}
			total += len(v) + 1
		case *BranchExpr:
			if len(in[n.Field]) == 0 {
				walk(n.IfEmpty)
			} else {
				walk(n.IfNonEmpty)
			}
		case *SeqExpr:
			for _, part := range n.Parts {
				walk(part)
			}
		}
	}
	walk(r.Root)
	return total
}

// Opcode is one linearized encoding instruction of an independent, non
// recursive encoder. Programs are compiled from the same validated expression
// but executed by a separate interpreter, giving a second implementation to
// cross-check Encode against.
type Opcode int

const (
	OpConst Opcode = iota
	OpRaw
	OpPrefix
	OpEscape
	OpBranchEmpty
	OpBranchNonEmpty
	OpEnd
)

// Instruction is one step of a compiled encoding program.
type Instruction struct {
	Op                 Opcode
	Field              string
	Data               []byte
	Escape, Terminator byte
	// Jump is the program offset of the next instruction for branch arms.
	Jump int
}

// Program is a linearized, independently executable encoding of a recipe.
type Program struct {
	recipe *Recipe
	code   []Instruction
}

// CompileProgram linearizes the recipe expression into an opcode program.
func (r *Recipe) CompileProgram() (*Program, error) {
	if err := ValidateRecipe(r); err != nil {
		return nil, err
	}
	p := &Program{recipe: r}
	if err := p.compile(r.Root); err != nil {
		return nil, err
	}
	p.code = append(p.code, Instruction{Op: OpEnd})
	return p, nil
}

func (p *Program) compile(e Expr) error {
	switch n := e.(type) {
	case *ConstExpr:
		p.code = append(p.code, Instruction{Op: OpConst, Data: append([]byte(nil), n.Value...)})
	case *RawExpr:
		p.code = append(p.code, Instruction{Op: OpRaw, Field: n.Field})
	case *PrefixExpr:
		p.code = append(p.code, Instruction{Op: OpPrefix, Field: n.Field})
	case *EscapeExpr:
		p.code = append(p.code, Instruction{
			Op: OpEscape, Field: n.Field, Escape: n.Escape, Terminator: n.Terminator,
		})
	case *BranchExpr:
		branchAt := len(p.code)
		p.code = append(p.code, Instruction{})
		if err := p.compile(n.IfEmpty); err != nil {
			return err
		}
		jumpAt := len(p.code)
		p.code = append(p.code, Instruction{})
		nonEmptyStart := len(p.code)
		if err := p.compile(n.IfNonEmpty); err != nil {
			return err
		}
		end := len(p.code)
		p.code[branchAt] = Instruction{
			Op: OpBranchEmpty, Field: n.Field, Jump: nonEmptyStart,
		}
		p.code[jumpAt] = Instruction{Op: OpBranchNonEmpty, Jump: end}
	case *SeqExpr:
		for _, part := range n.Parts {
			if err := p.compile(part); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("recipereview: unknown expression type %T", e)
	}
	return nil
}

// Encode executes the program without recursing through the expression tree,
// providing an encoding path independent from Recipe.Encode.
func (p *Program) Encode(in Assignment) ([]byte, error) {
	if err := p.recipe.ValidateAssignment(in); err != nil {
		return nil, err
	}
	out := make([]byte, 0)
	pc := 0
	for pc < len(p.code) {
		ins := p.code[pc]
		switch ins.Op {
		case OpConst:
			out = append(out, ins.Data...)
			pc++
		case OpRaw:
			out = append(out, in[ins.Field]...)
			pc++
		case OpPrefix:
			v := in[ins.Field]
			out = append(out, byte(len(v)))
			out = append(out, v...)
			pc++
		case OpEscape:
			for _, c := range in[ins.Field] {
				if c == ins.Escape || c == ins.Terminator {
					out = append(out, ins.Escape)
				}
				out = append(out, c)
			}
			out = append(out, ins.Terminator)
			pc++
		case OpBranchEmpty:
			if len(in[ins.Field]) == 0 {
				pc++
			} else {
				pc = ins.Jump
			}
		case OpBranchNonEmpty:
			pc = ins.Jump
		case OpEnd:
			return out, nil
		default:
			return nil, fmt.Errorf("recipereview: unknown opcode %d", ins.Op)
		}
	}
	return nil, errors.New("recipereview: program ran past end")
}

// Decode parses an info string back into a unique field assignment. It returns
// an error when the bytes are not a valid encoding of this recipe or when more
// than one assignment could produce them.
func (r *Recipe) Decode(info []byte) (Assignment, error) {
	if err := ValidateRecipe(r); err != nil {
		return nil, err
	}
	results, err := decodeExpr(r, r.Root, info, 0, Assignment{})
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	valid := make([]Assignment, 0, len(results))
	for _, cand := range results {
		if cand.end != len(info) {
			continue
		}
		complete := cloneAssignment(cand.in)
		for name, f := range r.Fields {
			if _, ok := complete[name]; !ok {
				if f.MinLen > 0 {
					complete = nil
					break
				}
				complete[name] = []byte{}
			}
		}
		if complete == nil {
			continue
		}
		key := assignmentKey(r, complete)
		if seen[key] {
			continue
		}
		seen[key] = true
		valid = append(valid, complete)
	}
	if len(valid) == 0 {
		return nil, errors.New("recipereview: bytes are not a valid recipe encoding")
	}
	if len(valid) > 1 {
		return nil, errors.New("recipereview: ambiguous encoding, multiple assignments decode")
	}
	return valid[0], nil
}

func assignmentKey(r *Recipe, in Assignment) string {
	var buf []byte
	for _, name := range r.sortedFieldNames() {
		buf = append(buf, name...)
		buf = append(buf, 0)
		buf = append(buf, in[name]...)
		buf = append(buf, 0)
	}
	return string(buf)
}

const maxDecodeAttempts = 10000

type decodeState struct {
	in  Assignment
	end int
}

// decodeExpr returns every (partial assignment, end offset) reachable by
// parsing e from offset pos in info.
func decodeExpr(r *Recipe, e Expr, info []byte, pos int, cur Assignment) ([]decodeState, error) {
	attempts := 0
	var rec func(Expr, int, Assignment) ([]decodeState, error)
	rec = func(node Expr, at int, in Assignment) ([]decodeState, error) {
		attempts++
		if attempts > maxDecodeAttempts {
			return nil, errors.New("recipereview: decode attempt budget exceeded")
		}
		switch n := node.(type) {
		case *ConstExpr:
			if at+len(n.Value) <= len(info) && string(info[at:at+len(n.Value)]) == string(n.Value) {
				return []decodeState{{in: cloneAssignment(in), end: at + len(n.Value)}}, nil
			}
			return nil, nil
		case *RawExpr:
			f := r.Fields[n.Field]
			if at+f.MaxLen > len(info) {
				return nil, nil
			}
			v := append([]byte(nil), info[at:at+f.MaxLen]...)
			if merged, ok := mergeValue(in, n.Field, v); ok {
				return []decodeState{{in: merged, end: at + f.MaxLen}}, nil
			}
			return nil, nil
		case *PrefixExpr:
			if at >= len(info) {
				return nil, nil
			}
			nl := int(info[at])
			start := at + 1
			if start+nl > len(info) {
				return nil, nil
			}
			f := r.Fields[n.Field]
			if nl < f.MinLen || nl > f.MaxLen {
				return nil, nil
			}
			v := append([]byte(nil), info[start:start+nl]...)
			if merged, ok := mergeValue(in, n.Field, v); ok {
				return []decodeState{{in: merged, end: start + nl}}, nil
			}
			return nil, nil
		case *EscapeExpr:
			f := r.Fields[n.Field]
			run := at
			value := make([]byte, 0)
			for run < len(info) {
				c := info[run]
				if c == n.Terminator {
					if len(value) < f.MinLen || len(value) > f.MaxLen {
						return nil, nil
					}
					if !valueInAlphabet(f, value) {
						return nil, nil
					}
					if merged, ok := mergeValue(in, n.Field, value); ok {
						return []decodeState{{in: merged, end: run + 1}}, nil
					}
					return nil, nil
				}
				if c == n.Escape {
					run++
					if run >= len(info) {
						return nil, nil
					}
					next := info[run]
					if next != n.Escape && next != n.Terminator {
						return nil, nil
					}
					value = append(value, next)
				} else {
					if !f.Alphabet.Contains(c) {
						return nil, nil
					}
					value = append(value, c)
				}
				run++
			}
			return nil, nil
		case *BranchExpr:
			out := make([]decodeState, 0)
			emptyIn := cloneAssignment(in)
			if merged, ok := mergeValue(emptyIn, n.Field, []byte{}); ok {
				states, err := rec(n.IfEmpty, at, merged)
				if err != nil {
					return nil, err
				}
				for _, st := range states {
					if len(st.in[n.Field]) == 0 {
						out = append(out, st)
					}
				}
			}
			more, err := decodeNonEmptyBranches(r, n, info, at, in, rec)
			if err != nil {
				return nil, err
			}
			return append(out, more...), nil
		case *SeqExpr:
			frontrunners := []decodeState{{in: cloneAssignment(in), end: at}}
			for _, part := range n.Parts {
				next := make([]decodeState, 0)
				for _, st := range frontrunners {
					more, err := rec(part, st.end, st.in)
					if err != nil {
						return nil, err
					}
					next = append(next, more...)
				}
				frontrunners = next
			}
			return frontrunners, nil
		default:
			return nil, fmt.Errorf("recipereview: unknown expression type %T", node)
		}
	}
	return rec(e, pos, cur)
}

func valueInAlphabet(f *Field, v []byte) bool {
	for _, c := range v {
		if !f.Alphabet.Contains(c) {
			return false
		}
	}
	return true
}

// decodeNonEmptyBranches tries every non-empty value of the branch field whose
// first bytes match info, then parses the non-empty arm.
func decodeNonEmptyBranches(r *Recipe, n *BranchExpr, info []byte, at int, in Assignment,
	rec func(Expr, int, Assignment) ([]decodeState, error)) ([]decodeState, error) {
	f := r.Fields[n.Field]
	out := make([]decodeState, 0)
	base := cloneAssignment(in)
	delete(base, n.Field)
	for _, v := range fieldValues(f) {
		if len(v) == 0 {
			continue
		}
		merged, ok := mergeValue(base, n.Field, v)
		if !ok {
			continue
		}
		states, err := rec(n.IfNonEmpty, at, merged)
		if err != nil {
			return nil, err
		}
		out = append(out, states...)
	}
	return out, nil
}

// mergeValue binds field to value inside a copied assignment, enforcing
// consistency when the same field is referenced more than once.
func mergeValue(in Assignment, field string, value []byte) (Assignment, bool) {
	if existing, ok := in[field]; ok {
		if string(existing) != string(value) {
			return nil, false
		}
		return in, true
	}
	out := cloneAssignment(in)
	out[field] = append([]byte(nil), value...)
	return out, true
}

func cloneAssignment(in Assignment) Assignment {
	out := make(Assignment, len(in))
	for k, v := range in {
		out[k] = append([]byte(nil), v...)
	}
	return out
}
