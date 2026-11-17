package netsec

import "fmt"

// ArgOp describes how an argument predicate compares one syscall argument.
type ArgOp uint8

const (
	// ArgEqual matches arg == Value. It is the zero value so a predicate with
	// only Index and Value set is an exact match.
	ArgEqual ArgOp = iota
	// ArgNotEqual matches arg != Value.
	ArgNotEqual
	// ArgLessThan matches arg < Value.
	ArgLessThan
	// ArgLessEqual matches arg <= Value.
	ArgLessEqual
	// ArgGreaterThan matches arg > Value.
	ArgGreaterThan
	// ArgGreaterEqual matches arg >= Value.
	ArgGreaterEqual
	// ArgBitsSet matches arg&p.Mask == p.Mask.
	ArgBitsSet
	// ArgBitsClear matches arg&p.Mask == 0.
	ArgBitsClear
	// ArgMaskedEqual matches arg&p.Mask == p.Value&p.Mask.
	ArgMaskedEqual
)

const (
	ArgEQ         = ArgEqual
	ArgNE         = ArgNotEqual
	ArgLT         = ArgLessThan
	ArgLE         = ArgLessEqual
	ArgGT         = ArgGreaterThan
	ArgGE         = ArgGreaterEqual
	ArgHasBits    = ArgBitsSet
	ArgLacksBits  = ArgBitsClear
	ArgMaskEquals = ArgMaskedEqual
	ArgEqualsMask = ArgMaskedEqual
)

func (op ArgOp) Valid() bool {
	return op <= ArgMaskedEqual
}

func (op ArgOp) String() string {
	switch op {
	case ArgEqual:
		return "eq"
	case ArgNotEqual:
		return "ne"
	case ArgLessThan:
		return "lt"
	case ArgLessEqual:
		return "le"
	case ArgGreaterThan:
		return "gt"
	case ArgGreaterEqual:
		return "ge"
	case ArgBitsSet:
		return "bits-set"
	case ArgBitsClear:
		return "bits-clear"
	case ArgMaskedEqual:
		return "masked-eq"
	default:
		return fmt.Sprintf("ArgOp(%d)", uint8(op))
	}
}

// ArgPredicate is a pure predicate over one of the six syscall arguments.
type ArgPredicate struct {
	Index int
	Op    ArgOp
	Value uint64
	Mask  uint64
}

// ArgConstraint and ArgMatcher are aliases for callers that use another name.
type (
	ArgConstraint = ArgPredicate
	ArgMatcher    = ArgPredicate
)

// Validate checks whether the predicate can be evaluated safely.
func (p ArgPredicate) Validate() error {
	if p.Index < 0 || p.Index >= 6 {
		return fmt.Errorf("%w: index %d is outside [0,5]", ErrInvalidArg, p.Index)
	}
	if !p.Op.Valid() {
		return fmt.Errorf("%w: op %d", ErrInvalidArg, uint8(p.Op))
	}
	if (p.Op == ArgBitsSet || p.Op == ArgBitsClear || p.Op == ArgMaskedEqual) && p.Mask == 0 {
		return fmt.Errorf("%w: bit operation needs a non-zero mask", ErrInvalidArg)
	}
	return nil
}

// Match evaluates the predicate against one argument value.
func (p ArgPredicate) Match(arg uint64) bool {
	switch p.Op {
	case ArgEqual:
		return arg == p.Value
	case ArgNotEqual:
		return arg != p.Value
	case ArgLessThan:
		return arg < p.Value
	case ArgLessEqual:
		return arg <= p.Value
	case ArgGreaterThan:
		return arg > p.Value
	case ArgGreaterEqual:
		return arg >= p.Value
	case ArgBitsSet:
		return arg&p.Mask == p.Mask
	case ArgBitsClear:
		return arg&p.Mask == 0
	case ArgMaskedEqual:
		return arg&p.Mask == p.Value&p.Mask
	default:
		return false
	}
}

// ArgEquals creates an exact argument match.
func ArgEquals(index int, value uint64) ArgPredicate {
	return ArgPredicate{Index: index, Op: ArgEqual, Value: value}
}

// ArgNotEquals creates an inequality argument match.
func ArgNotEquals(index int, value uint64) ArgPredicate {
	return ArgPredicate{Index: index, Op: ArgNotEqual, Value: value}
}

// ArgMasked creates a masked equality predicate:
// arg&mask == value&mask.
func ArgMasked(index int, mask, value uint64) ArgPredicate {
	return ArgPredicate{Index: index, Op: ArgMaskedEqual, Mask: mask, Value: value}
}

// ArgAllBits creates a predicate that requires all mask bits to be set.
func ArgAllBits(index int, mask uint64) ArgPredicate {
	return ArgPredicate{Index: index, Op: ArgBitsSet, Mask: mask}
}

// ArgNoBits creates a predicate that requires all mask bits to be clear.
func ArgNoBits(index int, mask uint64) ArgPredicate {
	return ArgPredicate{Index: index, Op: ArgBitsClear, Mask: mask}
}

// Rule is one row of a caller-supplied pure decision table. Rules in a filter
// are checked in order and the first matching row wins.
type Rule struct {
	Syscall uint32
	Args    []ArgPredicate
	Action  Action
	Data    uint32
}

// Entry and FilterRule are aliases for Rule.
type (
	Entry      = Rule
	FilterRule = Rule
)

// Validate checks a rule independently of any chain.
func (r Rule) Validate() error {
	if !r.Action.Valid() {
		return fmt.Errorf("%w: %w: %d", ErrInvalidRule, ErrInvalidAction, uint8(r.Action))
	}
	for i, pred := range r.Args {
		if err := pred.Validate(); err != nil {
			return fmt.Errorf("%w: arg predicate %d: %w", ErrInvalidRule, i, err)
		}
	}
	return nil
}

// Matches reports whether the rule applies to the invocation.
func (r Rule) Matches(syscall uint32, args [6]uint64) bool {
	if r.Syscall != syscall {
		return false
	}
	for _, pred := range r.Args {
		if pred.Index < 0 || pred.Index >= len(args) {
			return false
		}
		if !pred.Match(args[pred.Index]) {
			return false
		}
	}
	return true
}

// AllowRule creates an allow row.
func AllowRule(syscall uint32, args ...ArgPredicate) Rule {
	return Rule{Syscall: syscall, Args: args, Action: ActionAllow}
}

// NotifyRule creates a notify row.
func NotifyRule(syscall uint32, data uint32, args ...ArgPredicate) Rule {
	return Rule{Syscall: syscall, Args: args, Action: ActionNotify, Data: data}
}

// DenyRule creates a deny row.
func DenyRule(syscall uint32, errno uint32, args ...ArgPredicate) Rule {
	return Rule{Syscall: syscall, Args: args, Action: ActionDeny, Data: errno}
}

// TerminateRule creates a terminate row.
func TerminateRule(syscall uint32, signal uint32, args ...ArgPredicate) Rule {
	return Rule{Syscall: syscall, Args: args, Action: ActionTerminate, Data: signal}
}

// Filter is a caller-supplied pure decision table. Default applies when no
// rule matches. Its zero value is an allow-all filter.
type Filter struct {
	Rules       []Rule
	Default     Action
	DefaultData uint32
}

// Table is an alias for Filter.
type Table = Filter

// NewFilter builds a filter with an allow default.
func NewFilter(rules ...Rule) Filter {
	return Filter{Rules: rules, Default: ActionAllow}
}

// NewFilterWithDefault builds a filter with an explicit default decision.
func NewFilterWithDefault(defaultAction Action, defaultData uint32, rules ...Rule) Filter {
	return Filter{Rules: rules, Default: defaultAction, DefaultData: defaultData}
}

// Validate checks the default action and every rule.
func (f Filter) Validate() error {
	if !f.Default.Valid() {
		return fmt.Errorf("%w: %w: default %d", ErrInvalidFilter, ErrInvalidAction, uint8(f.Default))
	}
	for i, rule := range f.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("%w: rule %d: %w", ErrInvalidFilter, i, err)
		}
	}
	return nil
}

// Evaluate returns the table's decision for one invocation.
func (f Filter) Evaluate(syscall uint32, args [6]uint64) Decision {
	for _, rule := range f.Rules {
		if rule.Matches(syscall, args) {
			return Decision{Action: rule.Action, Data: rule.Data}
		}
	}
	return Decision{Action: f.Default, Data: f.DefaultData}
}

// Decide is an alias for Evaluate.
func (f Filter) Decide(syscall uint32, args [6]uint64) Decision {
	return f.Evaluate(syscall, args)
}

func (f Filter) clone() Filter {
	cloned := Filter{
		Default:     f.Default,
		DefaultData: f.DefaultData,
	}
	if f.Rules == nil {
		return cloned
	}
	cloned.Rules = make([]Rule, len(f.Rules))
	for i, rule := range f.Rules {
		cloned.Rules[i] = rule
		if rule.Args != nil {
			cloned.Rules[i].Args = append([]ArgPredicate(nil), rule.Args...)
		}
	}
	return cloned
}
