// Package shrink performs structured minimization of the inputs that trigger
// an assertion failure in a security library.
//
// A schema describes a structured input as a fixed set of fields. Every field
// is either required (always present) or optional, whose presence is part of
// the search space. Integer fields enumerate every value of an inclusive
// range; string fields enumerate one representative value for every length in
// [0, MaxLen]. At most MaxOptionalFields optional fields are supported so the
// presence mask fits in two bytes.
//
// Candidates are emitted in minimization order: first by the number of
// present optional fields (fewest first), then by the total serialized byte
// size of the candidate (shortest first), with deterministic tie breaks.
// Every candidate exposed to the host satisfies the public precondition
// before the failing assertion is replayed, and every candidate carries the
// original failing assertion ID unchanged.
package shrink

// MaxOptionalFields is the largest number of optional fields a schema may
// declare; the candidate presence mask occupies at most two bytes.
const MaxOptionalFields = 16

// MaxTotalFields bounds the combined number of required and optional fields.
const MaxTotalFields = 32

// MaxStringLen bounds the enumerated length of string fields.
const MaxStringLen = 4096

// MaxIntRange bounds the cardinality of a single integer field so that a
// small domain can be enumerated exhaustively in memory.
const MaxIntRange = 1_000_000

// ErrInvalidSchema is wrapped by every schema validation error.
var ErrInvalidSchema = newInvalidSchemaError()
