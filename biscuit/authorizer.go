package biscuit

import "fmt"

// Decision 是最终授权结论。
type Decision int

const (
	// DecisionAllow 命中第一条 allow 策略。
	DecisionAllow Decision = iota
	// DecisionDeny 明确拒绝：check 失败、deny 命中、无策略匹配或不动点超限。
	DecisionDeny
)

func (d Decision) String() string {
	if d == DecisionAllow {
		return "allow"
	}
	return "deny"
}

// Reason 细化拒绝原因，便于服务端记录审计日志。
type Reason int

const (
	// ReasonNone 结论为 allow 时使用。
	ReasonNone Reason = iota
	// ReasonCheckFailed 某个块的 check if 约束无法满足。
	ReasonCheckFailed
	// ReasonDenied 第一条匹配的策略是 deny。
	ReasonDenied
	// ReasonNoPolicy 所有 allow 策略都未命中，默认拒绝。
	ReasonNoPolicy
	// ReasonFixpoint 规则迭代超过 MaxFixpointRounds 轮仍未收敛。
	ReasonFixpoint
)

func (r Reason) String() string {
	switch r {
	case ReasonCheckFailed:
		return "check_failed"
	case ReasonDenied:
		return "deny_policy"
	case ReasonNoPolicy:
		return "no_matching_policy"
	case ReasonFixpoint:
		return "fixpoint_limit"
	default:
		return "none"
	}
}

// Result 是一次授权评估的结论与定位信息。
type Result struct {
	Decision    Decision
	Reason      Reason
	BlockID     int    // check 失败或不动点超限时所在的块；否则为 -1
	PolicyIndex int    // 命中的策略下标；否则为 -1
	Message     string // 人类可读的说明
}

func (r *Result) String() string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("decision=%s reason=%s block=%d policy=%d: %s",
		r.Decision, r.Reason, r.BlockID, r.PolicyIndex, r.Message)
}

// Authorizer 评估一个 Biscuit 令牌的全部块与一组服务端策略。
//
// 信任边界：blocks[0] 是 authority 块，其事实与规则推出的事实构成
// authority 作用域；衰减块（blocks[1:]）推出的事实只对本块 check 可见，
// 既不会进入 authority 作用域，也不会出现在 allow / deny 策略的事实集合中。
type Authorizer struct {
	blocks   []*Block
	policies []Policy
}

// New 构造 Authorizer。blocks 至少包含一个 authority 块；
// 传入的块与策略会做语法安全性校验（例如事实不允许含变量、
// 规则 head 变量必须出现在 body 中）。
func New(blocks []*Block, policies []Policy) (*Authorizer, error) {
	if len(blocks) == 0 {
		return nil, fmt.Errorf("biscuit: at least one authority block is required")
	}
	for id, block := range blocks {
		if block == nil {
			return nil, fmt.Errorf("biscuit: block %d is nil", id)
		}
		block.ID = id
		for _, fact := range block.Facts {
			if err := validateGroundFact(fact); err != nil {
				return nil, fmt.Errorf("biscuit: block %d: %w", id, err)
			}
		}
		for _, rule := range block.Rules {
			if err := validateRule(rule); err != nil {
				return nil, fmt.Errorf("biscuit: block %d: %w", id, err)
			}
		}
		for checkIndex, check := range block.Checks {
			if len(check.Queries) == 0 {
				return nil, fmt.Errorf("biscuit: block %d check %d has no queries", id, checkIndex)
			}
			for _, query := range check.Queries {
				if err := validateRule(query); err != nil {
					return nil, fmt.Errorf("biscuit: block %d check %d: %w", id, checkIndex, err)
				}
			}
		}
	}
	for index, policy := range policies {
		if len(policy.Queries) == 0 {
			return nil, fmt.Errorf("biscuit: policy %d has no queries", index)
		}
		for _, query := range policy.Queries {
			if err := validateRule(query); err != nil {
				return nil, fmt.Errorf("biscuit: policy %d: %w", index, err)
			}
		}
	}
	clonedBlocks := make([]*Block, len(blocks))
	copy(clonedBlocks, blocks)
	clonedPolicies := make([]Policy, len(policies))
	copy(clonedPolicies, policies)
	return &Authorizer{blocks: clonedBlocks, policies: clonedPolicies}, nil
}

// Authorize 执行完整评估：
//  1. 求值 authority 块的事实与规则，建立 authority 作用域；
//  2. 逐个在私有作用域内求值衰减块，运行该块全部 check；
//  3. 所有块 check 通过后，按顺序评估 allow / deny，第一条命中者决定结论；
//  4. 任一阶段不动点超限、check 失败或无策略命中都返回拒绝。
func (a *Authorizer) Authorize() *Result {
	authority := newFactSet()
	authorityBlock := a.blocks[0]
	authorityView := world{authority: authority}
	if err := solve(authorityBlock.Facts, authorityBlock.Rules, authority, authorityView, "authority"); err != nil {
		return fixpointResult(0, err)
	}

	// authority 块自身的 check 同样必须通过。
	if result := runChecks(authorityBlock, authorityView); result != nil {
		return result
	}

	for id := 1; id < len(a.blocks); id++ {
		block := a.blocks[id]
		local := newFactSet()
		// 合并视图包含只读的 authority 与本块私有事实；
		// 新事实只写入 local，head 谓词同名也不会污染 authority。
		view := world{authority: authority, local: local}
		if err := solve(block.Facts, block.Rules, local, view, fmt.Sprintf("block %d", id)); err != nil {
			return fixpointResult(id, err)
		}
		if result := runChecks(block, view); result != nil {
			return result
		}
	}

	// 策略只看 authority 作用域：衰减块事实在构造上就不在这里。
	policyView := world{authority: authority}
	for index, policy := range a.policies {
		for _, query := range policy.Queries {
			if !queryHits(query.Body, policyView) {
				continue
			}
			if policy.Kind == PolicyDeny {
				return &Result{
					Decision:    DecisionDeny,
					Reason:      ReasonDenied,
					BlockID:     -1,
					PolicyIndex: index,
					Message:     fmt.Sprintf("deny policy %d matched first", index),
				}
			}
			return &Result{
				Decision:    DecisionAllow,
				Reason:      ReasonNone,
				BlockID:     -1,
				PolicyIndex: index,
				Message:     fmt.Sprintf("allow policy %d matched", index),
			}
		}
	}
	return &Result{
		Decision:    DecisionDeny,
		Reason:      ReasonNoPolicy,
		BlockID:     -1,
		PolicyIndex: -1,
		Message:     "no allow policy matched; default deny",
	}
}

func runChecks(block *Block, view world) *Result {
	for checkIndex, check := range block.Checks {
		passed := false
		for _, query := range check.Queries {
			if queryHits(query.Body, view) {
				passed = true
				break
			}
		}
		if !passed {
			return &Result{
				Decision:    DecisionDeny,
				Reason:      ReasonCheckFailed,
				BlockID:     block.ID,
				PolicyIndex: -1,
				Message:     fmt.Sprintf("block %d check %d failed", block.ID, checkIndex),
			}
		}
	}
	return nil
}

func fixpointResult(blockID int, err error) *Result {
	return &Result{
		Decision:    DecisionDeny,
		Reason:      ReasonFixpoint,
		BlockID:     blockID,
		PolicyIndex: -1,
		Message:     err.Error(),
	}
}
