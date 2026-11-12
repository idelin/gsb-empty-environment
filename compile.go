package netsec

import "math/big"

// op identifies a flattened interpreter instruction.
type op int

const (
	opAssign op = iota
	opObserve
	opFail
	opFlip
	opRepeatEnter
	opRepeatBack
	opRepeatExit
	opWhileEnter
	opWhileBack
	opWhileExit
	opJump
	opHalt
)

// instr is one flattened instruction. Jumps use absolute code indexes.
type instr struct {
	op      op
	name    string // Assign.Name / Observe.Label / Fail.Name
	expr    Expr   // Assign.Expr / Observe.Expr / loop condition
	limit   int    // While.Limit / Repeat.Count
	nodeID  string // Flip.NodeID
	weights []*big.Rat
	targets []int // flip branch targets; loop jump targets
}

type program struct {
	code []instr
}

// compile validates every probabilistic node and flattens the program into
// linear instructions with explicit, bounded loop control.
func compile(stmts []Stmt) (*program, error) {
	c := &compiler{}
	if err := c.block(stmts); err != nil {
		return nil, err
	}
	c.code = append(c.code, instr{op: opHalt})
	return &program{code: c.code}, nil
}

type compiler struct {
	code []instr
}

func (c *compiler) block(stmts []Stmt) error {
	for _, st := range stmts {
		if err := c.stmt(st); err != nil {
			return err
		}
	}
	return nil
}

func (c *compiler) stmt(st Stmt) error {
	switch s := st.(type) {
	case Assign:
		c.code = append(c.code, instr{op: opAssign, name: s.Name, expr: s.Expr})
	case Observe:
		c.code = append(c.code, instr{op: opObserve, name: s.Label, expr: s.Expr})
	case Fail:
		c.code = append(c.code, instr{op: opFail, name: s.Name})
	case Seq:
		return c.block(s.Body)
	case Flip:
		return c.flip(s)
	case Repeat:
		return c.repeat_(s)
	case While:
		return c.while(s)
	default:
		return &EvalError{Reason: "unknown statement type"}
	}
	return nil
}

func (c *compiler) flip(f Flip) error {
	if err := validateFlip(f); err != nil {
		return err
	}
	weights := make([]*big.Rat, len(f.Branches))
	for i, br := range f.Branches {
		weights[i] = new(big.Rat).Set(br.Weight)
	}

	idx := len(c.code)
	c.code = append(c.code, instr{op: opFlip, nodeID: f.NodeID, weights: weights})
	targets := make([]int, len(f.Branches))
	jumps := make([]int, len(f.Branches))
	for i, br := range f.Branches {
		targets[i] = len(c.code)
		if err := c.block(br.Body); err != nil {
			return err
		}
		jumps[i] = len(c.code)
		c.code = append(c.code, instr{})
	}
	join := len(c.code)
	c.code[idx].targets = targets
	for _, jumpAt := range jumps {
		c.code[jumpAt] = instr{op: opJump, targets: []int{join}}
	}
	return nil
}

func (c *compiler) repeat_(r Repeat) error {
	if r.Count < 0 {
		return &EvalError{Reason: "repeat count must be non-negative"}
	}
	enter := len(c.code)
	c.code = append(c.code, instr{op: opRepeatEnter, limit: r.Count})
	body := len(c.code)
	if err := c.block(r.Body); err != nil {
		return err
	}
	back := len(c.code)
	c.code = append(c.code, instr{op: opRepeatBack, targets: []int{body}})
	exit := len(c.code)
	c.code = append(c.code, instr{op: opRepeatExit})
	c.code[enter].targets = []int{body, exit}
	c.code[back].targets = []int{body, exit}
	return nil
}

func (c *compiler) while(w While) error {
	if w.Limit < 0 {
		return &EvalError{Reason: "while limit must be non-negative"}
	}
	enter := len(c.code)
	c.code = append(c.code, instr{op: opWhileEnter, expr: w.Cond, limit: w.Limit})
	body := len(c.code)
	if err := c.block(w.Body); err != nil {
		return err
	}
	back := len(c.code)
	c.code = append(c.code, instr{op: opWhileBack, expr: w.Cond, targets: []int{body}})
	exit := len(c.code)
	c.code = append(c.code, instr{op: opWhileExit})
	c.code[enter].targets = []int{body, exit}
	c.code[back].targets = []int{body, exit}
	return nil
}
