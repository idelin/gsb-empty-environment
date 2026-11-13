package netsec

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// MaxSecretInputs is the largest number of secret boolean inputs accepted by
// the concrete differential reference.
const MaxSecretInputs = 3

// Termination describes why concrete execution stopped.
type Termination string

const (
	TerminationNormal   Termination = "normal"
	TerminationReturn   Termination = "return"
	TerminationAbnormal Termination = "abnormal"
	TerminationBudget   Termination = "budget"
	TerminationError    Termination = "error"
)

// TraceEvent is one public write. Sensitive is true when the concrete value
// still carries a secret label or executes under an active secret program
// counter. It is used to distinguish permitted declassified differences from
// genuine leaks.
type TraceEvent struct {
	Output    int   `json:"output"`
	Value     uint8 `json:"value"`
	Sensitive bool  `json:"sensitive,omitempty"`
}

// Trace is an ordered sequence of public writes.
type Trace []TraceEvent

// Equal compares observable output events, including order and count.
func (t Trace) Equal(other Trace) bool {
	if len(t) != len(other) {
		return false
	}
	for i := range t {
		if t[i].Output != other[i].Output || t[i].Value != other[i].Value {
			return false
		}
	}
	return true
}

// SensitiveTrace returns the subsequence of writes that were not
// declassified.
func (t Trace) SensitiveTrace() Trace {
	result := make(Trace, 0, len(t))
	for _, event := range t {
		if event.Sensitive {
			result = append(result, event)
		}
	}
	return result
}

func (t Trace) String() string {
	parts := make([]string, 0, len(t))
	for _, event := range t {
		if event.Sensitive {
			parts = append(parts, fmt.Sprintf("%d:%d*", event.Output, event.Value))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%d", event.Output, event.Value))
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ExecConfig configures concrete reference execution.
type ExecConfig struct {
	StepLimit           int
	DeclassifyWhitelist LabelSet
	Whitelist           LabelSet
}

func (c ExecConfig) whitelist() LabelSet {
	return c.DeclassifyWhitelist.Union(c.Whitelist)
}

func parseExecConfig(configs []ExecConfig) ExecConfig {
	var result ExecConfig
	for _, config := range configs {
		result.DeclassifyWhitelist = result.whitelist().Union(config.whitelist())
		result.Whitelist = nil
		if config.StepLimit > 0 {
			result.StepLimit = config.StepLimit
		}
	}
	return result
}

// ConcreteResult is one concrete execution.
type ConcreteResult struct {
	Trace       Trace              `json:"trace"`
	Registers   map[Register]uint8 `json:"registers,omitempty"`
	Steps       int                `json:"steps"`
	Termination Termination        `json:"termination"`
	Unknown     bool               `json:"unknown"`
	Err         error              `json:"-"`
}

// RunConcrete executes a program with eight-bit values. secretValues
// overrides initial register values; public inputs keep the values declared
// by Program.Inputs. The executor also tracks labels so the differential
// checker can tell a declassified output from a genuine leak.
func RunConcrete(program any, secretValues map[Register]uint8, configs ...ExecConfig) ConcreteResult {
	p, err := asProgram(program)
	if err != nil {
		return ConcreteResult{Unknown: true, Termination: TerminationError, Err: err}
	}
	if err := validateProgram(p); err != nil {
		return ConcreteResult{Unknown: true, Termination: TerminationError, Err: err}
	}
	postdom, err := computePostDominance(p)
	if err != nil {
		return ConcreteResult{Unknown: true, Termination: TerminationError, Err: err}
	}
	config := parseExecConfig(configs)
	limit := config.StepLimit
	if limit <= 0 {
		limit = defaultExecutionSteps
	}
	return runConcrete(p, secretValues, config.whitelist(), limit, postdom.BranchByNode)
}

type concreteState struct {
	values map[Register]uint8
	labels map[Register]LabelSet
	ctrl   map[int]LabelSet
}

func (s *concreteState) clone() *concreteState {
	result := &concreteState{
		values: make(map[Register]uint8, len(s.values)),
		labels: make(map[Register]LabelSet, len(s.labels)),
		ctrl:   make(map[int]LabelSet, len(s.ctrl)),
	}
	for reg, value := range s.values {
		result.values[reg] = value
	}
	for reg, labels := range s.labels {
		if !labels.Empty() {
			result.labels[reg] = labels.Clone()
		}
	}
	for branch, labels := range s.ctrl {
		if !labels.Empty() {
			result.ctrl[branch] = labels.Clone()
		}
	}
	return result
}

func (s *concreteState) pcLabels() LabelSet {
	var result LabelSet
	for _, labels := range s.ctrl {
		result = result.Union(labels)
	}
	return result
}

func (s *concreteState) setLabels(reg Register, labels LabelSet) {
	labels = canonicalLabels(labels)
	if labels.Empty() {
		delete(s.labels, reg)
		return
	}
	s.labels[reg] = labels
}

func runConcrete(p *Program, secretValues map[Register]uint8, whitelist LabelSet, limit int,
	joins map[int]BranchJoin) ConcreteResult {
	state := &concreteState{
		values: make(map[Register]uint8),
		labels: make(map[Register]LabelSet),
		ctrl:   make(map[int]LabelSet),
	}
	for _, input := range p.Inputs {
		state.values[input.Register] = input.Value
		state.setLabels(input.Register, input.Labels)
	}
	for reg, value := range secretValues {
		state.values[reg] = value
	}

	result := ConcreteResult{Termination: TerminationNormal, Registers: make(map[Register]uint8)}
	node := p.Entry
	for steps := 0; ; steps++ {
		result.Steps = steps
		if steps >= limit {
			result.Unknown = true
			result.Termination = TerminationBudget
			result.Err = ErrExecutionBudget
			result.Registers = copyValues(state.values)
			return result
		}
		if node < 0 || node >= len(p.Nodes) {
			result.Unknown = true
			result.Termination = TerminationError
			result.Err = fmt.Errorf("%w: execution reached invalid node %d", ErrInvalidProgram, node)
			result.Registers = copyValues(state.values)
			return result
		}
		cfgNode := p.Nodes[node]
		instr := cfgNode.Instr
		next := -1
		termination := Termination("")

		switch instr.Op {
		case OpNop:
		case OpConst:
			state.values[instr.Dst] = instr.Imm
			state.setLabels(instr.Dst, state.pcLabels())
		case OpCopy:
			state.values[instr.Dst] = state.values[instr.Src1]
			state.setLabels(instr.Dst, state.labels[instr.Src1].Union(state.pcLabels()))
		case OpAdd:
			state.values[instr.Dst] = state.values[instr.Src1] + state.values[instr.Src2]
			state.setLabels(instr.Dst, state.labels[instr.Src1].Union(state.labels[instr.Src2], state.pcLabels()))
		case OpSub:
			state.values[instr.Dst] = state.values[instr.Src1] - state.values[instr.Src2]
			state.setLabels(instr.Dst, state.labels[instr.Src1].Union(state.labels[instr.Src2], state.pcLabels()))
		case OpMul:
			state.values[instr.Dst] = state.values[instr.Src1] * state.values[instr.Src2]
			state.setLabels(instr.Dst, state.labels[instr.Src1].Union(state.labels[instr.Src2], state.pcLabels()))
		case OpSelect:
			if state.values[instr.Src1] != 0 {
				state.values[instr.Dst] = state.values[instr.Src2]
			} else {
				state.values[instr.Dst] = state.values[instr.Src3]
			}
			state.setLabels(instr.Dst,
				state.labels[instr.Src1].Union(state.labels[instr.Src2], state.labels[instr.Src3],
					state.pcLabels()))
		case OpPublicWrite:
			valueLabels := state.labels[instr.Src1].Clone()
			pcLabels := state.pcLabels()
			result.Trace = append(result.Trace, TraceEvent{
				Output:    instr.Output,
				Value:     state.values[instr.Src1],
				Sensitive: !valueLabels.Empty() || !pcLabels.Empty(),
			})
		case OpDeclassify:
			allowed := canonicalLabels(instr.Labels).Intersect(whitelist)
			state.setLabels(instr.Dst, state.labels[instr.Dst].Minus(allowed))
		case OpEqBranch:
			targets, _ := cfgNode.outgoing()
			if len(targets) != 2 {
				result.Unknown = true
				result.Termination = TerminationError
				result.Err = fmt.Errorf("%w: branch node %d has %d successors", ErrInvalidProgram, node, len(targets))
				result.Registers = copyValues(state.values)
				return result
			}
			if state.values[instr.Src1] == state.values[instr.Src2] {
				next = targets[0]
			} else {
				next = targets[1]
			}
			result.Registers = copyValues(state.values)
		case OpReturn, OpAbort:
			termination = TerminationReturn
			if instr.Op == OpAbort || cfgNode.AbnormalExit {
				termination = TerminationAbnormal
			}
		default:
			result.Unknown = true
			result.Termination = TerminationError
			result.Err = fmt.Errorf("%w: node %d has unknown op %d", ErrInvalidProgram, node, instr.Op)
			result.Registers = copyValues(state.values)
			return result
		}

		if termination != "" {
			result.Termination = termination
			result.Registers = copyValues(state.values)
			return result
		}
		if cfgNode.terminal() {
			result.Termination = TerminationNormal
			if cfgNode.Return {
				result.Termination = TerminationReturn
			} else if cfgNode.AbnormalExit {
				result.Termination = TerminationAbnormal
			}
			result.Registers = copyValues(state.values)
			return result
		}

		if next < 0 {
			targets, _ := cfgNode.outgoing()
			if len(targets) == 0 {
				result.Termination = TerminationNormal
				result.Registers = copyValues(state.values)
				return result
			}
			next = targets[0]
		}

		join, isBranch := joins[node]
		for branchNode, branchJoin := range joins {
			if branchJoin.Join == next {
				delete(state.ctrl, branchNode)
			}
		}
		if isBranch && instr.Op == OpEqBranch {
			if join.IntroducesPC && next != join.Join {
				if condition := stateBranchCondition(p.Nodes[node], state); !condition.Empty() {
					state.ctrl[node] = state.ctrl[node].Union(condition)
				}
			}
		}
		node = next
	}
}

func stateBranchCondition(node Node, state *concreteState) LabelSet {
	return state.labels[node.Instr.Src1].Union(state.labels[node.Instr.Src2])
}

func copyValues(values map[Register]uint8) map[Register]uint8 {
	result := make(map[Register]uint8, len(values))
	for reg, value := range values {
		result[reg] = value
	}
	return result
}

// SecretAssignment maps each secret boolean input register to zero or one.
type SecretAssignment struct {
	Values map[Register]uint8 `json:"values"`
}

// Equal compares assignments by register and value.
func (a SecretAssignment) Equal(other SecretAssignment) bool {
	if len(a.Values) != len(other.Values) {
		return false
	}
	for reg, value := range a.Values {
		if otherValue, ok := other.Values[reg]; !ok || otherValue != value {
			return false
		}
	}
	return true
}

func (a SecretAssignment) String() string {
	registers := make([]int, 0, len(a.Values))
	for reg := range a.Values {
		registers = append(registers, int(reg))
	}
	sort.Ints(registers)
	parts := make([]string, 0, len(registers))
	for _, reg := range registers {
		parts = append(parts, fmt.Sprintf("r%d=%d", reg, a.Values[Register(reg)]))
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// TracePair records the two traces of a secret assignment pair.
type TracePair struct {
	Left       SecretAssignment `json:"left"`
	Right      SecretAssignment `json:"right"`
	LeftTrace  Trace            `json:"left_trace"`
	RightTrace Trace            `json:"right_trace"`
}

// CheckReport is the differential soundness check for the static result.
// Missed is empty for a sound analysis. Conservative lists pairs where the
// analysis flagged a leak but concrete traces were equal; those false
// positives are permitted by the contract.
type CheckReport struct {
	Analysis     Result             `json:"analysis"`
	Assignments  []SecretAssignment `json:"assignments,omitempty"`
	Executions   int                `json:"executions"`
	Pairs        int                `json:"pairs"`
	Covered      int                `json:"covered"`
	Differences  []TracePair        `json:"differences,omitempty"`
	Missed       []TracePair        `json:"missed,omitempty"`
	Declassified []TracePair        `json:"declassified,omitempty"`
	Inconclusive []TracePair        `json:"inconclusive,omitempty"`
	Conservative []TracePair        `json:"conservative,omitempty"`
	Unknown      bool               `json:"unknown"`
	Err          error              `json:"-"`
}

// Sound reports that the analysis found every sensitive concrete difference.
// Conservative false positives do not make the result unsound.
func (r CheckReport) Sound() bool {
	return r.Err == nil && !r.Unknown && len(r.Missed) == 0
}

// OK is an alias for Sound.
func (r CheckReport) OK() bool {
	return r.Sound()
}

// CrossCheck enumerates all secret boolean assignments (at most three
// inputs), executes each pair with identical public inputs, and checks that
// every sensitive trace difference is covered by a static leak. Differences
// that occur only in declassified writes are accepted.
func CrossCheck(program any, options ...any) CheckReport {
	opt := parseOptions(options)
	analysis := analyze(program, opt)
	report := CheckReport{Analysis: analysis, Unknown: analysis.Unknown}
	p, err := asProgram(program)
	if err != nil {
		report.Err = err
		report.Unknown = true
		return report
	}
	if err := validateProgram(p); err != nil {
		report.Err = err
		report.Unknown = true
		return report
	}
	secretInputs := secretInputsOf(p)
	if len(secretInputs) > MaxSecretInputs {
		report.Err = ErrTooManySecrets
		report.Unknown = true
		return report
	}
	assignments := enumerateAssignments(secretInputs)
	report.Assignments = assignments

	execConfig := ExecConfig{
		StepLimit:           opt.executionLimit(),
		DeclassifyWhitelist: opt.whitelist(),
	}
	runs := make([]ConcreteResult, len(assignments))
	for index, assignment := range assignments {
		runs[index] = RunConcrete(p, assignment.Values, execConfig)
		report.Executions++
		if runs[index].Unknown {
			report.Unknown = true
			if report.Err == nil {
				report.Err = runs[index].Err
			}
		}
	}

	for left := 0; left < len(assignments); left++ {
		for right := left + 1; right < len(assignments); right++ {
			report.Pairs++
			pair := TracePair{
				Left:       assignments[left],
				Right:      assignments[right],
				LeftTrace:  runs[left].Trace,
				RightTrace: runs[right].Trace,
			}
			if runs[left].Unknown || runs[right].Unknown {
				report.Inconclusive = append(report.Inconclusive, pair)
				continue
			}
			if !pair.LeftTrace.Equal(pair.RightTrace) {
				report.Differences = append(report.Differences, pair)
				if pair.LeftTrace.SensitiveTrace().Equal(pair.RightTrace.SensitiveTrace()) {
					report.Declassified = append(report.Declassified, pair)
					continue
				}
				switch {
				case analysis.Err != nil && !errorsIsBudget(analysis.Err):
					report.Inconclusive = append(report.Inconclusive, pair)
					report.Err = analysis.Err
					report.Unknown = true
				case analysis.Unknown:
					report.Inconclusive = append(report.Inconclusive, pair)
				case analysis.HasLeak():
					report.Covered++
				default:
					report.Missed = append(report.Missed, pair)
				}
				continue
			}
			if analysis.HasLeak() {
				report.Conservative = append(report.Conservative, pair)
			}
		}
	}
	return report
}

// Verify is an alias for CrossCheck.
func Verify(program any, options ...any) CheckReport {
	return CrossCheck(program, options...)
}

func errorsIsBudget(err error) bool {
	return errors.Is(err, ErrBudgetExceeded)
}

func secretInputsOf(p *Program) []Input {
	result := make([]Input, 0, MaxSecretInputs)
	for _, input := range p.Inputs {
		if !input.Labels.Empty() {
			result = append(result, input)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Register < result[j].Register
	})
	return result
}

func enumerateAssignments(inputs []Input) []SecretAssignment {
	count := 1 << uint(len(inputs))
	result := make([]SecretAssignment, 0, count)
	for mask := 0; mask < count; mask++ {
		values := make(map[Register]uint8, len(inputs))
		for index, input := range inputs {
			if mask&(1<<uint(index)) != 0 {
				values[input.Register] = 1
			} else {
				values[input.Register] = 0
			}
		}
		result = append(result, SecretAssignment{Values: values})
	}
	return result
}
