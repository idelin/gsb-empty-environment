package policycheck

import (
	"fmt"
	"sort"
	"strings"
)

// ViolationCode 标识等价校验失败类型。
type ViolationCode string

const (
	ViolationRelaxedAction   ViolationCode = "relaxed_action"
	ViolationStrongerInZone  ViolationCode = "stronger_action_in_transparent_zone"
	ViolationActionMismatch  ViolationCode = "action_mismatch"
	ViolationPayloadMismatch ViolationCode = "payload_mismatch"
	ViolationMissingAudit    ViolationCode = "missing_audit_obligation"
	ViolationMissingNotify   ViolationCode = "missing_notify_obligation"
	ViolationExtraAudit      ViolationCode = "extra_audit_obligation"
	ViolationExtraNotify     ViolationCode = "extra_notify_obligation"
	ViolationStructuralDAG   ViolationCode = "structural_dag"
	ViolationEvaluation      ViolationCode = "evaluation_error"
)

// Violation 是一个具体等价偏差。
type Violation struct {
	Code   ViolationCode `json:"code"`
	Detail string        `json:"detail"`
}

// AtomAssignment 是一个原子谓词在符号立方体中的取值。
type AtomAssignment struct {
	Key        string `json:"key"`
	Expression string `json:"expression"`
	Value      bool   `json:"value"`
}

// Region 是一个符号输入区域。
//
// 完整 AtomTruth 最小项之间两两不相交；Witness 非空时给出该区域
// 内的一个具体合法输入。ExactPoint 为真时该区域就是 Witness 单点。
type Region struct {
	Arch        Arch             `json:"arch,omitempty"`
	Nr          *uint64          `json:"nr,omitempty"`
	ExactPoint  bool             `json:"exact_point,omitempty"`
	AtomTruth   map[string]bool  `json:"atom_truth,omitempty"`
	Assignments []AtomAssignment `json:"assignments,omitempty"`
	Literals    []string         `json:"literals,omitempty"`
	Witness     *Input           `json:"witness,omitempty"`
	Satisfiable bool             `json:"satisfiable,omitempty"`
	// WitnessFound 表示已找到满足完整赋值的具体输入。
	WitnessFound bool `json:"witness_found,omitempty"`
}

// Mismatch 是某个不相交区域上的双方决定差异。
type Mismatch struct {
	Violations []Violation `json:"violations"`
	Region     Region      `json:"region"`
	Reference  Decision    `json:"reference"`
	Candidate  Decision    `json:"candidate"`
}

// Code 返回首个违规码，便于调用方分类。
func (m Mismatch) Code() ViolationCode {
	if len(m.Violations) == 0 {
		return ""
	}
	return m.Violations[0].Code
}

// Zone 是调用方指定必须逐动作完全等价的透明区域。
type Zone struct {
	Archs    []Arch      `json:"archs,omitempty"`
	NrRanges [][2]uint64 `json:"nr_ranges,omitempty"`
	Pred     *Pred       `json:"pred,omitempty"`
}

// ContainsInput 报告具体输入是否位于透明区。
func (z *Zone) ContainsInput(in Input, reg Registry) (bool, error) {
	if z == nil {
		return false, nil
	}
	pred := z.predicate()
	if pred == nil {
		return true, nil
	}
	return pred.Eval(in, reg)
}

func (z *Zone) containsSymbolic(values map[string]bool) (bool, error) {
	if z == nil {
		return false, nil
	}
	if z.Pred != nil {
		return evalSymbolic(z.Pred, values)
	}
	// Arch/Nr 的区域约束通常也会作为策略/DAG 原子谓词出现；若调用方
	// 只提供具体区间，则符号模式无法仅靠赋值判断，保守视为区域内。
	return true, nil
}

// Options 控制等价校验强度和搜索范围。
type Options struct {
	Registry Registry `json:"-"`
	// SigningKey 非空时，Verify 会在报告中附带 HMAC 签名的溯源证书。
	SigningKey []byte `json:"-"`
	// Legal 声明合法双架构输入域；nil 时调用号按整个 uint64 空间处理。
	Legal *LegalDomain `json:"legal,omitempty"`
	// Transparent 指定必须完全等价的区域。
	Transparent *Zone `json:"transparent,omitempty"`
	// Strict 要求整个空间都逐动作、载荷、义务完全一致。
	Strict bool `json:"strict,omitempty"`
	// Exhaustive 非空时执行小位宽全枚举。
	Exhaustive *ExhaustiveConfig `json:"exhaustive,omitempty"`
	// MaxAssignments 限制符号最小项数量；0 使用默认值。
	MaxAssignments int `json:"max_assignments,omitempty"`
	// MaxMismatches 限制返回的不相交差异区域数量；0 使用默认值 64。
	MaxMismatches int `json:"max_mismatches,omitempty"`
	// Solver 可注入完整 64 位区域求解器。
	Solver WitnessSolver `json:"-"`
	// SkipSymbolic 关闭符号最小项证明，仅在调用方明确需要时使用。
	SkipSymbolic bool `json:"skip_symbolic,omitempty"`
}

// LegalDomain 描述宿主认可的合法输入域。
type LegalDomain struct {
	// Numbers 为每个架构指定合法调用号；nil/缺省表示整个 uint64 调用号空间。
	Numbers map[Arch][]uint64 `json:"numbers,omitempty"`
}

func (d *LegalDomain) numbersFor(arch Arch) ([]uint64, bool) {
	if d == nil || d.Numbers == nil {
		return nil, false
	}
	values, ok := d.Numbers[arch]
	return values, ok
}

// Report 是等价校验结果和证明材料。
type Report struct {
	OK                 bool         `json:"ok"`
	Equivalent         bool         `json:"equivalent"`
	Strict             bool         `json:"strict"`
	SymbolicRun        bool         `json:"symbolic_run"`
	AssignmentCount    int          `json:"assignment_count,omitempty"`
	SymbolicComplete   bool         `json:"symbolic_complete,omitempty"`
	CheckedInputs      uint64       `json:"checked_inputs,omitempty"`
	ExhaustiveComplete bool         `json:"exhaustive_complete,omitempty"`
	Mismatches         []Mismatch   `json:"mismatches,omitempty"`
	NamedPredicates    []string     `json:"named_predicates,omitempty"`
	Certificate        *Certificate `json:"certificate,omitempty"`
}

// VerificationError 包装一份失败报告，便于 if err != nil 风格宿主。
type VerificationError struct{ Report *Report }

func (e *VerificationError) Error() string {
	if e == nil || e.Report == nil || len(e.Report.Mismatches) == 0 {
		return "policycheck: equivalence verification failed"
	}
	m := e.Report.Mismatches[0]
	parts := make([]string, 0, len(m.Violations))
	for _, v := range m.Violations {
		parts = append(parts, string(v.Code)+": "+v.Detail)
	}
	return "policycheck: equivalence verification failed: " + strings.Join(parts, "; ")
}

// Err 返回包含完整报告的错误；OK 时返回 nil。
func (r *Report) Err() error {
	if r == nil || r.OK {
		return nil
	}
	return &VerificationError{Report: r}
}

// Verify 校验参考规则链与优化 DAG。
func Verify(policy *Policy, dag *DAG, opts *Options) (*Report, error) {
	if opts == nil {
		opts = &Options{}
	} else {
		optionsCopy := *opts
		opts = &optionsCopy
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if err := dag.ValidateForward(); err != nil {
		return nil, err
	}

	report := &Report{OK: true, Strict: opts.Strict}
	report.NamedPredicates = CollectNamedNames(appendPolicyPreds(policy, dag, opts)...)
	if cert, certErr := BuildProvenanceWithRegistry(policy, dag, opts.Registry, opts.SigningKey); certErr == nil {
		report.Certificate = cert
	} else {
		return nil, certErr
	}

	if !opts.SkipSymbolic {
		if err := verifySymbolic(policy, dag, opts, report); err != nil {
			return nil, err
		}
	}

	if opts.Exhaustive != nil {
		recordConcrete := opts.SkipSymbolic || len(report.Mismatches) == 0
		if err := verifyExhaustive(policy, dag, opts, report, recordConcrete); err != nil {
			return nil, err
		}
	}

	if len(report.Mismatches) > 0 {
		report.OK = false
	}
	report.Equivalent = report.OK
	return report, nil
}

// CheckEquivalence 是 Verify 的语义化别名。
func CheckEquivalence(policy *Policy, dag *DAG, opts *Options) (*Report, error) {
	return Verify(policy, dag, opts)
}

// VerifyOrError 与 Verify 相同，但在发现策略差异时直接返回报告错误。
func VerifyOrError(policy *Policy, dag *DAG, opts *Options) (*Report, error) {
	report, err := Verify(policy, dag, opts)
	if err != nil {
		return report, err
	}
	return report, report.Err()
}

func appendPolicyPreds(policy *Policy, dag *DAG, opts *Options) []*Pred {
	var preds []*Pred
	if policy != nil {
		for i := range policy.Rules {
			preds = append(preds, policy.Rules[i].When)
		}
	}
	if dag != nil {
		for _, n := range dag.Nodes {
			if n != nil && n.Test != nil {
				preds = append(preds, n.Test)
			}
		}
	}
	if opts != nil && opts.Transparent != nil {
		preds = append(preds, opts.Transparent.predicate())
	}
	return preds
}

func maxMismatches(opts *Options) int {
	if opts != nil && opts.MaxMismatches > 0 {
		return opts.MaxMismatches
	}
	return 64
}

func compareDecisions(ref, got Decision, transparent, strict bool) []Violation {
	var violations []Violation

	if got.Action.Rank() < ref.Action.Rank() {
		violations = append(violations, Violation{
			Code:   ViolationRelaxedAction,
			Detail: fmt.Sprintf("candidate %s is weaker than reference %s", got.Action, ref.Action),
		})
	}

	if transparent || strict {
		if got.Action != ref.Action {
			code := ViolationActionMismatch
			detail := fmt.Sprintf("action differs: reference=%s candidate=%s", ref.Action, got.Action)
			if transparent && got.Action.StrongerThan(ref.Action) {
				code = ViolationStrongerInZone
				detail = "transparent zone requires exact action; " + detail
			}
			violations = append(violations, Violation{Code: code, Detail: detail})
		}
		if !got.Payload.Equal(ref.Payload) {
			violations = append(violations, Violation{
				Code:   ViolationPayloadMismatch,
				Detail: fmt.Sprintf("payload differs: reference=%s candidate=%s", ref.Payload, got.Payload),
			})
		}
		if !equalStrings(got.Audit, ref.Audit) {
			violations = append(violations, Violation{
				Code:   ViolationExtraAudit,
				Detail: fmt.Sprintf("audit obligations differ exactly: reference=%v candidate=%v", ref.Audit, got.Audit),
			})
		}
		if !equalStrings(got.Notify, ref.Notify) {
			violations = append(violations, Violation{
				Code:   ViolationExtraNotify,
				Detail: fmt.Sprintf("notify obligations differ exactly: reference=%v candidate=%v", ref.Notify, got.Notify),
			})
		}
		return violations
	}

	// 非透明区允许加强动作，但同动作时载荷不能串号或被旧值覆盖。
	if got.Action == ref.Action && !got.Payload.Equal(ref.Payload) {
		violations = append(violations, Violation{
			Code: ViolationPayloadMismatch,
			Detail: fmt.Sprintf("same action %s but payload differs: reference=%s candidate=%s",
				ref.Action, ref.Payload, got.Payload),
		})
	}
	if ref.Payload.PassthroughNR != nil && !sameUintPtr(ref.Payload.PassthroughNR, got.Payload.PassthroughNR) {
		violations = append(violations, Violation{
			Code:   ViolationPayloadMismatch,
			Detail: fmt.Sprintf("syscall-number passthrough payload scrambled: reference_nr=%d candidate_nr=%v", *ref.Payload.PassthroughNR, got.Payload.PassthroughNR),
		})
	}
	if missing := MissingObligations(ref.Audit, got.Audit); len(missing) > 0 {
		violations = append(violations, Violation{
			Code:   ViolationMissingAudit,
			Detail: fmt.Sprintf("missing audit labels %v; reference=%v candidate=%v", missing, ref.Audit, got.Audit),
		})
	}
	if missing := MissingObligations(ref.Notify, got.Notify); len(missing) > 0 {
		violations = append(violations, Violation{
			Code:   ViolationMissingNotify,
			Detail: fmt.Sprintf("missing notify labels %v; reference=%v candidate=%v", missing, ref.Notify, got.Notify),
		})
	}
	return violations
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameUintPtr(a, b *uint64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
