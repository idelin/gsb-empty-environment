package main

import (
	"fmt"
	"math/big"
)

// verify 对求解结果做独立核对：
//  1. 相等点：精确根直接回代验证 f(r)==g(r)；区间根用浮点残差验证。
//  2. 重合区间：构造公共因子（约化型一致 + 叉乘恒零），并用错位断点采样核对。
func (s *Solver) verify() {
	v := &s.out.Verification
	v.AllPassed = true
	fail := func(msg string) {
		v.AllPassed = false
		v.Checks = append(v.Checks, "FAIL: "+msg)
	}
	pass := func(msg string) {
		v.Checks = append(v.Checks, "ok: "+msg)
	}

	// ---- 相等点核对 ----
	for i, rr := range s.roots {
		if rr.root.Exact != nil {
			r := rr.root.Exact
			fv, ok1 := s.F.Eval(r)
			gv, ok2 := s.G.Eval(r)
			if !ok1 || !ok2 {
				fail(fmt.Sprintf("根 #%d (%s): 函数在此无定义", i+1, r.RatString()))
				continue
			}
			if fv.Cmp(gv) != 0 {
				fail(fmt.Sprintf("根 #%d (%s): f=%s ≠ g=%s", i+1, r.RatString(), fv.RatString(), gv.RatString()))
				continue
			}
			pass(fmt.Sprintf("根 #%d: 精确根 x=%s 回代 f=g=%s（重数 %d）",
				i+1, r.RatString(), fv.RatString(), rr.mult))
		} else {
			// 区间根：取中点浮点回代，检查残差
			mid := new(big.Rat).Add(rr.root.Lo, rr.root.Hi)
			mid.Quo(mid, big.NewRat(2, 1))
			x, _ := mid.Float64()
			fv, ok1 := s.F.EvalFloat(x)
			gv, ok2 := s.G.EvalFloat(x)
			if !ok1 || !ok2 {
				fail(fmt.Sprintf("根 #%d (%s..%s): 函数在近似点无定义", i+1,
					rr.root.Lo.RatString(), rr.root.Hi.RatString()))
				continue
			}
			res := fv - gv
			if res < 0 {
				res = -res
			}
			if res > 1e-6 {
				fail(fmt.Sprintf("根 #%d: 浮点残差 |f-g|=%g 过大", i+1, res))
				continue
			}
			width := new(big.Rat).Sub(rr.root.Hi, rr.root.Lo)
			pass(fmt.Sprintf("根 #%d: 隔离区间宽 %s ≤ eps，近似 x≈%g 处 |f-g|=%.2e（重数 %d）",
				i+1, width.RatString(), x, res, rr.mult))
		}
	}

	// ---- 重合区间核对（公共因子 + 错位断点） ----
	for i := range s.coincs {
		s.verifyCoincident(i, &s.coincs[i], fail, pass)
	}
}

// verifyCoincident 核对一个极大重合区间，并把证据写入输出。
func (s *Solver) verifyCoincident(idx int, c *coincRaw, fail, pass func(string)) {
	ev := CoincEvidence{
		Reason:   "叉乘差多项式 n1*d2-n2*d1 恒为零：两有理函数约化型一致，在该区间逐点相等",
		Segments: c.segEv,
	}

	// 公共因子核对：各原子段约化型必须逐系数一致（分母已首一化）。
	for _, se := range c.segEv {
		if se.ReducedF != se.ReducedG {
			fail(fmt.Sprintf("重合区间 #%d 段 %v: 约化型不一致 %s vs %s",
				idx+1, se.Segment, se.ReducedF, se.ReducedG))
			return
		}
	}

	// 错位断点采样：两组彼此错开的偏移族，避开所有断点与极点，
	// 在区间内部精确回代验证 f(t)==g(t)。
	families := [][]*big.Rat{
		offsetFamily(5), // (2j+1)/10
		offsetFamily(7), // (2j+1)/14
	}
	total := 0
	for fi, fam := range families {
		famOK := 0
		for _, off := range fam {
			t := new(big.Rat).Mul(new(big.Rat).Sub(c.hi, c.lo), off)
			t.Add(t, c.lo)
			if s.isBreakpoint(t) {
				continue // 错位原则：采样点不得落在断点上
			}
			fv, ok1 := s.F.Eval(t)
			gv, ok2 := s.G.Eval(t)
			if !ok1 || !ok2 {
				continue // 极点洞，跳过
			}
			eq := fv.Cmp(gv) == 0
			ev.StaggeredSamples = append(ev.StaggeredSamples, SampleCheck{
				At: t.RatString(), F: fv.RatString(), G: gv.RatString(), Equal: eq,
			})
			if !eq {
				fail(fmt.Sprintf("重合区间 #%d: 错位采样 t=%s 处 f=%s ≠ g=%s",
					idx+1, t.RatString(), fv.RatString(), gv.RatString()))
				return
			}
			famOK++
			total++
		}
		if famOK == 0 {
			fail(fmt.Sprintf("重合区间 #%d: 第 %d 组错位采样无有效样本", idx+1, fi+1))
			return
		}
	}

	// 内部断点右连续核对：合并跨越的每个断点处，按右连续取值必须相等。
	for _, b := range s.bps {
		if b.Cmp(c.lo) <= 0 || b.Cmp(c.hi) >= 0 {
			continue
		}
		fv, ok1 := s.F.Eval(b)
		gv, ok2 := s.G.Eval(b)
		if !ok1 || !ok2 {
			continue // 极点处不合并，不会出现在内部
		}
		eq := fv.Cmp(gv) == 0
		ev.BreakpointChecks = append(ev.BreakpointChecks, SampleCheck{
			At: b.RatString(), F: fv.RatString(), G: gv.RatString(), Equal: eq,
		})
		if !eq {
			fail(fmt.Sprintf("重合区间 #%d: 内部断点 x=%s 处右连续取值 f=%s ≠ g=%s",
				idx+1, b.RatString(), fv.RatString(), gv.RatString()))
			return
		}
	}

	pass(fmt.Sprintf("重合区间 #%d: [%s, %s]%s 公共因子核对一致，%d 个错位采样全部相等",
		idx+1, c.lo.RatString(), c.hi.RatString(), inclStr(c), total))

	s.out.Coincident = append(s.out.Coincident, CoincOut{
		Lo:         c.lo.RatString(),
		Hi:         c.hi.RatString(),
		LoIncluded: c.loInc,
		HiIncluded: c.hiInc,
		Evidence:   ev,
	})
}

// offsetFamily 生成 (2j+1)/(2n) 形式的错位偏移族。
func offsetFamily(n int) []*big.Rat {
	out := make([]*big.Rat, n)
	for j := 0; j < n; j++ {
		out[j] = big.NewRat(int64(2*j+1), int64(2*n))
	}
	return out
}

func (s *Solver) isBreakpoint(t *big.Rat) bool {
	for _, b := range s.bps {
		if b.Cmp(t) == 0 {
			return true
		}
	}
	return false
}

func inclStr(c *coincRaw) string {
	switch {
	case c.loInc && c.hiInc:
		return "（双端包含）"
	case c.hiInc:
		return "（含右端点）"
	case c.loInc:
		return "（含左端点）"
	default:
		return "（双端开）"
	}
}
