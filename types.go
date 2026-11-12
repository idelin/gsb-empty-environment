package netsec

import "fmt"

// Op is a comparison used by a call guard. The zero value means that the
// guard adds no condition beyond the analysis's parameter-domain checks.
type Op uint8

const (
	OpAlways Op = iota
	OpLess
	OpLessEqual
	OpGreater
	OpGreaterEqual
	OpEqual
	OpNotEqual
)

// Guard restricts a call to a range of the caller's integer parameter. The
// parameter is the single integer argument tracked by the analysis.
type Guard struct {
	Op    Op
	Value int64
}

// Holds reports whether v satisfies g.
func (g Guard) Holds(v int64) bool {
	switch g.Op {
	case OpAlways:
		return true
	case OpLess:
		return v < g.Value
	case OpLessEqual:
		return v <= g.Value
	case OpGreater:
		return v > g.Value
	case OpGreaterEqual:
		return v >= g.Value
	case OpEqual:
		return v == g.Value
	case OpNotEqual:
		return v != g.Value
	default:
		return false
	}
}

// CallKind describes how a call affects the caller's frame.
type CallKind uint8

const (
	// CallNormal keeps the caller frame alive for the duration of the callee.
	CallNormal CallKind = iota
	// CallTail releases the caller frame before entering the callee.
	CallTail
)

// Edge is one outgoing call of a function. Delta is added to the caller's
// parameter to obtain the callee's parameter, so recursive calls normally use
// a negative Delta. Guard is evaluated against the caller's parameter.
type Edge struct {
	Target string
	Kind   CallKind
	Guard  Guard
	Delta  int64
	Label  string

	// To, Tail, Cond, and Decrement are alternate spellings accepted on input.
	// To is used when Target is empty. Tail promotes Kind to CallTail. Cond is
	// used when Guard is zero. Decrement is a positive amount and is converted
	// to Delta = -Decrement when Delta is zero.
	To        string
	Tail      bool
	Cond      Guard
	Decrement int64
}

// Function describes a fixed-size frame and its outgoing calls.
type Function struct {
	Name      string
	FrameSize int64
	Calls     []Edge

	// Frame is an alternate spelling for FrameSize accepted on input. When both
	// are set they must agree.
	Frame int64
}

// Func is an alias for Function.
type Func = Function

// Call is an alias for Edge.
type Call = Edge

// Cond is an alias for Guard.
type Cond = Guard

// Program is a finite call graph plus the entry function name. Functions are
// matched by Name.
type Program struct {
	Entry     string
	Functions []Function

	// Funcs is an alternate input list. Functions and Funcs are combined in
	// order; duplicate names are rejected.
	Funcs []Function
}

// Status is the proof status of an analysis.
type Status uint8

const (
	// StatusProven means every recursive cycle was shown to decrease the
	// integer parameter and a finite maximum was computed.
	StatusProven Status = iota
	// StatusUnknown means the analysis could not prove a finite bound.
	StatusUnknown
)

const (
	// Proven is an alias for StatusProven.
	Proven = StatusProven
	// Unknown is an alias for StatusUnknown.
	Unknown = StatusUnknown
)

func (s Status) String() string {
	if s == StatusProven {
		return "proven"
	}
	return "unknown"
}

// Step is one frame allocation on the witness path. Live is the number of
// bytes alive at that point, including this frame and all unreturned callers.
// TailCall is true when this frame was entered by a tail call.
type Step struct {
	Function  string
	Parameter int64
	FrameSize int64
	Live      int64
	TailCall  bool
}

// CallSite records the stack requirement of a feasible call edge. Required is
// the peak number of bytes needed from the stack prefix below the caller. For
// a normal call that is caller frame plus the callee's peak; for a tail call
// it is the larger of the caller frame and the callee's peak.
type CallSite struct {
	Caller      string
	Callee      string
	CallerParam int64
	CalleeParam int64
	Tail        bool
	CallerFrame int64
	CalleeNeed  int64
	Required    int64
	Label       string
}

// CallCheck is a call-site obligation on the concrete witness path. LiveBefore
// includes the caller frame. Additional is the extra capacity needed above the
// bytes already live at the call.
type CallCheck struct {
	Caller      string
	Callee      string
	CallerParam int64
	CalleeParam int64
	Tail        bool
	LiveBefore  int64
	CalleeNeed  int64
	Peak        int64
	Additional  int64
	Remaining   int64
	Fits        bool
	Label       string
}

// Options controls optional resource limits and stack-limit checking.
type Options struct {
	// StackLimit is the host's stack capacity in bytes. Zero means unbounded.
	StackLimit int64
	// Limit is an alternate spelling for StackLimit.
	Limit int64
	// MaxStates bounds the number of distinct (function, parameter) states.
	// Zero selects a conservative default.
	MaxStates int
	// AllowNegativeParameters disables the default non-negative depth-budget
	// assumption. Without it, a call is considered only when both the caller and
	// callee parameters are non-negative.
	AllowNegativeParameters bool
}

// Result is the outcome of an analysis. MaxStack, MaxStackBytes, MaxBytes,
// PeakStack, and WorstStack are aliases; MaxDepth and Depth are aliases; and
// PathNames and CallPath are aliases. Both spellings are kept for callers that
// use either naming convention.
type Result struct {
	Status        Status
	Known         bool
	Reason        string
	Entry         string
	EntryParam    int64
	MaxStack      int64
	MaxStackBytes int64
	MaxBytes      int64
	PeakStack     int64
	WorstStack    int64
	MaxDepth      int
	Depth         int
	MaxLiveFrames int
	Path          []Step
	PathNames     []string
	CallPath      []string
	ParameterPath []int64
	FramePath     []int64
	CallSites     []CallSite
	Checks        []CallCheck
	HasLimit      bool
	StackLimit    int64
	Fits          bool
}

// Report is an alias for Result.
type Report = Result

// Error is returned for malformed programs. A non-decreasing cycle is not a
// validation error: Analyze reports it as StatusUnknown.
type Error struct {
	Message string
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func validationError(format string, args ...any) error {
	return &Error{Message: fmt.Sprintf(format, args...)}
}
