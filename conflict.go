package netsec

import "sort"

func (p *problem) findMinimalConflict(budget *searchBudget) (*Conflict, bool, bool) {
	if p.n == 0 {
		return nil, true, false
	}
	order := sortedRecordOrder(p.records)
	for size := 1; size <= p.n; size++ {
		combinations := combinationsOfSize(p.n, size)
		sort.Slice(combinations, func(i, j int) bool {
			return lessIntSlice(combinations[i], combinations[j])
		})
		for _, positions := range combinations {
			active := uint16(0)
			for _, position := range positions {
				active |= uint16(1) << uint(order[position])
			}
			_, found, exhausted := p.firstSolution(active, p.allDomains(), budget)
			if exhausted {
				return nil, false, true
			}
			if found {
				continue
			}
			conflict, complete, exhausted := p.buildConflict(positions, order, budget)
			return conflict, complete, exhausted
		}
	}
	// The whole record set is known to be unsatisfiable before this search.
	return nil, true, false
}

func (p *problem) buildConflict(positions, order []int, budget *searchBudget) (*Conflict, bool, bool) {
	conflict := &Conflict{
		RecordIndices: make([]int, 0, len(positions)),
		RecordIDs:     make([]string, 0, len(positions)),
	}
	for _, position := range positions {
		index := order[position]
		conflict.RecordIndices = append(conflict.RecordIndices, index)
		conflict.RecordIDs = append(conflict.RecordIDs, p.records[index].ID)
	}
	for _, removed := range conflict.RecordIndices {
		subsetActive := uint16(0)
		allActive := uint16(0)
		allIndices := uint16(0)
		for i := range p.records {
			allIndices |= uint16(1) << uint(i)
		}
		for _, index := range conflict.RecordIndices {
			if index != removed {
				subsetActive |= uint16(1) << uint(index)
			}
		}
		allActive = allIndices &^ (uint16(1) << uint(removed))
		witnesses, exhausted := p.lexWitnesses(subsetActive, p.allDomains(), 1, budget)
		if exhausted {
			return conflict, false, true
		}
		if len(witnesses) == 0 {
			return conflict, false, false
		}
		witness := witnesses[0]
		deletion := p.makeDeletionWitness(removed, witness)
		conflict.Deletions = append(conflict.Deletions, deletion)
		fullWitnesses, exhausted := p.lexWitnesses(allActive, p.allDomains(), 1, budget)
		if exhausted {
			return conflict, false, true
		}
		if len(fullWitnesses) > 0 {
			conflict.Deletions[len(conflict.Deletions)-1] = p.makeDeletionWitness(removed, fullWitnesses[0])
		}
	}
	return conflict, true, false
}

func (p *problem) makeDeletionWitness(removed int, witness []int) DeletionWitness {
	deletion := DeletionWitness{
		RemovedID:    p.records[removed].ID,
		RemovedIndex: removed,
		FullIndices:  witness,
	}
	for index, candidate := range witness {
		if candidate < 0 {
			continue
		}
		deletion.RecordIndices = append(deletion.RecordIndices, index)
		deletion.RecordIDs = append(deletion.RecordIDs, p.records[index].ID)
		deletion.CandidateIndices = append(deletion.CandidateIndices, candidate)
	}
	deletion.CoversAllInput = len(deletion.RecordIndices) == p.n-1
	return deletion
}

func combinationsOfSize(n, size int) [][]int {
	if size < 0 || size > n {
		return nil
	}
	var combinations [][]int
	current := make([]int, 0, size)
	var visit func(start int)
	visit = func(start int) {
		if len(current) == size {
			combination := make([]int, len(current))
			copy(combination, current)
			combinations = append(combinations, combination)
			return
		}
		for i := start; i <= n-(size-len(current)); i++ {
			current = append(current, i)
			visit(i + 1)
			current = current[:len(current)-1]
		}
	}
	visit(0)
	return combinations
}

func lessIntSlice(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}
