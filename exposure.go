package taskpkg

import "math/big"

// validateProblem 检查 Problem 的规模与数值契约，返回深拷贝后的候选
// 曝光（首灯被规范化为恰好一个候选 1）以及错误。
func validateProblem(p *Problem) ([][]*big.Rat, error) {
	if p == nil {
		return nil, ErrInvalidProblem
	}
	n := len(p.Lights)
	if n < RequiredTripleLen || n > MaxLights {
		return nil, ErrInvalidProblem
	}
	for k := 0; k < n; k++ {
		for c := 0; c < 3; c++ {
			if p.Lights[k][c] == nil {
				return nil, ErrInvalidProblem
			}
		}
	}
	if len(p.ExposureCandidates) != n {
		return nil, ErrInvalidProblem
	}
	cands := make([][]*big.Rat, n)
	for k := 0; k < n; k++ {
		list := p.ExposureCandidates[k]
		if len(list) == 0 || len(list) > MaxExposureCands {
			return nil, ErrInvalidProblem
		}
		for _, e := range list {
			if e == nil || e.Sign() <= 0 {
				return nil, ErrInvalidProblem
			}
		}
		if k == 0 {
			// 首灯曝光固定为 1：忽略传入内容，规范化为单一候选 1。
			cands[0] = []*big.Rat{big.NewRat(1, 1)}
			continue
		}
		cp := make([]*big.Rat, len(list))
		for i, e := range list {
			cp[i] = cloneRat(e)
		}
		cands[k] = cp
	}
	if len(p.Observations) == 0 || len(p.Observations) > MaxPixels {
		return nil, ErrInvalidProblem
	}
	for px := range p.Observations {
		if len(p.Observations[px]) != n {
			return nil, ErrInvalidProblem
		}
		for k := 0; k < n; k++ {
			b := p.Observations[px][k].Brightness
			if b == nil || b.Sign() < 0 {
				return nil, ErrInvalidProblem
			}
		}
	}
	if p.ResidualThreshold == nil || p.ResidualThreshold.Sign() < 0 {
		return nil, ErrInvalidProblem
	}
	if p.Budget < 0 {
		return nil, ErrInvalidProblem
	}
	return cands, nil
}

// exposureEnumerator 按字典序枚举曝光向量：灯 0 为最高位、灯 n-1 为
// 最低位（变化最快），索引与 next 返回顺序一致，从全零组合开始。
type exposureEnumerator struct {
	cands [][]*big.Rat
	idx   []int
	done  bool
}

func newExposureEnumerator(cands [][]*big.Rat) *exposureEnumerator {
	return &exposureEnumerator{
		cands: cands,
		idx:   make([]int, len(cands)),
	}
}

// total 返回完整枚举的曝光向量数量。
func (e *exposureEnumerator) total() int {
	total := 1
	for _, list := range e.cands {
		total *= len(list)
	}
	return total
}

// index 返回当前曝光向量的字典序索引。
func (e *exposureEnumerator) index() int {
	index := 0
	for k := 0; k < len(e.cands); k++ {
		index = index*len(e.cands[k]) + e.idx[k]
	}
	return index
}

// current 返回当前曝光向量（候选对象本身只读共享，不做拷贝）。
func (e *exposureEnumerator) current() []*big.Rat {
	out := make([]*big.Rat, len(e.cands))
	for k := 0; k < len(e.cands); k++ {
		out[k] = e.cands[k][e.idx[k]]
	}
	return out
}

// next 推进到下一字典序组合，返回是否仍有有效组合。
func (e *exposureEnumerator) next() bool {
	if e.done {
		return false
	}
	for k := len(e.cands) - 1; k >= 0; k-- {
		e.idx[k]++
		if e.idx[k] < len(e.cands[k]) {
			return true
		}
		e.idx[k] = 0
	}
	e.done = true
	return false
}
