package admission

import "slices"

// stateAt 返回某个根对应的完整映像状态。共享分支通过记忆化只回放一次；
// 每条候选根都从检查点独立派生，不同历史之间绝不共享可变状态。
func (a *Admission) stateAt(key string) (*intervalMap[pageState], error) {
	if st, ok := a.g.states[key]; ok {
		return st, nil
	}
	rcpt := a.g.byNew[key]
	parent, err := a.stateAt(string(rcpt.OldRoot))
	if err != nil {
		return nil, err
	}
	st := parent.clone()
	trust := a.g.in.Verifier.VerifyReceipt(rcpt)
	applyReceipt(st, rcpt, trust)
	a.g.states[key] = st
	return st, nil
}

// Admit 裁决请求区间，返回确定可执行、确定禁止与未知三类精确子区间。
// 每个初等区间都在所有与检查点相容的历史上独立核对：先在每条历史内部
// 得到完整位置状态，再逐段取跨历史结论，因此不可能把 A 历史的可信内容
// 与 B 历史的签名范围拼成一个确定结果。
func (a *Admission) Admit() ([]Decision, error) {
	req := a.g.in.Request
	stateMaps := make([]*intervalMap[pageState], 0, len(a.g.heads))
	cuts := map[uint64]struct{}{req.Start: {}}
	for _, head := range a.g.heads {
		st, err := a.stateAt(string(head))
		if err != nil {
			return nil, err
		}
		stateMaps = append(stateMaps, st)
		st.boundaries(cuts)
	}
	if req.End < MaxPage {
		cuts[req.End+1] = struct{}{}
	} else {
		cuts[PageCount] = struct{}{}
	}
	bounds := make([]uint64, 0, len(cuts))
	for b := range cuts {
		bounds = append(bounds, b)
	}
	slices.Sort(bounds)

	var raw []Decision
	for i := 0; i+1 < len(bounds); i++ {
		start, end := bounds[i], bounds[i+1]-1
		if end < req.Start {
			continue
		}
		if start > req.End {
			break
		}
		sample := start
		allSupported, allExec := true, true
		firstDigest, sameDigest := "", true
		for hi, st := range stateMaps {
			ps := st.get(sample)
			if hi == 0 {
				firstDigest = ps.digest
			} else if ps.digest != firstDigest {
				sameDigest = false
			}
			if !ps.attested || ps.trust != TrustTrusted {
				allSupported = false
			}
			if ps.perm&a.need != a.need {
				allExec = false
			}
		}
		verdict := VerdictForbidden
		switch {
		case allSupported && sameDigest && allExec:
			verdict = VerdictExecutable
		case allExec:
			// 权限足够但内容在历史间不一致或不可信：不能确定可执行。
			verdict = VerdictUnknown
		default:
			// 每条历史都缺足够权限——无论内容如何，执行都会被拒绝。
			verdict = VerdictForbidden
		}
		// 只收集落在请求区间内的段；裁剪与合并在第二阶段统一处理，
		// 避免边裁剪边合并时边界衔接判断错位。
		segStart := max(start, req.Start)
		segEnd := min(end, req.End)
		if n := len(raw); n > 0 &&
			raw[n-1].Verdict == verdict &&
			raw[n-1].Pages.End+1 == segStart {
			raw[n-1].Pages.End = segEnd
			continue
		}
		raw = append(raw, Decision{
			Verdict: verdict,
			Pages:   Range{Start: segStart, End: segEnd},
		})
	}
	return raw, nil
}

// RangesByVerdict 把 Admit 的判定拆成三类区间列表，方便宿主直接消费。
func RangesByVerdict(decisions []Decision) (executable, forbidden, unknown []Range) {
	for _, d := range decisions {
		switch d.Verdict {
		case VerdictExecutable:
			executable = append(executable, d.Pages)
		case VerdictForbidden:
			forbidden = append(forbidden, d.Pages)
		default:
			unknown = append(unknown, d.Pages)
		}
	}
	return coalesceRanges(executable), coalesceRanges(forbidden),
		coalesceRanges(unknown)
}
