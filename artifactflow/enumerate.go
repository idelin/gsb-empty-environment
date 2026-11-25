package artifactflow

import (
	"fmt"
	"sort"
)

// realization is one fully bound candidate: every input and output slot is
// mapped to a distinct receipt consistent with paths, digests, roles and
// rename-rule capture bindings.
type realization struct {
	stepID    string
	candidate string
	inputs    map[string]int
	outputs   map[string]int
	receipts  []int // sorted unique receipt indices used by this realization
	dedupKey  string
}

// enumeration holds everything the search needs once all realizations and
// lookup indices have been built.
type enumeration struct {
	indexed  *solved
	all      []realization
	producer map[int][]int // receipt index -> realization indices that produce it
	rootByID map[int]int   // receipt index -> root receipt index (itself) for root-capable receipts
	byStep   [][]int       // step ordinal -> realization indices
}

// enumerate builds all realizations for every candidate.
func enumerate(s *solved) (out *enumeration, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			if bp, ok := rec.(*budgetPanic); ok {
				out = nil
				err = bp.err
				return
			}
			panic(rec)
		}
	}()
	e := &enumeration{
		indexed:  s,
		producer: make(map[int][]int),
		rootByID: make(map[int]int),
		byStep:   make([][]int, len(s.steps)),
	}
	for ri, ir := range s.receipts {
		if ir.receipt.IsRoot() {
			e.rootByID[ri] = ri
		}
	}

	for stepOrd, si := range s.steps {
		seen := make(map[string]bool)
		for candOrd := range si.candidates {
			ci := &si.candidates[candOrd]
			bound, err := enumerateCandidate(s, ci)
			if err != nil {
				return nil, err
			}
			for _, r := range bound {
				if seen[r.dedupKey] {
					continue
				}
				seen[r.dedupKey] = true
				idx := len(e.all)
				e.all = append(e.all, r)
				e.byStep[stepOrd] = append(e.byStep[stepOrd], idx)
				for _, recIdx := range r.outputs {
					e.producer[recIdx] = append(e.producer[recIdx], idx)
				}
			}
		}
	}
	return e, nil
}

// enumerateCandidate enumerates all distinct slot bindings of one candidate.
func enumerateCandidate(s *solved, ci *candidateIndex) ([]realization, error) {
	inputMatches := make([][]int, len(ci.inputs))
	for i, si := range ci.inputs {
		inputMatches[i] = compatibleReceipts(s, ci, si)
		if len(inputMatches[i]) == 0 {
			return nil, nil
		}
	}
	outputMatches := make([][]int, len(ci.outputs))
	for i, so := range ci.outputs {
		outputMatches[i] = compatibleReceipts(s, ci, so)
		if len(outputMatches[i]) == 0 {
			return nil, nil
		}
	}

	en := &candidateEnumerator{
		s:             s,
		ci:            ci,
		inputMatches:  inputMatches,
		outputMatches: outputMatches,
		used:          make(map[int]bool),
		boundInputs:   make(map[string]int, len(ci.inputs)),
		boundOutputs:  make(map[string]int, len(ci.outputs)),
	}
	if err := en.bindInputs(0, nil); err != nil {
		return nil, err
	}
	return en.results, nil
}

type candidateEnumerator struct {
	s             *solved
	ci            *candidateIndex
	inputMatches  [][]int
	outputMatches [][]int
	used          map[int]bool
	boundInputs   map[string]int
	boundOutputs  map[string]int
	results       []realization
}

func (en *candidateEnumerator) bindInputs(i int, captures map[string]string) error {
	if i == len(en.ci.inputs) {
		return en.bindOutputs(0, captures)
	}
	si := en.ci.inputs[i]
	for _, recIdx := range en.inputMatches[i] {
		if en.used[recIdx] {
			continue
		}
		bindings, ok := si.pattern.match(en.s.receipts[recIdx].path, captures)
		if !ok {
			continue
		}
		en.used[recIdx] = true
		en.boundInputs[si.slot.Name] = recIdx
		if err := en.bindInputs(i+1, bindings); err != nil {
			return err
		}
		delete(en.used, recIdx)
		delete(en.boundInputs, si.slot.Name)
	}
	return nil
}

func (en *candidateEnumerator) bindOutputs(i int, captures map[string]string) error {
	if i == len(en.ci.outputs) {
		en.emit()
		return nil
	}
	so := en.ci.outputs[i]
	rule := en.ci.ruleByOutput[so.slot.Name]
	for _, recIdx := range en.outputMatches[i] {
		if en.used[recIdx] {
			continue
		}
		bindings, ok := en.outputBinding(so, rule, recIdx, captures)
		if !ok {
			continue
		}
		en.used[recIdx] = true
		en.boundOutputs[so.slot.Name] = recIdx
		if err := en.bindOutputs(i+1, bindings); err != nil {
			return err
		}
		delete(en.used, recIdx)
		delete(en.boundOutputs, so.slot.Name)
	}
	return nil
}

// outputBinding checks the rename rule of an output slot and returns the
// merged capture bindings when the candidate receipt fits.
func (en *candidateEnumerator) outputBinding(so slotIndex, rule *ruleIndex, recIdx int, captures map[string]string) (map[string]string, bool) {
	ir := en.s.receipts[recIdx]
	if rule == nil {
		return so.pattern.match(ir.path, captures)
	}
	inputRec, ok := en.boundInputs[rule.inputName]
	if !ok {
		return nil, false
	}
	inputSlot := en.ci.inputSlot(rule.inputName)
	bindings, ok := inputSlot.pattern.match(en.s.receipts[inputRec].path, captures)
	if !ok {
		return nil, false
	}
	target := so.pattern
	if rule.target != nil {
		target = rule.target
	}
	return target.match(ir.path, bindings)
}

func (en *candidateEnumerator) emit() {
	all := make(map[int]bool, len(en.boundInputs)+len(en.boundOutputs))
	for _, idx := range en.boundInputs {
		all[idx] = true
	}
	for _, idx := range en.boundOutputs {
		all[idx] = true
	}
	receipts := make([]int, 0, len(all))
	for idx := range all {
		receipts = append(receipts, idx)
	}
	sort.Ints(receipts)

	inputs := make(map[string]int, len(en.boundInputs))
	for k, v := range en.boundInputs {
		inputs[k] = v
	}
	outputs := make(map[string]int, len(en.boundOutputs))
	for k, v := range en.boundOutputs {
		outputs[k] = v
	}
	r := realization{
		stepID:    en.ci.stepID,
		candidate: en.ci.candidate.Name,
		inputs:    inputs,
		outputs:   outputs,
		receipts:  receipts,
	}
	r.dedupKey = r.key()
	en.results = append(en.results, r)
	if len(en.results) > maxRealizationsPerCandidate {
		panic(&budgetPanic{err: &ValidationError{
			Field:  "candidate",
			Reason: fmt.Sprintf("candidate %s/%s has more than %d distinct bindings", en.ci.stepID, en.ci.candidate.Name, maxRealizationsPerCandidate),
		}})
	}
}

func (ci *candidateIndex) inputSlot(name string) slotIndex {
	for _, si := range ci.inputs {
		if si.slot.Name == name {
			return si
		}
	}
	return slotIndex{}
}

// compatibleReceipts returns receipts matching a slot on content digest and
// signer role, and for output slots enforces that the receipt's signed StepID
// is exactly the candidate's step, so a receipt produced by another step can
// never be bound as this output. Path matching happens later because capture
// bindings are global to a candidate binding.
func compatibleReceipts(s *solved, ci *candidateIndex, si slotIndex) []int {
	var out []int
	for i, ir := range s.receipts {
		if ir.normDigest != si.digest || !signerAllowed(si.slot, ir.receipt.Signer) {
			continue
		}
		output := isOutputSlot(ci, si)
		switch {
		case output && ir.receipt.IsProduced() && ir.receipt.StepID == ci.stepID:
			out = append(out, i)
		case !output && !ir.receipt.IsProduced():
			// Root/import receipts feed input slots only.
			out = append(out, i)
		case !output && ir.receipt.IsProduced():
			// A step output feeding another step's input is allowed: it is
			// an input here regardless of its own producing step.
			out = append(out, i)
		}
	}
	return out
}

func isOutputSlot(ci *candidateIndex, target slotIndex) bool {
	for _, so := range ci.outputs {
		if so.slot.Name == target.slot.Name {
			return true
		}
	}
	return false
}

// key renders a stable identity for a binding, used to deduplicate.
func (r realization) key() string {
	parts := []string{r.stepID, r.candidate}
	keys := make([]string, 0, len(r.inputs)+len(r.outputs))
	for slot, rec := range r.inputs {
		keys = append(keys, "i:"+slot+"="+itoaInt(rec))
	}
	sort.Strings(keys)
	parts = append(parts, keys...)
	keys = keys[:0]
	for slot, rec := range r.outputs {
		keys = append(keys, "o:"+slot+"="+itoaInt(rec))
	}
	sort.Strings(keys)
	parts = append(parts, keys...)
	return joinStrings(parts, "|")
}

func itoaInt(n int) string {
	return itoa(int64(n))
}

func joinStrings(parts []string, sep string) string {
	total := 0
	for _, p := range parts {
		total += len(p) + len(sep)
	}
	buf := make([]byte, 0, total)
	for i, p := range parts {
		if i > 0 {
			buf = append(buf, sep...)
		}
		buf = append(buf, p...)
	}
	return string(buf)
}

// budgetPanic lets the deep enumerator abort with a validation error without
// threading errors through every recursion.
type budgetPanic struct {
	err error
}
