package netsec

import (
	"errors"
	"fmt"
	"math"
)

// NoiseMode selects the noise mechanism applied to released values.
type NoiseMode string

const (
	// NoiseNone releases the rounded, noiseless aggregate.
	NoiseNone NoiseMode = "none"
	// NoiseLaplace adds Laplace noise calibrated to sensitivity/epsilon.
	NoiseLaplace NoiseMode = "laplace"
)

// SensitiveMode controls how sensitive attributes are handled.
type SensitiveMode string

const (
	// SensitiveThreshold releases a sensitive aggregate only when its group
	// meets SensitiveMinGroupSize. This is the default.
	SensitiveThreshold SensitiveMode = "threshold"
	// SensitiveSuppressAll suppresses every aggregate involving a sensitive
	// attribute, regardless of group size.
	SensitiveSuppressAll SensitiveMode = "suppress"
)

// Policy is the disclosure-control configuration. Its zero value is replaced
// with safe defaults by Normalize; use DefaultPolicy for an explicit copy.
type Policy struct {
	// MinGroupSize is the minimum number of distinct subjects in a group
	// before any aggregate for that group can be released.
	MinGroupSize int

	// SensitiveMinGroupSize is the minimum distinct group size for a
	// sensitive aggregate. It should normally be at least MinGroupSize.
	SensitiveMinGroupSize int

	// SensitiveMode selects threshold-based or unconditional suppression.
	SensitiveMode SensitiveMode

	// SensitiveAttributes marks attribute names that carry sensitive data.
	// Queries may also set Query.Sensitive explicitly.
	SensitiveAttributes map[string]bool

	// TotalBudget is the global epsilon budget consumed by accepted queries.
	TotalBudget float64

	// SubjectBudget is the epsilon budget per distinct subject. Cross-query
	// correlation is accumulated against this budget. A zero value defaults
	// to TotalBudget.
	SubjectBudget float64

	// DefaultEpsilon is used when Query.Epsilon is zero.
	DefaultEpsilon float64

	// MaxQueriesPerSubject bounds how many accepted queries may include the
	// same subject. A zero value defaults to DefaultMaxQueriesPerSubject.
	MaxQueriesPerSubject int

	// MaxQueryOverlap is the containment coefficient at which a new query is
	// considered a differencing/cross-query attempt and rejected. For subject
	// sets A and B, the coefficient is |A∩B| / min(|A|,|B|). A value in (0,1]
	// is valid; a negative value disables this check. A zero value defaults to
	// DefaultMaxQueryOverlap.
	MaxQueryOverlap float64

	// RoundingBase quantizes released values. A value of 1 disables
	// quantization; zero or negative selects the default of 5. Rounding
	// happens after noise.
	RoundingBase float64

	// NoiseMode selects the output perturbation mechanism.
	NoiseMode NoiseMode

	// RandomSeed makes Laplace noise reproducible for testing or replay.
	// When nil, the engine seeds from crypto/rand.
	RandomSeed *int64

	// AllowUnlinkedCells permits summaries that provide Count but no subject
	// identifiers. Such cells still receive suppression, rounding, noise,
	// and global budget checks, but cannot participate in per-subject
	// correlation accounting. It is false by default because per-subject
	// linkage is part of the disclosure guarantee.
	AllowUnlinkedCells bool
}

const (
	// DefaultMinGroupSize is applied when Policy.MinGroupSize is unset.
	DefaultMinGroupSize = 5
	// DefaultSensitiveMinGroupSize is applied when the sensitive threshold
	// is unset.
	DefaultSensitiveMinGroupSize = 10
	// DefaultTotalBudget is the default global epsilon budget.
	DefaultTotalBudget = 1.0
	// DefaultEpsilon is the default cost of a query.
	DefaultEpsilon = 0.1
	// DefaultMaxQueriesPerSubject bounds repeated subject participation.
	DefaultMaxQueriesPerSubject = 10
	// DefaultMaxQueryOverlap is the default containment coefficient threshold.
	DefaultMaxQueryOverlap = 0.8
	// DefaultRoundingBase is the default output granularity.
	DefaultRoundingBase = 5.0
)

// DefaultPolicy returns a privacy-preserving configuration.
func DefaultPolicy() Policy {
	return Policy{
		MinGroupSize:          DefaultMinGroupSize,
		SensitiveMinGroupSize: DefaultSensitiveMinGroupSize,
		SensitiveMode:         SensitiveThreshold,
		SensitiveAttributes:   map[string]bool{},
		TotalBudget:           DefaultTotalBudget,
		SubjectBudget:         DefaultTotalBudget,
		DefaultEpsilon:        DefaultEpsilon,
		MaxQueriesPerSubject:  DefaultMaxQueriesPerSubject,
		MaxQueryOverlap:       DefaultMaxQueryOverlap,
		RoundingBase:          DefaultRoundingBase,
		NoiseMode:             NoiseLaplace,
	}
}

// Normalize fills unset fields with safe defaults and validates the result.
// It returns a copy; the receiver is not modified.
func (p Policy) Normalize() (Policy, error) {
	out := p
	out.SensitiveAttributes = cloneBoolMap(p.SensitiveAttributes)

	if out.MinGroupSize <= 0 {
		out.MinGroupSize = DefaultMinGroupSize
	}
	if out.SensitiveMinGroupSize <= 0 {
		out.SensitiveMinGroupSize = DefaultSensitiveMinGroupSize
		if out.SensitiveMinGroupSize < out.MinGroupSize {
			out.SensitiveMinGroupSize = out.MinGroupSize
		}
	}
	if out.SensitiveMode == "" {
		out.SensitiveMode = SensitiveThreshold
	}
	if out.TotalBudget <= 0 {
		out.TotalBudget = DefaultTotalBudget
	}
	if out.SubjectBudget <= 0 {
		out.SubjectBudget = out.TotalBudget
	}
	if out.DefaultEpsilon <= 0 {
		out.DefaultEpsilon = DefaultEpsilon
	}
	if out.MaxQueriesPerSubject <= 0 {
		out.MaxQueriesPerSubject = DefaultMaxQueriesPerSubject
	}
	if out.MaxQueryOverlap == 0 {
		out.MaxQueryOverlap = DefaultMaxQueryOverlap
	}
	if out.RoundingBase <= 0 {
		out.RoundingBase = DefaultRoundingBase
	}
	if out.NoiseMode == "" {
		out.NoiseMode = NoiseLaplace
	}

	switch out.SensitiveMode {
	case SensitiveThreshold, SensitiveSuppressAll:
	default:
		return Policy{}, fmt.Errorf("policy: unknown sensitive mode %q", out.SensitiveMode)
	}
	switch out.NoiseMode {
	case NoiseNone, NoiseLaplace:
	default:
		return Policy{}, fmt.Errorf("policy: unknown noise mode %q", out.NoiseMode)
	}
	if math.IsNaN(out.TotalBudget) || math.IsInf(out.TotalBudget, 0) {
		return Policy{}, errors.New("policy: total budget must be finite")
	}
	if math.IsNaN(out.SubjectBudget) || math.IsInf(out.SubjectBudget, 0) {
		return Policy{}, errors.New("policy: subject budget must be finite")
	}
	if math.IsNaN(out.DefaultEpsilon) || math.IsInf(out.DefaultEpsilon, 0) {
		return Policy{}, errors.New("policy: default epsilon must be finite")
	}
	if math.IsNaN(out.MaxQueryOverlap) {
		return Policy{}, errors.New("policy: max query overlap must be a number")
	}
	if out.MaxQueryOverlap > 1 {
		return Policy{}, errors.New("policy: max query overlap cannot exceed 1")
	}
	if math.IsNaN(out.RoundingBase) || math.IsInf(out.RoundingBase, 0) {
		return Policy{}, errors.New("policy: rounding base must be finite")
	}
	return out, nil
}

// Validate reports whether the policy's explicitly configured fields are
// internally consistent. Zero values are treated as defaults.
func (p Policy) Validate() error {
	_, err := p.Normalize()
	return err
}

func cloneBoolMap(in map[string]bool) map[string]bool {
	out := make(map[string]bool, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func (p Policy) isSensitiveAttribute(attribute string) bool {
	return attribute != "" && p.SensitiveAttributes[attribute]
}
