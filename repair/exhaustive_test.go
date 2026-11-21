package repair

import (
	"math/rand"
	"testing"
)

func bruteForce(dim int, shards []Shard, shardRack []int, fees []int64, targets [][]byte) ([]int, int64, bool) {
	n := len(shards)
	var best []int
	var bestCost int64
	found := false

	for mask := 0; mask < 1<<n; mask++ {
		basis := newEchelon(dim)
		set := make([]int, 0, n)
		for i := 0; i < n; i++ {
			if mask&(1<<i) != 0 {
				basis.addRow(shards[i].Row)
				set = append(set, i)
			}
		}
		feasible := true
		for _, target := range targets {
			if !basis.contains(target) {
				feasible = false
				break
			}
		}
		if !feasible {
			continue
		}

		cost := int64(0)
		seen := make(map[int]bool)
		for _, i := range set {
			cost += shards[i].ReadPrice
			rack := shardRack[i]
			if !seen[rack] {
				seen[rack] = true
				cost += fees[rack]
			}
		}

		better := !found || cost < bestCost
		if !better && found && cost == bestCost {
			if len(set) < len(best) {
				better = true
			} else if len(set) == len(best) {
				for k := range set {
					if set[k] != best[k] {
						better = set[k] < best[k]
						break
					}
				}
			}
		}
		if better {
			found = true
			best = append([]int(nil), set...)
			bestCost = cost
		}
	}
	return best, bestCost, found
}

func TestExhaustiveRandomSmall(t *testing.T) {
	rng := rand.New(rand.NewSource(20261121))
	for iteration := 0; iteration < 400; iteration++ {
		dim := 1 + rng.Intn(5)
		n := rng.Intn(8)
		rackCount := 1 + rng.Intn(3)

		shards := make([]Shard, n)
		shardRack := make([]int, n)
		fees := make([]int64, rackCount)
		rackFee := make(map[int]int64)
		for r := 0; r < rackCount; r++ {
			fees[r] = int64(rng.Intn(10))
			rackFee[r] = fees[r]
		}
		for i := range shards {
			row := make([]byte, dim)
			for j := range row {
				row[j] = byte(rng.Intn(4))
			}
			rack := rng.Intn(rackCount)
			shardRack[i] = rack
			shards[i] = Shard{
				Row:       row,
				ReadPrice: int64(rng.Intn(6)),
				Rack:      rack,
			}
		}
		targetCount := 1 + rng.Intn(3)
		targets := make([][]byte, targetCount)
		for i := range targets {
			target := make([]byte, dim)
			for j := range target {
				target[j] = byte(rng.Intn(4))
			}
			targets[i] = target
		}

		wantSet, wantCost, wantFeasible := bruteForce(dim, shards, shardRack, fees, targets)
		result, err := Schedule(dim, shards, rackFee, targets)
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", iteration, err)
		}

		if !wantFeasible {
			if result.Certificate == nil {
				t.Fatalf("iteration %d: expected certificate, got plan", iteration)
			}
			if err := CheckCertificate(dim, shards, targets, result.Certificate); err != nil {
				t.Fatalf("iteration %d: invalid certificate: %v", iteration, err)
			}
			continue
		}
		if result.Plan == nil {
			t.Fatalf("iteration %d: expected plan, got certificate", iteration)
		}
		if result.Plan.Cost != wantCost {
			t.Fatalf("iteration %d: cost = %d, want %d", iteration, result.Plan.Cost, wantCost)
		}
		if !equalInts(result.Plan.ShardIndexes, wantSet) {
			t.Fatalf("iteration %d: set = %v, want %v", iteration, result.Plan.ShardIndexes, wantSet)
		}
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
