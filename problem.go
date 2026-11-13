package netsec

import (
	"bytes"
	"math/bits"
)

type problem struct {
	records   []Record
	n         int
	keys      [][][]byte
	starts    []int
	ends      []int
	neighbors [][]int
	compat    [][][]uint64
}

func newProblem(records []Record) *problem {
	p := &problem{
		records:   records,
		n:         len(records),
		keys:      make([][][]byte, len(records)),
		starts:    make([]int, len(records)),
		ends:      make([]int, len(records)),
		neighbors: make([][]int, len(records)),
		compat:    make([][][]uint64, len(records)),
	}
	for i, record := range records {
		p.starts[i] = record.Offset
		p.ends[i] = record.Offset + len(record.Ciphertext)
		p.keys[i] = make([][]byte, len(record.Candidates))
		for c, candidate := range record.Candidates {
			key := make([]byte, len(candidate))
			for t := range candidate {
				key[t] = record.Ciphertext[t] ^ candidate[t]
			}
			p.keys[i][c] = key
		}
	}
	for i := 0; i < p.n; i++ {
		p.compat[i] = make([][]uint64, p.n)
		for j := 0; j < p.n; j++ {
			if i == j || !p.overlap(i, j) {
				continue
			}
			p.compat[i][j] = make([]uint64, len(records[i].Candidates))
			for a := range p.compat[i][j] {
				p.compat[i][j][a] = p.compatibleMask(i, a, j)
			}
		}
	}
	for i := 0; i < p.n; i++ {
		for j := i + 1; j < p.n; j++ {
			if p.overlap(i, j) {
				p.neighbors[i] = append(p.neighbors[i], j)
				p.neighbors[j] = append(p.neighbors[j], i)
			}
		}
	}
	return p
}

func (p *problem) overlap(i, j int) bool {
	return p.starts[i] < p.ends[j] && p.starts[j] < p.ends[i]
}

func (p *problem) compatibleMask(i, a, j int) uint64 {
	lo := max(p.starts[i], p.starts[j])
	hi := min(p.ends[i], p.ends[j])
	left := p.keys[i][a][lo-p.starts[i] : hi-p.starts[i]]
	mask := uint64(0)
	for b, key := range p.keys[j] {
		right := key[lo-p.starts[j] : hi-p.starts[j]]
		if bytes.Equal(left, right) {
			mask |= uint64(1) << uint(b)
		}
	}
	return mask
}

func candidateMask(count int) uint64 {
	if count >= 64 {
		return ^uint64(0)
	}
	if count <= 0 {
		return 0
	}
	return (uint64(1) << uint(count)) - 1
}

func (p *problem) allDomains() []uint64 {
	domains := make([]uint64, p.n)
	for i, record := range p.records {
		domains[i] = candidateMask(len(record.Candidates))
	}
	return domains
}

func (p *problem) domainsFromStates(states [][]CandidateState) []uint64 {
	domains := make([]uint64, p.n)
	for i := range p.records {
		for c, state := range states[i] {
			if state == CandidateFeasible {
				domains[i] |= uint64(1) << uint(c)
			}
		}
	}
	return domains
}

func cloneDomains(domains []uint64) []uint64 {
	out := make([]uint64, len(domains))
	copy(out, domains)
	return out
}

func activeCount(active uint16) int {
	return bits.OnesCount16(active)
}

func activeIndices(active uint16, n int) []int {
	out := make([]int, 0, activeCount(active))
	for i := 0; i < n; i++ {
		if active&(uint16(1)<<uint(i)) != 0 {
			out = append(out, i)
		}
	}
	return out
}

type searchBudget struct {
	limit     uint64
	used      uint64
	unlimited bool
	exhausted bool
}

func newSearchBudget(limit uint64) *searchBudget {
	return &searchBudget{limit: limit, unlimited: limit == UnlimitedSearchNodes}
}

func (b *searchBudget) spend(n uint64) bool {
	if b.exhausted {
		return false
	}
	if !b.unlimited {
		if b.used > b.limit || n > b.limit-b.used {
			b.exhausted = true
			return false
		}
	}
	b.used += n
	return true
}
