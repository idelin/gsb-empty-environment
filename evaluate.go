package netsec

import "fmt"

// Evaluate 按 Biscuit 信任模型评估令牌：
//
//  1. 块 0 是 authority 块，其事实与规则推导出的 authority 事实集合对所有块可见；
//  2. 每个衰减块在自己的私有作用域内（authority 事实 + 本块事实/规则）求不动点，
//     推导结果只用于本块 check，绝不写回 authority 作用域，也不进入服务端策略；
//  3. 所有块的 check 必须全部通过，任一失败即拒绝；
//  4. 之后按书写顺序评估服务端 allow/deny 策略，第一条匹配者决定，无匹配则拒绝；
//  5. 任一作用域规则迭代超过 MaxRounds 轮即拒绝。
//
// 解析错误应在调用前通过 ParseToken / ParseAuthorizer 暴露；本函数只返回评估期错误。
func Evaluate(token Token, auth Authorizer) Result {
	if len(token.Blocks) == 0 {
		return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: -1, Reason: ErrEmptyToken}
	}

	authority := token.Blocks[0]

	// authority 作用域：只包含 authority 块自己的事实与规则推导结果。
	authorityFacts, err := solve(authority.Facts, authority.Rules)
	if err != nil {
		return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: 0, Reason: err}
	}

	// 逐块做 check。每个块拿到的只是 authority 事实的只读快照，
	// 本块推导在独立 factSet 中进行，从结构上杜绝写回 authority 作用域。
	for _, block := range token.Blocks {
		seed := make([]Predicate, 0, len(authorityFacts.byKey)+len(block.Facts))
		for _, fact := range authorityFacts.byKey {
			seed = append(seed, fact)
		}
		seed = append(seed, block.Facts...)

		localFacts, err := solve(seed, block.Rules)
		if err != nil {
			return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: block.Index, Reason: err}
		}
		for _, check := range block.Checks {
			if !checkPassed(localFacts, check) {
				return Result{
					Decision:      DecisionDeny,
					MatchedPolicy: -1,
					Block:         block.Index,
					Reason:        fmt.Errorf("%w: block %d", ErrCheckFailed, block.Index),
				}
			}
		}
	}

	// 策略阶段的事实集合只包含 authority 事实、服务端事实与环境事实，
	// 任何衰减块事实（无论直接声明还是规则推导）都不在其中。
	policyFacts := newFactSet()
	for _, fact := range authorityFacts.byKey {
		policyFacts.add(fact)
	}
	for _, fact := range auth.Facts {
		if !fact.IsGround() {
			return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: -1,
				Reason: fmt.Errorf("%w: authorizer fact %s must be ground", ErrSafety, fact.Name)}
		}
		policyFacts.add(fact)
	}
	for _, fact := range auth.Ambient {
		if !fact.IsGround() {
			return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: -1,
				Reason: fmt.Errorf("%w: ambient fact %s must be ground", ErrSafety, fact.Name)}
		}
		policyFacts.add(fact)
	}

	for i, policy := range auth.Policies {
		if len(query(policyFacts, policy.Body)) == 0 {
			continue
		}
		switch policy.Kind {
		case PolicyAllow:
			return Result{Decision: DecisionAllow, MatchedPolicy: i, Block: -1, Reason: nil}
		case PolicyDeny:
			return Result{
				Decision:      DecisionDeny,
				MatchedPolicy: i,
				Block:         -1,
				Reason:        fmt.Errorf("%w: policy %d", ErrPolicyDenied, i),
			}
		}
	}

	return Result{Decision: DecisionDeny, MatchedPolicy: -1, Block: -1, Reason: ErrNoPolicyMatched}
}
