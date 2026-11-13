package netsec

import (
	"errors"
	"fmt"
	"sort"
)

const (
	defaultAnalysisBudget = 1_000_000
	defaultExecutionSteps = 100_000
)

// Options configures static analysis and the differential checker. A
// declassification is allowed only when the instruction names the label and
// the host whitelist also names it.
type Options struct {
	DeclassifyWhitelist LabelSet
	Whitelist           LabelSet
	Budget              int
	StepLimit           int
	ExecStepLimit       int
}

func (o Options) whitelist() LabelSet {
	return o.DeclassifyWhitelist.Union(o.Whitelist)
}

func parseOptions(args []any) Options {
	var result Options
	for _, arg := range args {
		switch value := arg.(type) {
		case nil:
		case Options:
			result = mergeOptions(result, value)
		case *Options:
			if value != nil {
				result = mergeOptions(result, *value)
			}
		case LabelSet:
			result.DeclassifyWhitelist = result.DeclassifyWhitelist.Union(canonicalLabels(value))
		case []Label:
			result.DeclassifyWhitelist = result.DeclassifyWhitelist.Union(canonicalLabels(value))
		case []string:
			result.DeclassifyWhitelist = result.DeclassifyWhitelist.Union(NewLabelSet(value...))
		case Label:
			result.DeclassifyWhitelist = result.DeclassifyWhitelist.Union(Labels(value))
		case string:
			result.DeclassifyWhitelist = result.DeclassifyWhitelist.Union(NewLabelSet(value))
		case int:
			if value > 0 {
				result.Budget = value
			}
		default:
			// Unknown option types are ignored. Analyze reports malformed
			// programs, not malformed variadic convenience arguments.
		}
	}
	return result
}

func mergeOptions(left, right Options) Options {
	return Options{
		DeclassifyWhitelist: left.whitelist().Union(right.whitelist()),
		Whitelist:           nil,
		Budget:              firstNonZero(right.Budget, left.Budget),
		StepLimit:           firstNonZero(right.StepLimit, left.StepLimit),
		ExecStepLimit:       firstNonZero(right.ExecStepLimit, left.ExecStepLimit),
	}
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

func (o Options) executionLimit() int {
	if limit := firstNonZero(o.StepLimit, o.ExecStepLimit); limit > 0 {
		return limit
	}
	return defaultExecutionSteps
}

// LeakKind describes why a public write was rejected.
type LeakKind string

const (
	LeakValue LeakKind = "value"
	LeakPC    LeakKind = "pc"
	LeakBoth  LeakKind = "both"
)

// Leak is one rejected public write.
type Leak struct {
	Node        int      `json:"node"`
	Output      int      `json:"output"`
	Register    Register `json:"register"`
	ValueLabels LabelSet `json:"value_labels,omitempty"`
	PCLabels    LabelSet `json:"pc_labels,omitempty"`
	Kind        LeakKind `json:"kind"`
}

// AuditEvent records a declassify instruction. Requested is exactly the set
// named by the instruction, Removed is the subset the host whitelist allowed
// to be removed from the value, and Remaining is the resulting value-label
// set. PCLabels is separate: declassification never removes the active
// program-counter token.
type AuditEvent struct {
	Node      int      `json:"node"`
	Register  Register `json:"register"`
	Requested LabelSet `json:"requested,omitempty"`
	Removed   LabelSet `json:"removed,omitempty"`
	Remaining LabelSet `json:"remaining,omitempty"`
	PCLabels  LabelSet `json:"pc_labels,omitempty"`
}

// NodeState is the fixed-point input state at one reachable CFG node.
type NodeState struct {
	Node      int                   `json:"node"`
	Registers map[Register]LabelSet `json:"registers,omitempty"`
	PC        LabelSet              `json:"pc,omitempty"`
	Control   map[int]LabelSet      `json:"control,omitempty"`
}

// Result is the result of the control-flow-sensitive label analysis.
type Result struct {
	Safe        bool         `json:"safe"`
	Leaks       []Leak       `json:"leaks,omitempty"`
	Audit       []AuditEvent `json:"audit,omitempty"`
	Unknown     bool         `json:"unknown"`
	Reason      string       `json:"reason,omitempty"`
	Err         error        `json:"-"`
	BudgetUsed  int          `json:"budget_used"`
	VirtualExit int          `json:"virtual_exit"`
	Branches    []BranchJoin `json:"branches,omitempty"`
	States      []NodeState  `json:"states,omitempty"`
}

// HasLeak reports whether at least one public write was rejected.
func (r Result) HasLeak() bool {
	return len(r.Leaks) > 0
}

// HasLeaks is an alias for HasLeak.
func (r Result) HasLeaks() bool {
	return r.HasLeak()
}

// OK reports whether analysis completed and found no leak.
func (r Result) OK() bool {
	return r.Safe
}

// Analyze runs the post-dominance-based program-counter label fixpoint. It
// accepts either Program or *Program. Optional arguments may be Options,
// label lists, or a positive integer analysis budget.
func Analyze(program any, options ...any) Result {
	return analyze(program, parseOptions(options))
}

// AnalyzeWithOptions is the typed form of Analyze.
func AnalyzeWithOptions(program any, options Options) Result {
	return analyze(program, options)
}

// Check is an alias for Analyze.
func Check(program any, options ...any) Result {
	return Analyze(program, options...)
}

// AnalyzeProgram returns a validation/analysis error in the second result.
// Budget exhaustion is reported through Result.Unknown, not as an error.
func AnalyzeProgram(program any, options ...any) (Result, error) {
	result := Analyze(program, options...)
	if result.Err != nil && !errors.Is(result.Err, ErrBudgetExceeded) {
		return result, result.Err
	}
	return result, nil
}

func analyze(program any, options Options) Result {
	p, err := asProgram(program)
	if err != nil {
		return Result{Unknown: true, Reason: err.Error(), Err: err}
	}
	if err := validateProgram(p); err != nil {
		return Result{Unknown: true, Reason: err.Error(), Err: err}
	}
	postdom, err := computePostDominance(p)
	if err != nil {
		return Result{Unknown: true, Reason: err.Error(), Err: err}
	}

	result := Result{
		VirtualExit: postdom.VirtualExit,
		Branches:    append([]BranchJoin(nil), postdom.Branches...),
	}
	joins := make(map[int]BranchJoin, len(postdom.BranchByNode))
	for branch, join := range postdom.BranchByNode {
		if join.IntroducesPC {
			joins[branch] = join
		}
	}

	states := make([]*analysisState, len(p.Nodes))
	initial := newAnalysisState()
	for _, input := range p.Inputs {
		initial.regs[input.Register] = initial.regs[input.Register].Union(input.Labels)
	}
	states[p.Entry] = initial

	queue := []int{p.Entry}
	queued := make([]bool, len(p.Nodes))
	queued[p.Entry] = true
	budget := options.Budget
	if budget <= 0 {
		budget = defaultAnalysisBudget
	}
	used := 0
	unknown := false

	for len(queue) > 0 {
		if used >= budget {
			unknown = true
			break
		}
		node := queue[0]
		queue = queue[1:]
		queued[node] = false
		used++

		state := states[node]
		if state == nil {
			continue
		}
		out, _, _ := transfer(p.Nodes[node].Instr, state, options)
		if p.Nodes[node].terminal() {
			continue
		}
		targets, _ := p.Nodes[node].outgoing()
		branch, isBranch := joins[node]
		var conditionLabels LabelSet
		if isBranch && len(targets) >= 2 && targets[0] != targets[1] {
			conditionLabels = state.regs[p.Nodes[node].Instr.Src1].
				Union(state.regs[p.Nodes[node].Instr.Src2])
		}

		for _, target := range targets {
			next := out.clone()
			for branchNode, join := range joins {
				if join.Join == target {
					delete(next.ctrl, branchNode)
				}
			}
			if isBranch && branch.IntroducesPC && target != branch.Join && !conditionLabels.Empty() {
				next.ctrl[node] = next.ctrl[node].Union(conditionLabels)
			}
			if states[target] == nil {
				states[target] = next
				if !queued[target] {
					queue = append(queue, target)
					queued[target] = true
				}
				continue
			}
			if states[target].mergeFrom(next) && !queued[target] {
				queue = append(queue, target)
				queued[target] = true
			}
		}
	}

	result.BudgetUsed = used
	if unknown {
		result.Unknown = true
		result.Reason = ErrBudgetExceeded.Error()
		result.Err = ErrBudgetExceeded
	}

	leaks := make(map[int]Leak)
	audits := make([]AuditEvent, 0)
	for node, state := range states {
		if state == nil {
			continue
		}
		_, leak, audit := transfer(p.Nodes[node].Instr, state, options)
		if leak != nil {
			leak.Node = node
			leaks[node] = *leak
		}
		if audit != nil {
			audit.Node = node
			audits = append(audits, *audit)
		}
		result.States = append(result.States, nodeState(node, state))
	}
	for _, leak := range leaks {
		result.Leaks = append(result.Leaks, leak)
	}
	sort.Slice(result.Leaks, func(i, j int) bool {
		if result.Leaks[i].Node != result.Leaks[j].Node {
			return result.Leaks[i].Node < result.Leaks[j].Node
		}
		return result.Leaks[i].Output < result.Leaks[j].Output
	})
	sort.Slice(audits, func(i, j int) bool {
		return audits[i].Node < audits[j].Node
	})
	result.Audit = audits
	result.Safe = !result.Unknown && result.Err == nil && len(result.Leaks) == 0
	return result
}

type analysisState struct {
	regs map[Register]LabelSet
	ctrl map[int]LabelSet
}

func newAnalysisState() *analysisState {
	return &analysisState{
		regs: make(map[Register]LabelSet),
		ctrl: make(map[int]LabelSet),
	}
}

func (s *analysisState) clone() *analysisState {
	result := newAnalysisState()
	for reg, labels := range s.regs {
		if !labels.Empty() {
			result.regs[reg] = labels.Clone()
		}
	}
	for branch, labels := range s.ctrl {
		if !labels.Empty() {
			result.ctrl[branch] = labels.Clone()
		}
	}
	return result
}

func (s *analysisState) mergeFrom(other *analysisState) bool {
	changed := false
	for reg, labels := range other.regs {
		merged := s.regs[reg].Union(labels)
		if !merged.Equal(s.regs[reg]) {
			s.regs[reg] = merged
			changed = true
		}
	}
	for branch, labels := range other.ctrl {
		merged := s.ctrl[branch].Union(labels)
		if !merged.Equal(s.ctrl[branch]) {
			s.ctrl[branch] = merged
			changed = true
		}
	}
	return changed
}

func (s *analysisState) pcLabels() LabelSet {
	var result LabelSet
	for _, labels := range s.ctrl {
		result = result.Union(labels)
	}
	return result
}

func (s *analysisState) setRegister(reg Register, labels LabelSet) {
	labels = canonicalLabels(labels)
	if labels.Empty() {
		delete(s.regs, reg)
		return
	}
	s.regs[reg] = labels
}

func transfer(instr Instruction, state *analysisState, options Options) (*analysisState, *Leak, *AuditEvent) {
	out := state.clone()
	pc := state.pcLabels()
	switch instr.Op {
	case OpConst:
		out.setRegister(instr.Dst, pc)
	case OpCopy:
		out.setRegister(instr.Dst, state.regs[instr.Src1].Union(pc))
	case OpAdd, OpSub, OpMul:
		out.setRegister(instr.Dst, state.regs[instr.Src1].Union(state.regs[instr.Src2], pc))
	case OpSelect:
		out.setRegister(instr.Dst,
			state.regs[instr.Src1].Union(state.regs[instr.Src2], state.regs[instr.Src3], pc))
	case OpPublicWrite:
		valueLabels := state.regs[instr.Src1].Clone()
		pcLabels := pc.Clone()
		if !valueLabels.Empty() || !pcLabels.Empty() {
			kind := LeakBoth
			switch {
			case valueLabels.Empty():
				kind = LeakPC
			case pcLabels.Empty():
				kind = LeakValue
			}
			leak := &Leak{
				Output:      instr.Output,
				Register:    instr.Src1,
				ValueLabels: valueLabels,
				PCLabels:    pcLabels,
				Kind:        kind,
			}
			return out, leak, nil
		}
	case OpDeclassify:
		requested := canonicalLabels(instr.Labels)
		allowed := requested.Intersect(options.whitelist())
		old := state.regs[instr.Dst]
		removed := old.Intersect(allowed)
		remaining := old.Minus(allowed)
		out.setRegister(instr.Dst, remaining)
		audit := &AuditEvent{
			Register:  instr.Dst,
			Requested: requested,
			Removed:   removed,
			Remaining: remaining.Clone(),
			PCLabels:  pc.Clone(),
		}
		return out, nil, audit
	}
	return out, nil, nil
}

func nodeState(node int, state *analysisState) NodeState {
	result := NodeState{
		Node:      node,
		Registers: make(map[Register]LabelSet, len(state.regs)),
		PC:        state.pcLabels(),
		Control:   make(map[int]LabelSet, len(state.ctrl)),
	}
	for reg, labels := range state.regs {
		if !labels.Empty() {
			result.Registers[reg] = labels.Clone()
		}
	}
	for branch, labels := range state.ctrl {
		if !labels.Empty() {
			result.Control[branch] = labels.Clone()
		}
	}
	return result
}

func (r Result) String() string {
	if r.Err != nil {
		return fmt.Sprintf("unknown (%v)", r.Err)
	}
	if r.Unknown {
		return "unknown (" + r.Reason + ")"
	}
	if r.HasLeak() {
		return fmt.Sprintf("%d leak(s)", len(r.Leaks))
	}
	return "safe"
}
