package writeproof

import (
	"fmt"
	"math/bits"
	"sort"
)

type budgetError struct{}

func (budgetError) Error() string { return "writeproof: proof budget exhausted" }

type gaussian struct {
	width int
	rows  []linearEquation
	pivot []int
	budget *Budget
}

type linearEquation struct {
	terms    []uint64
	constant bool
}

func parity(v uint64) int {
	v ^= v >> 32
	v ^= v >> 16
	v ^= v >> 8
	v ^= v >> 4
	v ^= v >> 2
	v ^= v >> 1
	return int(v & 1)
}

func bitWord(bit int) int { return bit / 64 }

func bitMask(bit int) uint64 { return uint64(1) << uint(bit%64) }

func hasBit(words []uint64, bit int) bool {
	return bit/64 < len(words) && words[bit/64]&bitMask(bit) != 0
}

func setBit(words []uint64, bit int) {
	words[bit/64] |= bitMask(bit)
}

func cloneWords(words []uint64) []uint64 {
	out := make([]uint64, len(words))
	copy(out, words)
	return out
}

func xorWords(dst, src []uint64) {
	for i := range dst {
		dst[i] ^= src[i]
	}
}

func equationIsZero(e linearEquation) bool {
	for _, word := range e.terms {
		if word != 0 {
			return false
		}
	}
	return true
}

func equationCopy(e linearEquation) linearEquation {
	return linearEquation{terms: cloneWords(e.terms), constant: e.constant}
}

func publicEquation(e linearEquation) LinearEquation {
	return LinearEquation{Terms: cloneWords(e.terms), Constant: e.constant}
}

func publicSystem(variables int, rows []linearEquation) LinearSystem {
	out := LinearSystem{Variables: variables, Equations: make([]LinearEquation, 0, len(rows))}
	for _, row := range rows {
		out.Equations = append(out.Equations, publicEquation(row))
	}
	return out
}

func newGaussian(variables int, budget *Budget) *gaussian {
	return &gaussian{
		width:  variables,
		rows:   make([]linearEquation, 0, variables),
		pivot:  make([]int, 0, variables),
		budget: budget,
	}
}

func (g *gaussian) charge() error {
	if g.budget == nil {
		return nil
	}
	if g.budget.Unlimited {
		return nil
	}
	if g.budget.UsedSystemOps >= g.budget.MaxSystemOps {
		return budgetError{}
	}
	g.budget.UsedSystemOps++
	return nil
}

func (g *gaussian) add(row linearEquation) error {
	if len(row.terms) != (g.width+63)/64 {
		return fmt.Errorf("writeproof: internal equation width mismatch")
	}
	if leadingBit(row.terms) >= g.width {
		return fmt.Errorf("writeproof: equation references variable outside system")
	}
	current := equationCopy(row)
	for {
		if equationIsZero(current) {
			if current.constant {
				return fmt.Errorf("writeproof: contradiction row")
			}
			return nil
		}
		if err := g.charge(); err != nil {
			return err
		}
		pivotBit := leadingBit(current.terms)
		match := -1
		for i, pivot := range g.pivot {
			if pivot == pivotBit {
				match = i
				break
			}
		}
		if match < 0 {
			g.rows = append(g.rows, current)
			g.pivot = append(g.pivot, pivotBit)
			return nil
		}
		xorWords(current.terms, g.rows[match].terms)
		current.constant = current.constant != g.rows[match].constant
	}
}

func leadingBit(words []uint64) int {
	for i := len(words) - 1; i >= 0; i-- {
		if words[i] != 0 {
			return i*64 + 63 - leadingZero64(words[i])
		}
	}
	return -1
}

func leadingZero64(v uint64) int {
	return bits.LeadingZeros64(v)
}

func solveSystem(variables int, rows []linearEquation, budget *Budget) ([]bool, []int, error) {
	g := newGaussian(variables, budget)
	for _, row := range rows {
		if err := g.add(row); err != nil {
			return nil, nil, err
		}
	}
	for i := len(g.rows) - 1; i >= 0; i-- {
		pivot := g.pivot[i]
		for j := 0; j < i; j++ {
			if hasBit(g.rows[j].terms, pivot) {
				xorWords(g.rows[j].terms, g.rows[i].terms)
				g.rows[j].constant = g.rows[j].constant != g.rows[i].constant
			}
		}
	}
	order := make([]int, len(g.pivot))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return g.pivot[order[i]] > g.pivot[order[j]] })
	sortedRows := make([]linearEquation, len(g.rows))
	sortedPivots := make([]int, len(g.pivot))
	for i, index := range order {
		sortedRows[i] = g.rows[index]
		sortedPivots[i] = g.pivot[index]
	}
	g.rows = sortedRows
	g.pivot = sortedPivots
	values := make([]bool, variables)
	for i := 0; i < len(g.rows); i++ {
		values[g.pivot[i]] = g.rows[i].constant
	}
	return values, g.pivot, nil
}

func bitEquation(variables, bit int, constant bool) linearEquation {
	terms := make([]uint64, (variables+63)/64)
	setBit(terms, bit)
	return linearEquation{terms: terms, constant: constant}
}

func affineBitTerms(variables int, coefficient uint64, varOffset int, constant bool) linearEquation {
	terms := make([]uint64, (variables+63)/64)
	for bit := 0; bit < 64; bit++ {
		if coefficient&(uint64(1)<<uint(bit)) != 0 {
			setBit(terms, varOffset+bit)
		}
	}
	return linearEquation{terms: terms, constant: constant}
}

func outputBitEquation(variables int, coefficient uint64, varOffset int, biasBit int, target int) linearEquation {
	row := affineBitTerms(variables, coefficient, varOffset, biasBit == 1)
	if target == 1 {
		row.constant = !row.constant
	}
	return row
}

func equalityBitEquation(variables int, coefficient uint64, varOffset, biasBit, targetBit int) linearEquation {
	row := affineBitTerms(variables, coefficient, varOffset, biasBit == 1)
	if targetBit == 1 {
		row.constant = !row.constant
	}
	return row
}

func minimizeAffine(
	variables int,
	baseRows []linearEquation,
	coefficients []uint64,
	varOffset int,
	bias uint64,
	bits int,
	budget *Budget,
) (uint64, []bool, []linearEquation, error) {
	rows := make([]linearEquation, 0, len(baseRows)+bits)
	rows = append(rows, baseRows...)
	var value uint64
	for bit := bits - 1; bit >= 0; bit-- {
		var coefficient uint64
		if bit < len(coefficients) {
			coefficient = coefficients[bit]
		}
		row := affineBitTerms(variables, coefficient, varOffset, int((bias>>uint(bit))&1) == 1)
		candidate := append(append([]linearEquation{}, rows...), row)
		_, _, err := solveSystem(variables, candidate, budget)
		if err == nil {
			rows = candidate
			continue
		}
		if _, ok := err.(budgetError); ok {
			return 0, nil, nil, err
		}
		row.constant = !row.constant
		rows = append(rows, row)
		value |= uint64(1) << uint(bit)
	}
	values, _, err := solveSystem(variables, rows, budget)
	if err != nil {
		return 0, nil, nil, err
	}
	return value, values, rows, nil
}

func minimizeVariables(
	variables int,
	baseRows []linearEquation,
	firstBit int,
	count int,
	budget *Budget,
) ([]bool, []linearEquation, error) {
	rows := make([]linearEquation, 0, len(baseRows)+count)
	rows = append(rows, baseRows...)
	for bit := firstBit + count - 1; bit >= firstBit; bit-- {
		row := bitEquation(variables, bit, false)
		candidate := append(append([]linearEquation{}, rows...), row)
		_, _, err := solveSystem(variables, candidate, budget)
		if err == nil {
			rows = candidate
			continue
		}
		if _, ok := err.(budgetError); ok {
			return nil, nil, err
		}
		row.constant = true
		rows = append(rows, row)
	}
	values, _, err := solveSystem(variables, rows, budget)
	if err != nil {
		return nil, nil, err
	}
	return values, rows, nil
}
