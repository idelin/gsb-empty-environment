package policycheck

// 常见架构命名别名。
const (
	ArchAMD64 = ArchX86_64
	ArchX64   = ArchX86_64
	ArchI386  = ArchIA32
	ArchX86   = ArchIA32
)

// Priority 是 Rank 的术语别名。
func (a Action) Priority() int { return a.Rank() }

// ArgEquals 是 ArgsEqual 的易名构造器。
func ArgEquals(i, j int) *Pred { return ArgsEqual(i, j) }

// ArgEq 是 ArgsEqual 的短别名。
func ArgEq(i, j int) *Pred { return ArgsEqual(i, j) }

// MaskedArgLE 是 ArgMaskLEArg 的易名构造器。
func MaskedArgLE(i int, mask uint64, j int) *Pred { return ArgMaskLEArg(i, mask, j) }

// ArgMaskLE 是 ArgMaskLEArg 的短别名。
func ArgMaskLE(i int, mask uint64, j int) *Pred { return ArgMaskLEArg(i, mask, j) }

// Result 是报告的术语别名。
type Result = Report

// Counterexample 是差异区域及双方决定的术语别名。
type Counterexample = Mismatch

// SymbolicRegion 是符号区域的术语别名。
type SymbolicRegion = Region

// VerifyPolicyAndDAG 是面向宿主的描述性别名。
func VerifyPolicyAndDAG(policy *Policy, dag *DAG, opts *Options) (*Report, error) {
	return Verify(policy, dag, opts)
}

// EquivalenceChecker 保存可复用的校验配置。
type EquivalenceChecker struct {
	Options *Options
}

// NewEquivalenceChecker 创建检查器；nil 配置使用默认严格前向和符号校验。
func NewEquivalenceChecker(opts *Options) *EquivalenceChecker {
	return &EquivalenceChecker{Options: opts}
}

// Check 等价于 Verify(policy, dag, c.Options)。
func (c *EquivalenceChecker) Check(policy *Policy, dag *DAG) (*Report, error) {
	var opts *Options
	if c != nil {
		opts = c.Options
	}
	return Verify(policy, dag, opts)
}

// Verify 是 Check 的别名。
func (c *EquivalenceChecker) Verify(policy *Policy, dag *DAG) (*Report, error) {
	return c.Check(policy, dag)
}

// PolicyEquivalenceChecker 是术语别名。
type PolicyEquivalenceChecker = EquivalenceChecker
