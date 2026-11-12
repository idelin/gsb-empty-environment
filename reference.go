package taskpkg

import "math/big"

// 本文件提供一套结构上独立的全组合参考实现，仅供小实例交叉验证使用。
// 它与 solver.go 采用不同的曝光枚举（递归而非混合进位）和不同的
// 线性求解（标量公式而非矩阵辅助函数），以降低共因错误的概率。

type refPixel struct {
	g          [3]*big.Rat
	triple     [3]int
	valid      bool
	consistent int
	sqErr      *big.Rat
}

type refSolution struct {
	exposure        []*big.Rat
	exposureIndex   int
	pixels          []refPixel
	totalConsistent int
	totalError      *big.Rat
}

// refSolve 对完整候选空间做朴素全枚举，返回参考最优解。
func refSolve(p *Problem) (*refSolution, error) {
	cands, err := validateProblem(p)
	if err != nil {
		return nil, err
	}
	n := len(p.Lights)

	var best *refSolution
	exposure := make([]*big.Rat, n)
	combo := make([]int, n)

	var recurse func(light int)
	recurse = func(light int) {
		if light == n {
			cur := refEvaluate(p, exposure, refComboIndex(cands, combo))
			if refAnySolved(cur) && (best == nil || refBetter(cur, best)) {
				best = cur
			}
			return
		}
		for idx, value := range cands[light] {
			exposure[light] = value
			combo[light] = idx
			recurse(light + 1)
		}
	}
	recurse(0)

	if best == nil {
		return nil, ErrNoFeasibleSolution
	}
	return best, nil
}

func refAnySolved(sol *refSolution) bool {
	for i := range sol.pixels {
		if sol.pixels[i].valid {
			return true
		}
	}
	return false
}

func refComboIndex(cands [][]*big.Rat, combo []int) int {
	index := 0
	for k := range cands {
		index = index*len(cands[k]) + combo[k]
	}
	return index
}

func refEvaluate(p *Problem, exposure []*big.Rat, expIndex int) *refSolution {
	sol := &refSolution{
		exposure:      append([]*big.Rat(nil), exposure...),
		exposureIndex: expIndex,
		pixels:        make([]refPixel, len(p.Observations)),
		totalError:    big.NewRat(0, 1),
	}
	for px := range p.Observations {
		rp := refSolvePixel(p, px, exposure)
		sol.pixels[px] = rp
		if rp.valid {
			sol.totalConsistent += rp.consistent
			sol.totalError.Add(sol.totalError, rp.sqErr)
		}
	}
	return sol
}

func refSolvePixel(p *Problem, px int, exposure []*big.Rat) refPixel {
	var usable []int
	for k := range p.Lights {
		if !p.Observations[px][k].Saturated {
			usable = append(usable, k)
		}
	}

	var best refPixel
	found := false
	for a := 0; a < len(usable); a++ {
		for b := a + 1; b < len(usable); b++ {
			for c := b + 1; c < len(usable); c++ {
				ids := [3]int{usable[a], usable[b], usable[c]}
				var rows [3][3]*big.Rat
				for r := 0; r < 3; r++ {
					for col := 0; col < 3; col++ {
						rows[r][col] = new(big.Rat).Mul(
							new(big.Rat).Set(exposure[ids[r]]),
							new(big.Rat).Set(p.Lights[ids[r]][col]))
					}
				}
				det := refDet(rows)
				if det.Sign() == 0 {
					continue
				}
				rhs := [3]*big.Rat{
					new(big.Rat).Set(p.Observations[px][ids[0]].Brightness),
					new(big.Rat).Set(p.Observations[px][ids[1]].Brightness),
					new(big.Rat).Set(p.Observations[px][ids[2]].Brightness),
				}
				g := refCramer(rows, det, rhs)
				consistent, sqErr := refScore(p, px, exposure, g)
				cand := refPixel{
					g:          g,
					triple:     ids,
					valid:      true,
					consistent: consistent,
					sqErr:      sqErr,
				}
				if !found || refPixelBetter(cand, best) {
					best = cand
					found = true
				}
			}
		}
	}
	return best
}

// refDet 以独立标量展开式计算行列式。
func refDet(m [3][3]*big.Rat) *big.Rat {
	// 沿首行做代数余子式展开；所有运算数都先复制，绝不原地改写输入。
	cp := func(x *big.Rat) *big.Rat { return new(big.Rat).Set(x) }
	minor := func(c1, c2 int) *big.Rat {
		left := new(big.Rat).Mul(cp(m[1][c1]), cp(m[2][c2]))
		right := new(big.Rat).Mul(cp(m[1][c2]), cp(m[2][c1]))
		return new(big.Rat).Sub(left, right)
	}
	d := big.NewRat(0, 1)
	for c := 0; c < 3; c++ {
		remaining := [2]int{(c + 1) % 3, (c + 2) % 3}
		mi := minor(remaining[0], remaining[1])
		term := new(big.Rat).Mul(cp(m[0][c]), mi)
		if c%2 == 0 {
			d.Add(d, term)
		} else {
			d.Sub(d, term)
		}
	}
	return d
}

func refCramer(m [3][3]*big.Rat, det *big.Rat, rhs [3]*big.Rat) [3]*big.Rat {
	var g [3]*big.Rat
	for col := 0; col < 3; col++ {
		var swapped [3][3]*big.Rat
		for r := 0; r < 3; r++ {
			for c := 0; c < 3; c++ {
				if c == col {
					swapped[r][c] = new(big.Rat).Set(rhs[r])
				} else {
					swapped[r][c] = new(big.Rat).Set(m[r][c])
				}
			}
		}
		num := refDet(swapped)
		g[col] = new(big.Rat).Quo(num, new(big.Rat).Set(det))
	}
	return g
}

func refScore(p *Problem, px int, exposure []*big.Rat, g [3]*big.Rat) (int, *big.Rat) {
	consistent := 0
	sqErr := big.NewRat(0, 1)
	for k := range p.Lights {
		if p.Observations[px][k].Saturated {
			continue
		}
		pred := refPredict(exposure[k], p.Lights[k], g)
		res := new(big.Rat).Sub(pred, p.Observations[px][k].Brightness)
		squared := new(big.Rat).Mul(new(big.Rat).Set(res), new(big.Rat).Set(res))
		sqErr.Add(sqErr, squared)
		absRes := new(big.Rat).Abs(new(big.Rat).Set(res))
		if absRes.Cmp(p.ResidualThreshold) <= 0 {
			consistent++
		}
	}
	return consistent, sqErr
}

func refPredict(e *big.Rat, light [3]*big.Rat, g [3]*big.Rat) *big.Rat {
	dot := big.NewRat(0, 1)
	for i := 0; i < 3; i++ {
		dot.Add(dot, new(big.Rat).Mul(light[i], g[i]))
	}
	raw := new(big.Rat).Mul(e, dot)
	if raw.Sign() < 0 {
		return big.NewRat(0, 1)
	}
	return raw
}

func refPixelBetter(a, b refPixel) bool {
	if a.consistent != b.consistent {
		return a.consistent > b.consistent
	}
	if cmp := a.sqErr.Cmp(b.sqErr); cmp != 0 {
		return cmp < 0
	}
	for i := 0; i < 3; i++ {
		if a.triple[i] != b.triple[i] {
			return a.triple[i] < b.triple[i]
		}
	}
	return false
}

func refBetter(a, b *refSolution) bool {
	if a.totalConsistent != b.totalConsistent {
		return a.totalConsistent > b.totalConsistent
	}
	if cmp := a.totalError.Cmp(b.totalError); cmp != 0 {
		return cmp < 0
	}
	if a.exposureIndex != b.exposureIndex {
		return a.exposureIndex < b.exposureIndex
	}
	for px := range a.pixels {
		pa, pb := a.pixels[px], b.pixels[px]
		if !pa.valid || !pb.valid {
			continue
		}
		for i := 0; i < 3; i++ {
			if pa.triple[i] != pb.triple[i] {
				return pa.triple[i] < pb.triple[i]
			}
		}
	}
	return false
}
