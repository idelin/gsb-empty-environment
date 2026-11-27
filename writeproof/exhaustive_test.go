package writeproof

import (
	"math/rand"
	"testing"
)

const testBits = 8

func TestExhaustive8Bit(t *testing.T) {
	rng := rand.New(rand.NewSource(2848))
	for iteration := 0; iteration < 120; iteration++ {
		write := randomWrite(rng)
		protections := []Protection{randomProtection(rng)}
		blockSize := []uint64{1, 2, 4, 8, 16, 32, 64, 128, 256}[rng.Intn(9)]
		req := Request{
			Writes:         []WriteSegment{write},
			Protections:    protections,
			EraseBlockSize: blockSize,
			PhysicalBits:   testBits,
			Budget:         Budget{Unlimited: true},
			logicalBits:    testBits,
		}
		report, err := Prove(req)
		if err != nil {
			t.Fatalf("iteration %d: %v write=%+v protection=%+v", iteration, err, write, protections[0])
		}
		reference := referenceConflict(write, protections[0], blockSize)
		if report.Status == StatusUnknown {
			t.Fatalf("iteration %d: unexpected unknown: %s", iteration, report.Reason)
		}
		if reference == nil && report.Status != StatusSafe {
			t.Fatalf("iteration %d: false conflict: %+v write=%+v protection=%+v block=%d", iteration, report.Conflict, write, protections[0], blockSize)
		}
		if reference != nil {
			if report.Status != StatusConflict {
				t.Fatalf("iteration %d: missed conflict reference=%+v block=%d write=%+v protection=%+v", iteration, reference, blockSize, write, protections[0])
			}
			if report.Conflict.PhysicalAddress != reference.address {
				t.Fatalf("iteration %d: got address %d want %d\nwrite=%+v\nprotection=%+v\nblock=%d\ngot=%+v", iteration, report.Conflict.PhysicalAddress, reference.address, write, protections[0], blockSize, report.Conflict)
			}
			if report.Conflict.Phase != reference.phase {
				t.Fatalf("iteration %d: got phase %s want %s", iteration, report.Conflict.Phase, reference.phase)
			}
			var wantWriteX uint64
			if report.Conflict.Phase == PhaseErase {
				wantWriteX = minimumWritePreimageForPhysical(write, report.Conflict.WritePreimage.PhysicalAddress)
			} else {
				wantWriteX = minimumWritePreimageForPhysical(write, report.Conflict.PhysicalAddress)
			}
			if report.Conflict.WritePreimage == nil || report.Conflict.WritePreimage.LogicalAddress != wantWriteX {
				t.Fatalf("iteration %d: phase=%s block=%d write preimage = %+v want x=%d reference=%+v", iteration, report.Conflict.Phase, report.Conflict.PhysicalAddress, report.Conflict.WritePreimage, wantWriteX, reference)
			}
			if protections[0].Kind == ProtectionLogical {
				wantProtectedX := reference.protectedX
				if report.Conflict.ProtectedPreimage != nil && report.Conflict.Phase == PhaseErase {
					wantProtectedX = minimumProtectedPreimageForPhysical(protections[0], report.Conflict.ProtectedPreimage.PhysicalAddress)
				}
				if report.Conflict.ProtectedPreimage == nil || report.Conflict.ProtectedPreimage.LogicalAddress != wantProtectedX {
					t.Fatalf("iteration %d: protected preimage = %+v want %d reference=%+v", iteration, report.Conflict.ProtectedPreimage, wantProtectedX, reference)
				}
			}
		}
	}
}

type referenceResult struct {
	phase      Phase
	address    uint64
	writeX     uint64
	protectedX uint64
}

func minimumWritePreimageForPhysical(write WriteSegment, target uint64) uint64 {
	var best uint64
	found := false
	for x := uint64(0); x < 1<<testBits; x++ {
		if x >= write.Start && x < write.End && x&write.Mask == write.Match && write.Transform.Apply(x) == target {
			if !found || x < best {
				best = x
				found = true
			}
		}
	}
	return best
}

func minimumProtectedPreimageForPhysical(protection Protection, target uint64) uint64 {
	var best uint64
	found := false
	for x := uint64(0); x < 1<<testBits; x++ {
		if x >= protection.Start && x < protection.End && protection.Transform.Apply(x) == target {
			if !found || x < best {
				best = x
				found = true
			}
		}
	}
	return best
}

func minimumWritePreimageForBlock(write WriteSegment, block, blockSize uint64) uint64 {
	var bestX uint64
	var bestP uint64
	found := false
	for x := uint64(0); x < 1<<testBits; x++ {
		if x < write.Start || x >= write.End || x&write.Mask != write.Match {
			continue
		}
		p := write.Transform.Apply(x)
		if p&^(blockSize-1) != block {
			continue
		}
		if !found || p < bestP || (p == bestP && x < bestX) {
			bestX = x
			bestP = p
			found = true
		}
	}
	return bestX
}

func referenceConflict(write WriteSegment, protection Protection, blockSize uint64) *referenceResult {
	writeSet := make(map[uint64]uint64)
	writePreimage := make(map[uint64]uint64)
	for x := uint64(0); x < 1<<testBits; x++ {
		if x < write.Start || x >= write.End || x&write.Mask != write.Match {
			continue
		}
		p := write.Transform.Apply(x)
		writeSet[x] = p
		if existing, ok := writePreimage[p]; !ok || x < existing {
			writePreimage[p] = x
		}
	}
	protectedSet := make(map[uint64]uint64)
	for x := uint64(0); x < 1<<testBits; x++ {
		if x < protection.Start || x >= protection.End {
			continue
		}
		if protection.Kind == ProtectionLogical {
			p := protection.Transform.Apply(x)
			if existing, ok := protectedSet[p]; !ok || x < existing {
				protectedSet[p] = x
			}
		}
	}
	minimumProtectedPreimage := make(map[uint64]uint64)
	for p, x := range protectedSet {
		minimumProtectedPreimage[p] = x
	}
	var best *referenceResult
	for _, p := range writeSet {
		x := writePreimage[p]
		block := p &^ (blockSize - 1)
		if protectedP, ok := protectedBlockPoint(protection, block, blockSize, protectedSet); ok {
			candidate := &referenceResult{phase: PhaseErase, address: block, writeX: x, protectedX: minimumProtectedPreimage[protectedP]}
			if best == nil || candidate.phase != best.phase || candidate.address < best.address ||
				(candidate.address == best.address && candidate.writeX < best.writeX) ||
				(candidate.address == best.address && candidate.writeX == best.writeX && candidate.protectedX < best.protectedX) {
				best = candidate
			}
		}
		if physicalPointProtected(protection, p, protectedSet) {
			candidate := &referenceResult{phase: PhaseWrite, address: p, writeX: x, protectedX: minimumProtectedPreimage[p]}
			if best == nil || best.phase == PhaseWrite && (candidate.address < best.address ||
				(candidate.address == best.address && candidate.writeX < best.writeX) ||
				(candidate.address == best.address && candidate.writeX == best.writeX && candidate.protectedX < best.protectedX)) {
				best = candidate
			}
		}
	}
	return best
}

func protectedBlockPoint(protection Protection, block, blockSize uint64, protectedSet map[uint64]uint64) (uint64, bool) {
	if protection.Kind == ProtectionPhysical {
		blockEnd := block + blockSize
		if protection.Start < blockEnd && protection.End > block {
			p := maxUint64(protection.Start, block)
			return p, true
		}
		return 0, false
	}
	var best uint64
	found := false
	for p := range protectedSet {
		if p&^(blockSize-1) == block && (!found || protectedSet[p] < protectedSet[best] ||
			(protectedSet[p] == protectedSet[best] && p < best)) {
			best = p
			found = true
		}
	}
	return best, found
}

func maxUint64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func physicalPointProtected(protection Protection, p uint64, protectedSet map[uint64]uint64) bool {
	if protection.Kind == ProtectionPhysical {
		return p >= protection.Start && p < protection.End
	}
	_, ok := protectedSet[p]
	return ok
}

func chooseReference(current, candidate *referenceResult) *referenceResult {
	if current == nil {
		return candidate
	}
	if candidate.phase != current.phase {
		if candidate.phase == PhaseErase {
			return candidate
		}
		return current
	}
	if candidate.address != current.address {
		if candidate.address < current.address {
			return candidate
		}
		return current
	}
	if candidate.writeX != current.writeX {
		if candidate.writeX < current.writeX {
			return candidate
		}
		return current
	}
	if candidate.protectedX < current.protectedX {
		return candidate
	}
	return current
}

func randomWrite(rng *rand.Rand) WriteSegment {
	start := uint64(rng.Intn(256))
	end := start + uint64(rng.Intn(256-int(start)))
	mask := uint64(rng.Intn(256))
	match := uint64(rng.Intn(256)) & mask
	return WriteSegment{
		Start:     start,
		End:       end,
		Mask:      mask,
		Match:     match,
		Transform: randomTransform(rng),
	}
}

func randomProtection(rng *rand.Rand) Protection {
	start := uint64(rng.Intn(256))
	end := start + uint64(rng.Intn(256-int(start)))
	kind := ProtectionPhysical
	if rng.Intn(2) == 0 {
		kind = ProtectionLogical
	}
	return Protection{
		Kind:      kind,
		Start:     start,
		End:       end,
		Transform: randomTransform(rng),
	}
}

func randomTransform(rng *rand.Rand) AffineTransform {
	var transform AffineTransform
	for row := 0; row < testBits; row++ {
		transform.Rows[row] = uint64(rng.Intn(256))
	}
	transform.Bias = uint64(rng.Intn(256))
	return transform
}
