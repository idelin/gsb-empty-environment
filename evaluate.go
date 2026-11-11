package netsec

import (
	"fmt"
	"slices"
)

// evaluate 是不依赖锁的纯函数：给定快照与分组，计算当前结论、最小决定证据
// 以及相关/无关的未结束检测器。
func evaluate(snap Snapshot, inAllow, inDeny []bool, threshold int) Status {
	n := len(snap.Outcome)

	allowVotes := 0
	denyHits := 0
	pendingDeny := 0
	pendingAllow := 0
	denyEvidence := -1
	allowEvidence := make([]int, 0, threshold)

	for id := 0; id < n; id++ {
		pending := !snap.Finished[id]
		switch {
		case inDeny[id]:
			switch {
			case snap.Outcome[id] == ProvenDeny:
				denyHits++
				if denyEvidence == -1 {
					denyEvidence = id
				}
			case pending:
				pendingDeny++
			}
		case inAllow[id]:
			switch {
			case snap.Outcome[id] == ProvenAllow:
				allowVotes++
				if len(allowEvidence) < threshold {
					allowEvidence = append(allowEvidence, id)
				}
			case pending:
				pendingAllow++
			}
		}
	}

	relevant := make([]int, 0, pendingDeny+pendingAllow)
	irrelevant := make([]int, 0)
	for id := 0; id < n; id++ {
		if snap.Finished[id] {
			continue
		}
		switch {
		case inDeny[id] && denyHits == 0:
			// 尚未出现拒绝命中时，拒绝组的每个未结束分支都可能翻转结论。
			relevant = append(relevant, id)
		case inAllow[id] && allowVotes < threshold:
			// 阈值未满时，允许组的未结束分支可能凑齐放行票数。
			relevant = append(relevant, id)
		default:
			// 拒绝已命中，或允许票数已足够：该分支不再能改变结论。
			irrelevant = append(irrelevant, id)
		}
	}

	switch {
	case denyHits > 0:
		// 任一拒绝命中即锁定拒绝，快速允许无法覆盖稍后返回的关键拒绝。
		return Status{
			Verdict:    Deny,
			Reason:     DenyHit,
			Evidence:   []int{denyEvidence},
			Relevant:   []int{},
			Irrelevant: append(irrelevant, relevant...),
		}

	case allowVotes >= threshold && pendingDeny == 0:
		// 达到允许阈值且拒绝组全部结束、无命中，放行。
		evidence := slices.Clone(allowEvidence)
		for id := 0; id < n; id++ {
			if inDeny[id] && snap.Finished[id] {
				evidence = append(evidence, id)
			}
		}
		slices.Sort(evidence)
		return Status{
			Verdict:    Allow,
			Reason:     NoReason,
			Evidence:   evidence,
			Irrelevant: append(irrelevant, relevant...),
			Relevant:   []int{},
		}

	case allowVotes+pendingAllow < threshold:
		// 即使剩余允许组检测器全部证明允许也达不到阈值，默认安全拒绝。
		// 只需 |允许组| - 阈值 + 1 个“未投允许”的已结束检测器即可锁死。
		need := countTrue(inAllow) - threshold + 1
		evidence := make([]int, 0, need)
		for id := 0; id < n && len(evidence) < need; id++ {
			if inAllow[id] && snap.Finished[id] && snap.Outcome[id] != ProvenAllow {
				evidence = append(evidence, id)
			}
		}
		return Status{
			Verdict:    Deny,
			Reason:     AllowUnreachable,
			Evidence:   evidence,
			Relevant:   []int{},
			Irrelevant: append(irrelevant, relevant...),
		}

	default:
		// 仍有相关分支可能改变结论：拒绝组可能命中，或允许组可能凑齐阈值。
		return Status{
			Verdict:    Pending,
			Reason:     NoReason,
			Evidence:   nil,
			Relevant:   relevant,
			Irrelevant: irrelevant,
		}
	}
}

// finalVerdict 是穷举补全时使用的参考判定：所有检测器都结束后的最终结论。
func finalVerdict(outcomes []Outcome, inAllow, inDeny []bool, threshold int) Verdict {
	allowVotes := 0
	for id := range outcomes {
		if inDeny[id] && outcomes[id] == ProvenDeny {
			return Deny
		}
		if inAllow[id] && outcomes[id] == ProvenAllow {
			allowVotes++
		}
	}
	if allowVotes >= threshold {
		return Allow
	}
	return Deny
}

func membership(cfg Config) (inAllow, inDeny []bool, n int) {
	n = len(cfg.AllowGroup) + len(cfg.DenyGroup)
	inAllow = make([]bool, n)
	inDeny = make([]bool, n)
	for _, id := range cfg.AllowGroup {
		inAllow[id] = true
	}
	for _, id := range cfg.DenyGroup {
		inDeny[id] = true
	}
	return inAllow, inDeny, n
}

func countTrue(flags []bool) int {
	count := 0
	for _, flag := range flags {
		if flag {
			count++
		}
	}
	return count
}

// VerifyInvariant 穷举 snap 中全部未结束检测器的每一种结果补全，核对最终判定
// 是否恒为 want，用于证明提前结束不会改变最终判定。
//
// 每个未结束检测器只需区分“证明本组命中”与否：拒绝组检测器取已证明拒绝或未知，
// 允许组检测器取已证明允许或未知；其它取值在判定上与未知等价。检测器上限为
// MaxDetectors，补全空间至多 2^MaxDetectors。
func VerifyInvariant(cfg Config, snap Snapshot, want Verdict) error {
	inAllow, inDeny, n := membership(cfg)
	if n != len(snap.Outcome) || n != len(snap.Finished) {
		return fmt.Errorf("netsec: snapshot size %d/%d does not match config size %d",
			len(snap.Outcome), len(snap.Finished), n)
	}

	pending := make([]int, 0, n)
	for id := 0; id < n; id++ {
		if !snap.Finished[id] {
			pending = append(pending, id)
		}
	}

	outcomes := slices.Clone(snap.Outcome)
	for mask := 0; mask < 1<<len(pending); mask++ {
		for bit, id := range pending {
			if mask&(1<<bit) != 0 {
				if inDeny[id] {
					outcomes[id] = ProvenDeny
				} else {
					outcomes[id] = ProvenAllow
				}
			} else {
				outcomes[id] = Unknown
			}
		}
		if got := finalVerdict(outcomes, inAllow, inDeny, cfg.AllowThreshold); got != want {
			return fmt.Errorf("netsec: early stop unsound: completion mask %0*b yields %s, want %s",
				len(pending), mask, got, want)
		}
	}
	return nil
}

// VerifyEvidence 核对 st.Evidence 确实是支撑 st.Verdict 的最小决定证据：
//   - 每个证据检测器都已结束，且其已证明结果与结论相容；
//   - 仅凭证据（其余检测器全部重置为未知）仍必然推出该结论；
//   - 去掉任意一个证据检测器后结论不再被锁定（包含意义上的最小）。
func VerifyEvidence(cfg Config, snap Snapshot, st Status) error {
	inAllow, inDeny, n := membership(cfg)

	seen := make(map[int]bool, len(st.Evidence))
	for _, id := range st.Evidence {
		if id < 0 || id >= n || !snap.Finished[id] {
			return fmt.Errorf("netsec: evidence detector %d is not a finished detector", id)
		}
		if seen[id] {
			return fmt.Errorf("netsec: evidence contains duplicate detector %d", id)
		}
		seen[id] = true

		switch st.Verdict {
		case Deny:
			if st.Reason == DenyHit && !(inDeny[id] && snap.Outcome[id] == ProvenDeny) {
				return fmt.Errorf("netsec: evidence %d cannot support deny-hit", id)
			}
			if st.Reason == AllowUnreachable &&
				!(inAllow[id] && snap.Outcome[id] != ProvenAllow) {
				return fmt.Errorf("netsec: evidence %d cannot support allow-unreachable", id)
			}
		case Allow:
			if !((inAllow[id] && snap.Outcome[id] == ProvenAllow) ||
				(inDeny[id] && snap.Outcome[id] != ProvenDeny)) {
				return fmt.Errorf("netsec: evidence %d cannot support allow", id)
			}
		default:
			return fmt.Errorf("netsec: cannot verify evidence for verdict %s", st.Verdict)
		}
	}

	evidenceOnly := Snapshot{
		Outcome:  make([]Outcome, n),
		Finished: make([]bool, n),
	}
	for _, id := range st.Evidence {
		evidenceOnly.Outcome[id] = snap.Outcome[id]
		evidenceOnly.Finished[id] = true
	}
	if got := evaluate(evidenceOnly, inAllow, inDeny, cfg.AllowThreshold); got.Verdict != st.Verdict ||
		(got.Verdict == Deny && got.Reason != st.Reason) {
		return fmt.Errorf("netsec: evidence does not entail %s/%s (got %s/%s)",
			st.Verdict, st.Reason, got.Verdict, got.Reason)
	}

	for _, removed := range st.Evidence {
		reduced := Snapshot{
			Outcome:  make([]Outcome, n),
			Finished: make([]bool, n),
		}
		for _, id := range st.Evidence {
			if id != removed {
				reduced.Outcome[id] = snap.Outcome[id]
				reduced.Finished[id] = true
			}
		}
		if got := evaluate(reduced, inAllow, inDeny, cfg.AllowThreshold); got.Verdict == st.Verdict {
			return fmt.Errorf("netsec: evidence not minimal: removing %d still yields %s",
				removed, st.Verdict)
		}
	}
	return nil
}

// VerifyEarlyStop 对当前快照做一次完整的提前结束核对：
//   - Pending 时确认 Relevant/Irrelevant 恰好划分全部未结束检测器；
//   - 已定案时穷举全部剩余结果补全，确认每种补全的最终判定都与当前结论一致；
//   - 核对返回的决定证据最小且充分。
func VerifyEarlyStop(cfg Config, snap Snapshot) (Status, error) {
	inAllow, inDeny, n := membership(cfg)
	st := evaluate(snap, inAllow, inDeny, cfg.AllowThreshold)

	pending := make([]int, 0)
	for id := 0; id < n; id++ {
		if !snap.Finished[id] {
			pending = append(pending, id)
		}
	}

	listed := append(slices.Clone(st.Relevant), st.Irrelevant...)
	if len(listed) != len(pending) {
		return st, fmt.Errorf("netsec: relevant+irrelevant = %d detectors, pending = %d",
			len(listed), len(pending))
	}
	slices.Sort(listed)
	if !slices.Equal(listed, pending) {
		return st, fmt.Errorf("netsec: relevant/irrelevant do not partition pending detectors")
	}

	if st.Verdict == Pending {
		return st, nil
	}
	if err := VerifyInvariant(cfg, snap, st.Verdict); err != nil {
		return st, err
	}
	if err := VerifyEvidence(cfg, snap, st); err != nil {
		return st, err
	}
	return st, nil
}
