package netsec

import (
	"fmt"
	"sort"
	"strings"
)

// SafepointInfo is the fixed-point result for one Safepoint instruction.
type SafepointInfo struct {
	At       ValueID
	Block    BlockID
	Live     ValueSet
	LivePtrs ValueSet
}

// Analysis is the result of control-flow liveness.
type Analysis struct {
	Function   *Function
	Safepoints []SafepointInfo
	LiveOut    map[ValueID]ValueSet

	types map[ValueID]ValueType
}

// Liveness is a short alias for Analysis.
type Liveness = Analysis

// LiveOutAt returns all values live immediately after an instruction.
func (a *Analysis) LiveOutAt(id ValueID) ValueSet {
	if set, ok := a.LiveOut[id]; ok {
		return set.Clone()
	}
	return ValueSet{}
}

// LivePtrsAfter returns the pointer values live immediately after id.
func (a *Analysis) LivePtrsAfter(id ValueID) ValueSet {
	set := ValueSet{}
	for value := range a.LiveOutAt(id) {
		if a.types[value].IsPtr() {
			set.Add(value)
		}
	}
	return set
}

// Safepoint returns the analysis record for a Safepoint instruction.
func (a *Analysis) Safepoint(id ValueID) (SafepointInfo, bool) {
	for _, sp := range a.Safepoints {
		if sp.At == id {
			return SafepointInfo{
				At:       sp.At,
				Block:    sp.Block,
				Live:     sp.Live.Clone(),
				LivePtrs: sp.LivePtrs.Clone(),
			}, true
		}
	}
	return SafepointInfo{}, false
}

// Type returns the declared type of a defined SSA value.
func (a *Analysis) Type(id ValueID) (ValueType, bool) {
	t, ok := a.types[id]
	return t, ok
}

type ssaNode struct {
	block *Block
	index int
	in    *Instr
	succ  []*ssaNode
}

type ssaGraph struct {
	function *Function
	blocks   map[BlockID]*Block
	byID     map[ValueID]*Instr
	byBlock  map[BlockID][]*ssaNode
	nodes    []*ssaNode
	first    map[BlockID]*ssaNode
}

// Validate checks that fn is a well-formed finite SSA function, including
// pointer metadata, object bounds, CFG edges, Phi predecessor edges, and SSA
// dominance.
func Validate(fn *Function) error {
	_, err := buildGraph(fn)
	return err
}

func buildGraph(fn *Function) (*ssaGraph, error) {
	if fn == nil {
		return nil, fmt.Errorf("netsec: nil function")
	}
	if len(fn.Blocks) == 0 {
		return nil, fmt.Errorf("netsec: function %q has no blocks", fn.Name)
	}
	g := &ssaGraph{
		function: fn,
		blocks:   make(map[BlockID]*Block, len(fn.Blocks)),
		byID:     make(map[ValueID]*Instr),
		byBlock:  make(map[BlockID][]*ssaNode),
		first:    make(map[BlockID]*ssaNode),
	}
	for i, b := range fn.Blocks {
		if b == nil {
			return nil, fmt.Errorf("netsec: function %q has a nil block", fn.Name)
		}
		if b.ID == "" {
			return nil, fmt.Errorf("netsec: block %d has an empty ID", i)
		}
		if _, exists := g.blocks[b.ID]; exists {
			return nil, fmt.Errorf("netsec: duplicate block %q", b.ID)
		}
		if len(b.Instrs) == 0 {
			return nil, fmt.Errorf("netsec: block %q is empty", b.ID)
		}
		g.blocks[b.ID] = b
		for j, in := range b.Instrs {
			if in == nil {
				return nil, fmt.Errorf("netsec: block %q has a nil instruction", b.ID)
			}
			if in.ID == NoValue {
				return nil, fmt.Errorf("netsec: block %q instruction %d has NoValue ID", b.ID, j)
			}
			if _, exists := g.byID[in.ID]; exists {
				return nil, fmt.Errorf("netsec: duplicate instruction/value ID %d", in.ID)
			}
			g.byID[in.ID] = in
			n := &ssaNode{block: b, index: j, in: in}
			g.nodes = append(g.nodes, n)
			g.byBlock[b.ID] = append(g.byBlock[b.ID], n)
			if j == 0 {
				g.first[b.ID] = n
			}
		}
	}
	if fn.Entry == "" {
		fn.Entry = fn.Blocks[0].ID
	}
	if _, ok := g.blocks[fn.Entry]; !ok {
		return nil, fmt.Errorf("netsec: entry block %q does not exist", fn.Entry)
	}

	layouts := make(map[ObjectID]int, len(fn.ObjectSizes))
	for id, size := range fn.ObjectSizes {
		if size <= 0 {
			return nil, fmt.Errorf("netsec: object site %q has non-positive size %d", id, size)
		}
		layouts[id] = size
	}

	phiSeenNonPhi := make(map[BlockID]bool)
	for _, b := range fn.Blocks {
		last := b.Instrs[len(b.Instrs)-1]
		for j, in := range b.Instrs {
			if in.Op == OpPhi {
				if phiSeenNonPhi[b.ID] {
					return nil, fmt.Errorf("netsec: block %q has a Phi after a non-Phi instruction", b.ID)
				}
			} else {
				phiSeenNonPhi[b.ID] = true
			}
			if isTerminator(in.Op) && j != len(b.Instrs)-1 {
				return nil, fmt.Errorf("netsec: block %q has %s before its end", b.ID, in.Op)
			}
			if err := validateInstructionShape(in, b); err != nil {
				return nil, err
			}
			if in.Op == OpAlloc {
				if old, ok := layouts[in.Object]; ok && old != in.Size {
					return nil, fmt.Errorf("netsec: object site %q has sizes %d and %d", in.Object, old, in.Size)
				}
				layouts[in.Object] = in.Size
			}
			if in.Op == OpCall && in.Summary.Return.Kind == RetFresh {
				site, size := in.Summary.Return.Object, in.Summary.Return.Size
				if old, ok := layouts[site]; ok && old != size {
					return nil, fmt.Errorf("netsec: object site %q has sizes %d and %d", site, old, size)
				}
				layouts[site] = size
			}
		}
		if !isTerminator(last.Op) {
			return nil, fmt.Errorf("netsec: block %q does not end in Return, Jump, or CondJump", b.ID)
		}
	}

	for id, size := range fn.ObjectSizes {
		if actual, ok := layouts[id]; ok && actual != size {
			return nil, fmt.Errorf("netsec: object site %q declared size %d but uses size %d", id, size, actual)
		}
	}

	// Build instruction edges after all shape checks.
	for _, b := range fn.Blocks {
		nodes := g.byBlock[b.ID]
		for j, n := range nodes {
			in := n.in
			if j+1 < len(nodes) {
				n.succ = append(n.succ, nodes[j+1])
			}
			switch in.Op {
			case OpJump:
				target := g.first[in.Target]
				if target == nil {
					return nil, fmt.Errorf("netsec: block %q jumps to missing block %q", b.ID, in.Target)
				}
				addSucc(&n.succ, target)
			case OpCondJump:
				t := g.first[in.True]
				f := g.first[in.False]
				if t == nil {
					return nil, fmt.Errorf("netsec: block %q branches to missing block %q", b.ID, in.True)
				}
				if f == nil {
					return nil, fmt.Errorf("netsec: block %q branches to missing block %q", b.ID, in.False)
				}
				addSucc(&n.succ, t)
				addSucc(&n.succ, f)
			case OpCall:
				handler := callExceptionTarget(in)
				if handler != "" {
					target := g.first[handler]
					if target == nil {
						return nil, fmt.Errorf("netsec: call %q targets missing exception block %q", in.Callee, handler)
					}
					addSucc(&n.succ, target)
				}
			}
		}
	}

	if err := checkCFGAndDominance(g); err != nil {
		return nil, err
	}
	if err := checkTypes(fn, g, layouts); err != nil {
		return nil, err
	}
	return g, nil
}

func addSucc(list *[]*ssaNode, node *ssaNode) {
	for _, existing := range *list {
		if existing == node {
			return
		}
	}
	*list = append(*list, node)
}

func isTerminator(op Op) bool {
	return op == OpReturn || op == OpJump || op == OpCondJump
}

func callExceptionTarget(in *Instr) BlockID {
	if in == nil || in.Op != OpCall || !in.Summary.MayThrow {
		return ""
	}
	return in.Summary.Exception
}

func validateInstructionShape(in *Instr, b *Block) error {
	if in.Type.Kind < KindNone || in.Type.Kind > KindNonPtr {
		return fmt.Errorf("netsec: block %q instruction %d has invalid type kind %d", b.ID, in.ID, in.Type.Kind)
	}
	switch in.Op {
	case OpAlloc:
		if in.Object == "" || in.Size <= 0 {
			return fmt.Errorf("netsec: Alloc %d needs a site and positive size", in.ID)
		}
		if !in.Type.IsPtr() {
			return fmt.Errorf("netsec: Alloc %d must be Ptr", in.ID)
		}
	case OpCopy:
		if len(in.Args) != 1 {
			return fmt.Errorf("netsec: Copy %d needs one argument", in.ID)
		}
		if in.Type.Kind == KindNone {
			return fmt.Errorf("netsec: Copy %d must produce a value", in.ID)
		}
	case OpField:
		if len(in.Args) != 1 {
			return fmt.Errorf("netsec: Field %d needs one base", in.ID)
		}
		if !in.Type.IsPtr() {
			return fmt.Errorf("netsec: Field %d must produce Ptr", in.ID)
		}
	case OpInt:
		if len(in.Args) != 0 || in.Type.Kind != KindNonPtr {
			return fmt.Errorf("netsec: Int %d must be NonPtr and have no operands", in.ID)
		}
	case OpPhi:
		if !in.Type.IsPtr() && in.Type.Kind != KindNonPtr {
			return fmt.Errorf("netsec: Phi %d must be Ptr or NonPtr", in.ID)
		}
		if len(in.Phi) == 0 {
			return fmt.Errorf("netsec: Phi %d has no incoming edges", in.ID)
		}
		seen := make(map[BlockID]bool)
		for _, edge := range in.Phi {
			if edge.Pred == "" {
				return fmt.Errorf("netsec: Phi %d has an empty predecessor", in.ID)
			}
			if seen[edge.Pred] {
				return fmt.Errorf("netsec: Phi %d has duplicate predecessor %q", in.ID, edge.Pred)
			}
			seen[edge.Pred] = true
		}
	case OpCall:
		if in.Summary.Name == "" && in.Callee == "" {
			return fmt.Errorf("netsec: Call %d has no callee name", in.ID)
		}
		if in.Summary.Return.Kind > RetFresh {
			return fmt.Errorf("netsec: Call %d has invalid return kind %d", in.ID, in.Summary.Return.Kind)
		}
		for _, idx := range in.Summary.LiveArgs {
			if idx < 0 || idx >= len(in.Args) {
				return fmt.Errorf("netsec: Call %d live argument index %d is out of range", in.ID, idx)
			}
		}
		if in.Summary.MayThrow && in.Summary.Exception == "" {
			return fmt.Errorf("netsec: Call %d may throw but has no exception block", in.ID)
		}
		if !in.Summary.MayThrow && in.Summary.Exception != "" {
			return fmt.Errorf("netsec: Call %d has an exception block but MayThrow is false", in.ID)
		}
		if in.Summary.Return.Kind == RetFresh && in.Summary.Return.Size <= 0 {
			return fmt.Errorf("netsec: Call %d fresh return needs a positive size", in.ID)
		}
		if in.Summary.Return.Kind == RetArg &&
			(in.Summary.Return.Arg < 0 || in.Summary.Return.Arg >= len(in.Args)) {
			return fmt.Errorf("netsec: Call %d return argument index is out of range", in.ID)
		}
		if in.Summary.Return.Kind == RetNone && in.Type.Kind != KindNone {
			return fmt.Errorf("netsec: Call %d returns none but declares %s", in.ID, in.Type)
		}
	case OpSafepoint:
		if in.Type.Kind != KindNone {
			return fmt.Errorf("netsec: Safepoint %d must not produce a value", in.ID)
		}
	case OpDereference:
		if len(in.Args) != 1 || in.Type.Kind != KindNonPtr {
			return fmt.Errorf("netsec: Dereference %d needs one pointer and returns NonPtr", in.ID)
		}
	case OpReturn:
		if in.Type.Kind != KindNone {
			return fmt.Errorf("netsec: Return %d must not produce a value", in.ID)
		}
		if len(in.Results) > 1 || len(in.Args) > 1 {
			return fmt.Errorf("netsec: Return %d has too many results", in.ID)
		}
		if len(in.Results) > 0 && len(in.Args) > 0 && in.Results[0] != in.Args[0] {
			return fmt.Errorf("netsec: Return %d has conflicting result operands", in.ID)
		}
	case OpJump:
		if in.Target == "" || in.Type.Kind != KindNone {
			return fmt.Errorf("netsec: Jump %d needs a target and no result", in.ID)
		}
	case OpCondJump:
		if in.Cond == NoValue || in.True == "" || in.False == "" || in.Type.Kind != KindNone {
			return fmt.Errorf("netsec: CondJump %d needs a condition and two targets", in.ID)
		}
	default:
		return fmt.Errorf("netsec: instruction %d has unknown opcode %d", in.ID, in.Op)
	}
	for _, arg := range in.Args {
		if arg == NoValue {
			return fmt.Errorf("netsec: instruction %d uses NoValue", in.ID)
		}
	}
	return nil
}

func checkCFGAndDominance(g *ssaGraph) error {
	reachable := make(map[BlockID]bool)
	var visit func(BlockID)
	visit = func(id BlockID) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, n := range g.byBlock[id] {
			for _, s := range n.succ {
				visit(s.block.ID)
			}
		}
	}
	visit(g.function.Entry)

	preds := make(map[BlockID]map[BlockID]bool)
	for id := range reachable {
		preds[id] = make(map[BlockID]bool)
	}
	for id := range reachable {
		for _, n := range g.byBlock[id] {
			for _, s := range n.succ {
				if s.block.ID == id && s != g.first[id] {
					continue
				}
				if reachable[s.block.ID] {
					preds[s.block.ID][id] = true
				}
			}
		}
	}

	dom := make(map[BlockID]map[BlockID]bool)
	all := make(map[BlockID]bool)
	for id := range reachable {
		all[id] = true
	}
	for id := range reachable {
		if id == g.function.Entry {
			dom[id] = map[BlockID]bool{id: true}
		} else {
			dom[id] = cloneSet(all)
		}
	}
	changed := true
	for changed {
		changed = false
		for id := range reachable {
			if id == g.function.Entry {
				continue
			}
			var next map[BlockID]bool
			for p := range preds[id] {
				if len(next) == 0 {
					next = cloneSet(dom[p])
				} else {
					for candidate := range next {
						if !dom[p][candidate] {
							delete(next, candidate)
						}
					}
				}
			}
			if next == nil {
				next = make(map[BlockID]bool)
			}
			next[id] = true
			if !setsEqual(dom[id], next) {
				dom[id] = next
				changed = true
			}
		}
	}

	defBlock := make(map[ValueID]BlockID)
	defIndex := make(map[ValueID]int)
	for _, b := range g.function.Blocks {
		for i, in := range b.Instrs {
			if in.Def() != NoValue {
				defBlock[in.Def()] = b.ID
				defIndex[in.Def()] = i
			}
		}
	}

	for _, b := range g.function.Blocks {
		if !reachable[b.ID] {
			continue
		}
		for i, in := range b.Instrs {
			if in.Op == OpPhi {
				for _, edge := range in.Phi {
					if !reachable[edge.Pred] {
						return fmt.Errorf("netsec: Phi %d names unreachable predecessor %q", in.ID, edge.Pred)
					}
					if !preds[b.ID][edge.Pred] {
						return fmt.Errorf("netsec: Phi %d predecessor %q does not reach block %q", in.ID, edge.Pred, b.ID)
					}
					if err := checkUse(dom, defBlock, defIndex, edge.Value, edge.Pred, len(g.blocks[edge.Pred].Instrs), true); err != nil {
						return fmt.Errorf("netsec: Phi %d: %w", in.ID, err)
					}
				}
				continue
			}
			for _, use := range in.Uses() {
				if err := checkUse(dom, defBlock, defIndex, use, b.ID, i, false); err != nil {
					return fmt.Errorf("netsec: instruction %d: %w", in.ID, err)
				}
			}
		}
	}

	// Every incoming CFG edge must have a Phi arm, and every Phi arm must be
	// an incoming edge.
	for _, b := range g.function.Blocks {
		for i, in := range b.Instrs {
			if in.Op != OpPhi {
				if len(in.Phi) != 0 {
					return fmt.Errorf("netsec: non-Phi instruction %d has Phi inputs", in.ID)
				}
				continue
			}
			if b.ID == g.function.Entry && i == 0 {
				return fmt.Errorf("netsec: entry block %q cannot start with a Phi", b.ID)
			}
			seen := make(map[BlockID]bool)
			for _, edge := range in.Phi {
				seen[edge.Pred] = true
			}
			for p := range preds[b.ID] {
				if !seen[p] {
					return fmt.Errorf("netsec: Phi %d in %q is missing predecessor %q", in.ID, b.ID, p)
				}
			}
		}
	}
	return nil
}

func checkUse(dom map[BlockID]map[BlockID]bool, defBlock map[ValueID]BlockID, defIndex map[ValueID]int, value ValueID, useBlock BlockID, useIndex int, edge bool) error {
	if value == NoValue {
		return fmt.Errorf("undefined NoValue operand")
	}
	db, ok := defBlock[value]
	if !ok {
		return fmt.Errorf("use of undefined value %d", value)
	}
	if !dom[useBlock][db] {
		return fmt.Errorf("value %d defined in block %q does not dominate use in %q", value, db, useBlock)
	}
	if db == useBlock {
		if !edge && defIndex[value] >= useIndex {
			return fmt.Errorf("value %d is used before its definition", value)
		}
	}
	return nil
}

func cloneSet(in map[BlockID]bool) map[BlockID]bool {
	out := make(map[BlockID]bool, len(in))
	for k := range in {
		out[k] = true
	}
	return out
}

func setsEqual(a, b map[BlockID]bool) bool {
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

func checkTypes(fn *Function, g *ssaGraph, layouts map[ObjectID]int) error {
	types := make(map[ValueID]ValueType, len(g.byID))
	for _, b := range fn.Blocks {
		for _, in := range b.Instrs {
			if in.Def() != NoValue {
				types[in.Def()] = in.Type
			}
		}
	}
	for _, b := range fn.Blocks {
		for _, in := range b.Instrs {
			var want ValueType
			switch in.Op {
			case OpAlloc:
				want = PtrType(NewObjectSet(in.Object), OffsetRange{Start: 0, End: 1})
			case OpCopy:
				want = types[in.Args[0]]
			case OpField:
				base := types[in.Args[0]]
				if !base.IsPtr() {
					return fmt.Errorf("netsec: Field %d base %d is not Ptr", in.ID, in.Args[0])
				}
				want = ValueType{
					Kind: KindPtr, Objects: append(ObjectSet(nil), base.Objects...),
					Offset: base.Offset.Shift(in.Offset), Base: in.Args[0],
				}
			case OpInt:
				want = NonPtrType()
			case OpPhi:
				var objects ObjectSet
				var offset OffsetRange
				sawPtr := false
				for _, edge := range in.Phi {
					t := types[edge.Value]
					if t.Kind == KindNone {
						return fmt.Errorf("netsec: Phi %d uses non-value %d", in.ID, edge.Value)
					}
					if t.IsPtr() {
						sawPtr = true
						objects = objects.Union(t.Objects)
						offset = offset.Union(t.Offset)
					}
				}
				if sawPtr {
					want = ValueType{Kind: KindPtr, Objects: objects, Offset: offset}
				} else {
					want = NonPtrType()
				}
			case OpCall:
				var err error
				want, err = expectedCallType(in, types, layouts)
				if err != nil {
					return err
				}
			case OpDereference:
				if !types[in.Args[0]].IsPtr() {
					return fmt.Errorf("netsec: Dereference %d operand %d is not Ptr", in.ID, in.Args[0])
				}
				want = NonPtrType()
			case OpSafepoint, OpReturn, OpJump, OpCondJump:
				want = ValueType{Kind: KindNone}
			}
			if !sameType(in.Type, want) {
				return fmt.Errorf("netsec: instruction %d (%s) type is %s, want %s", in.ID, in.Op, in.Type, want)
			}
			if in.Cond != NoValue && types[in.Cond].Kind != KindNonPtr {
				return fmt.Errorf("netsec: CondJump %d condition must be NonPtr", in.ID)
			}
		}
	}

	for id, typ := range types {
		if !typ.IsPtr() {
			continue
		}
		if len(typ.Objects) == 0 {
			return fmt.Errorf("netsec: pointer value %d has an empty object set", id)
		}
		if !typ.Offset.Valid() {
			return fmt.Errorf("netsec: pointer value %d has invalid offset range %s", id, typ.Offset)
		}
		for _, object := range typ.Objects {
			size, ok := layouts[object]
			if !ok {
				return fmt.Errorf("netsec: pointer value %d refers to object site %q with no layout", id, object)
			}
			if !typ.Offset.Within(size) {
				return fmt.Errorf(
					"netsec: pointer value %d offset %s is outside object %q of size %d",
					id, typ.Offset, object, size,
				)
			}
		}
		if typ.Base != NoValue {
			base, ok := types[typ.Base]
			if !ok || !base.IsPtr() {
				return fmt.Errorf("netsec: pointer value %d has invalid derived base %d", id, typ.Base)
			}
			if !typ.Objects.Equal(base.Objects) {
				return fmt.Errorf("netsec: pointer value %d derived base %d has a different object set", id, typ.Base)
			}
			if base.Offset.Start > typ.Offset.Start {
				return fmt.Errorf("netsec: pointer value %d offset precedes its base %d", id, typ.Base)
			}
		}
	}
	return nil
}

func expectedCallType(in *Instr, types map[ValueID]ValueType, layouts map[ObjectID]int) (ValueType, error) {
	switch in.Summary.Return.Kind {
	case RetNone:
		return ValueType{Kind: KindNone}, nil
	case RetNonPtr:
		return NonPtrType(), nil
	case RetArg:
		idx := in.Summary.Return.Arg
		if idx < 0 || idx >= len(in.Args) {
			return ValueType{}, fmt.Errorf("netsec: Call %d return argument %d out of range", in.ID, idx)
		}
		t := types[in.Args[idx]]
		if !t.IsPtr() {
			return ValueType{}, fmt.Errorf("netsec: Call %d return argument %d is not Ptr", in.ID, idx)
		}
		t.Offset = t.Offset.Shift(in.Summary.Return.Offset)
		t.Base = in.Args[idx]
		t.Objects = append(ObjectSet(nil), t.Objects...)
		return t, nil
	case RetFresh:
		if in.Summary.Return.Object == "" {
			return ValueType{}, fmt.Errorf("netsec: Call %d fresh return has no object site", in.ID)
		}
		if size, ok := layouts[in.Summary.Return.Object]; ok && size != in.Summary.Return.Size {
			return ValueType{}, fmt.Errorf("netsec: Call %d fresh return size does not match site layout", in.ID)
		}
		return PtrType(NewObjectSet(in.Summary.Return.Object), OffsetRange{Start: 0, End: 1}), nil
	default:
		return ValueType{}, fmt.Errorf("netsec: Call %d has invalid return kind", in.ID)
	}
}

func sameType(a, b ValueType) bool {
	if a.Kind != b.Kind {
		return false
	}
	if a.Kind != KindPtr {
		return true
	}
	return a.Base == b.Base && a.Offset == b.Offset && a.Objects.Equal(b.Objects)
}

// Analyze validates fn and computes instruction-level liveness to a fixed
// point. LiveOut at each Safepoint contains every pointer that may still be
// read along a normal, exceptional, or loop-backedge path.
func Analyze(fn *Function) (*Analysis, error) {
	g, err := buildGraph(fn)
	if err != nil {
		return nil, err
	}
	types := make(map[ValueID]ValueType, len(g.byID))
	for _, in := range g.byID {
		if in.Def() != NoValue {
			types[in.Def()] = in.Type
		}
	}
	liveIn := make(map[*ssaNode]ValueSet, len(g.nodes))
	liveOut := make(map[*ssaNode]ValueSet, len(g.nodes))
	for _, n := range g.nodes {
		liveIn[n] = ValueSet{}
		liveOut[n] = ValueSet{}
	}

	changed := true
	for changed {
		changed = false
		for i := len(g.nodes) - 1; i >= 0; i-- {
			n := g.nodes[i]
			out := ValueSet{}
			for _, succ := range n.succ {
				for id := range liveIn[succ] {
					out.Add(id)
				}
				if g.first[succ.block.ID] == succ {
					for _, phi := range succ.block.Instrs {
						if phi.Op != OpPhi {
							break
						}
						for _, edge := range phi.Phi {
							if edge.Pred == n.block.ID {
								out.Add(edge.Value)
							}
						}
					}
				}
			}
			inSet := ValueSet{}
			for _, use := range n.in.Uses() {
				inSet.Add(use)
			}
			for id := range out {
				inSet.Add(id)
			}
			if def := n.in.Def(); def != NoValue {
				delete(inSet, def)
			}
			if !setsEqualValues(liveOut[n], out) {
				liveOut[n] = out
				changed = true
			}
			if !setsEqualValues(liveIn[n], inSet) {
				liveIn[n] = inSet
				changed = true
			}
		}
	}

	a := &Analysis{
		Function: fn,
		LiveOut:  make(map[ValueID]ValueSet, len(g.nodes)),
		types:    types,
	}
	for _, n := range g.nodes {
		a.LiveOut[n.in.ID] = liveOut[n].Clone()
		if n.in.Op != OpSafepoint {
			continue
		}
		livePtrs := ValueSet{}
		for id := range liveOut[n] {
			if types[id].IsPtr() {
				livePtrs.Add(id)
			}
		}
		a.Safepoints = append(a.Safepoints, SafepointInfo{
			At: n.in.ID, Block: n.block.ID, Live: liveOut[n].Clone(), LivePtrs: livePtrs,
		})
	}
	sort.Slice(a.Safepoints, func(i, j int) bool { return a.Safepoints[i].At < a.Safepoints[j].At })
	return a, nil
}

func setsEqualValues(a, b ValueSet) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if !b.Contains(id) {
			return false
		}
	}
	return true
}

// FormatPath renders SSA values in a stable, diagnostic-friendly form.
func FormatPath(fn *Function, values []ValueID) string {
	if fn == nil || len(values) == 0 {
		return ""
	}
	byID := make(map[ValueID]*Instr)
	for _, b := range fn.Blocks {
		for _, in := range b.Instrs {
			byID[in.ID] = in
		}
	}
	parts := make([]string, 0, len(values))
	for _, id := range values {
		if in := byID[id]; in != nil {
			parts = append(parts, fmt.Sprintf("%d:%s", id, in.Op))
		} else {
			parts = append(parts, fmt.Sprintf("%d:?", id))
		}
	}
	return strings.Join(parts, " -> ")
}
