package stlf

import (
	"fmt"
	"time"
)

// SearchStatus is the outcome category of a witness search.
type SearchStatus string

const (
	// StatusWitness means a speculative dual-input witness was found.
	StatusWitness SearchStatus = "Witness"
	// StatusSafe means no architectural and no speculative leak was found.
	StatusSafe SearchStatus = "Safe"
	// StatusNativeLeak means only an architectural leak was found.
	StatusNativeLeak SearchStatus = "NativeLeak"
	// StatusUnknown means the search timed out and must not be treated as safe.
	StatusUnknown SearchStatus = "Unknown"
)

// Alias spellings used by some hosts.
const (
	StatusNativeVulnerable = StatusNativeLeak
	StatusTimeout          = StatusUnknown
)

// WitnessCost orders witness candidates.  The default "shortest" order is:
// fewest speculative instructions, then fewest probes, then earliest
// divergence, then lexicographic public/secret order.
type WitnessCost struct {
	Steps           int
	SpecProbes      int
	ArchProbes      int
	FirstDivergence int
}

// CostMetric selects the tie-breaking order for the "shortest" witness.
type CostMetric uint8

const (
	// CostSteps minimizes speculative instructions, then probes, then
	// divergence.  It is the default.
	CostSteps CostMetric = iota
	// CostSpecProbes minimizes the number of speculative probe observations.
	CostSpecProbes
	// CostFirstDivergence minimizes the position of the first differing
	// observation.
	CostFirstDivergence
)

// Witness is a valid dual-input STLF witness.  PublicLow6 is identical in A
// and B; SecretA and SecretB differ; the architectural probe traces are equal;
// and the speculative observation traces differ.
type Witness struct {
	PublicLow6 uint8
	SecretA    uint8
	SecretB    uint8
	A          Execution
	B          Execution
	Cost       WitnessCost
	Native     bool
	// UsesPartialForward is true when at least one side of the witness
	// exercised a low-six-bit-only STLF match.
	UsesPartialForward bool
}

// SearchOptions bounds the finite input search.
type SearchOptions struct {
	// PublicValues defaults to 0..63.
	PublicValues []uint8
	// SecretValues defaults to 0..255.
	SecretValues []uint8
	// Timeout defaults to five seconds.  A timeout yields StatusUnknown.
	Timeout time.Duration
	// MaxSteps overrides RunOptions.MaxSteps.
	MaxSteps int
	// MaxNativeWitnesses caps representative architectural-leak pairs retained
	// in the result.  Zero selects a small default; NativeCount is exact.
	MaxNativeWitnesses int
	// RequirePartialForward restricts speculative witnesses to pairs in which
	// at least one execution exercised partial STLF.
	RequirePartialForward bool
	// Metric selects the shortest-witness ordering.  The default is CostSteps.
	Metric CostMetric
}

func (o SearchOptions) publicValues() []uint8 {
	if len(o.PublicValues) != 0 {
		out := make([]uint8, len(o.PublicValues))
		for i, v := range o.PublicValues {
			out[i] = v & 0x3f
		}
		return out
	}
	out := make([]uint8, 64)
	for i := range out {
		out[i] = uint8(i)
	}
	return out
}

func (o SearchOptions) secretValues() []uint8 {
	if len(o.SecretValues) != 0 {
		return append([]uint8(nil), o.SecretValues...)
	}
	out := make([]uint8, 256)
	for i := range out {
		out[i] = uint8(i)
	}
	return out
}

func (o SearchOptions) timeout() time.Duration {
	if o.Timeout == 0 {
		return 5 * time.Second
	}
	return o.Timeout
}

// SearchResult is returned by FindWitness.
type SearchResult struct {
	Status SearchStatus

	// Witness is the shortest speculative STLF witness, when one exists.
	Witness *Witness
	// Native lists representative pairs whose committed probe traces already
	// differ.  These are architectural leaks, not merely speculation artifacts.
	Native []Witness
	// NativeCount is the exact number of input pairs with differing committed
	// probe traces among the searched domain.
	NativeCount int

	CheckedInputs int
	CheckedPairs  int
	Elapsed       time.Duration

	Unknown  bool
	TimedOut bool
	Message  string
	Err      error
}

// FindWitness searches the finite public-low/secret input product for the
// shortest dual-input STLF witness.  It also reports architectural leaks
// separately.  A timeout returns StatusUnknown rather than Safe.
func FindWitness(host Host, code []Instruction, opts ...SearchOptions) SearchResult {
	start := time.Now()
	res := SearchResult{Status: StatusSafe}
	if err := Validate(host, code); err != nil {
		res.Status = StatusUnknown
		res.Unknown = true
		res.Err = err
		res.Message = err.Error()
		return res
	}
	var opt SearchOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	deadline := start.Add(opt.timeout())
	pubs := opt.publicValues()
	secs := opt.secretValues()
	maxNative := opt.MaxNativeWitnesses
	if maxNative <= 0 {
		maxNative = 8
	}

	var best *Witness
	checkedPairs := 0
	for _, pub := range pubs {
		execs := make([]Execution, len(secs))
		for j, secret := range secs {
			if time.Now().After(deadline) {
				return unknownResult(res, checkedPairs, start)
			}
			ex := Execute(host, code, Input{PublicLow6: pub, Secret: secret}, RunOptions{MaxSteps: opt.MaxSteps})
			if ex.Truncated {
				res.Status = StatusUnknown
				res.Unknown = true
				res.Err = fmt.Errorf("execution exceeded the step bound; analysis is incomplete")
				res.Message = "Unknown"
				res.Elapsed = time.Since(start)
				return res
			}
			execs[j] = ex
			res.CheckedInputs++
		}

		for i := 0; i < len(secs); i++ {
			for j := i + 1; j < len(secs); j++ {
				checkedPairs++
				if checkedPairs%4096 == 0 && time.Now().After(deadline) {
					return unknownResult(res, checkedPairs, start)
				}
				a, b := execs[i], execs[j]
				if !a.ArchTrace.Equal(b.ArchTrace) {
					res.NativeCount++
					if len(res.Native) < maxNative {
						res.Native = append(res.Native, makeWitness(pub, secs[i], secs[j], a, b, true))
					}
					continue
				}
				if a.SpecTrace.Equal(b.SpecTrace) {
					continue
				}
				if opt.RequirePartialForward && !a.PartialForward && !b.PartialForward {
					continue
				}
				w := makeWitness(pub, secs[i], secs[j], a, b, false)
				if best == nil || betterWitness(w, *best, opt.Metric) {
					copy := w
					best = &copy
				}
			}
		}
	}

	res.CheckedPairs = checkedPairs
	res.Elapsed = time.Since(start)
	res.Witness = best
	switch {
	case best != nil:
		res.Status = StatusWitness
		res.Message = fmt.Sprintf("speculative STLF witness: public low6=%#02x secret=%#02x/%#02x, arch=%s spec=%s/%s",
			best.PublicLow6, best.SecretA, best.SecretB,
			best.A.ArchTrace.String(), best.A.SpecTrace.String(), best.B.SpecTrace.String())
	case res.NativeCount > 0:
		res.Status = StatusNativeLeak
		res.Message = fmt.Sprintf("architectural probe trace leaks across %d input pairs", res.NativeCount)
	default:
		res.Status = StatusSafe
		res.Message = "no architectural or speculative leak in searched domain"
	}
	return res
}

// SearchWitness is an alias for FindWitness.
func SearchWitness(host Host, code []Instruction, opts ...SearchOptions) SearchResult {
	return FindWitness(host, code, opts...)
}

// FindShortestWitness is an alias for FindWitness.
func FindShortestWitness(host Host, code []Instruction, opts ...SearchOptions) SearchResult {
	return FindWitness(host, code, opts...)
}

// Analyze is an alias for FindWitness.
func Analyze(host Host, code []Instruction, opts ...SearchOptions) SearchResult {
	return FindWitness(host, code, opts...)
}

func unknownResult(partial SearchResult, pairs int, start time.Time) SearchResult {
	partial.CheckedPairs = pairs
	partial.Elapsed = time.Since(start)
	partial.Status = StatusUnknown
	partial.Unknown = true
	partial.TimedOut = true
	partial.Witness = nil
	partial.Message = "Unknown"
	return partial
}

func makeWitness(pub, sa, sb uint8, a, b Execution, native bool) Witness {
	cost := WitnessCost{
		Steps:           maxInt(a.Steps, b.Steps),
		SpecProbes:      len(a.SpecTrace) + len(b.SpecTrace),
		ArchProbes:      len(a.ArchTrace) + len(b.ArchTrace),
		FirstDivergence: FirstDivergence(a.SpecTrace, b.SpecTrace),
	}
	if native {
		cost.FirstDivergence = FirstDivergence(a.ArchTrace, b.ArchTrace)
	}
	return Witness{
		PublicLow6:         pub,
		SecretA:            sa,
		SecretB:            sb,
		A:                  a,
		B:                  b,
		Cost:               cost,
		Native:             native,
		UsesPartialForward: a.PartialForward || b.PartialForward,
	}
}

func betterWitness(a, b Witness, metric CostMetric) bool {
	less := func(x, y int) (bool, bool) {
		if x != y {
			return x < y, true
		}
		return false, false
	}
	switch metric {
	case CostSpecProbes:
		if lt, ok := less(a.Cost.SpecProbes, b.Cost.SpecProbes); ok {
			return lt
		}
		if lt, ok := less(a.Cost.Steps, b.Cost.Steps); ok {
			return lt
		}
	case CostFirstDivergence:
		if lt, ok := less(a.Cost.FirstDivergence, b.Cost.FirstDivergence); ok {
			return lt
		}
		if lt, ok := less(a.Cost.Steps, b.Cost.Steps); ok {
			return lt
		}
	default:
		if lt, ok := less(a.Cost.Steps, b.Cost.Steps); ok {
			return lt
		}
		if lt, ok := less(a.Cost.SpecProbes, b.Cost.SpecProbes); ok {
			return lt
		}
	}
	if lt, ok := less(a.Cost.SpecProbes, b.Cost.SpecProbes); ok {
		return lt
	}
	if lt, ok := less(a.Cost.FirstDivergence, b.Cost.FirstDivergence); ok {
		return lt
	}
	if a.PublicLow6 != b.PublicLow6 {
		return a.PublicLow6 < b.PublicLow6
	}
	if a.SecretA != b.SecretA {
		return a.SecretA < b.SecretA
	}
	return a.SecretB < b.SecretB
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
