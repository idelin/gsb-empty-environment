package cfverify

import "fmt"

type runner struct {
	cfg     Config
	owner   map[uint64]uint64 // byte address -> owning instruction start
	parent  map[uint64]uint64 // reached address -> predecessor instruction PC
	queued  map[uint64]bool   // addresses already enqueued
	queue   []queueEntry
	decoded map[uint64]DecodedInstr
	stats   Stats
}

type edgeKind int

const (
	edgeEntry edgeKind = iota
	edgeDirect
	edgeIndirect
)

type queueEntry struct {
	addr uint64
	kind edgeKind
}

func (r *runner) inImage(addr uint64) bool {
	end := r.cfg.Base + uint64(len(r.cfg.Image))
	return addr >= r.cfg.Base && addr < end
}

func (r *runner) intervalInImage(span Interval) bool {
	imageEnd := r.cfg.Base + uint64(len(r.cfg.Image))
	return span.Start >= r.cfg.Base && span.End <= imageEnd
}

// hasDirectWork reports whether the queue still holds an entry or direct
// successor that can decode new instructions (as opposed to deferred
// indirect-target validations).
func (r *runner) hasDirectWork() bool {
	for _, queued := range r.queue {
		if queued.kind != edgeIndirect {
			return true
		}
	}
	return false
}

// step processes one queued address. It returns a non-nil result when the
// verification terminates; nil means traversal continues.
func (r *runner) step(budget Budget) *Result {
	entry := r.queue[0]
	r.queue = r.queue[1:]
	pc := entry.addr

	// Once both budgets are spent, no further edge can be added and any
	// remaining queued address could only be drained forever. Stop with
	// Unknown instead of looping over a backlog.
	if r.stats.Visited >= budget.MaxInstructions && r.stats.Edges >= budget.MaxEdges {
		res := r.unknownResult(budget, "verification budget exhausted")
		return &res
	}

	// A permitted indirect target must be the unique start of an instruction.
	// It is validated against the byte-ownership table rather than decoded as
	// a new instruction, so a target hidden mid-instruction is a conflict and
	// never causes a fresh decode that could mask the original owner.
	if entry.kind == edgeIndirect {
		owner, owned := r.owner[pc]
		if !owned {
			// The target may become a legal instruction start once a
			// pending direct-successor path decodes it. Re-queue behind any
			// remaining direct work so forward progress is guaranteed; if it
			// is still unowned when the queue drains, finalCheck reports it.
			if r.hasDirectWork() {
				r.queue = append(r.queue, entry)
				return nil
			}
			res := r.conflictResult(Conflict{
				Kind:   ConflictIndirectNotStart,
				Target: pc,
				Detail: fmt.Sprintf("permitted indirect target 0x%x is not an instruction start", pc),
			})
			return &res
		}
		if owner != pc {
			res := r.conflictResult(Conflict{
				Kind:     ConflictMidEntry,
				Target:   pc,
				OwnerPC:  owner,
				HasOwner: true,
				Detail:   fmt.Sprintf("permitted indirect target 0x%x lands inside instruction at 0x%x", pc, owner),
			})
			return &res
		}
		return nil
	}

	if _, done := r.decoded[pc]; done {
		return nil
	}

	// Charge the decode budget only for an instruction that will actually be
	// decoded. Already-decoded addresses above are free re-entries, so the
	// budget cannot be bypassed by cycling through a long queue.
	if r.stats.Visited == budget.MaxInstructions {
		res := r.unknownResult(budget, "instruction decode budget exhausted")
		return &res
	}

	// A reached address already owned by an instruction is a mid-entry unless
	// it is that instruction's own start. The owner is never overwritten.
	if owner, ok := r.owner[pc]; ok && owner != pc {
		res := r.conflictResult(Conflict{
			Kind:     ConflictMidEntry,
			Target:   pc,
			OwnerPC:  owner,
			HasOwner: true,
			Detail:   fmt.Sprintf("address 0x%x lands inside instruction starting at 0x%x", pc, owner),
		})
		return &res
	}

	instr, err := r.cfg.Decode(r.cfg.Image, pc)
	if err != nil {
		res := rejectResult(r.stats,
			fmt.Sprintf("decode failed at 0x%x: %v", pc, err), r.buildWitness(pc))
		return &res
	}
	if instr.PC != pc {
		res := rejectResult(r.stats,
			fmt.Sprintf("decoder returned PC 0x%x for query 0x%x", instr.PC, pc), r.buildWitness(pc))
		return &res
	}
	if instr.Bytes.End <= instr.Bytes.Start || instr.Bytes.Start != pc {
		res := rejectResult(r.stats,
			fmt.Sprintf("invalid interval [%d, %d) for instruction at 0x%x",
				instr.Bytes.Start, instr.Bytes.End, pc), r.buildWitness(pc))
		return &res
	}
	if !r.intervalInImage(instr.Bytes) {
		res := rejectResult(r.stats,
			fmt.Sprintf("interval [%d, %d) of instruction at 0x%x exceeds image bounds",
				instr.Bytes.Start, instr.Bytes.End, pc), r.buildWitness(pc))
		return &res
	}

	if conflict := r.firstOverlap(instr.Bytes); conflict != nil {
		res := r.conflictResult(*conflict)
		return &res
	}

	// Claim every byte of the interval. Existing owners would have been
	// reported above, so this registration cannot erase an owner.
	for addr := instr.Bytes.Start; addr < instr.Bytes.End; addr++ {
		r.owner[addr] = pc
	}
	r.decoded[pc] = instr
	r.stats.Decoded++
	r.stats.Visited++

	// Direct successors extend the reachable graph; permitted indirect targets
	// are validated as unique instruction starts. Keep successor ordering
	// deterministic with first-seen de-duplication per kind.
	for _, target := range appendUnique(nil, instr.Successors) {
		if result := r.addEdge(pc, target, edgeDirect, budget); result != nil {
			return result
		}
	}
	for _, target := range appendUnique(nil, instr.IndirectTargets) {
		if result := r.addEdge(pc, target, edgeIndirect, budget); result != nil {
			return result
		}
	}
	return nil
}

// addEdge enqueues target as a successor of predecessor. It returns a non-nil
// result when the edge budget or image bounds are violated.
func (r *runner) addEdge(predecessor, target uint64, kind edgeKind, budget Budget) *Result {
	if r.queued[target] {
		return nil
	}
	if r.stats.Edges == budget.MaxEdges {
		res := r.unknownResult(budget, "successor edge budget exhausted")
		return &res
	}
	if !r.inImage(target) {
		imageEnd := r.cfg.Base + uint64(len(r.cfg.Image))
		res := rejectResult(r.stats,
			fmt.Sprintf("successor 0x%x from 0x%x outside image [0x%x, 0x%x)",
				target, predecessor, r.cfg.Base, imageEnd),
			r.buildWitness(predecessor))
		return &res
	}
	r.stats.Edges++
	r.queued[target] = true
	r.parent[target] = predecessor
	r.queue = append(r.queue, queueEntry{addr: target, kind: kind})
	return nil
}

// firstOverlap scans every byte of the candidate interval and returns the
// first conflict against an existing owner. The scan order is fixed from the
// interval start so the finding is deterministic.
func (r *runner) firstOverlap(span Interval) *Conflict {
	for addr := span.Start; addr < span.End; addr++ {
		if owner, ok := r.owner[addr]; ok {
			kind := ConflictOverlap
			if owner == span.Start {
				kind = ConflictMidEntry
			}
			return &Conflict{
				Kind:     kind,
				Target:   addr,
				OwnerPC:  owner,
				HasOwner: true,
				Detail: fmt.Sprintf("interval [0x%x, 0x%x) collides at 0x%x with instruction starting at 0x%x",
					span.Start, span.End, addr, owner),
			}
		}
	}
	return nil
}

// finalCheck asserts the indirect-target invariant over every decoded
// instruction. BFS already enqueued these targets, so this documents and
// defensively enforces the unique-start requirement.
func (r *runner) finalCheck(imageLen uint64) Result {
	for off := uint64(0); off < imageLen; off++ {
		pc := r.cfg.Base + off
		instr, ok := r.decoded[pc]
		if !ok {
			continue
		}
		for _, target := range instr.IndirectTargets {
			owner, owned := r.owner[target]
			if !owned {
				return r.conflictResult(Conflict{
					Kind:   ConflictIndirectNotStart,
					Target: target,
					Detail: fmt.Sprintf("indirect target 0x%x from 0x%x is not an instruction start", target, pc),
				})
			}
			if owner != target {
				return r.conflictResult(Conflict{
					Kind:     ConflictMidEntry,
					Target:   target,
					OwnerPC:  owner,
					HasOwner: true,
					Detail: fmt.Sprintf("indirect target 0x%x from 0x%x lands inside instruction at 0x%x",
						target, pc, owner),
				})
			}
		}
	}
	return Result{Verdict: VerdictSafe, Stats: r.stats.snapshot()}
}

// buildWitness reconstructs the BFS path from a trusted entry to target.
// Because the graph is explored FIFO, the parent chain is a shortest path.
func (r *runner) buildWitness(target uint64) []uint64 {
	var path []uint64
	cur, ok := target, true
	for ok {
		path = append(path, cur)
		parent := r.parent[cur]
		if parent == noParent {
			break
		}
		cur = parent
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	out := make([]uint64, len(path))
	copy(out, path)
	return out
}

func (r *runner) conflictResult(c Conflict) Result {
	return Result{
		Verdict:  VerdictConflict,
		Conflict: &c,
		Witness:  r.buildWitness(c.Target),
		Stats:    r.stats.snapshot(),
	}
}

func (r *runner) unknownResult(budget Budget, reason string) Result {
	return Result{
		Verdict: VerdictUnknown,
		Witness: r.buildWitness(r.queueHead()),
		Stats:   r.stats.snapshot(),
		Reason:  fmt.Sprintf("%s (budget: max_instr=%d max_edges=%d)", reason, budget.MaxInstructions, budget.MaxEdges),
	}
}

func (r *runner) queueHead() uint64 {
	if len(r.queue) == 0 {
		return noParent
	}
	return r.queue[0].addr
}

func rejectResult(stats Stats, reason string, witness []uint64) Result {
	return Result{
		Verdict: VerdictReject,
		Witness: witness,
		Stats:   stats.snapshot(),
		Reason:  reason,
	}
}

func (s Stats) snapshot() Stats {
	return Stats{Decoded: s.Decoded, Edges: s.Edges, Visited: s.Visited}
}

// appendUnique appends values to dst while preserving first-seen order.
func appendUnique(dst []uint64, values []uint64) []uint64 {
	seen := make(map[uint64]bool, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		dst = append(dst, value)
	}
	return dst
}
