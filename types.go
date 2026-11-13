package netsec

import (
	"errors"
	"fmt"
)

const (
	maxInt = int(^uint(0) >> 1)
	minInt = -maxInt - 1
)

const (
	// StackSize is the fixed stack size of the model.
	StackSize = 64
	// ReturnSlotSize is the number of bytes in a saved return address.
	ReturnSlotSize = 4
	// CanarySize is the number of bytes in the optional canary.
	CanarySize = 4
	// MaxWrites is the maximum number of copy operations.
	MaxWrites = 8
	// MaxInputBytes is the maximum number of attacker-controlled input bytes.
	MaxInputBytes = 3
)

// Accepted constant aliases for platform adapters.
const (
	StackBytes         = StackSize
	ReturnBytes        = ReturnSlotSize
	CanaryBytes        = CanarySize
	MaxOperations      = MaxWrites
	MaxInput           = MaxInputBytes
	MaxControlledBytes = MaxInputBytes
)

// Affine is a mathematical affine form A*x+B. x is the integer value of one
// input byte (0..255). A and B are ordinary integers; no uint8 or intN
// wraparound is applied before the range checks performed by Replay.
//
// Mul and Add are accepted aliases for A and B. If A or B is non-zero they
// take precedence; otherwise Mul and Add are used.
type Affine struct {
	A   int
	B   int
	Mul int
	Add int
	// Coef and Const are additional accepted spellings.
	Coef  int
	Const int
}

// NewAffine constructs the affine form a*x+b.
func NewAffine(a, b int) Affine { return Affine{A: a, B: b} }

// Eval evaluates the form at x. x may be a byte, uint8, or int; byte values
// are treated as their unsigned integer value.
func (a Affine) Eval(x any) int {
	var value int
	switch v := x.(type) {
	case byte:
		value = int(v)
	case int:
		value = v
	case int8:
		value = int(v)
	case uint:
		value = int(v)
	default:
		return 0
	}
	v, ok := a.evalChecked(value)
	if ok {
		return v
	}
	coef, _ := a.coefficients()
	if (coef >= 0 && value >= 0) || (coef < 0 && value < 0) {
		return maxInt
	}
	return minInt
}

// EvalByte evaluates the form at an input byte value.
func (a Affine) EvalByte(x byte) int { return a.Eval(x) }

func (a Affine) coefficients() (int, int) {
	if a.A != 0 || a.B != 0 {
		return a.A, a.B
	}
	if a.Mul != 0 || a.Add != 0 {
		return a.Mul, a.Add
	}
	if a.Coef != 0 || a.Const != 0 {
		return a.Coef, a.Const
	}
	return 0, 0
}

func (a Affine) isZero() bool {
	coef, constant := a.coefficients()
	return coef == 0 && constant == 0
}

// evalChecked evaluates the form using mathematical integers and reports
// whether the exact result fits in an int.
func (a Affine) evalChecked(x int) (int, bool) {
	coef, constant := a.coefficients()
	if coef == 0 {
		return constant, true
	}
	if coef == -1 && x == minInt {
		return 0, false
	}
	product := coef * x
	if product/coef != x {
		return 0, false
	}
	switch {
	case constant > 0 && product > maxInt-constant:
		return 0, false
	case constant < 0 && product < minInt-constant:
		return 0, false
	}
	return product + constant, true
}

// Span is a half-open interval [Start, Start+Len) in the 64-byte stack.
//
// Offset and Length are accepted aliases for Start and Len. If Len is zero
// and Length is non-zero, Length is used; Start is used when non-zero,
// otherwise Offset is used. A span with Start 0 is of course valid.
type Span struct {
	Start  int
	Len    int
	Offset int
	Length int
}

// InputSlot, Interval, and Range are aliases for Span.
type (
	InputSlot  = Span
	Interval   = Span
	Range      = Span
	ReturnSlot = Span
	CanarySlot = Span
	StackRange = Span
)

// NewSpan constructs the interval [start, start+length).
func NewSpan(start, length int) Span { return Span{Start: start, Len: length} }

// End returns Start+Len. It may be negative or larger than StackSize for an
// invalid span; callers should use Valid before relying on it.
func (s Span) End() int {
	s = s.normalized()
	return s.Start + s.Len
}

// Valid reports whether the span lies entirely inside a buffer of size n.
func (s Span) Valid(n int) bool {
	s = s.normalized()
	return s.Start >= 0 && s.Len >= 0 && s.Start <= n && s.Len <= n-s.Start
}

// Overlaps reports whether two spans share at least one byte.
func (s Span) Overlaps(t Span) bool {
	s, t = s.normalized(), t.normalized()
	if s.Len <= 0 || t.Len <= 0 {
		return false
	}
	return s.Start < t.Start+t.Len && t.Start < s.Start+s.Len
}

// Contains reports whether pos is inside the span.
func (s Span) Contains(pos int) bool {
	s = s.normalized()
	return pos >= s.Start && pos < s.Start+s.Len
}

func (s Span) normalized() Span {
	if s.Len == 0 && s.Length != 0 {
		s.Len = s.Length
	}
	if s.Start == 0 && s.Offset != 0 {
		s.Start = s.Offset
	}
	return s
}

// Write is one copy operation. Offset and Length are affine forms of one
// controlled input byte. OffsetInput and LengthInput select which byte
// drives each form. Byte (or Index) is a convenient fallback that selects
// the same byte for both forms when the two explicit indices are zero.
//
// Dest and Count are aliases for Offset and Length.
type Write struct {
	Offset Affine
	Length Affine

	OffsetInput int
	LengthInput int
	OffsetByte  int
	LengthByte  int
	Byte        int
	Index       int
	Var         int

	Dest  Affine
	Count Affine
}

func (w Write) offsetForm() Affine {
	if !w.Offset.isZero() {
		return w.Offset
	}
	return w.Dest
}

func (w Write) lengthForm() Affine {
	if !w.Length.isZero() {
		return w.Length
	}
	return w.Count
}

func (w Write) inputIndices() (offsetIndex, lengthIndex int) {
	offsetIndex, lengthIndex = w.OffsetInput, w.LengthInput
	fallback := w.Byte
	if w.Index != 0 {
		fallback = w.Index
	} else if w.Var != 0 {
		fallback = w.Var
	}
	if offsetIndex == 0 {
		offsetIndex = w.OffsetByte
	}
	if lengthIndex == 0 {
		lengthIndex = w.LengthByte
	}
	if fallback != 0 && offsetIndex == 0 {
		offsetIndex = fallback
	}
	if fallback != 0 && lengthIndex == 0 {
		lengthIndex = fallback
	}
	return offsetIndex, lengthIndex
}

// Canary describes an optional four-byte stack canary.
//
// A non-nil *Canary in a Problem enables the canary. Value is the initial
// canary value; Initial is an accepted alias. If Value is all zero and
// Initial is not, Initial is used. If both are zero, the initial value is
// read from InitialStack at Offset.
type Canary struct {
	Offset  int
	Value   [CanarySize]byte
	Initial [CanarySize]byte
	At      int

	// Enabled is advisory. A non-nil *Canary enables the canary regardless
	// of this field; use a nil pointer to disable it.
	Enabled bool
}

// Problem is the complete description supplied by the testing platform.
//
// InitialStack, InputSlot, ReturnSlot, InitialRet, Writes, Allowed,
// AllowedTargets, and Canary are the canonical fields. The other fields are
// accepted spellings used by platform adapters; they are normalized by
// Validate before replay.
//
// The first len(Allowed) bytes of InputSlot are attacker-controlled. If
// InputSlot is longer, the remaining bytes are fixed by InitialStack and may
// still appear in a copied source prefix.
type Problem struct {
	InitialStack [StackSize]byte
	Stack        [StackSize]byte // alias for InitialStack
	StackImage   [StackSize]byte // alias for InitialStack

	InputSlot  Span
	Input      Span // alias for InputSlot
	InputRange Span // alias for InputSlot
	InputStart int
	InputLen   int

	ReturnSlot   Span
	RetSlot      Span // alias for ReturnSlot
	ReturnOffset int
	RetOffset    int

	InitialRet    uint32
	InitialReturn uint32 // alias for InitialRet
	RetValue      uint32 // alias for InitialRet

	Writes []Write

	Allowed       [][]byte
	Values        [][]byte // alias for Allowed
	InputValues   [][]byte // alias for Allowed
	Alphabet      [][]byte // alias for Allowed
	AllowedInputs [][]byte // alias for Allowed

	AllowedTargets []uint32
	Targets        []uint32 // alias for AllowedTargets

	// Optional replay budget. Solve's variadic budget argument takes
	// precedence when supplied; a zero value here means "unspecified".
	Budget        int
	MaxCandidates int // alias for Budget

	Canary *Canary

	// Flat canary aliases for callers that do not use *Canary.
	CanaryEnabled bool
	CanaryOffset  int
	CanaryValue   [CanarySize]byte
	CanaryInitial [CanarySize]byte
}

// Config, Spec, Params, and Case are aliases for Problem.
type (
	Config       = Problem
	Spec         = Problem
	Params       = Problem
	Case         = Problem
	ReachProblem = Problem
	StackProblem = Problem
)

// AffineExpr, LinearForm, WriteOp, and WriteOperation are accepted aliases.
type (
	AffineExpr     = Affine
	LinearForm     = Affine
	WriteOp        = Write
	WriteOperation = Write
)

// Status classifies a replay or a completed search.
type Status string

const (
	// StatusExploit means all writes completed, the canary was intact, and
	// the final return address is an allowed target different from the
	// initial address.
	StatusExploit Status = "exploit"
	// StatusSuccess is an alias for StatusExploit.
	StatusSuccess = StatusExploit

	// StatusCanaryOnly means no control-flow transfer was possible, but at
	// least one complete execution destroyed the canary. This is not a
	// successful exploit.
	StatusCanaryOnly Status = "canary-only"
	// StatusCanaryDamaged is an alias for StatusCanaryOnly.
	StatusCanaryDamaged = StatusCanaryOnly

	// StatusNoExploit means all candidates were replayed and none reached
	// an allowed target with an intact canary.
	StatusNoExploit Status = "no-exploit"

	// StatusBudgetUnknown means the caller's replay budget was exhausted
	// before the candidate space was decided. It is neither success nor a
	// proof that no exploit exists.
	StatusBudgetUnknown Status = "budget-unknown"
	// StatusUnknown is an alias for StatusBudgetUnknown.
	StatusUnknown = StatusBudgetUnknown

	// StatusTrap means a replay trapped before the return epilogue.
	StatusTrap Status = "trap"

	// StatusInvalid means configuration validation failed.
	StatusInvalid Status = "invalid"

	// Compact aliases used by some platform adapters.
	BudgetUnknown     = StatusBudgetUnknown
	UnknownBudget     = StatusBudgetUnknown
	CanaryClobbered   = StatusCanaryOnly
	ExploitSuccess    = StatusExploit
	NoExploitPossible = StatusNoExploit
)

// TrapReason identifies the first checked condition that trapped a replay.
type TrapReason string

const (
	TrapNone         TrapReason = ""
	TrapSourceShort  TrapReason = "source-short"
	TrapStackBounds  TrapReason = "stack-bounds"
	TrapInvalidInput TrapReason = "invalid-input"
)

// WriteSpan records one completed copy operation.
type WriteSpan struct {
	Write  int
	Start  int
	Offset int
	Len    int
	Length int
	End    int

	Source Span
	Target Span
}

// Interval returns the target interval of the write.
func (w WriteSpan) Interval() Span {
	return Span{Start: w.Start, Len: w.Len}
}

// Result is returned by Replay and Solve.
type Result struct {
	Status Status

	Input       []byte
	InputBytes  []byte // alias for Input
	InputString []byte // initial bytes of the whole input slot

	Writes     []WriteSpan
	WriteSpans []WriteSpan // alias for Writes
	Intervals  []Span      // target intervals of completed writes

	InitialStack [StackSize]byte
	FinalStack   [StackSize]byte
	Stack        [StackSize]byte // alias for FinalStack

	InitialRet uint32
	FinalRet   uint32
	ReturnAddr uint32 // alias for FinalRet

	Allowed bool // FinalRet is in the allowed target set
	Changed bool // FinalRet differs from InitialRet

	CanaryEnabled bool
	CanaryInitial [CanarySize]byte
	CanaryFinal   [CanarySize]byte
	CanaryIntact  bool
	CanaryOK      bool // alias for CanaryIntact

	Trapped    bool
	TrapReason TrapReason
	TrapWrite  int // index of the failed write, or -1

	SawTrap   bool // a Solve search observed at least one trapping candidate
	TrapCount int  // number of trapping candidates observed by Solve

	Returned    bool // all writes completed and the return epilogue was reached
	Transferred bool // control-flow transfer to an allowed, changed return address

	Exhausted  bool   // candidate space was fully examined
	Candidates int    // number of replays performed
	Space      uint64 // number of candidates in the space, saturated

	Err error
}

// Exploit reports whether the result is a successful control-flow transfer.
func (r Result) Exploit() bool {
	return r.Status == StatusExploit && r.Transferred
}

// CanaryOnly reports whether the only observed effect was canary damage.
func (r Result) CanaryOnly() bool { return r.Status == StatusCanaryOnly }

// Unknown reports whether the search was cut short by its budget.
func (r Result) Unknown() bool { return r.Status == StatusBudgetUnknown }

// BudgetExhausted reports whether the search stopped at its replay budget.
func (r Result) BudgetExhausted() bool { return r.Status == StatusBudgetUnknown }

// Solution and Finding are aliases for Result.
type (
	Solution = Result
	Finding  = Result
)

// Validation errors returned by Validate and the solving functions.
var (
	ErrTooManyWrites     = errors.New("netsec: too many writes")
	ErrTooManyInputBytes = errors.New("netsec: too many input bytes")
	ErrInvalidInputSlot  = errors.New("netsec: invalid input slot")
	ErrInvalidReturnSlot = errors.New("netsec: invalid return slot")
	ErrInvalidCanary     = errors.New("netsec: invalid canary")
	ErrSlotsOverlap      = errors.New("netsec: return slot overlaps canary")
	ErrInvalidWriteIndex = errors.New("netsec: write input index out of range")
	ErrInvalidInput      = errors.New("netsec: invalid input value")
	ErrInputLength       = errors.New("netsec: input length does not match allowed bytes")
	ErrInconsistentSpan  = errors.New("netsec: inconsistent span aliases")
	ErrInconsistentValue = errors.New("netsec: inconsistent value aliases")
)

func wrap(kind error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", kind, fmt.Sprintf(format, args...))
}
