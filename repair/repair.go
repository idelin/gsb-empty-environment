package repair

import (
	"errors"
	"fmt"
	"math"
)

// Shard describes one readable shard: a known linear combination, over
// GF(256), of the dim source byte vectors, together with its read price and
// the rack whose first-read fee applies.
type Shard struct {
	Row       []byte
	ReadPrice int64
	Rack      int
}

// Plan is returned when every requested target is reconstructable.
type Plan struct {
	// ShardIndexes are the selected readable shard indices in ascending order.
	ShardIndexes []int
	// Cost is the total charge of reading ShardIndexes, including rack fees.
	Cost int64
	// Decode[t][k] is the coefficient applied to selected shard
	// ShardIndexes[k] when reconstructing target t. A zero target yields an
	// all-zero coefficient vector (and needs no reads on its own).
	Decode [][]byte
}

// Certificate witnesses that one target is not recoverable from any readable
// shard. Vector is a column vector such that every readable shard row is
// orthogonal to it, while the indicated target row has inner product one.
type Certificate struct {
	TargetIndex int
	Vector      []byte
}

// Result carries either a feasible Plan or an infeasibility Certificate.
type Result struct {
	Plan        *Plan
	Certificate *Certificate
}

// Schedule selects the cheapest set of readable shards that reconstructs all
// target rows. dim is the number of source dimensions (at most twelve), at
// most 24 shards may be supplied, and rackFee charges the first read on each
// rack; a rack absent from the map is treated as having a zero fee. A single
// read serves every target.
//
// Ties on total cost are broken first by fewer selected shards, then by the
// lexicographically smaller ascending list of shard indices. Even when the
// full source stripe cannot be decoded, targets lying in the span of the
// readable rows are still recovered; otherwise a dual-space certificate is
// returned for one unrecoverable target.
func Schedule(dim int, shards []Shard, rackFee map[int]int64, targets [][]byte) (*Result, error) {
	if dim < 1 || dim > 12 {
		return nil, fmt.Errorf("repair: dim must be in [1,12], got %d", dim)
	}
	if len(shards) > 24 {
		return nil, fmt.Errorf("repair: at most 24 shards allowed, got %d", len(shards))
	}
	if rackFee == nil {
		return nil, errors.New("repair: rackFee map must not be nil")
	}
	for rack, fee := range rackFee {
		if fee < 0 {
			return nil, fmt.Errorf("repair: negative rack fee for rack %d", rack)
		}
	}
	for i, shard := range shards {
		if len(shard.Row) != dim {
			return nil, fmt.Errorf("repair: shard %d row has length %d, want %d", i, len(shard.Row), dim)
		}
		if shard.ReadPrice < 0 {
			return nil, fmt.Errorf("repair: shard %d has negative read price", i)
		}
	}
	for i, target := range targets {
		if len(target) != dim {
			return nil, fmt.Errorf("repair: target %d row has length %d, want %d", i, len(target), dim)
		}
	}

	shardRack, fees := compactRacks(shards, rackFee)

	targetBasis := newEchelon(dim)
	for _, target := range targets {
		targetBasis.addRow(target)
	}
	var targetBasisRows [][]byte
	for _, row := range targetBasis.rows {
		if row != nil {
			targetBasisRows = append(targetBasisRows, row)
		}
	}

	state := &searchState{
		dim:             dim,
		shards:          shards,
		shardRack:       shardRack,
		fees:            fees,
		used:            make([]bool, len(fees)),
		basis:           newEchelon(dim),
		targetBasisRows: targetBasisRows,
		reduceBuffer:    make([]byte, dim),
		probeBuffer:     make([]byte, dim),
		rowPool:         make([][]byte, dim),
		chosenStack:     make([]int, 0, dim),
		targetRank:      len(targetBasisRows),
		bestCost:        math.MaxInt64,
	}
	for i := range state.rowPool {
		state.rowPool[i] = make([]byte, dim)
	}
	state.dfs(0)

	if state.bestSet != nil {
		return state.buildPlan(targets), nil
	}

	for i, target := range targets {
		if state.basis.contains(target) {
			continue
		}
		certificate := buildCertificate(dim, shards, i, target)
		if certificate != nil {
			return &Result{Certificate: certificate}, nil
		}
	}
	return nil, errors.New("repair: internal error: no feasible plan and no certificate")
}

func compactRacks(shards []Shard, rackFee map[int]int64) ([]int, []int64) {
	rackIndex := make(map[int]int)
	shardRack := make([]int, len(shards))
	for i, shard := range shards {
		index, ok := rackIndex[shard.Rack]
		if !ok {
			index = len(rackIndex)
			rackIndex[shard.Rack] = index
		}
		shardRack[i] = index
	}
	fees := make([]int64, len(rackIndex))
	for rack, index := range rackIndex {
		fees[index] = rackFee[rack]
	}
	return shardRack, fees
}

type searchState struct {
	dim             int
	shards          []Shard
	shardRack       []int
	fees            []int64
	used            []bool
	cost            int64
	basis           *echelon
	targetBasisRows [][]byte
	reduceBuffer    []byte
	probeBuffer     []byte
	rowPool         [][]byte
	chosenStack     []int
	targetRank      int
	bestSet         []int
	bestCost        int64
}

// feasible reports whether every distinct target row belongs to the span of
// the current shard basis. The target rows are mutually independent, so the
// span check needs no separate probe basis.
func (s *searchState) feasible() bool {
	if s.basis.rank() < s.targetRank {
		return false
	}
	for _, targetRow := range s.targetBasisRows {
		if s.basis.reduceInto(targetRow, s.probeBuffer) >= 0 {
			return false
		}
	}
	return true
}

func (s *searchState) better(chosen []int, cost int64) bool {
	if s.bestSet == nil || cost < s.bestCost {
		return true
	}
	if cost > s.bestCost {
		return false
	}
	if len(chosen) != len(s.bestSet) {
		return len(chosen) < len(s.bestSet)
	}
	for i := range chosen {
		if chosen[i] != s.bestSet[i] {
			return chosen[i] < s.bestSet[i]
		}
	}
	return false
}

// dfs enumerates subsets in ascending-index order. Only rank-increasing shards
// are ever extended: an inclusion-minimal cheapest set is linearly independent,
// so any set needing a dependent row is dominated by its cheaper subset.
func (s *searchState) dfs(start int) {
	if s.feasible() {
		chosen := make([]int, len(s.chosenStack))
		copy(chosen, s.chosenStack)
		if s.better(chosen, s.cost) {
			s.bestSet = chosen
			s.bestCost = s.cost
		}
	}

	// Extensions cannot improve on an equally cheap node: they only add shards
	// and non-negative charges, so they lose the cardinality tie-break.
	if s.cost >= s.bestCost || s.basis.rank() == s.dim {
		return
	}
	// Even if every remaining shard raised the rank, the targets could never
	// become feasible from this node: prune the whole branch.
	if s.basis.rank()+(len(s.shards)-start) < s.targetRank {
		return
	}
	for i := start; i < len(s.shards); i++ {
		pivot := s.basis.reduceInto(s.shards[i].Row, s.reduceBuffer)
		if pivot < 0 {
			continue
		}

		rack := s.shardRack[i]
		delta := s.shards[i].ReadPrice
		rackFirst := false
		if !s.used[rack] {
			delta += s.fees[rack]
			rackFirst = true
		}
		// Equal-cost nodes must still be visited: they may win the
		// cardinality or lexicographic tie-break even though extensions of
		// them cannot (all charges are non-negative).
		if s.cost+delta > s.bestCost {
			continue
		}

		poolRow := s.rowPool[pivot]
		copy(poolRow, s.reduceBuffer)
		inverse := gfInv(poolRow[pivot])
		for j := pivot; j < s.dim; j++ {
			poolRow[j] = mul(poolRow[j], inverse)
		}
		s.basis.rows[pivot] = poolRow
		s.chosenStack = append(s.chosenStack, i)
		if rackFirst {
			s.used[rack] = true
		}
		s.cost += delta

		s.dfs(i + 1)

		s.cost -= delta
		if rackFirst {
			s.used[rack] = false
		}
		s.chosenStack = s.chosenStack[:len(s.chosenStack)-1]
		s.basis.rows[pivot] = nil
	}
}

func (s *searchState) buildPlan(targets [][]byte) *Result {
	decode := make([][]byte, len(targets))
	for t, target := range targets {
		coefficients, ok := decodeCoeffs(s.dim, s.shards, s.bestSet, target)
		if !ok {
			return nil
		}
		decode[t] = coefficients
	}
	return &Result{Plan: &Plan{
		ShardIndexes: append(make([]int, 0, len(s.bestSet)), s.bestSet...),
		Cost:         s.bestCost,
		Decode:       decode,
	}}
}

// decodeCoeffs solves selected-row combination equals target for the per-shard
// coefficients. The system has one equation per source dimension and one
// variable per selected shard; free variables take the value zero.
func decodeCoeffs(dim int, shards []Shard, selected []int, target []byte) ([]byte, bool) {
	m := len(selected)
	augmented := make([][]byte, dim)
	for j := 0; j < dim; j++ {
		row := make([]byte, m+1)
		for k, shardIndex := range selected {
			row[k] = shards[shardIndex].Row[j]
		}
		row[m] = target[j]
		augmented[j] = row
	}
	return gaussJordan(augmented, m)
}

func buildCertificate(dim int, shards []Shard, targetIndex int, target []byte) *Certificate {
	augmented := make([][]byte, 0, len(shards)+1)
	for _, shard := range shards {
		row := make([]byte, dim+1)
		copy(row, shard.Row)
		augmented = append(augmented, row)
	}
	targetRow := make([]byte, dim+1)
	copy(targetRow, target)
	targetRow[dim] = 1
	augmented = append(augmented, targetRow)

	vector, ok := gaussJordan(augmented, dim)
	if !ok {
		return nil
	}
	return &Certificate{TargetIndex: targetIndex, Vector: vector}
}

// gaussJordan puts augmented (width numVars+1, RHS in the last column) into
// reduced row-echelon form and returns one solution with free variables set to
// zero. It reports false if the system is inconsistent.
func gaussJordan(augmented [][]byte, numVars int) ([]byte, bool) {
	width := numVars + 1
	rowForColumn := make([]int, numVars)
	for i := range rowForColumn {
		rowForColumn[i] = -1
	}
	rank := 0
	for column := 0; column < numVars && rank < len(augmented); column++ {
		pivot := -1
		for i := rank; i < len(augmented); i++ {
			if augmented[i][column] != 0 {
				pivot = i
				break
			}
		}
		if pivot < 0 {
			continue
		}
		augmented[rank], augmented[pivot] = augmented[pivot], augmented[rank]

		inverse := gfInv(augmented[rank][column])
		for j := column; j < width; j++ {
			augmented[rank][j] = mul(augmented[rank][j], inverse)
		}
		for i := 0; i < len(augmented); i++ {
			if i == rank || augmented[i][column] == 0 {
				continue
			}
			factor := augmented[i][column]
			for j := column; j < width; j++ {
				augmented[i][j] ^= mul(factor, augmented[rank][j])
			}
		}
		rowForColumn[column] = rank
		rank++
	}

	for i := rank; i < len(augmented); i++ {
		allZero := true
		for j := 0; j < numVars; j++ {
			if augmented[i][j] != 0 {
				allZero = false
				break
			}
		}
		if allZero && augmented[i][numVars] != 0 {
			return nil, false
		}
	}

	solution := make([]byte, numVars)
	for column, row := range rowForColumn {
		if row >= 0 {
			solution[column] = augmented[row][numVars]
		}
	}
	return solution, true
}
