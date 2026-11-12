package synthisa_test

import (
	"errors"
	"testing"

	"example.org/domainreissue/q007/cfverify"
	"example.org/domainreissue/q007/cfverify/synthisa"
)

// refInstr is an independently decoded synthetic instruction, kept separate
// from the production decoder so the exhaustive oracle shares no code with
// the system under test.
type refInstr struct {
	start, end      uint64
	successors      []uint64
	indirectTargets []uint64
}

type oracleVerdict int

const (
	oracleSafe oracleVerdict = iota
	oracleConflict
	oracleReject
)

type oracleResult struct {
	verdict oracleVerdict
	target  uint64
	owner   uint64
	witness []uint64
}

const noParentMarker = ^uint64(0)

// referenceDecode independently interprets the synthetic ISA.
func referenceDecode(image []byte, pc uint64) (refInstr, error) {
	if pc >= uint64(len(image)) {
		return refInstr{}, errors.New("pc outside image")
	}
	switch image[pc] {
	case synthisa.OpHalt:
		return refInstr{start: pc, end: pc + synthisa.LenHalt}, nil
	case synthisa.OpNop:
		return refInstr{
			start:      pc,
			end:        pc + synthisa.LenNop,
			successors: []uint64{pc + synthisa.LenNop},
		}, nil
	case synthisa.OpJmp:
		if pc+synthisa.LenJmp > uint64(len(image)) {
			return refInstr{}, errors.New("truncated jmp")
		}
		rel := int8(image[pc+1])
		return refInstr{
			start:      pc,
			end:        pc + synthisa.LenJmp,
			successors: []uint64{pc + uint64(int64(rel))},
		}, nil
	case synthisa.OpCalli:
		if pc+synthisa.LenCalli > uint64(len(image)) {
			return refInstr{}, errors.New("truncated calli")
		}
		target := uint64(image[pc+1]) | uint64(image[pc+2])<<8
		return refInstr{
			start:           pc,
			end:             pc + synthisa.LenCalli,
			successors:      []uint64{pc + synthisa.LenCalli},
			indirectTargets: []uint64{target},
		}, nil
	default:
		return refInstr{}, errors.New("bad opcode")
	}
}

// oracle runs an independent BFS over image and reports the expected verdict
// plus the shortest conflict witness.
func oracle(image []byte, entries []uint64) oracleResult {
	size := uint64(len(image))
	owner := map[uint64]uint64{}
	decoded := map[uint64]refInstr{}
	parent := map[uint64]uint64{}
	queued := map[uint64]bool{}
	type node struct {
		addr     uint64
		indirect bool
	}
	var queue []node

	rebuild := func(target uint64) []uint64 {
		var path []uint64
		cur := target
		for {
			path = append(path, cur)
			p, ok := parent[cur]
			if !ok || p == noParentMarker {
				break
			}
			cur = p
		}
		for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
			path[i], path[j] = path[j], path[i]
		}
		return path
	}

	for _, entry := range entries {
		if entry >= size {
			return oracleResult{verdict: oracleReject}
		}
		if !queued[entry] {
			queued[entry] = true
			parent[entry] = noParentMarker
			queue = append(queue, node{addr: entry})
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		pc := cur.addr

		// Permitted indirect targets are validated as unique starts but do
		// not extend the decoded reachable graph.
		if cur.indirect {
			if pc >= size {
				return oracleResult{verdict: oracleReject}
			}
			existing, ok := owner[pc]
			if !ok {
				directPending := false
				for _, other := range queue {
					if !other.indirect {
						directPending = true
						break
					}
				}
				if directPending {
					queue = append(queue, cur)
					continue
				}
				return oracleResult{
					verdict: oracleConflict,
					target:  pc,
					witness: rebuild(pc),
				}
			}
			if existing != pc {
				return oracleResult{
					verdict: oracleConflict,
					target:  pc,
					owner:   existing,
					witness: rebuild(pc),
				}
			}
			continue
		}

		if existing, ok := owner[pc]; ok && existing != pc {
			return oracleResult{
				verdict: oracleConflict,
				target:  pc,
				owner:   existing,
				witness: rebuild(pc),
			}
		}
		if _, ok := decoded[pc]; ok {
			continue
		}

		instr, err := referenceDecode(image, pc)
		if err != nil {
			return oracleResult{verdict: oracleReject}
		}
		for addr := instr.start; addr < instr.end; addr++ {
			if existing, ok := owner[addr]; ok {
				return oracleResult{
					verdict: oracleConflict,
					target:  addr,
					owner:   existing,
					witness: rebuild(addr),
				}
			}
		}
		for addr := instr.start; addr < instr.end; addr++ {
			owner[addr] = pc
		}
		decoded[pc] = instr

		// Direct successors extend the graph.
		seen := map[uint64]bool{}
		for _, target := range instr.successors {
			if seen[target] {
				continue
			}
			seen[target] = true
			if target >= size {
				return oracleResult{verdict: oracleReject}
			}
			if !queued[target] {
				queued[target] = true
				parent[target] = pc
				queue = append(queue, node{addr: target})
			}
		}
		// Indirect targets are enqueued as validation-only nodes.
		for _, target := range instr.indirectTargets {
			if !queued[target] {
				queued[target] = true
				parent[target] = pc
				queue = append(queue, node{addr: target, indirect: true})
			}
		}
	}

	// Defensive re-assertion of the indirect-target invariant once drained.
	for _, instr := range decoded {
		for _, target := range instr.indirectTargets {
			if target >= size {
				return oracleResult{verdict: oracleReject}
			}
			existing, ok := owner[target]
			if !ok || existing != target {
				return oracleResult{
					verdict: oracleConflict,
					target:  target,
					owner:   existing,
					witness: rebuild(target),
				}
			}
		}
	}
	return oracleResult{verdict: oracleSafe}
}

// TestExhaustiveSmallImages enumerates every small image over the synthetic
// opcode alphabet. This covers legal shared tails (multiple entries converging
// on one instruction) and jumps into the middle of a decoded instruction.
func TestExhaustiveSmallImages(t *testing.T) {
	const maxBody = 5
	alphabet := []byte{
		synthisa.OpHalt,
		synthisa.OpNop,
		synthisa.OpJmp,
		synthisa.OpCalli,
		synthisa.OpBad,
	}

	images := 0
	counts := map[cfverify.Verdict]int{}

	for n := 0; n <= maxBody; n++ {
		body := make([]byte, n)
		for {
			image := make([]byte, 0, n+1)
			image = append(image, synthisa.VersionByte)
			image = append(image, body...)
			images++

			// Offset 0 holds the fixed version byte; code begins at 1. A
			// second trusted entry where possible exercises shared-tail
			// convergence and arrivals in the middle of an instruction.
			entries := []uint64{1}
			if uint64(len(image)) > 4 {
				entries = append(entries, 4)
			}

			want := oracle(image, entries)
			got := cfverify.Verify(cfverify.Config{
				Image:         image,
				Entries:       entries,
				VersionOffset: 0,
				VersionByte:   synthisa.VersionByte,
				Decode:        synthisa.Decode,
			})

			switch want.verdict {
			case oracleSafe:
				if got.Verdict != cfverify.VerdictSafe {
					t.Fatalf("image %v: want safe, got %s (%s)", image, got.Verdict, got.Reason)
				}
			case oracleConflict:
				if got.Verdict != cfverify.VerdictConflict {
					t.Fatalf("image %v: want conflict, got %s (%s)", image, got.Verdict, got.Reason)
				}
				if got.Conflict == nil || got.Conflict.Target != want.target {
					t.Fatalf("image %v: conflict target mismatch: got %+v want %d", image, got.Conflict, want.target)
				}
				if len(got.Witness) == 0 || got.Witness[len(got.Witness)-1] != want.target {
					t.Fatalf("image %v: witness must end at %d, got %v", image, want.target, got.Witness)
				}
				if !witnessWalkable(image, got.Witness) {
					t.Fatalf("image %v: non-walkable witness %v", image, got.Witness)
				}
				if len(got.Witness) != len(want.witness) {
					t.Fatalf("image %v: witness length %d vs oracle shortest %d (%v vs %v)",
						image, len(got.Witness), len(want.witness), got.Witness, want.witness)
				}
			case oracleReject:
				if got.Verdict != cfverify.VerdictReject {
					t.Fatalf("image %v: want reject, got %s", image, got.Verdict)
				}
			}
			counts[got.Verdict]++

			if !increment(body, alphabet) {
				break
			}
		}
	}

	t.Logf("exhausted %d images: %+v", images, counts)
	if counts[cfverify.VerdictSafe] == 0 || counts[cfverify.VerdictConflict] == 0 || counts[cfverify.VerdictReject] == 0 {
		t.Fatalf("enumeration must cover every verdict class: %+v", counts)
	}
}

// witnessWalkable confirms each consecutive witness pair is an actual edge of
// the independently decoded graph.
func witnessWalkable(image []byte, witness []uint64) bool {
	for i := 0; i+1 < len(witness); i++ {
		instr, err := referenceDecode(image, witness[i])
		if err != nil {
			return false
		}
		edge := false
		for _, succ := range append(append([]uint64{}, instr.successors...), instr.indirectTargets...) {
			if succ == witness[i+1] {
				edge = true
				break
			}
		}
		if !edge {
			return false
		}
	}
	return true
}

func increment(digits, alphabet []byte) bool {
	for i := len(digits) - 1; i >= 0; i-- {
		idx := int(digits[i])
		if idx+1 < len(alphabet) {
			digits[i] = alphabet[idx+1]
			for j := i + 1; j < len(digits); j++ {
				digits[j] = alphabet[0]
			}
			return true
		}
	}
	return false
}
