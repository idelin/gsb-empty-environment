package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// UnknownPolicy selects how fields that are neither known nor reserved are
// handled. Hard violations (reserved numbers, type mismatches, cardinality
// breaches) are always rejected regardless of this policy.
type UnknownPolicy int

const (
	// PolicyReject is the zero value: any unknown field fails sanitization.
	PolicyReject UnknownPolicy = iota
	// PolicyDiscard silently removes unknown fields before downstream use.
	PolicyDiscard
	// PolicyQuarantine removes unknown fields from the readable view but
	// retains them in a separately audited quarantine bucket.
	PolicyQuarantine
)

// ViolationKind categorizes a single policy violation.
type ViolationKind int

const (
	ViolationReservedNumber ViolationKind = iota + 1
	ViolationUnknownField
	ViolationTypeMismatch
	ViolationSingularRepeated
	ViolationMaxCountExceeded
)

// String renders the violation kind for diagnostics.
func (k ViolationKind) String() string {
	switch k {
	case ViolationReservedNumber:
		return "reserved field number"
	case ViolationUnknownField:
		return "unknown field"
	case ViolationTypeMismatch:
		return "type mismatch"
	case ViolationSingularRepeated:
		return "singular field repeated"
	case ViolationMaxCountExceeded:
		return "repeated field max count exceeded"
	default:
		return "unknown violation"
	}
}

// Violation describes one rejected field occurrence.
type Violation struct {
	Kind   ViolationKind
	Number int
	// Detail carries human-readable context, e.g. the declared type.
	Detail string
}

// ValidationError aggregates all violations found in one sanitization pass.
// Use errors.As to inspect structured violations from a service boundary.
type ValidationError struct {
	Violations []Violation
}

func (e *ValidationError) Error() string {
	parts := make([]string, 0, len(e.Violations))
	for _, v := range e.Violations {
		parts = append(parts, fmt.Sprintf("field %d: %s%s", v.Number, v.Kind, v.Detail))
	}
	return "netsec: message rejected: " + strings.Join(parts, "; ")
}

// Sanitizer enforces one version schema with one unknown-field policy.
// A Sanitizer is safe for concurrent use after construction because its
// inputs are immutable and Sanitize allocates per-call state.
type Sanitizer struct {
	schema *VersionSchema
	policy UnknownPolicy
}

// NewSanitizer builds a Sanitizer for a compiled schema.
func NewSanitizer(schema *VersionSchema, policy UnknownPolicy) *Sanitizer {
	return &Sanitizer{schema: schema, policy: policy}
}

// Schema returns the bound schema.
func (z *Sanitizer) Schema() *VersionSchema { return z.schema }

// Policy returns the configured unknown-field policy.
func (z *Sanitizer) Policy() UnknownPolicy { return z.policy }

// Sanitize validates a raw message and returns a downstream-safe view.
//
// Hard violations (reserved numbers, type mismatches, singular repetition,
// repeated-field count limits) always produce a *ValidationError. Unknown
// fields follow the configured policy: reject, discard, or quarantine.
func (z *Sanitizer) Sanitize(raw RawMessage) (*SanitizedMessage, error) {
	if z == nil || z.schema == nil {
		return nil, errors.New("netsec: sanitizer requires a non-nil schema")
	}
	counts := make(map[int]int)
	for _, f := range raw {
		counts[f.Number]++
	}

	var violations []Violation
	out := &SanitizedMessage{
		schema: z.schema,
		known:  make(map[int][]any),
	}

	for _, f := range raw {
		spec, known := z.schema.Field(f.Number)
		switch {
		case z.schema.IsReserved(f.Number):
			violations = append(violations, Violation{
				Kind:   ViolationReservedNumber,
				Number: f.Number,
				Detail: " (reserved numbers must never carry data)",
			})
			out.stats.ReservedRejected++
		case !known:
			switch z.policy {
			case PolicyDiscard:
				out.stats.UnknownDropped++
			case PolicyQuarantine:
				out.quarantine = append(out.quarantine, f)
				out.stats.UnknownQuarantined++
			default:
				violations = append(violations, Violation{
					Kind:   ViolationUnknownField,
					Number: f.Number,
				})
			}
		default:
			if !matchesType(spec.Type, f.Value) {
				violations = append(violations, Violation{
					Kind:   ViolationTypeMismatch,
					Number: f.Number,
					Detail: fmt.Sprintf(" (declared %s, got %T)", spec.Type, f.Value),
				})
				continue
			}
			if spec.Cardinality == CardinalitySingular && counts[f.Number] > 1 {
				violations = append(violations, Violation{
					Kind:   ViolationSingularRepeated,
					Number: f.Number,
					Detail: fmt.Sprintf(" (%d occurrences)", counts[f.Number]),
				})
				continue
			}
			if spec.Cardinality == CardinalityRepeated && spec.MaxCount > 0 && counts[f.Number] > spec.MaxCount {
				violations = append(violations, Violation{
					Kind:   ViolationMaxCountExceeded,
					Number: f.Number,
					Detail: fmt.Sprintf(" (%d occurrences, max %d)", counts[f.Number], spec.MaxCount),
				})
				continue
			}
			out.known[f.Number] = append(out.known[f.Number], f.Value)
			out.stats.KnownAccepted++
		}
	}

	if len(violations) > 0 {
		sort.SliceStable(violations, func(i, j int) bool {
			if violations[i].Number != violations[j].Number {
				return violations[i].Number < violations[j].Number
			}
			return violations[i].Kind < violations[j].Kind
		})
		return nil, &ValidationError{Violations: violations}
	}
	return out, nil
}

// AsValidationError extracts a *ValidationError from a sanitization failure.
func AsValidationError(err error) (*ValidationError, bool) {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return ve, true
	}
	return nil, false
}
