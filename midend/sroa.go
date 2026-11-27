package midend

import (
	"fmt"
	"sort"
)

// ptrDesc describes a pointer register relative to a candidate allocation.
// A register may have multiple descriptors when a phi merges pointers.
type ptrDesc struct {
	alloc   string // allocation register name
	offset  int    // static byte offset within the allocation
	base    bool   // true for the allocation's own base pointer
	dyn     bool   // reached through AddrAdd (dynamic offset)
	unknown bool   // points to an unknown/escaped object
}

// sroaResult reports what the pass did.
type sroaResult struct {
	// EliminatedHeapAllocs counts heap allocations fully removed by SROA.
	EliminatedHeapAllocs int
	// PromotedStackAllocs counts non-escaping allocs kept but stack-resident.
	PromotedStackAllocs int
	// Rejected maps an allocation name to the reason SROA was refused.
	Rejected map[string]string
}

type sroaPass struct {
	prog  *Program
	fn    *Func
	esc   *EscapeResult
	types map[string]Type
	cfg   *cfgInfo

	// descriptors[reg] = all abstract pointer descriptors for the register.
	descriptors map[string][]*ptrDesc
	allocs      map[string]*Alloc
}

// RunSROA performs scalar replacement on every function. The input program is
// not mutated; a transformed copy is returned together with statistics.
func RunSROA(prog *Program, esc *EscapeResult) (*Program, *sroaResult) {
	out := CloneProgram(prog)
	// Re-analyze the clone: EscapeResult keys are function/object pointers that
	// differ after deep copying.
	esc = AnalyzeEscape(out)
	res := &sroaResult{Rejected: map[string]string{}}
	for _, f := range out.Funcs {
		runFuncSROA(out, f, esc, res)
	}
	return out, res
}

func runFuncSROA(prog *Program, f *Func, esc *EscapeResult, res *sroaResult) {
	p := &sroaPass{
		prog:        prog,
		fn:          f,
		esc:         esc,
		types:       regTypes(f),
		cfg:         buildCFG(f),
		descriptors: map[string][]*ptrDesc{},
		allocs:      map[string]*Alloc{},
	}
	p.collectAllocs()
	if len(p.allocs) == 0 {
		return
	}
	p.buildDescriptors()

	var candidates []string
	for name := range p.allocs {
		if esc.AllocEscaped(f, name) {
			res.Rejected[name] = "allocation escapes"
			continue
		}
		if reason := p.checkEligible(name); reason != "" {
			res.Rejected[name] = reason
			continue
		}
		candidates = append(candidates, name)
	}
	sort.Strings(candidates)

	for _, name := range candidates {
		heapBefore := p.allocs[name].Heap
		if p.transform(name, res) {
			if heapBefore {
				res.EliminatedHeapAllocs++
			}
		}
	}

	// Non-escaping allocations that were not eliminated are safely promoted
	// from heap to stack.
	for name, a := range p.allocs {
		// Escaping allocations are never touched.
		if esc.AllocEscaped(f, name) {
			continue
		}
		if a.Heap && !p.wasEliminated(name) {
			a.Heap = false
			res.PromotedStackAllocs++
		}
	}
}

func (p *sroaPass) collectAllocs() {
	for _, b := range p.fn.Blocks {
		for _, ins := range b.Insts {
			if a, ok := ins.(*Alloc); ok {
				p.allocs[a.Name] = a
			}
		}
	}
}

func (p *sroaPass) buildDescriptors() {
	// RPO propagation with iteration until phi descriptors stabilize.
	for changed := true; changed; {
		changed = false
		for _, b := range p.cfg.order {
			for _, ins := range b.Insts {
				if p.updateDesc(ins) {
					changed = true
				}
			}
		}
	}
}

func sameDesc(a, b *ptrDesc) bool {
	return a.alloc == b.alloc && a.offset == b.offset && a.base == b.base &&
		a.dyn == b.dyn && a.unknown == b.unknown
}

func (p *sroaPass) addDesc(reg string, d *ptrDesc) bool {
	for _, ex := range p.descriptors[reg] {
		if sameDesc(ex, d) {
			return false
		}
	}
	p.descriptors[reg] = append(p.descriptors[reg], d)
	return true
}

func (p *sroaPass) updateDesc(ins Inst) bool {
	changed := false
	switch x := ins.(type) {
	case *Alloc:
		changed = p.addDesc(x.Name, &ptrDesc{alloc: x.Name, base: true})
	case *GlobalAddr:
		changed = p.addDesc(x.Name, &ptrDesc{unknown: true})
	case *FieldAddr:
		if !x.Ptr.IsReg() {
			return false
		}
		st := pointeeStruct(p.types, x)
		off := st.Offset(x.Field)
		for _, d := range p.descriptors[x.Ptr.Reg] {
			if d.unknown {
				changed = p.addDesc(x.Name, &ptrDesc{unknown: true}) || changed
				continue
			}
			nd := &ptrDesc{alloc: d.alloc, offset: d.offset + off, dyn: d.dyn}
			changed = p.addDesc(x.Name, nd) || changed
		}
	case *AddrAdd:
		for _, d := range p.descriptors[x.Ptr.Reg] {
			nd := *d
			nd.dyn = true
			changed = p.addDesc(x.Name, &nd) || changed
		}
	case *Load:
		// A loaded pointer may alias anything: mark unknown.
		if _, isPtr := x.Ty.(*PtrType); isPtr {
			changed = p.addDesc(x.Name, &ptrDesc{unknown: true}) || changed
		}
	case *Phi:
		for _, e := range x.Incoming {
			if !e.Val.IsReg() {
				changed = p.addDesc(x.Name, &ptrDesc{unknown: true}) || changed
				continue
			}
			if len(p.descriptors[e.Val.Reg]) == 0 {
				continue
			}
			for _, d := range p.descriptors[e.Val.Reg] {
				nd := *d
				changed = p.addDesc(x.Name, &nd) || changed
			}
		}
	case *Call, *ExtCall:
		if x.GetType() != nil {
			if _, isPtr := x.GetType().(*PtrType); isPtr {
				changed = p.addDesc(x.GetName(), &ptrDesc{unknown: true}) || changed
			}
		}
	}
	return changed
}

func (p *sroaPass) checkEligible(alloc string) string {
	for _, b := range p.fn.Blocks {
		for _, ins := range b.Insts {
			if reason := p.insnReason(alloc, ins); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func (p *sroaPass) insnReason(alloc string, ins Inst) string {
	switch x := ins.(type) {
	case *AddrAdd:
		for _, d := range p.descriptors[x.Name] {
			if d.alloc == alloc && d.dyn {
				return "dynamic offset (AddrAdd) on candidate allocation"
			}
		}
	case *Load:
		if !x.Ptr.IsReg() {
			return ""
		}
		for _, d := range p.descriptors[x.Ptr.Reg] {
			if d.alloc != alloc {
				continue
			}
			if d.unknown {
				return "load through unknown alias"
			}
			if d.dyn {
				return "load through dynamic offset"
			}
			if reason := checkPartialOverlap(p, d, x.Ty.size()); reason != "" {
				return reason
			}
		}
	case *Store:
		if !x.Ptr.IsReg() {
			return ""
		}
		for _, d := range p.descriptors[x.Ptr.Reg] {
			if d.alloc != alloc {
				continue
			}
			if d.unknown {
				return "store through unknown alias"
			}
			if d.dyn {
				return "store through dynamic offset"
			}
			size := p.typeSize(x.Val)
			if reason := checkPartialOverlap(p, d, size); reason != "" {
				return reason
			}
		}
	case *Call:
		for _, a := range x.Args {
			if !a.IsReg() {
				continue
			}
			for _, d := range p.descriptors[a.Reg] {
				if d.alloc == alloc {
					return "candidate address passed to call"
				}
			}
		}
	case *ExtCall:
		for _, a := range x.Args {
			if !a.IsReg() {
				continue
			}
			for _, d := range p.descriptors[a.Reg] {
				if d.alloc == alloc {
					return "candidate address passed to external call"
				}
			}
		}
	case *Phi:
		if _, isPtr := x.Ty.(*PtrType); !isPtr {
			return ""
		}
		descs := p.descriptors[x.Name]
		if len(descs) == 0 {
			return ""
		}
		onlyAlloc := true
		for _, d := range descs {
			if d.unknown {
				return "pointer phi includes unknown alias"
			}
			if d.alloc != alloc {
				onlyAlloc = false
			}
		}
		if !onlyAlloc {
			return "pointer phi merges distinct allocations"
		}
		// Every source must be a static (non-dynamic) offset of the same
		// non-escaping allocation for the phi to be safely splittable.
		for _, d := range descs {
			if d.dyn {
				return "pointer phi includes dynamic-offset source"
			}
		}
	}
	return ""
}

// checkPartialOverlap rejects accesses whose byte range does not line up with
// a whole struct field, which would create different-sized partial writes.
func checkPartialOverlap(p *sroaPass, d *ptrDesc, size int) string {
	a := p.allocs[d.alloc]
	st := a.Ty
	matched := false
	for i := range st.Fields {
		if st.Offset(i) == d.offset && st.Fields[i].Ty.size() == size {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Sprintf("partial/overlapping access at offset %d size %d", d.offset, size)
	}
	return ""
}

func (p *sroaPass) typeSize(o Operand) int {
	if o.IsConst() && o.Ty != nil {
		return o.Ty.size()
	}
	if o.IsReg() {
		if ty, ok := p.types[o.Reg]; ok {
			return ty.size()
		}
	}
	if o.Ty != nil {
		return o.Ty.size()
	}
	return ptrSize
}

func (p *sroaPass) wasEliminated(name string) bool {
	for _, b := range p.fn.Blocks {
		for _, ins := range b.Insts {
			if a, ok := ins.(*Alloc); ok && a.Name == name {
				return false
			}
		}
	}
	return true
}

// slot identifies one scalarized field of a candidate allocation.
type slot struct {
	alloc string
	field int
}

func (p *sroaPass) transform(alloc string, res *sroaResult) bool {
	a := p.allocs[alloc]
	st := a.Ty

	// 1. Discover which slots are ever accessed through the allocation.
	used := map[int]bool{}
	for _, b := range p.fn.Blocks {
		for _, ins := range b.Insts {
			p.collectSlotUses(alloc, ins, used)
		}
	}
	if len(used) == 0 {
		// Dead allocation: just remove it.
		p.removeAlloc(alloc)
		return true
	}

	// 2. Determine defining blocks (stores) per slot for phi placement.
	defBlocks := map[slot]map[string]bool{}
	for fi := range used {
		defBlocks[slot{alloc, fi}] = map[string]bool{}
	}
	for _, b := range p.fn.Blocks {
		for _, ins := range b.Insts {
			if s, ok := p.storeSlot(alloc, ins); ok {
				defBlocks[s][b.Name] = true
			}
		}
	}

	// 3. Insert phi placeholders at dominance frontiers.
	phiNodes := map[string]map[slot]*Phi{}
	for s, defs := range defBlocks {
		work := mapKeys(defs)
		seen := map[string]bool{}
		for len(work) > 0 {
			d := work[0]
			work = work[1:]
			for df := range p.cfg.dfront[d] {
				if seen[df] {
					continue
				}
				seen[df] = true
				reg := fmt.Sprintf("%s.f%d.%s", alloc, s.field, df)
				ty := st.Fields[s.field].Ty
				ph := &Phi{Name: reg, Ty: ty}
				blk := p.fn.blockByName(df)
				blk.Insts = append([]Inst{ph}, blk.Insts...)
				if phiNodes[df] == nil {
					phiNodes[df] = map[slot]*Phi{}
				}
				phiNodes[df][s] = ph
				work = append(work, df)
			}
		}
	}

	// 4. Rename pass along the dominator tree.
	rn := &sroaRenamer{
		pass:     p,
		st:       st,
		alloc:    alloc,
		values:   map[slot][]Operand{},
		phiNodes: phiNodes,
		zero:     map[int]Operand{},
	}
	for fi := range used {
		rn.zero[fi] = zeroOperand(st.Fields[fi].Ty)
		rn.values[slot{alloc, fi}] = []Operand{rn.zero[fi]}
	}
	rn.renameBlock(p.fn.Blocks[0])

	// 5. Delete memory-only instructions belonging to this allocation.
	p.removeAlloc(alloc)
	return true
}

func mapKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func zeroOperand(ty Type) Operand {
	if _, ok := ty.(*PtrType); ok {
		return Nil(ty.(*PtrType))
	}
	return C(0, ty)
}

func (p *sroaPass) collectSlotUses(alloc string, ins Inst, used map[int]bool) {
	switch x := ins.(type) {
	case *Load:
		if s, ok := p.accessSlot(alloc, x.Ptr); ok {
			used[s.field] = true
		}
	case *Store:
		if s, ok := p.accessSlot(alloc, x.Ptr); ok {
			used[s.field] = true
		}
	}
}

func (p *sroaPass) accessSlot(alloc string, ptr Operand) (slot, bool) {
	if !ptr.IsReg() {
		return slot{}, false
	}
	for _, d := range p.descriptors[ptr.Reg] {
		if d.alloc == alloc && !d.unknown && !d.dyn {
			fi := p.fieldAtOffset(alloc, d.offset)
			if fi >= 0 {
				return slot{alloc, fi}, true
			}
		}
	}
	return slot{}, false
}

func (p *sroaPass) storeSlot(alloc string, ins Inst) (slot, bool) {
	s, ok := ins.(*Store)
	if !ok {
		return slot{}, false
	}
	return p.accessSlot(alloc, s.Ptr)
}

func (p *sroaPass) fieldAtOffset(alloc string, off int) int {
	st := p.allocs[alloc].Ty
	for i := range st.Fields {
		if st.Offset(i) == off {
			return i
		}
	}
	return -1
}

func (p *sroaPass) removeAlloc(alloc string) {
	for _, b := range p.fn.Blocks {
		out := b.Insts[:0]
		for _, ins := range b.Insts {
			if p.belongsToAlloc(alloc, ins) {
				continue
			}
			out = append(out, ins)
		}
		b.Insts = out
	}
}

func (p *sroaPass) belongsToAlloc(alloc string, ins Inst) bool {
	switch x := ins.(type) {
	case *Alloc:
		return x.Name == alloc
	case *Phi:
		if _, isPtr := x.Ty.(*PtrType); !isPtr {
			return false
		}
		for _, d := range p.descriptors[x.Name] {
			if d.alloc == alloc {
				return true
			}
		}
	case *FieldAddr:
		for _, d := range p.descriptors[x.Name] {
			if d.alloc == alloc {
				return true
			}
		}
	case *AddrAdd:
		for _, d := range p.descriptors[x.Name] {
			if d.alloc == alloc {
				return true
			}
		}
	case *Load, *Store:
		// Removed during renaming; any remaining one does not belong here.
		return false
	}
	return false
}

type sroaRenamer struct {
	pass     *sroaPass
	st       *StructType
	alloc    string
	values   map[slot][]Operand
	phiNodes map[string]map[slot]*Phi
	zero     map[int]Operand
}

func (rn *sroaRenamer) top(s slot) Operand {
	stk := rn.values[s]
	return stk[len(stk)-1]
}

func (rn *sroaRenamer) renameBlock(b *Block) {
	// Push the phi results defined at the top of this block.
	var pushed []slot
	if phis, ok := rn.phiNodes[b.Name]; ok {
		for s, ph := range phis {
			rn.values[s] = append(rn.values[s], R(ph.Name))
			pushed = append(pushed, s)
		}
	}

	kept := b.Insts[:0]
	for _, ins := range b.Insts {
		// Skip the phi placeholders we already manage; their incoming lists
		// are filled when processing predecessor terminators below.
		if rn.isManagedPhi(ins) {
			kept = append(kept, ins)
			continue
		}
		if rn.rewrite(ins) {
			continue
		}
		kept = append(kept, ins)
	}
	b.Insts = kept

	// Fill successor phi incoming lists with the current slot values.
	for _, succName := range rn.pass.cfg.succs[b.Name] {
		succ := rn.pass.fn.blockByName(succName.Name)
		if phis, ok := rn.phiNodes[succ.Name]; ok {
			for s, ph := range phis {
				ph.Incoming = append(ph.Incoming, PhiEdge{Block: b.Name, Val: rn.top(s)})
			}
		}
	}

	// Recurse over blocks immediately dominated by this one.
	for _, c := range rn.pass.cfg.order {
		if c.Name != b.Name && rn.pass.cfg.idom[c.Name] == b.Name {
			rn.renameBlock(c)
		}
	}

	for _, s := range pushed {
		rn.values[s] = rn.values[s][:len(rn.values[s])-1]
	}
}

func (rn *sroaRenamer) isManagedPhi(ins Inst) bool {
	ph, ok := ins.(*Phi)
	if !ok {
		return false
	}
	for _, phis := range rn.phiNodes {
		for _, cand := range phis {
			if cand.Name == ph.Name {
				return true
			}
		}
	}
	return false
}

// rewrite replaces one memory instruction with SSA operations. It reports
// true when the instruction must be deleted. Loads are rewritten in place by
// substituting their result register everywhere it is used within the current
// function, which preserves SSA because the dominating load is replaced by the
// dominating slot value.
func (rn *sroaRenamer) rewrite(ins Inst) bool {
	switch x := ins.(type) {
	case *Load:
		if s, ok := rn.pass.accessSlot(rn.alloc, x.Ptr); ok {
			rn.replaceReg(x.Name, rn.top(s))
			return true
		}
	case *Store:
		if s, ok := rn.pass.accessSlot(rn.alloc, x.Ptr); ok {
			rn.values[s] = append(rn.values[s], x.Val)
			return true
		}
	case *FieldAddr, *AddrAdd:
		for _, d := range rn.pass.descriptors[ins.GetName()] {
			if d.alloc == rn.alloc {
				return true
			}
		}
	case *Alloc:
		if x.Name == rn.alloc {
			return true
		}
	}
	return false
}

// replaceReg redirects every use of old register to the given SSA operand.
// SSA validity is preserved because the replacement value dominates every use
// of the removed load.
func (rn *sroaRenamer) replaceReg(old string, val Operand) {
	rewriteOperands(rn.pass.fn, func(o Operand) Operand {
		if o.IsReg() && o.Reg == old {
			return val
		}
		return o
	})
}
