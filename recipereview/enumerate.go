package recipereview

import (
	"bytes"
	"sort"
)

// fieldValues enumerates every bounded string of the field's alphabet in
// ascending (length, raw byte) order. For small review schemas this is the
// finite domain that full enumeration quantifies over.
func fieldValues(f *Field) [][]byte {
	symbols := f.Alphabet.sortedBytes()
	out := make([][]byte, 0)
	for length := f.MinLen; length <= f.MaxLen; length++ {
		out = append(out, stringsOfLength(symbols, length)...)
	}
	return out
}

// stringsOfLength returns all length-n strings over symbols in lex order.
func stringsOfLength(symbols []byte, n int) [][]byte {
	if n == 0 {
		return [][]byte{{}}
	}
	shorter := stringsOfLength(symbols, n-1)
	out := make([][]byte, 0, len(shorter)*len(symbols))
	for _, prefix := range shorter {
		for _, c := range symbols {
			v := make([]byte, 0, n)
			v = append(v, prefix...)
			v = append(v, c)
			out = append(out, v)
		}
	}
	return out
}

// assignmentTotalLen is the sum of all field value lengths.
func assignmentTotalLen(r *Recipe, in Assignment) int {
	total := 0
	for name := range r.Fields {
		total += len(in[name])
	}
	return total
}

// enumerateAssignments yields every valid assignment of the recipe in
// ascending (total length, raw canonical byte) order, charging each emitted
// assignment against the shared state budget.
func enumerateAssignments(r *Recipe, budget *Budget) ([]Assignment, bool) {
	names := r.sortedFieldNames()
	domains := make([][][]byte, len(names))
	var domainSize int64 = 1
	for i, name := range names {
		domains[i] = fieldValues(r.Fields[name])
		domainSize *= int64(len(domains[i]))
	}
	if !budget.charge(domainSize) {
		return nil, false
	}
	if domainSize > int64(maxMaterializedAssignments) {
		budget.exhausted = true
		return nil, false
	}
	out := make([]Assignment, 0, int(domainSize))
	indices := make([]int, len(names))
	for {
		in := make(Assignment, len(names))
		for i, name := range names {
			in[name] = append([]byte(nil), domains[i][indices[i]]...)
		}
		out = append(out, in)
		carry := len(indices) - 1
		for carry >= 0 {
			indices[carry]++
			if indices[carry] < len(domains[carry]) {
				break
			}
			indices[carry] = 0
			carry--
		}
		if carry < 0 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return compareAssignments(r, out[i], out[j]) < 0
	})
	return out, true
}

// compareAssignments orders assignments by total length first, then by the
// concatenation of field bytes in sorted field-name order.
func compareAssignments(r *Recipe, a, b Assignment) int {
	la, lb := assignmentTotalLen(r, a), assignmentTotalLen(r, b)
	if la != lb {
		if la < lb {
			return -1
		}
		return 1
	}
	names := r.sortedFieldNames()
	for _, name := range names {
		if c := bytes.Compare(a[name], b[name]); c != 0 {
			return c
		}
	}
	return 0
}

// pairKey is the global conflict tie-breaker: total parameter length of the
// pair, then recipe number of each side, then raw bytes in sorted field order.
type pairKey struct {
	totalLen     int
	firstRecipe  int
	secondRecipe int
	firstBytes   []byte
	secondBytes  []byte
}

func makePairKey(r1 *Recipe, a Assignment, r2 *Recipe, b Assignment) pairKey {
	return pairKey{
		totalLen:     assignmentTotalLen(r1, a) + assignmentTotalLen(r2, b),
		firstRecipe:  r1.ID,
		secondRecipe: r2.ID,
		firstBytes:   canonicalBytes(r1, a),
		secondBytes:  canonicalBytes(r2, b),
	}
}

func canonicalBytes(r *Recipe, in Assignment) []byte {
	var buf bytes.Buffer
	for _, name := range r.sortedFieldNames() {
		buf.Write(in[name])
	}
	return buf.Bytes()
}

func comparePairKey(x, y pairKey) int {
	switch {
	case x.totalLen != y.totalLen:
		if x.totalLen < y.totalLen {
			return -1
		}
		return 1
	case x.firstRecipe != y.firstRecipe:
		if x.firstRecipe < y.firstRecipe {
			return -1
		}
		return 1
	case x.secondRecipe != y.secondRecipe:
		if x.secondRecipe < y.secondRecipe {
			return -1
		}
		return 1
	}
	if c := bytes.Compare(x.firstBytes, y.firstBytes); c != 0 {
		return c
	}
	return bytes.Compare(x.secondBytes, y.secondBytes)
}

// Budget bounds the search state the auditor may spend. Once exhausted the
// review returns Unknown instead of silently passing an unsettled property.
type Budget struct {
	remaining int64
	exhausted bool
}

// NewBudget creates a state budget; non-positive values select the default.
func NewBudget(states int64) *Budget {
	if states <= 0 {
		states = defaultBudgetStates
	}
	return &Budget{remaining: states}
}

// DefaultBudgetStates is the release-gate default state allowance.
const DefaultBudgetStates int64 = 2_000_000

const defaultBudgetStates int64 = DefaultBudgetStates

// maxMaterializedAssignments bounds the assignments materialized at once so a
// large finite domain fails closed as Unknown instead of exhausting memory.
const maxMaterializedAssignments = 5_000_000

func (b *Budget) charge(n int64) bool {
	if b.exhausted {
		return false
	}
	b.remaining -= int64(n)
	if b.remaining < 0 {
		b.exhausted = true
		return false
	}
	return true
}

// Exhausted reports whether the budget ran out.
func (b *Budget) Exhausted() bool { return b.exhausted }
