package repair_test

import (
	"math/rand"
	"testing"

	"prescreen.local/ps-8303db8549/repair"
)

// mul256 is an independent reference implementation of GF(256) multiplication
// using modulus 0x11d, kept separate from the package so reconstruction tests
// do not merely reuse the implementation under test.
func mul256(a, b byte) byte {
	var product uint16
	current := uint16(a)
	for bit := 0; bit < 8; bit++ {
		if b&(1<<bit) != 0 {
			product ^= current
		}
		current <<= 1
		if current&0x100 != 0 {
			current ^= 0x11d
		}
	}
	return byte(product)
}

func dot256(row, vector []byte) byte {
	var acc byte
	for i, coefficient := range row {
		acc ^= mul256(coefficient, vector[i])
	}
	return acc
}

func applyRow(row []byte, stripe [][]byte, length int) []byte {
	out := make([]byte, length)
	for b := 0; b < length; b++ {
		column := make([]byte, len(row))
		for d := range row {
			column[d] = stripe[d][b]
		}
		out[b] = dot256(row, column)
	}
	return out
}

func independentCost(indexes []int, shards []repair.Shard, rackFee map[int]int64) int64 {
	cost := int64(0)
	seen := make(map[int]bool)
	for _, i := range indexes {
		cost += shards[i].ReadPrice
		if !seen[shards[i].Rack] {
			seen[shards[i].Rack] = true
			cost += rackFee[shards[i].Rack]
		}
	}
	return cost
}

// Two targets sharing one expensive rack is cheaper than spreading reads over
// two racks: the first-read fee is charged once and serves both targets.
func TestSharedExpensiveRack(t *testing.T) {
	dim := 2
	shards := []repair.Shard{
		{Row: []byte{1, 0}, ReadPrice: 0, Rack: 0},
		{Row: []byte{0, 1}, ReadPrice: 0, Rack: 0},
		{Row: []byte{1, 0}, ReadPrice: 50, Rack: 1},
		{Row: []byte{0, 1}, ReadPrice: 50, Rack: 1},
	}
	rackFee := map[int]int64{0: 100, 1: 100}
	targets := [][]byte{{1, 0}, {0, 1}}

	result, err := repair.Schedule(dim, shards, rackFee, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if result.Plan == nil {
		t.Fatalf("expected feasible plan, got certificate for target %d", result.Certificate.TargetIndex)
	}
	plan := result.Plan
	if len(plan.ShardIndexes) != 2 || plan.ShardIndexes[0] != 0 || plan.ShardIndexes[1] != 1 {
		t.Fatalf("selected %v, want [0 1]", plan.ShardIndexes)
	}
	if plan.Cost != 100 || independentCost(plan.ShardIndexes, shards, rackFee) != plan.Cost {
		t.Fatalf("cost = %d, want 100", plan.Cost)
	}
	assertReconstruction(t, dim, shards, rackFee, targets, plan, 4096)
}

// Duplicate coefficient rows with different prices must pick the cheapest
// source; equal prices fall back to the lexicographically smallest index.
func TestDuplicateRowsDifferentPrices(t *testing.T) {
	dim := 1
	targets := [][]byte{{1}}

	shards := []repair.Shard{
		{Row: []byte{1}, ReadPrice: 10, Rack: 0},
		{Row: []byte{1}, ReadPrice: 3, Rack: 0},
		{Row: []byte{1}, ReadPrice: 3, Rack: 1},
	}
	result, err := repair.Schedule(dim, shards, map[int]int64{0: 0, 1: 0}, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(result.Plan.ShardIndexes) != 1 || result.Plan.ShardIndexes[0] != 1 {
		t.Fatalf("selected %v, want [1]", result.Plan.ShardIndexes)
	}

	shards[2].ReadPrice = 2
	result, err = repair.Schedule(dim, shards, map[int]int64{0: 0, 1: 0}, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if len(result.Plan.ShardIndexes) != 1 || result.Plan.ShardIndexes[0] != 2 {
		t.Fatalf("selected %v, want [2]", result.Plan.ShardIndexes)
	}
	assertReconstruction(t, dim, shards, map[int]int64{0: 0, 1: 0}, targets, result.Plan, 64)
}

// Coefficients other than 0 and 1 exercise real GF(256) arithmetic, not just
// XOR. Reconstructing row (1,0) from row (2,0) needs the inverse of 2.
func TestNonTrivialCoefficients(t *testing.T) {
	dim := 2
	shards := []repair.Shard{
		{Row: []byte{2, 0}, ReadPrice: 1, Rack: 0},
		{Row: []byte{0, 2}, ReadPrice: 1, Rack: 0},
		{Row: []byte{3, 0x53}, ReadPrice: 1, Rack: 1},
	}
	targets := [][]byte{{1, 0}, {0, 1}, {3, 0x53}}
	rackFee := map[int]int64{0: 0, 1: 7}

	result, err := repair.Schedule(dim, shards, rackFee, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if result.Plan == nil {
		t.Fatalf("expected plan, got certificate")
	}
	plan := result.Plan

	inverseTwo := byte(0)
	for candidate := 1; candidate < 256; candidate++ {
		if mul256(2, byte(candidate)) == 1 {
			inverseTwo = byte(candidate)
			break
		}
	}
	if inverseTwo == 0 {
		t.Fatal("reference GF: inverse of 2 not found")
	}
	position := -1
	for k, shardIndex := range plan.ShardIndexes {
		if shardIndex == 0 {
			position = k
		}
	}
	if position < 0 || plan.Decode[0][position] != inverseTwo {
		t.Fatalf("decode coefficient for target e1 = %v, want inverse(2)=%#x at position %d",
			plan.Decode[0], inverseTwo, position)
	}
	assertReconstruction(t, dim, shards, rackFee, targets, plan, 2048)
}

// An all-zero target is already known, so it needs no reads, alone or together
// with other targets.
func TestZeroTargetNeedsNoReads(t *testing.T) {
	dim := 2

	result, err := repair.Schedule(dim, nil, map[int]int64{}, [][]byte{{0, 0}})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if result.Plan == nil || len(result.Plan.ShardIndexes) != 0 || result.Plan.Cost != 0 {
		t.Fatalf("zero-only target = %+v, want empty zero-cost plan", result)
	}

	shards := []repair.Shard{
		{Row: []byte{1, 0}, ReadPrice: 5, Rack: 0},
		{Row: []byte{0, 1}, ReadPrice: 5, Rack: 0},
	}
	rackFee := map[int]int64{0: 9}
	targets := [][]byte{{0, 0}, {1, 0}}
	result, err = repair.Schedule(dim, shards, rackFee, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if result.Plan == nil {
		t.Fatalf("expected plan, got certificate")
	}
	if len(result.Plan.ShardIndexes) != 1 || result.Plan.ShardIndexes[0] != 0 || result.Plan.Cost != 14 {
		t.Fatalf("mixed zero target plan = %+v, want shard [0] cost 14", result.Plan)
	}
	for _, coefficient := range result.Plan.Decode[0] {
		if coefficient != 0 {
			t.Fatalf("zero target decode = %v, want all zero", result.Plan.Decode[0])
		}
	}
	assertReconstruction(t, dim, shards, rackFee, targets, result.Plan, 128)
}

// Even when the full stripe cannot be decoded, an in-span target must still be
// recovered; an out-of-span target yields a dual-space certificate.
func TestPartialRecoveryAndCertificate(t *testing.T) {
	dim := 3
	shards := []repair.Shard{
		{Row: []byte{1, 0, 0}, ReadPrice: 1, Rack: 0},
		{Row: []byte{0, 1, 0}, ReadPrice: 1, Rack: 0},
	}
	rackFee := map[int]int64{0: 0}

	recoverable, err := repair.Schedule(dim, shards, rackFee, [][]byte{{1, 1, 0}})
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if recoverable.Plan == nil {
		t.Fatalf("in-span target unexpectedly rejected with certificate")
	}

	targets := [][]byte{{0, 0, 1}}
	infeasible, err := repair.Schedule(dim, shards, rackFee, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if infeasible.Certificate == nil {
		t.Fatalf("expected certificate, got plan %+v", infeasible.Plan)
	}
	certificate := infeasible.Certificate
	if certificate.TargetIndex != 0 {
		t.Fatalf("certificate target = %d, want 0", certificate.TargetIndex)
	}
	if err := repair.CheckCertificate(dim, shards, targets, certificate); err != nil {
		t.Fatalf("CheckCertificate: %v", err)
	}
	if dot256(targets[0], certificate.Vector) != 1 {
		t.Fatalf("target inner product = %d, want 1", dot256(targets[0], certificate.Vector))
	}
	for i, shard := range shards {
		if dot256(shard.Row, certificate.Vector) != 0 {
			t.Fatalf("shard %d inner product nonzero", i)
		}
	}

	tampered := &repair.Certificate{TargetIndex: 0, Vector: append([]byte(nil), certificate.Vector...)}
	tampered.Vector[0] ^= 1
	if err := repair.CheckCertificate(dim, shards, targets, tampered); err == nil {
		t.Fatal("tampered certificate accepted")
	}
	if err := repair.CheckCertificate(dim, shards, targets, &repair.Certificate{
		TargetIndex: 9,
		Vector:      certificate.Vector,
	}); err == nil {
		t.Fatal("out-of-range certificate target accepted")
	}
	if err := repair.CheckCertificate(dim, shards, targets, nil); err == nil {
		t.Fatal("nil certificate accepted")
	}
	if err := repair.CheckCertificate(dim, shards, targets, &repair.Certificate{
		TargetIndex: 0,
		Vector:      []byte{1, 2},
	}); err == nil {
		t.Fatal("wrong-length certificate vector accepted")
	}
	badLengthShards := []repair.Shard{{Row: []byte{1}, ReadPrice: 0, Rack: 0}}
	if err := repair.CheckCertificate(dim, badLengthShards, targets, &repair.Certificate{
		TargetIndex: 0,
		Vector:      certificate.Vector,
	}); err == nil {
		t.Fatal("bad shard length accepted")
	}
	badLengthTargets := [][]byte{{1}}
	if err := repair.CheckCertificate(dim, shards, badLengthTargets, &repair.Certificate{
		TargetIndex: 0,
		Vector:      certificate.Vector,
	}); err == nil {
		t.Fatal("bad target length accepted")
	}
}

func TestInputValidation(t *testing.T) {
	validRow := []byte{1, 0}
	shards := []repair.Shard{{Row: validRow, ReadPrice: 0, Rack: 0}}
	targets := [][]byte{{1, 0}}

	if _, err := repair.Schedule(0, shards, map[int]int64{}, targets); err == nil {
		t.Error("dim=0 accepted")
	}
	if _, err := repair.Schedule(13, shards, map[int]int64{}, targets); err == nil {
		t.Error("dim=13 accepted")
	}
	if _, err := repair.Schedule(3, shards, map[int]int64{}, targets); err == nil {
		t.Error("row length mismatch accepted")
	}
	if _, err := repair.Schedule(2, nil, nil, [][]byte{validRow}); err == nil {
		t.Error("nil rack fee map accepted")
	}
	if _, err := repair.Schedule(2, []repair.Shard{
		{Row: validRow, ReadPrice: -1, Rack: 0},
	}, map[int]int64{}, targets); err == nil {
		t.Error("negative read price accepted")
	}
	many := make([]repair.Shard, 25)
	for i := range many {
		many[i] = repair.Shard{Row: []byte{1, 0}, ReadPrice: 0, Rack: 0}
	}
	if _, err := repair.Schedule(2, many, map[int]int64{}, targets); err == nil {
		t.Error("25 shards accepted")
	}
}

func TestRandomStripeReconstruction(t *testing.T) {
	rng := rand.New(rand.NewSource(99173))
	const stripeLength = 257

	for iteration := 0; iteration < 120; iteration++ {
		dim := 1 + rng.Intn(12)
		n := rng.Intn(25)
		rackCount := 1 + rng.Intn(5)
		rackFee := make(map[int]int64)
		for rack := 0; rack < rackCount; rack++ {
			rackFee[rack] = int64(rng.Intn(50))
		}

		shards := make([]repair.Shard, n)
		for i := range shards {
			row := make([]byte, dim)
			for j := range row {
				row[j] = byte(rng.Intn(256))
			}
			shards[i] = repair.Shard{
				Row:       row,
				ReadPrice: int64(rng.Intn(20)),
				Rack:      rng.Intn(rackCount),
			}
		}

		targetCount := 1 + rng.Intn(4)
		targets := make([][]byte, targetCount)
		for i := range targets {
			target := make([]byte, dim)
			for j := range target {
				target[j] = byte(rng.Intn(256))
			}
			targets[i] = target
		}

		result, err := repair.Schedule(dim, shards, rackFee, targets)
		if err != nil {
			t.Fatalf("iteration %d: %v", iteration, err)
		}
		if result.Certificate != nil {
			if err := repair.CheckCertificate(dim, shards, targets, result.Certificate); err != nil {
				t.Fatalf("iteration %d: invalid certificate: %v", iteration, err)
			}
			continue
		}

		plan := result.Plan
		for i := 1; i < len(plan.ShardIndexes); i++ {
			if plan.ShardIndexes[i-1] >= plan.ShardIndexes[i] {
				t.Fatalf("iteration %d: shard indexes not strictly ascending: %v", iteration, plan.ShardIndexes)
			}
		}
		if len(plan.Decode) != len(targets) {
			t.Fatalf("iteration %d: %d decode rows for %d targets", iteration, len(plan.Decode), len(targets))
		}
		if plan.Cost != independentCost(plan.ShardIndexes, shards, rackFee) {
			t.Fatalf("iteration %d: cost mismatch", iteration)
		}
		assertReconstruction(t, dim, shards, rackFee, targets, plan, stripeLength)
	}
}

// A full-size instance (12 dimensions, 24 readable shards) must finish promptly
// and still reconstruct every feasible target from a random stripe.
func TestFullSizeInstance(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	dim := 12
	shards := make([]repair.Shard, 24)
	rackFee := map[int]int64{}
	for i := range shards {
		row := make([]byte, dim)
		for j := range row {
			row[j] = byte(rng.Intn(256))
		}
		rack := i % 6
		rackFee[rack] = int64(10 * (rack + 1))
		shards[i] = repair.Shard{
			Row:       row,
			ReadPrice: int64(1 + rng.Intn(8)),
			Rack:      rack,
		}
	}
	targets := make([][]byte, 3)
	for i := range targets {
		target := make([]byte, dim)
		target[i] = 1
		targets[i] = target
	}

	result, err := repair.Schedule(dim, shards, rackFee, targets)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if result.Plan == nil {
		if err := repair.CheckCertificate(dim, shards, targets, result.Certificate); err != nil {
			t.Fatalf("certificate invalid: %v", err)
		}
		return
	}
	if result.Plan.Cost != independentCost(result.Plan.ShardIndexes, shards, rackFee) {
		t.Fatal("cost mismatch")
	}
	assertReconstruction(t, dim, shards, rackFee, targets, result.Plan, 512)
}

func assertReconstruction(t *testing.T, dim int, shards []repair.Shard, rackFee map[int]int64, targets [][]byte, plan *repair.Plan, length int) {
	t.Helper()
	rng := rand.New(rand.NewSource(0xBADC0FFE))
	stripe := make([][]byte, dim)
	for d := range stripe {
		stripe[d] = make([]byte, length)
		if _, err := rng.Read(stripe[d]); err != nil {
			t.Fatalf("random stripe: %v", err)
		}
	}

	reads := make([][]byte, len(plan.ShardIndexes))
	for k, shardIndex := range plan.ShardIndexes {
		reads[k] = applyRow(shards[shardIndex].Row, stripe, length)
	}

	for targetIndex, target := range targets {
		want := applyRow(target, stripe, length)
		got := make([]byte, length)
		if len(plan.Decode[targetIndex]) != len(plan.ShardIndexes) {
			t.Fatalf("target %d: %d decode coefficients for %d shards",
				targetIndex, len(plan.Decode[targetIndex]), len(plan.ShardIndexes))
		}
		for b := 0; b < length; b++ {
			var value byte
			for k, read := range reads {
				value ^= mul256(plan.Decode[targetIndex][k], read[b])
			}
			got[b] = value
		}
		for b := range want {
			if got[b] != want[b] {
				t.Fatalf("target %d: reconstruction mismatch at byte %d: %d != %d", targetIndex, b, got[b], want[b])
			}
		}
	}

	if cost := independentCost(plan.ShardIndexes, shards, rackFee); cost != plan.Cost {
		t.Fatalf("plan cost %d, independent recomputation %d", plan.Cost, cost)
	}
}
