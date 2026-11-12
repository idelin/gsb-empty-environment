package netsec

// 独立小域语义复核：不依赖 verify.go 的区间运算，直接在有限整数域
// [semLo, semHi] 上逐值求值每一步的实际取值集合，再检查结论区间是否覆盖。
// 这是对规则核验层的防御性复核，用于捕获规则实现本身的缺陷。

const (
	semLo = -64
	semHi = 64
)

// semSet 为一步在小域上的实际取值集合；escaped 表示有值逸出小域，
// 此时本步及其后继弃权复核（规则层已做过核验，语义层仅作补充）。
type semSet struct {
	vals    map[int64]struct{}
	escaped bool
}

func semanticVerify(cert *Certificate) error {
	sets := make([]semSet, len(cert.Steps))
	for i, st := range cert.Steps {
		s := evalStepSem(cert, i, sets)
		sets[i] = s
		if s.escaped {
			continue
		}
		for v := range s.vals {
			if v < st.Claim.Lo || v > st.Claim.Hi {
				return fail(i, "claim", "小域语义复核失败：值 %d 可达但不在结论 [%d,%d] 内",
					v, st.Claim.Lo, st.Claim.Hi)
			}
		}
	}
	return nil
}

func evalStepSem(cert *Certificate, i int, sets []semSet) semSet {
	st := cert.Steps[i]
	instr := cert.Program[st.Instr]
	switch st.Rule {
	case RuleContain:
		if len(st.Refs) == 0 {
			s := semSet{vals: map[int64]struct{}{}}
			lo := instr.Bounds.Lo
			if lo < semLo {
				lo = semLo
			}
			hi := instr.Bounds.Hi
			if hi > semHi {
				hi = semHi
			}
			for v := lo; v <= hi; v++ {
				s.vals[v] = struct{}{}
			}
			return s
		}
		return sets[st.Refs[0]]
	case RuleIntersect:
		a, b := sets[st.Refs[0]], sets[st.Refs[1]]
		s := semSet{vals: map[int64]struct{}{}, escaped: a.escaped || b.escaped}
		for v := range a.vals {
			if _, ok := b.vals[v]; ok {
				s.vals[v] = struct{}{}
			}
		}
		return s
	case RuleAddConst:
		a := sets[st.Refs[0]]
		s := semSet{vals: map[int64]struct{}{}, escaped: a.escaped}
		for v := range a.vals {
			nv := addSat(v, instr.Const)
			if nv < semLo || nv > semHi {
				s.escaped = true
				continue
			}
			s.vals[nv] = struct{}{}
		}
		return s
	case RuleJoin:
		a, b := sets[st.Refs[0]], sets[st.Refs[1]]
		s := semSet{vals: map[int64]struct{}{}, escaped: a.escaped || b.escaped}
		for v := range a.vals {
			s.vals[v] = struct{}{}
		}
		for v := range b.vals {
			s.vals[v] = struct{}{}
		}
		return s
	}
	return semSet{vals: map[int64]struct{}{}, escaped: true}
}
