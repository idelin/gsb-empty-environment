package main

import "fmt"

// PairEvidence 记录对一个互可接受对 (a, h) 的阻挡对检查结论。
type PairEvidence struct {
	A        int
	H        int
	Blocking bool   // 是否构成阻挡对
	AEager   bool   // 申请者一侧是否严格偏好 h 胜于现状
	Reason   string // 人类可读的判定依据
}

// VerifyWeakStability 检查匹配 M 的弱稳定性,并为每个互可接受对生成证据。
// 阻挡对定义:(a, h) 互可接受、未匹配在一起,且
//   1. a 严格偏好 h 胜于现状(或 a 未匹配),并且
//   2. h 有空位,或 h 严格偏好 a 胜于其最差在院者。
// 只有双方都"严格更好"才构成阻挡对;任一侧为并列即不阻挡。
func VerifyWeakStability(p *Problem, m *Matching) []PairEvidence {
	occ := p.Occupants(m)
	var ev []PairEvidence
	for a := 0; a < p.NumA; a++ {
		cur := m.Cur[a]
		for _, h := range p.AHosps[a] {
			if h == cur {
				continue
			}
			ha, ok := p.HRank[h][a]
			if !ok {
				continue // 非互可接受,不可能阻挡
			}
			e := PairEvidence{A: a, H: h}
			e.AEager = p.APrefersStrict(a, h, cur)
			if !e.AEager {
				e.Reason = fmt.Sprintf("%s 并不严格偏好 %s 胜于现状,申请者一侧不满足",
					p.ANames[a], p.HNames[h])
				ev = append(ev, e)
				continue
			}
			if len(occ[h]) < p.Cap[h] {
				e.Blocking = true
				e.Reason = fmt.Sprintf("%s 严格偏好 %s 胜于现状,且 %s 有空位并愿意接收 %s:双方严格更优",
					p.ANames[a], p.HNames[h], p.HNames[h], p.ANames[a])
				ev = append(ev, e)
				continue
			}
			// 医院已满:比较 a 与最差在院者
			worst, worstRank := -1, -1
			for _, b := range occ[h] {
				if r := p.HRank[h][b]; r > worstRank {
					worst, worstRank = b, r
				}
			}
			if ha < worstRank {
				e.Blocking = true
				e.Reason = fmt.Sprintf("%s 严格偏好 %s,且 %s 严格偏好 %s 胜于其最差在院者 %s:双方严格更优",
					p.ANames[a], p.HNames[h], p.HNames[h], p.ANames[a], p.ANames[worst])
			} else if ha == worstRank {
				e.Reason = fmt.Sprintf("%s 严格偏好 %s,但 %s 已满且 %s 与其最差在院者 %s 并列,医院一侧未严格更优,不阻挡",
					p.ANames[a], p.HNames[h], p.HNames[h], p.ANames[a], p.ANames[worst])
			} else {
				e.Reason = fmt.Sprintf("%s 严格偏好 %s,但 %s 已满且其最差在院者 %s 严格优于 %s,不阻挡",
					p.ANames[a], p.HNames[h], p.HNames[h], p.ANames[worst], p.ANames[a])
			}
			ev = append(ev, e)
		}
	}
	return ev
}

// BlockingPairs 从证据中筛出真正的阻挡对。
func BlockingPairs(ev []PairEvidence) []PairEvidence {
	var out []PairEvidence
	for _, e := range ev {
		if e.Blocking {
			out = append(out, e)
		}
	}
	return out
}
