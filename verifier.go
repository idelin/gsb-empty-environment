package netsec

import "fmt"

// DefaultBudget caps the number of concrete executions when the caller does
// not supply an explicit budget. It is chosen so a verification stays fast
// while still covering the whole MaxInputBytes-sized domain when every value
// is explored (2^48 is far above it, hence the Unknown verdict path).
const DefaultBudget = 200_000

// Verdict classifies a verification result.
type Verdict uint8

const (
	// Unknown means the budget was exhausted before the secret domain was
	// fully explored and no leak was observed.
	Unknown Verdict = iota
	// SecretIndependent means every explored secret assignment produced the
	// same externally observable behavior.
	SecretIndependent
	// LeakFound means two secret assignments under identical public inputs
	// produced different observations.
	LeakFound
)

func (v Verdict) String() string {
	switch v {
	case SecretIndependent:
		return "SecretIndependent"
	case LeakFound:
		return "LeakFound"
	default:
		return "Unknown"
	}
}

// Budget limits a verification in executions. MaxRuns <= 0 means
// DefaultBudget. Runs counts how many concrete executions were performed.
type Budget struct {
	MaxRuns uint64
	Runs    uint64
}

// Counterexample carries the two replayable state sequences that demonstrate
// secret-dependent behavior. Both runs share the same public input and differ
// only in their secret assignments.
type Counterexample struct {
	Public       Input
	SecretA      Input
	SecretB      Input
	ObservationA Observation
	ObservationB Observation
	TraceA       *Trace
	TraceB       *Trace
}

// Result is the verifier output.
type Result struct {
	Verdict        Verdict
	Budget         Budget
	DomainSize     uint64
	Explored       uint64
	Complete       bool
	Counterexample *Counterexample
}

// secretVar describes one variable that the verifier enumerates.
type secretVar struct {
	index uint8
	bits  uint8
}

// Verify performs dual-execution differential checking. Public variables are
// fixed from input while every Secret variable is enumerated over its bounded
// domain (at most MaxInputBytes combined). It compares the return value,
// typed exception (including faulting pc) and ordered host-call sequence
// across runs. When the budget runs out before the whole domain is explored
// the verdict is Unknown; an observed divergence yields LeakFound with two
// replayable traces.
func Verify(p *Program, input Input, host Host, budget uint64) (*Result, error) {
	if err := Validate(p); err != nil {
		return nil, err
	}
	secrets, err := collectSecrets(p)
	if err != nil {
		return nil, err
	}
	if err := requireHost(p, host); err != nil {
		return nil, err
	}
	base, err := initialRegisters(p, input)
	if err != nil {
		return nil, err
	}
	if budget == 0 {
		budget = DefaultBudget
	}

	res := &Result{Budget: Budget{MaxRuns: budget}, DomainSize: domainSize(secrets)}
	anchor := (*Trace)(nil)
	anchorSecret := Input{}

	runOnce := func(secretValues []uint64) *Trace {
		regs := append([]uint64(nil), base...)
		for i, sv := range secrets {
			regs[sv.index] = secretValues[i]
		}
		res.Budget.Runs++
		res.Explored++
		return execute(p, regs, host)
	}

	consider := func(tr *Trace, secretValues []uint64) bool {
		secretInput := secretAssignment(secrets, append([]uint64(nil), secretValues...))
		if anchor == nil {
			anchor = tr
			anchorSecret = secretInput
			return false
		}
		if !observationsEqual(anchor.Observe(), tr.Observe()) {
			res.Verdict = LeakFound
			res.Counterexample = &Counterexample{
				Public:       cloneInput(input),
				SecretA:      anchorSecret,
				SecretB:      secretInput,
				ObservationA: anchor.Observe(),
				ObservationB: tr.Observe(),
				TraceA:       anchor,
				TraceB:       tr,
			}
			return true
		}
		return false
	}

	// First probe the deterministic corner combinations, which expose most
	// boundary-driven exception leaks cheaply.
	corners := cornerCombinations(secrets)
	for _, combo := range corners {
		if res.Budget.Runs >= res.Budget.MaxRuns {
			return res, nil
		}
		if consider(runOnce(combo), combo) {
			return res, nil
		}
	}

	// Then enumerate the full domain in odometer order, skipping the corner
	// combinations already evaluated.
	total := res.DomainSize
	if total <= res.Budget.MaxRuns {
		seen := cornerSet(secrets, corners)
		values := make([]uint64, len(secrets))
		for n := uint64(0); n < total; n++ {
			decode(n, secrets, values)
			if seen[keyOf(values)] {
				continue
			}
			if res.Budget.Runs >= res.Budget.MaxRuns {
				return res, nil
			}
			if consider(runOnce(values), values) {
				return res, nil
			}
		}
		res.Complete = true
		res.Verdict = SecretIndependent
		return res, nil
	}

	// Domain exceeds the budget: keep walking from zero until exhausted.
	seen := cornerSet(secrets, corners)
	values := make([]uint64, len(secrets))
	for n := uint64(0); res.Budget.Runs < res.Budget.MaxRuns; n++ {
		decode(n, secrets, values)
		if seen[keyOf(values)] {
			continue
		}
		if consider(runOnce(values), values) {
			return res, nil
		}
	}
	res.Verdict = Unknown
	return res, nil
}

func collectSecrets(p *Program) ([]secretVar, error) {
	var totalBits uint
	out := make([]secretVar, 0)
	for i, v := range p.Vars {
		if v.Visibility == Secret {
			out = append(out, secretVar{index: uint8(i), bits: v.Bits})
			totalBits += uint(v.Bits)
		}
	}
	if totalBits > 8*MaxInputBytes {
		return nil, fmt.Errorf("verify: secret input is %d bits, exceeding MaxInputBytes %d bytes", totalBits, MaxInputBytes)
	}
	return out, nil
}

func domainSize(secrets []secretVar) uint64 {
	size := uint64(1)
	for _, sv := range secrets {
		size *= 1 << sv.bits
	}
	return size
}

// cornerValues returns the deterministic boundary values for one variable:
// zero, the maximum representable value, and (for widths > 1) one.
func cornerValues(bits uint8) []uint64 {
	max := mask(bits)
	if bits == 1 {
		return []uint64{0, 1}
	}
	return []uint64{0, 1, max}
}

// cornerCombinations returns the Cartesian product of per-variable corners.
func cornerCombinations(secrets []secretVar) [][]uint64 {
	combos := [][]uint64{{}}
	for _, sv := range secrets {
		next := make([][]uint64, 0, len(combos)*3)
		for _, prefix := range combos {
			for _, val := range cornerValues(sv.bits) {
				extended := append(append([]uint64(nil), prefix...), val)
				next = append(next, extended)
			}
		}
		combos = next
	}
	return combos
}

// decode maps an odometer index n to a value per secret variable, least
// significant variable first.
func decode(n uint64, secrets []secretVar, out []uint64) {
	for i, sv := range secrets {
		out[i] = n & mask(sv.bits)
		n >>= sv.bits
	}
}

func keyOf(values []uint64) string {
	// Fixed-width encoding keeps distinct assignments unambiguous.
	b := make([]byte, 0, len(values)*8)
	for _, v := range values {
		for shift := 56; shift >= 0; shift -= 8 {
			b = append(b, byte(v>>shift))
		}
	}
	return string(b)
}

func cornerSet(secrets []secretVar, combos [][]uint64) map[string]bool {
	set := make(map[string]bool, len(combos))
	for _, combo := range combos {
		set[keyOf(combo)] = true
	}
	return set
}

func secretAssignment(secrets []secretVar, values []uint64) Input {
	out := make(Input, len(secrets))
	for i, sv := range secrets {
		out[sv.index] = values[i]
	}
	return out
}

func cloneInput(in Input) Input {
	out := make(Input, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// observationsEqual compares every externally visible channel: the fixed
// return value, the typed exception and its instruction pc, and the ordered
// host-call sequence including arguments, results and faults.
func observationsEqual(a, b Observation) bool {
	if a.Return != b.Return || a.Exc != b.Exc || a.ExcPC != b.ExcPC {
		return false
	}
	if len(a.Calls) != len(b.Calls) {
		return false
	}
	for i := range a.Calls {
		if a.Calls[i] != b.Calls[i] {
			return false
		}
	}
	return true
}
