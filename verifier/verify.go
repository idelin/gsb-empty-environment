package verifier

import (
	"fmt"
	"sort"

	"prescreen.local/ddd-10554-v3/spec"
)

// Report 汇总独立核验结论。
type Report struct {
	// Checks 是逐项核验结果（全部通过时无 false 项）。
	Checks map[string]bool
	// Beliefs 是核验器独立枚举得到的信念数量。
	Beliefs int
	// ConcreteStates 是独立枚举得到的具体状态数量。
	ConcreteStates int
	// WorstCaseBurns 是核验器独立求解的最小最坏烧录次数。
	WorstCaseBurns int
	// UnsafeStates 列出违反安全不变量的具体状态指纹（应为空）。
	UnsafeStates []string
	// Errors 是核验中发现的所有问题；为空表示证书通过核验。
	Errors []string
}

// OK 报告核验是否全部通过。
func (r *Report) OK() bool { return len(r.Errors) == 0 }

func (r *Report) fail(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

func (r *Report) check(name string, ok bool) {
	r.Checks[name] = ok
	if !ok {
		r.fail("核验项失败: %s", name)
	}
}

// Verify 对证书做完全独立的重新建模、枚举与求解，并交叉核验证书内容。
func Verify(cert *spec.Certificate) *Report {
	r := &Report{Checks: map[string]bool{}}
	if cert == nil {
		r.fail("证书为空")
		return r
	}
	if cert.SchemaVersion != 1 {
		r.fail("不支持的证书 schema 版本: %d", cert.SchemaVersion)
		return r
	}

	// 1. 规格指纹一致。
	fp, err := spec.Fingerprint(cert.Chip)
	if err != nil {
		r.fail("规格指纹计算失败: %v", err)
		return r
	}
	r.check("spec_fingerprint", fp == cert.SpecFingerprint)

	// 2. 独立重建语义并枚举。
	eng, err := newEngine(cert.Chip)
	if err != nil {
		r.fail("芯片规格独立校验失败: %v", err)
		return r
	}
	en, err := eng.enumerate()
	if err != nil {
		r.fail("独立枚举失败: %v", err)
		return r
	}
	r.Beliefs = len(en.nodes)
	r.ConcreteStates = len(en.stateOf)

	// 3. 每个枚举状态独立满足安全不变量；初始状态亦同。
	r.check("initial_state_safe", eng.safe(cert.Chip.Initial))
	allInitSafe := eng.safe(cert.Chip.Initial)
	for _, alt := range cert.Chip.InitialAlternatives {
		if !eng.safe(alt) {
			allInitSafe = false
		}
	}
	r.check("all_initial_states_safe", allInitSafe)
	unsafe := []string{}
	for sk, s := range en.stateOf {
		if !eng.safe(s) {
			unsafe = append(unsafe, sk)
		}
	}
	sort.Strings(unsafe)
	r.UnsafeStates = unsafe
	r.check("all_states_safe", len(unsafe) == 0)

	// 4. 证书状态集合与独立枚举一致。
	r.check("state_set_equal", stringSetEqual(mapKeys(cert.States), mapKeysState(en.stateOf)))
	for sk := range en.stateOf {
		cs, ok := cert.States[sk]
		if !ok || stateKey(cs) != sk {
			r.fail("证书状态对象与指纹不一致: %s", sk)
		}
	}

	// 5. 信念集合一致，且每个信念的相容状态集合一致。
	indBeliefs := map[string][]string{}
	for k, n := range en.nodes {
		sk := make([]string, 0, len(n.states))
		for s := range n.states {
			sk = append(sk, s)
		}
		sort.Strings(sk)
		indBeliefs[k] = sk
	}
	r.check("belief_set_equal", stringSetEqual(mapKeysBelief(cert.Beliefs), mapKeysString(indBeliefs)))
	for k, sk := range indBeliefs {
		bn, ok := cert.Beliefs[k]
		if !ok {
			continue
		}
		r.check("belief_members:"+short(k), equalSorted(sk, bn.States))
	}
	r.check("root_belief", cert.RootBeliefKey == en.root)

	// 6. 信念级转移一致（动作 -> boot 观测 -> 后继信念）。
	transOK := true
	certEdge := map[string]map[string]string{}
	for _, t := range cert.Transitions {
		certEdge[t.BeliefKey+"|"+t.ActionKey] = t.Outcomes
	}
	for bk, n := range en.nodes {
		for ak, byObs := range n.edges {
			got, ok := certEdge[bk+"|"+ak]
			if !ok || len(got) != len(byObs) {
				transOK = false
				continue
			}
			for ok2, child := range byObs {
				if got[ok2] != child {
					transOK = false
				}
			}
		}
	}
	r.check("transitions_match", transOK)

	// 7. 独立 Bellman 求解。
	sol, err := en.solve()
	if err != nil {
		r.fail("独立求解失败: %v", err)
		return r
	}
	r.WorstCaseBurns = sol.RootWorstCase()
	r.check("worst_case_burns", cert.WorstCaseBurns == sol.RootWorstCase())

	// 8. 策略表：每个非终止信念的每个预算格点都存在，
	// 且证书动作的最坏烧录值等于独立最优值；
	// 同时验证证书动作确实合法，杜绝“看不见的内部状态选不同动作”。
	policyOK := true
	seen := map[string]map[int]bool{}
	B := cert.Chip.MaxBurns
	for _, row := range cert.Policy {
		n, ok := en.nodes[row.BeliefKey]
		if !ok {
			policyOK = false
			continue
		}
		act, ok := cert.Actions[row.ActionKey]
		if !ok || actionKey(act) != row.ActionKey {
			policyOK = false
			continue
		}
		legal := sol.legal[row.BudgetLeft][row.BeliefKey]
		found := false
		for _, la := range legal {
			if sameAction(la, act) {
				found = true
				break
			}
		}
		if !found {
			policyOK = false
			continue
		}
		choice := sol.choice[row.BeliefKey][row.BudgetLeft]
		if choice == nil || choice.value != row.WorstCaseBurns {
			policyOK = false
			continue
		}
		if seen[row.BeliefKey] == nil {
			seen[row.BeliefKey] = map[int]bool{}
		}
		seen[row.BeliefKey][row.BudgetLeft] = true
		_ = n
	}
	for bk, n := range en.nodes {
		if eng.terminalBelief(n) {
			continue
		}
		for b := 0; b <= B; b++ {
			if len(sol.legal[b][bk]) == 0 {
				continue
			}
			if !seen[bk][b] {
				policyOK = false
			}
		}
	}
	r.check("policy_optimal_and_observational", policyOK)

	// 9. 证书观测对象自洽且不含指针/非公开位置信息。
	obsOK := true
	for ok2, o := range cert.Observations {
		if obsKey(o) != ok2 {
			obsOK = false
		}
		if o.Kind == spec.ObsBoot {
			for pid := range o.Readback {
				if !eng.positions[pid].Public {
					obsOK = false
				}
			}
		}
	}
	r.check("observations_public_only", obsOK)

	return r
}

func mapKeys(m map[string]*spec.State) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mapKeysState(m map[string]*spec.State) []string { return mapKeys(m) }

func mapKeysBelief(m map[string]*spec.BeliefNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func mapKeysString(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func stringSetEqual(a, b []string) bool { return equalSorted(a, b) }

func equalSorted(a, b []string) bool {
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

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
