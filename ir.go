package netsec

import (
	"errors"
	"fmt"
)

// MaxInputs is the maximum number of input bytes a graph may depend on.
const MaxInputs = 12

// Op identifies the operation of an SSA value.
type Op uint8

const (
	OpInvalid  Op = iota
	OpConst       // literal constant
	OpInput       // one input byte
	OpAdd         // signed wrapping add
	OpSub         // signed wrapping sub
	OpMul         // signed wrapping mul
	OpOverflow    // bool: signed overflow flag of the Add/Sub/Mul operand
	OpTrunc       // truncate to a narrower width
	OpSExt        // sign extend to a wider width
	OpZExt        // zero extend to a wider width
	OpEq          // signed equality, width 1
	OpNe          // signed not-equal
	OpSlt         // signed less than, width 1
	OpSle         // signed less or equal, width 1
	OpNot         // bool not, width 1
	OpAnd         // bool and
	OpOr          // bool or
)

var opNames = [...]string{
	OpInvalid:  "invalid",
	OpConst:    "const",
	OpInput:    "input",
	OpAdd:      "sadd",
	OpSub:      "ssub",
	OpMul:      "smul",
	OpOverflow: "overflow",
	OpTrunc:    "trunc",
	OpSExt:     "sext",
	OpZExt:     "zext",
	OpEq:       "eq",
	OpNe:       "ne",
	OpSlt:      "slt",
	OpSle:      "sle",
	OpNot:      "not",
	OpAnd:      "and",
	OpOr:       "or",
}

func (o Op) String() string {
	if int(o) < len(opNames) {
		return opNames[o]
	}
	return "unknown"
}

// Value is a single SSA definition. Integer values use widths 8, 16 or 32;
// boolean values use width 1 and are held as 0/1.
type Value struct {
	ID    int
	Node  int // source-level node identity, referenced by error terminators
	Op    Op
	Width int
	Imm   uint64 // OpConst
	Input int    // input byte index for OpInput
	X     int    // first operand value ID
	Y     int    // second operand value ID
}

func (v *Value) operands() []int {
	switch v.Op {
	case OpConst, OpInput:
		return nil
	case OpNot, OpOverflow, OpTrunc, OpSExt, OpZExt:
		return []int{v.X}
	default:
		return []int{v.X, v.Y}
	}
}

// Terminator ends a basic block.
type Terminator uint8

const (
	TermInvalid Terminator = iota
	TermReturn
	TermError
	TermBranch
)

// Block is a basic block: value definitions followed by a terminator.
// Control flow must be acyclic.
type Block struct {
	ID      int
	Insts   []int // value IDs defined here, in definition order
	Term    Terminator
	Ret     int // TermReturn: returned value ID
	Cond    int // TermBranch: boolean value ID
	Then    int // TermBranch: target block when Cond is true
	Else    int // TermBranch: target block when Cond is false
	ErrNode int // TermError: source node that raised the error
}

func (b *Block) successors() []int {
	if b.Term == TermBranch {
		return []int{b.Then, b.Else}
	}
	return nil
}

func validateTerminator(b *Block, byID map[int]*Block) error {
	switch b.Term {
	case TermReturn, TermError, TermBranch:
	default:
		return fmt.Errorf("netsec: block %d has no terminator", b.ID)
	}
	if b.Term == TermBranch {
		if _, ok := byID[b.Then]; !ok {
			return fmt.Errorf("netsec: block %d then-target %d missing", b.ID, b.Then)
		}
		if _, ok := byID[b.Else]; !ok {
			return fmt.Errorf("netsec: block %d else-target %d missing", b.ID, b.Else)
		}
	}
	if b.Term == TermError && b.ErrNode < 0 {
		return errors.New("netsec: error node must be non-negative")
	}
	return nil
}

func validateTermOperands(b *Block, g *Graph, index map[int]int, dom map[int]map[int]bool) error {
	switch b.Term {
	case TermReturn:
		v, ok := g.Values[b.Ret]
		if !ok {
			return fmt.Errorf("netsec: block %d returns unknown value %d", b.ID, b.Ret)
		}
		if !intWidths[v.Width] {
			return fmt.Errorf("netsec: block %d returns non-integer value %d", b.ID, v.ID)
		}
		if err := requireDominating(g, b, v, index, dom); err != nil {
			return err
		}
	case TermBranch:
		v, ok := g.Values[b.Cond]
		if !ok {
			return fmt.Errorf("netsec: block %d branches on unknown value %d", b.ID, b.Cond)
		}
		if v.Width != 1 {
			return fmt.Errorf("netsec: branch condition %d is not boolean", b.Cond)
		}
		if err := requireDominating(g, b, v, index, dom); err != nil {
			return err
		}
	}
	return nil
}

func requireDominating(g *Graph, b *Block, v *Value, index map[int]int, dom map[int]map[int]bool) error {
	if _, defined := index[v.ID]; defined {
		return nil
	}
	for _, blk := range g.Blocks {
		for _, vid := range blk.Insts {
			if vid == v.ID {
				if !dom[b.ID][blk.ID] {
					return fmt.Errorf("netsec: value %d does not dominate use in block %d", v.ID, b.ID)
				}
				return nil
			}
		}
	}
	return fmt.Errorf("netsec: value %d not attached to any block", v.ID)
}

func validateValue(v *Value, g *Graph) error {
	if v == nil {
		return errors.New("netsec: nil value")
	}
	if !allowedWidths[v.Width] {
		return fmt.Errorf("netsec: value %d has bad width %d", v.ID, v.Width)
	}
	switch v.Op {
	case OpConst:
		if v.Imm&^widthMask(v.Width) != 0 {
			return fmt.Errorf("netsec: const %d does not fit in %d bits", v.ID, v.Width)
		}
	case OpInput:
		if v.Width != 8 {
			return fmt.Errorf("netsec: input %d must be width 8", v.ID)
		}
		if v.Input < 0 || v.Input >= g.Inputs {
			return fmt.Errorf("netsec: input %d index %d out of range", v.ID, v.Input)
		}
	case OpAdd, OpSub, OpMul:
		if !intWidths[v.Width] {
			return fmt.Errorf("netsec: arithmetic %d requires integer width", v.ID)
		}
		x, y := g.Values[v.X], g.Values[v.Y]
		if x.Width != v.Width || y.Width != v.Width {
			return fmt.Errorf("netsec: arithmetic %d operand widths must match %d", v.ID, v.Width)
		}
	case OpOverflow:
		if v.Width != 1 {
			return fmt.Errorf("netsec: overflow %d must be boolean", v.ID)
		}
		x := g.Values[v.X]
		if x.Op != OpAdd && x.Op != OpSub && x.Op != OpMul {
			return fmt.Errorf("netsec: overflow %d must reference arithmetic, got %s", v.ID, x.Op)
		}
	case OpTrunc:
		x := g.Values[v.X]
		if !intWidths[v.Width] || !intWidths[x.Width] || v.Width >= x.Width {
			return fmt.Errorf("netsec: trunc %d must narrow an integer width", v.ID)
		}
	case OpSExt, OpZExt:
		x := g.Values[v.X]
		if !intWidths[v.Width] || !(intWidths[x.Width] || x.Width == 1) || v.Width <= x.Width {
			return fmt.Errorf("netsec: extension %d must widen an integer width", v.ID)
		}
	case OpEq, OpNe, OpSlt, OpSle:
		if v.Width != 1 {
			return fmt.Errorf("netsec: comparison %d must be boolean", v.ID)
		}
		x, y := g.Values[v.X], g.Values[v.Y]
		if !intWidths[x.Width] || x.Width != y.Width {
			return fmt.Errorf("netsec: comparison %d operand widths mismatch", v.ID)
		}
	case OpNot:
		if v.Width != 1 || g.Values[v.X].Width != 1 {
			return fmt.Errorf("netsec: not %d requires boolean operands", v.ID)
		}
	case OpAnd, OpOr:
		if v.Width != 1 || g.Values[v.X].Width != 1 || g.Values[v.Y].Width != 1 {
			return fmt.Errorf("netsec: boolean op %d requires boolean operands", v.ID)
		}
	default:
		return fmt.Errorf("netsec: value %d has invalid op %s", v.ID, v.Op)
	}
	return nil
}

// dominators returns, for each block id, the set of block ids dominating it.
// The graph is acyclic, so data-flow fixed point iteration always settles.
func dominators(g *Graph) map[int]map[int]bool {
	all := make(map[int]bool, len(g.Blocks))
	for _, blk := range g.Blocks {
		all[blk.ID] = true
	}
	dom := make(map[int]map[int]bool, len(g.Blocks))
	for _, blk := range g.Blocks {
		if blk.ID == g.Entry {
			dom[blk.ID] = map[int]bool{blk.ID: true}
			continue
		}
		set := make(map[int]bool, len(g.Blocks))
		for id := range all {
			set[id] = true
		}
		dom[blk.ID] = set
	}

	order := make([]int, 0, len(g.Blocks))
	color := map[int]uint8{}
	var dfs func(int)
	dfs = func(id int) {
		color[id] = 1
		for _, next := range g.block(id).successors() {
			if color[next] == 0 {
				dfs(next)
			}
		}
		color[id] = 2
		order = append(order, id)
	}
	dfs(g.Entry)

	changed := true
	for changed {
		changed = false
		for i := len(order) - 1; i >= 0; i-- {
			id := order[i]
			if id == g.Entry {
				continue
			}
			preds := g.predecessors(id)
			if len(preds) == 0 {
				continue
			}
			inter := cloneSet(dom[preds[0]])
			for _, p := range preds[1:] {
				inter = intersectSets(inter, dom[p])
			}
			inter[id] = true
			if !sameSet(inter, dom[id]) {
				dom[id] = inter
				changed = true
			}
		}
	}
	return dom
}

func (g *Graph) block(id int) *Block {
	for _, b := range g.Blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (g *Graph) predecessors(id int) []int {
	var preds []int
	for _, b := range g.Blocks {
		for _, s := range b.successors() {
			if s == id {
				preds = append(preds, b.ID)
				break
			}
		}
	}
	return preds
}

func cloneSet(in map[int]bool) map[int]bool {
	out := make(map[int]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func intersectSets(a, b map[int]bool) map[int]bool {
	out := make(map[int]bool)
	for k := range a {
		if b[k] {
			out[k] = true
		}
	}
	return out
}

func sameSet(a, b map[int]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// Graph is an SSA function over Inputs bytes.
type Graph struct {
	Name   string
	Inputs int
	Entry  int
	Blocks []*Block
	Values map[int]*Value
}

var (
	allowedWidths = map[int]bool{1: true, 8: true, 16: true, 32: true}
	intWidths     = map[int]bool{8: true, 16: true, 32: true}
)

func widthMask(width int) uint64 {
	if width >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<width - 1
}

// Validate checks structural and typing rules and returns the first error.
func (g *Graph) Validate() error {
	if g == nil {
		return errors.New("netsec: nil graph")
	}
	if g.Inputs < 0 || g.Inputs > MaxInputs {
		return fmt.Errorf("netsec: input count %d out of range [0,%d]", g.Inputs, MaxInputs)
	}
	if len(g.Blocks) == 0 {
		return errors.New("netsec: graph has no blocks")
	}
	blockByID := make(map[int]*Block, len(g.Blocks))
	for _, blk := range g.Blocks {
		if blk == nil {
			return errors.New("netsec: nil block")
		}
		if _, dup := blockByID[blk.ID]; dup {
			return fmt.Errorf("netsec: duplicate block id %d", blk.ID)
		}
		blockByID[blk.ID] = blk
	}
	entry, ok := blockByID[g.Entry]
	if !ok {
		return fmt.Errorf("netsec: entry block %d missing", g.Entry)
	}

	owner := make(map[int]int)
	for _, blk := range g.Blocks {
		seen := make(map[int]bool)
		for _, vid := range blk.Insts {
			if _, ok := g.Values[vid]; !ok {
				return fmt.Errorf("netsec: block %d references unknown value %d", blk.ID, vid)
			}
			if seen[vid] {
				return fmt.Errorf("netsec: value %d listed twice in block %d", vid, blk.ID)
			}
			seen[vid] = true
			if other, dup := owner[vid]; dup {
				return fmt.Errorf("netsec: value %d owned by blocks %d and %d", vid, other, blk.ID)
			}
			owner[vid] = blk.ID
		}
	}
	for id := range g.Values {
		if _, ok := owner[id]; !ok {
			return fmt.Errorf("netsec: value %d is not attached to any block", id)
		}
	}

	color := make(map[int]uint8)
	var visit func(int) error
	visit = func(id int) error {
		color[id] = 1
		blk := blockByID[id]
		for _, next := range blk.successors() {
			child, ok := blockByID[next]
			if !ok {
				return fmt.Errorf("netsec: block %d branches to missing block %d", id, next)
			}
			switch color[child.ID] {
			case 1:
				return fmt.Errorf("netsec: control-flow cycle through block %d", child.ID)
			case 0:
				if err := visit(child.ID); err != nil {
					return err
				}
			}
		}
		color[id] = 2
		return nil
	}
	if err := visit(entry.ID); err != nil {
		return err
	}
	for _, blk := range g.Blocks {
		if color[blk.ID] == 0 {
			return fmt.Errorf("netsec: block %d unreachable from entry", blk.ID)
		}
		if err := validateTerminator(blk, blockByID); err != nil {
			return err
		}
	}

	dom := dominators(g)
	for _, blk := range g.Blocks {
		index := make(map[int]int, len(blk.Insts))
		for i, vid := range blk.Insts {
			index[vid] = i
		}
		for _, vid := range blk.Insts {
			v := g.Values[vid]
			if err := validateValue(v, g); err != nil {
				return err
			}
			for _, opID := range v.operands() {
				op, ok := g.Values[opID]
				if !ok {
					return fmt.Errorf("netsec: value %d references unknown operand %d", v.ID, opID)
				}
				defBlock := owner[op.ID]
				if defBlock != blk.ID {
					if !dom[blk.ID][defBlock] {
						return fmt.Errorf("netsec: operand %d does not dominate use (blocks %d -> %d)",
							op.ID, defBlock, blk.ID)
					}
				} else if index[op.ID] >= index[v.ID] {
					return fmt.Errorf("netsec: value %d used before definition in block %d", op.ID, blk.ID)
				}
			}
		}
		if err := validateTermOperands(blk, g, index, dom); err != nil {
			return err
		}
	}
	return nil
}
