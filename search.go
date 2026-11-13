package netsec

import (
	"math/bits"
)

type recordPair struct {
	left  int
	right int
}

func (p *problem) ac3(active uint16, domains []uint64, budget *searchBudget) (bool, bool) {
	queue := make([]recordPair, 0, p.n*p.n)
	for i := 0; i < p.n; i++ {
		if active&(uint16(1)<<uint(i)) == 0 {
			continue
		}
		for _, j := range p.neighbors[i] {
			if active&(uint16(1)<<uint(j)) != 0 {
				queue = append(queue, recordPair{left: i, right: j})
			}
		}
	}
	return p.ac3WithQueue(active, domains, queue, budget)
}

func (p *problem) ac3WithQueue(active uint16, domains []uint64, queue []recordPair, budget *searchBudget) (bool, bool) {
	for i := 0; i < p.n; i++ {
		if active&(uint16(1)<<uint(i)) != 0 && domains[i] == 0 {
			return false, false
		}
	}
	for head := 0; head < len(queue); head++ {
		pair := queue[head]
		changed, consistent, exhausted := p.revise(pair.left, pair.right, domains, budget)
		if exhausted {
			return false, true
		}
		if !consistent {
			return false, false
		}
		if !changed {
			continue
		}
		for _, k := range p.neighbors[pair.left] {
			if k != pair.right && active&(uint16(1)<<uint(k)) != 0 {
				queue = append(queue, recordPair{left: k, right: pair.left})
			}
		}
	}
	return true, false
}

func (p *problem) seedsForVariable(active uint16, variable int) []recordPair {
	queue := make([]recordPair, 0, len(p.neighbors[variable]))
	for _, neighbor := range p.neighbors[variable] {
		if active&(uint16(1)<<uint(neighbor)) != 0 {
			queue = append(queue, recordPair{left: neighbor, right: variable})
		}
	}
	return queue
}

func (p *problem) revise(i, j int, domains []uint64, budget *searchBudget) (bool, bool, bool) {
	domain := domains[i]
	other := domains[j]
	if other == 0 {
		domains[i] = 0
		return true, false, false
	}
	if bits.OnesCount64(other) < bits.OnesCount64(domain) {
		supported := uint64(0)
		for b := 0; b < len(p.compat[j][i]); b++ {
			if other&(uint64(1)<<uint(b)) == 0 {
				continue
			}
			if !budget.spend(1) {
				return false, false, true
			}
			supported |= p.compat[j][i][b]
		}
		nextDomain := domain & supported
		if nextDomain == domain {
			return false, true, false
		}
		domains[i] = nextDomain
		return true, nextDomain != 0, false
	}
	changed := false
	for a := 0; a < len(p.compat[i][j]); a++ {
		if domain&(uint64(1)<<uint(a)) == 0 {
			continue
		}
		if !budget.spend(1) {
			return false, false, true
		}
		if p.compat[i][j][a]&other == 0 {
			domain &^= uint64(1) << uint(a)
			changed = true
		}
	}
	if !changed {
		return false, true, false
	}
	domains[i] = domain
	if domain == 0 {
		return true, false, false
	}
	return true, true, false
}

func (p *problem) firstSolution(active uint16, initial []uint64, budget *searchBudget) ([]int, bool, bool) {
	domains := cloneDomains(initial)
	consistent, exhausted := p.ac3(active, domains, budget)
	if exhausted || !consistent {
		return nil, false, exhausted
	}
	return p.firstSolutionFromConsistent(active, domains, budget)
}

func (p *problem) firstSolutionFixed(active uint16, initial []uint64, variable, candidate int, budget *searchBudget) ([]int, bool, bool) {
	domains := cloneDomains(initial)
	domains[variable] = uint64(1) << uint(candidate)
	consistent, exhausted := p.ac3WithQueue(active, domains, p.seedsForVariable(active, variable), budget)
	if exhausted || !consistent {
		return nil, false, exhausted
	}
	return p.firstSolutionFromConsistent(active, domains, budget)
}

func (p *problem) firstSolutionFromConsistent(active uint16, domains []uint64, budget *searchBudget) ([]int, bool, bool) {
	solution := make([]int, p.n)
	for i := range solution {
		solution[i] = -1
	}
	if p.assignFirst(active, domains, solution, budget) {
		return solution, true, false
	}
	return nil, false, budget.exhausted
}

func (p *problem) assignFirst(active uint16, domains []uint64, solution []int, budget *searchBudget) bool {
	if budget.exhausted {
		return false
	}
	variable := -1
	variableCount := 65
	variableDegree := -1
	for i := 0; i < p.n; i++ {
		if active&(uint16(1)<<uint(i)) == 0 {
			continue
		}
		count := bits.OnesCount64(domains[i])
		if count == 0 {
			return false
		}
		degree := 0
		for _, j := range p.neighbors[i] {
			if active&(uint16(1)<<uint(j)) != 0 {
				degree++
			}
		}
		if count > 1 && (count < variableCount || (count == variableCount && degree > variableDegree)) {
			variable = i
			variableCount = count
			variableDegree = degree
		}
	}
	if variable < 0 {
		for i := 0; i < p.n; i++ {
			if active&(uint16(1)<<uint(i)) != 0 {
				solution[i] = bits.TrailingZeros64(domains[i])
			}
		}
		return true
	}
	for c := 0; c < 64; c++ {
		bit := uint64(1) << uint(c)
		if domains[variable]&bit == 0 {
			continue
		}
		if !budget.spend(1) {
			return false
		}
		next := cloneDomains(domains)
		next[variable] = bit
		consistent, exhausted := p.ac3WithQueue(active, next, p.seedsForVariable(active, variable), budget)
		if exhausted {
			return false
		}
		if !consistent {
			continue
		}
		solution[variable] = c
		if p.assignFirst(active, next, solution, budget) {
			return true
		}
		solution[variable] = -1
	}
	return false
}

func (p *problem) lexWitnesses(active uint16, initial []uint64, limit int, budget *searchBudget) ([][]int, bool) {
	domains := cloneDomains(initial)
	result := make([][]int, 0, limit)
	consistent, exhausted := p.ac3(active, domains, budget)
	if exhausted || !consistent {
		return result, budget.exhausted
	}
	p.walkLex(active, domains, limit, &result, budget)
	return result, budget.exhausted
}

func (p *problem) walkLex(active uint16, domains []uint64, limit int, result *[][]int, budget *searchBudget) bool {
	if budget.exhausted || len(*result) >= limit {
		return true
	}
	variable := -1
	for i := 0; i < p.n; i++ {
		if active&(uint16(1)<<uint(i)) == 0 {
			continue
		}
		count := bits.OnesCount64(domains[i])
		if count == 0 {
			return false
		}
		if count > 1 {
			variable = i
			break
		}
	}
	if variable < 0 {
		witness := make([]int, p.n)
		for i := 0; i < p.n; i++ {
			witness[i] = -1
			if active&(uint16(1)<<uint(i)) != 0 {
				witness[i] = bits.TrailingZeros64(domains[i])
			}
		}
		*result = append(*result, witness)
		return false
	}
	for c := 0; c < 64; c++ {
		bit := uint64(1) << uint(c)
		if domains[variable]&bit == 0 {
			continue
		}
		if !budget.spend(1) {
			return true
		}
		next := cloneDomains(domains)
		next[variable] = bit
		consistent, exhausted := p.ac3WithQueue(active, next, p.seedsForVariable(active, variable), budget)
		if exhausted {
			return true
		}
		if !consistent {
			continue
		}
		if p.walkLex(active, next, limit, result, budget) {
			return true
		}
	}
	return false
}
