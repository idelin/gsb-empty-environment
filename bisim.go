package main

// Pair 是系统一状态与系统二状态组成的状态对。
type Pair struct{ P, Q string }

// Removal 记录某状态对被剔除出候选关系时的证据，
// 用于事后构造区分公式与可重放的区分过程。
type Removal struct {
	Round      int        // 被剔除的迭代轮次（保证区分公式构造可终止）
	Attacker   int        // 1: P（系统一）侧发起进攻; 2: Q（系统二）侧发起进攻
	Move       Transition // 进攻方走出的、防守方无法匹配的具体转移
	Responders []string   // 防守方对该动作的全部弱响应状态（当时无一在关系内）
}

// BisimResult 保存最大弱互模拟关系及剔除证据。
type BisimResult struct {
	Relation []Pair
	removal  map[Pair]Removal
	l1, l2   *LTS
}

// Related 报告 (p, q) 是否在最大弱互模拟关系内。
func (r *BisimResult) Related(p, q string) bool {
	for _, pr := range r.Relation {
		if pr.P == p && pr.Q == q {
			return true
		}
	}
	return false
}

// WeakBisimulation 用不动点迭代计算两个 LTS 间的最大弱互模拟关系。
// 从全积关系出发，反复剔除不满足转移匹配条件的状态对直至收敛。
// 转移匹配基于弱转移：可见动作 a 用 tau*·a·tau* 匹配，tau 用 tau* 匹配，
// 因此内部循环不会被当作可见进展。
func WeakBisimulation(l1, l2 *LTS) *BisimResult {
	res := &BisimResult{l1: l1, l2: l2, removal: make(map[Pair]Removal)}
	inRel := make(map[Pair]bool)
	var all []Pair
	for _, p := range l1.States {
		for _, q := range l2.States {
			all = append(all, Pair{p, q})
			inRel[Pair{p, q}] = true
		}
	}
	round := 0
	for {
		var removed []Pair
		for _, pr := range all {
			if !inRel[pr] {
				continue
			}
			if rem, bad := checkPair(l1, l2, pr, inRel, round); bad {
				res.removal[pr] = rem
				removed = append(removed, pr)
			}
		}
		if len(removed) == 0 {
			break
		}
		for _, pr := range removed {
			delete(inRel, pr)
		}
		round++
	}
	for _, pr := range all {
		if inRel[pr] {
			res.Relation = append(res.Relation, pr)
		}
	}
	return res
}

// checkPair 检查状态对是否满足双向弱转移匹配；
// 不满足时返回第一处反例作为剔除证据。
func checkPair(l1, l2 *LTS, pr Pair, inRel map[Pair]bool, round int) (Removal, bool) {
	// 系统一（P 侧）进攻：P 的每条转移都要能被 Q 的弱转移匹配。
	for _, tr := range l1.Transitions(pr.P) {
		responders := l2.WeakMove(pr.Q, tr.Label)
		if !anyRelated(tr.To, responders, inRel, true) {
			return Removal{Round: round, Attacker: 1, Move: tr, Responders: responders}, true
		}
	}
	// 系统二（Q 侧）进攻：Q 的每条转移都要能被 P 的弱转移匹配。
	for _, tr := range l2.Transitions(pr.Q) {
		responders := l1.WeakMove(pr.P, tr.Label)
		if !anyRelated(tr.To, responders, inRel, false) {
			return Removal{Round: round, Attacker: 2, Move: tr, Responders: responders}, true
		}
	}
	return Removal{}, false
}

// anyRelated 检查目标状态与响应状态集合中是否存在仍在关系内的配对。
func anyRelated(to string, responders []string, inRel map[Pair]bool, pMoved bool) bool {
	for _, r := range responders {
		var pr Pair
		if pMoved {
			pr = Pair{to, r}
		} else {
			pr = Pair{r, to}
		}
		if inRel[pr] {
			return true
		}
	}
	return false
}
