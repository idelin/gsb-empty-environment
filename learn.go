package netsec

// learnMinimalSeparator 学习一个把 good（抽象路径上具体可达取值）与
// bad（同一抽象立方内会导向错误的取值）区分开的合取谓词。
//
// 策略是贪心决策树：每轮选择一个在 good 上恒真、又能排除最多 bad 的
// 文字；若单个文字无法干净切分，则逐步合取直到覆盖全部 good。最后做
// 局部冗余消除，保证去掉任一合取项都会重新混入 bad，即“最小”谓词。
func learnMinimalSeparator(good, bad valSet, nvars int) Expr {
	if len(good) == 0 || len(bad) == 0 {
		return nil
	}

	literals := candidateLiterals(nvars)
	var conjunction []Expr
	used := make(map[string]struct{})

	// 每轮加入一个文字，直到合取在 good 上恒真且在 bad 上恒假。
	for {
		if len(conjunction) > 0 &&
			allHoldConjunction(conjunction, good) &&
			countHoldingConjunction(conjunction, bad) == 0 {
			break
		}
		// remaining 是 good 中尚未被当前合取覆盖的取值，新文字必须在其上恒真。
		remaining := make(valSet)
		for s := range good {
			if !holdsConjunction(conjunction, s) {
				remaining.add(s)
			}
		}
		var best Expr
		bestBad, bestCover, found := 0, -1, false
		for _, lit := range literals {
			if _, ok := used[lit.String()]; ok {
				continue
			}
			if len(remaining) > 0 && !allHold(lit, remaining) {
				continue
			}
			cover := 0
			for s := range good {
				if holdsConjunction(conjunction, s) || lit.eval(s) {
					cover++
				}
			}
			badHits := 0
			for s := range bad {
				if lit.eval(s) {
					badHits++
				}
			}
			if !found || badHits < bestBad || (badHits == bestBad && cover > bestCover) {
				best, bestBad, bestCover = lit, badHits, cover
				found = true
			}
		}
		if best == nil {
			return nil
		}
		used[best.String()] = struct{}{}
		conjunction = append(conjunction, best)
		if len(conjunction) > 2*nvars {
			return nil
		}
	}

	if countHoldingConjunction(conjunction, bad) != 0 {
		return nil
	}

	// 局部最小化：移除任何冗余合取项。
	minimal := make([]Expr, 0, len(conjunction))
	for i, lit := range conjunction {
		trial := append(append([]Expr(nil), conjunction[:i]...), conjunction[i+1:]...)
		if countHoldingConjunction(trial, bad) == 0 && allHoldConjunction(trial, good) {
			continue
		}
		minimal = append(minimal, lit)
	}
	if len(minimal) == 0 {
		// 冗余消除后为空只会发生在 bad 本身为空的情形，入口已排除；
		// 退回原始合取，保证总是返回非空区分谓词。
		minimal = conjunction
	}
	return joinConjunction(minimal)
}

// candidateLiterals 给出全部单变量文字（正/负极性）。
func candidateLiterals(nvars int) []Expr {
	out := make([]Expr, 0, 2*nvars)
	for i := 0; i < nvars; i++ {
		out = append(out, Var{N: i}, Not{Inner: Var{N: i}})
	}
	return out
}

func allHold(lit Expr, set valSet) bool {
	for s := range set {
		if !lit.eval(s) {
			return false
		}
	}
	return true
}

func allHoldConjunction(conj []Expr, set valSet) bool {
	for s := range set {
		for _, lit := range conj {
			if !lit.eval(s) {
				return false
			}
		}
	}
	return true
}

func countHoldingConjunction(conj []Expr, set valSet) int {
	n := 0
	for s := range set {
		ok := true
		for _, lit := range conj {
			if !lit.eval(s) {
				ok = false
				break
			}
		}
		if ok {
			n++
		}
	}
	return n
}

func holdsConjunction(conj []Expr, s Valuation) bool {
	for _, lit := range conj {
		if !lit.eval(s) {
			return false
		}
	}
	return true
}

func joinConjunction(conj []Expr) Expr {
	if len(conj) == 0 {
		return nil
	}
	out := conj[0]
	for _, lit := range conj[1:] {
		out = And{L: out, R: lit}
	}
	return out
}
