package netsec

import (
	"fmt"
	"sort"
)

// maxCombinations bounds the size of the joint offset domain that Evaluate
// enumerates. The package is intentionally restricted to small integer
// domains so that exhaustive boundary checking remains cheap and auditable.
const maxCombinations = 1_000_000

// Verdict is the three-way result of evaluating a credential time problem.
type Verdict int

const (
	// VerdictReject means the credential is outside its validity windows for
	// every feasible offset assignment.
	VerdictReject Verdict = iota

	// VerdictResync means the credential is valid under some but not all
	// feasible offset assignments: the host must re-synchronize its clocks
	// (or reject) instead of accepting.
	VerdictResync

	// VerdictAccept means every window holds under every feasible assignment.
	VerdictAccept
)

// String returns the stable English name of the verdict.
func (v Verdict) String() string {
	switch v {
	case VerdictAccept:
		return "accept"
	case VerdictResync:
		return "resync"
	case VerdictReject:
		return "reject"
	default:
		return fmt.Sprintf("verdict(%d)", int(v))
	}
}

// Instant is an observed timestamp: a reading taken from Clock at the single
// check event ("now"), in whole seconds. Under offset o the reading equals
// the event's true time plus o.
type Instant struct {
	Clock   *Clock
	Reading int64
}

// Problem bundles the clocks, the verifier's anchor observation and the
// validity windows that jointly define an authentication-time decision.
type Problem struct {
	// Clocks are the time sources participating in the decision.
	Clocks []*Clock

	// Now anchors the check event to one observed clock reading. Its true
	// time under a candidate assignment is Reading - offset(Now.Clock); every
	// window is evaluated at that same true time.
	Now Instant

	// Windows are the half-open validity intervals that must all hold.
	Windows []Window
}

// Assignment fixes one integer offset for each participating clock. Clocks
// are referenced by pointer identity.
type Assignment map[*Clock]int64

// Calibration describes, for one clock, the offsets under which the whole
// conjunction of windows held during a VerdictResync result.
type Calibration struct {
	Clock *Clock

	// FeasibleOffsets lists every offset (relative to the clock's declared
	// domain) for which at least one full joint assignment succeeded.
	FeasibleOffsets []int64
}

// Report is the output of Evaluate.
type Report struct {
	Verdict Verdict

	// TotalCombinations is the size of the enumerated joint offset domain.
	TotalCombinations int64

	// ValidCombinations counts joint assignments under which every window
	// holds simultaneously.
	ValidCombinations int64

	// Calibration is populated only for VerdictResync and identifies, per
	// clock, the offsets compatible with a valid credential.
	Calibration []Calibration
}

// Evaluate exhaustively enumerates the joint offset domain of the clocks in
// p and classifies the credential.
//
// All windows referencing the same *Clock share one offset on every
// enumerated assignment (the shared-clock constraint is enforced jointly).
// The check event's true time is derived from Now and the assigned offset of
// its clock; a window on clock c holds when c's reading at that true time
// lies within [NotBefore, NotAfter).
//
// It returns ErrInfeasibleOffsets for an empty offset domain,
// ErrInvalidWindow for a malformed window or a missing anchor,
// ErrUnknownClock for an unregistered clock and ErrDomainTooLarge when
// enumeration would exceed maxCombinations.
func (p *Problem) Evaluate() (*Report, error) {
	if p == nil {
		return nil, ErrUnknownClock
	}
	clocks, err := p.prepare()
	if err != nil {
		return nil, err
	}

	total, err := domainSize(clocks)
	if err != nil {
		return nil, err
	}

	feasible := make(map[*Clock][]int64, len(clocks))
	assignment := make(Assignment, len(clocks))
	var valid int64

	var enumerate func(index int)
	enumerate = func(index int) {
		if index == len(clocks) {
			if p.holds(assignment) {
				valid++
				for _, clock := range clocks {
					feasible[clock] = append(feasible[clock], assignment[clock])
				}
			}
			return
		}
		clock := clocks[index]
		for offset := clock.MinOffset; ; offset++ {
			assignment[clock] = offset
			enumerate(index + 1)
			if offset == clock.MaxOffset {
				break
			}
		}
	}
	enumerate(0)

	report := &Report{TotalCombinations: total, ValidCombinations: valid}
	switch {
	case valid == 0:
		report.Verdict = VerdictReject
	case valid == total:
		report.Verdict = VerdictAccept
	default:
		report.Verdict = VerdictResync
		report.Calibration = buildCalibration(clocks, feasible)
	}
	return report, nil
}

// prepare validates inputs and returns the de-duplicated, ordered list of
// clocks actually referenced by the anchor and the windows.
func (p *Problem) prepare() ([]*Clock, error) {
	registered := make(map[*Clock]struct{}, len(p.Clocks))
	for _, clock := range p.Clocks {
		if err := clock.Validate(); err != nil {
			return nil, fmt.Errorf("netsec: clock %q: %w", clockName(clock), err)
		}
		if _, dup := registered[clock]; dup {
			return nil, fmt.Errorf("netsec: clock %q registered more than once", clock.Name)
		}
		registered[clock] = struct{}{}
	}

	if p.Now.Clock == nil {
		return nil, fmt.Errorf("netsec: anchor instant: %w", ErrUnknownClock)
	}
	if len(p.Windows) == 0 {
		return nil, ErrInvalidWindow
	}

	seen := make(map[*Clock]struct{})
	var clocks []*Clock
	note := func(clock *Clock) error {
		if _, ok := registered[clock]; !ok {
			return fmt.Errorf("netsec: clock %q: %w", clockName(clock), ErrUnknownClock)
		}
		if _, ok := seen[clock]; !ok {
			seen[clock] = struct{}{}
			clocks = append(clocks, clock)
		}
		return nil
	}

	if err := note(p.Now.Clock); err != nil {
		return nil, err
	}
	for _, window := range p.Windows {
		if err := window.Validate(); err != nil {
			return nil, fmt.Errorf("netsec: window: %w", err)
		}
		if err := note(window.NotBefore.Clock); err != nil {
			return nil, err
		}
	}
	return clocks, nil
}

// holds reports whether every window is satisfied at once under assignment.
func (p *Problem) holds(assignment Assignment) bool {
	trueNow := p.Now.Reading - assignment[p.Now.Clock]
	for _, window := range p.Windows {
		clock := window.NotBefore.Clock
		if !window.contains(trueNow, assignment[clock]) {
			return false
		}
	}
	return true
}

// domainSize computes the Cartesian-product size with an overflow-safe cap.
func domainSize(clocks []*Clock) (int64, error) {
	total := int64(1)
	for _, clock := range clocks {
		size, ok := clock.Size()
		if !ok {
			return 0, fmt.Errorf("netsec: clock %q: %w", clockName(clock), ErrInfeasibleOffsets)
		}
		if total > maxCombinations/size {
			return 0, fmt.Errorf("netsec: clock %q domain size %d: %w", clockName(clock), size, ErrDomainTooLarge)
		}
		total *= size
	}
	return total, nil
}

func buildCalibration(clocks []*Clock, feasible map[*Clock][]int64) []Calibration {
	out := make([]Calibration, 0, len(clocks))
	for _, clock := range clocks {
		offsets := feasible[clock]
		sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
		unique := offsets[:0]
		for _, offset := range offsets {
			if len(unique) == 0 || unique[len(unique)-1] != offset {
				unique = append(unique, offset)
			}
		}
		out = append(out, Calibration{Clock: clock, FeasibleOffsets: unique})
	}
	return out
}

func clockName(clock *Clock) string {
	if clock == nil || clock.Name == "" {
		return "<unnamed>"
	}
	return clock.Name
}
