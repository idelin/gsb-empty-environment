package netsec

import "fmt"

// Error 为证书核验失败错误，携带出错的步骤编号与字段名，供接入层定位拒绝原因。
// Step 为 -1 时表示错误位于证书级字段而非某一步。
type Error struct {
	Step   int
	Field  string
	Reason string
}

func (e *Error) Error() string {
	if e.Step >= 0 {
		return fmt.Sprintf("证书第 %d 步字段 %q 非法: %s", e.Step, e.Field, e.Reason)
	}
	return fmt.Sprintf("证书字段 %q 非法: %s", e.Field, e.Reason)
}

func fail(step int, field, format string, args ...any) *Error {
	return &Error{Step: step, Field: field, Reason: fmt.Sprintf(format, args...)}
}

// Verify 对证书做完整核验：结构检查、逐条规则推演，以及独立小域语义复核。
// 全部通过返回 nil，否则返回 *Error 指明拒绝位置。
func Verify(cert *Certificate) error {
	if cert == nil {
		return fail(-1, "certificate", "证书为 nil")
	}
	if len(cert.Program) == 0 {
		return fail(-1, "program", "原程序为空")
	}
	for i, instr := range cert.Program {
		if !instr.Kind.valid() {
			return fail(-1, "program", "第 %d 条指令类别 %d 非法", i, instr.Kind)
		}
		if instr.Kind == InstrInput && instr.Bounds.IsEmpty() {
			return fail(-1, "program", "第 %d 条输入指令边界为空", i)
		}
	}
	if len(cert.Steps) == 0 {
		return fail(-1, "steps", "证明步骤为空")
	}
	// claims 保存每一步经核验后成立的事实区间，供后续步骤引用。
	claims := make([]Interval, len(cert.Steps))
	for i := range cert.Steps {
		if err := checkStep(cert, i, claims); err != nil {
			return err
		}
		claims[i] = cert.Steps[i].Claim
	}
	if cert.Goal < 0 || cert.Goal >= len(cert.Steps) {
		return fail(-1, "goal", "最终结论编号 %d 越界（共 %d 步）", cert.Goal, len(cert.Steps))
	}
	return semanticVerify(cert)
}

// checkStep 核验第 i 步，claims 为此前各步已成立的事实。
func checkStep(cert *Certificate, i int, claims []Interval) error {
	st := cert.Steps[i]
	if !st.Rule.valid() {
		return fail(i, "rule", "规则编号 %d 非法", st.Rule)
	}
	if err := checkRefs(cert, i, st); err != nil {
		return err
	}
	if st.Instr < 0 || st.Instr >= len(cert.Program) {
		return fail(i, "instr", "绑定指令编号 %d 越界（共 %d 条指令）", st.Instr, len(cert.Program))
	}
	instr := cert.Program[st.Instr]

	var derived Interval
	switch st.Rule {
	case RuleContain:
		if len(st.Refs) == 0 {
			if instr.Kind != InstrInput {
				return fail(i, "instr", "无前提的区间包含规则须绑定输入指令，实际绑定类别 %d", instr.Kind)
			}
			derived = instr.Bounds
		} else {
			derived = claims[st.Refs[0]]
		}
	case RuleIntersect:
		derived = Intersect(claims[st.Refs[0]], claims[st.Refs[1]])
	case RuleAddConst:
		if instr.Kind != InstrAdd {
			return fail(i, "instr", "加常数规则须绑定加法指令，实际绑定类别 %d", instr.Kind)
		}
		derived = AddConst(claims[st.Refs[0]], instr.Const)
	case RuleJoin:
		if instr.Kind != InstrBranch {
			return fail(i, "instr", "分支合流规则须绑定分支指令，实际绑定类别 %d", instr.Kind)
		}
		derived = Hull(claims[st.Refs[0]], claims[st.Refs[1]])
	}
	if !st.Claim.SupersetOf(derived) {
		return fail(i, "claim", "非法强化：结论 [%d,%d] 未覆盖可推出区间 [%d,%d]",
			st.Claim.Lo, st.Claim.Hi, derived.Lo, derived.Hi)
	}
	return nil
}

// checkRefs 核验前提引用：数量匹配规则、编号不越界、严格小于本步编号（拒绝循环与前向引用）。
func checkRefs(cert *Certificate, i int, st Step) error {
	want := 0
	switch st.Rule {
	case RuleContain:
		want = -1 // 0 或 1
	case RuleAddConst:
		want = 1
	case RuleIntersect, RuleJoin:
		want = 2
	}
	if want >= 0 && len(st.Refs) != want {
		return fail(i, "refs", "规则 %d 需要 %d 个前提，实际 %d 个", st.Rule, want, len(st.Refs))
	}
	if want < 0 && len(st.Refs) > 1 {
		return fail(i, "refs", "区间包含规则至多 1 个前提，实际 %d 个", len(st.Refs))
	}
	for _, r := range st.Refs {
		if r < 0 || r >= len(cert.Steps) {
			return fail(i, "refs", "前提编号 %d 越界（共 %d 步）", r, len(cert.Steps))
		}
		if r >= i {
			return fail(i, "refs", "前提编号 %d 不小于本步编号 %d，构成循环或前向引用", r, i)
		}
	}
	return nil
}
