package netsec

import "math/rand"

// randCircuit 生成随机电路：nIn 个输入、nGates 个内部门、1 个输出。
func randCircuit(r *rand.Rand, nIn, nGates int) *Circuit {
	nodes := make([]Node, 0, nIn+nGates+1)
	for i := 0; i < nIn; i++ {
		nodes = append(nodes, Node{Kind: KindInput})
	}
	kinds := []NodeKind{KindAnd, KindXor, KindNot}
	for g := 0; g < nGates; g++ {
		kind := kinds[r.Intn(len(kinds))]
		n := len(nodes)
		if kind == KindNot {
			nodes = append(nodes, Node{Kind: kind, Args: []int{r.Intn(n)}})
		} else {
			nodes = append(nodes, Node{Kind: kind, Args: []int{r.Intn(n), r.Intn(n)}})
		}
	}
	nodes = append(nodes, Node{Kind: KindOutput, Args: []int{r.Intn(len(nodes))}})
	c, err := NewCircuit(nodes)
	if err != nil {
		panic(err)
	}
	return c
}

// bruteDangerous 暴力判定故障是否危险，并返回最小逃逸输入（无则 -1）。
func bruteDangerous(c *Circuit, f Fault) (bool, int) {
	escape := -1
	for j := 0; j < c.InputSpace(); j++ {
		in := uint32(j)
		if !c.Output(c.Eval(in)) && c.Output(c.EvalWithFault(in, f)) {
			if escape < 0 {
				escape = j
			}
		}
	}
	return escape >= 0, escape
}

// bruteDetected 暴力判定故障是否被测试集发现。
func bruteDetected(c *Circuit, f Fault, tests []uint32) bool {
	for _, t := range tests {
		if c.Output(c.Eval(t)) != c.Output(c.EvalWithFault(t, f)) {
			return true
		}
	}
	return false
}

// bruteMinCover 暴力枚举全部向量子集，求覆盖 remaining 中全部故障的
// 最小新增向量集；数量并列时取升序序列字典序最小者。仅用于小规模校验。
func bruteMinCover(c *Circuit, remaining []Fault) []uint32 {
	nf := len(remaining)
	if nf == 0 {
		return nil
	}
	space := c.InputSpace()
	// 每个向量对剩余故障的覆盖掩码。
	type vec struct {
		j    uint32
		mask uint64
	}
	var cands []vec
	for j := 0; j < space; j++ {
		var m uint64
		for k, f := range remaining {
			if c.Output(c.Eval(uint32(j))) != c.Output(c.EvalWithFault(uint32(j), f)) {
				m |= 1 << k
			}
		}
		if m != 0 {
			cands = append(cands, vec{uint32(j), m})
		}
	}
	full := uint64(1)<<nf - 1
	var best []uint32
	// 按子集大小递增枚举；同大小内跟踪字典序最小。
	var visit func(start int, chosen []uint32, mask uint64)
	visit = func(start int, chosen []uint32, mask uint64) {
		if best != nil && len(chosen) >= len(best) {
			return
		}
		if mask == full {
			if best == nil || len(chosen) < len(best) || lexLess(chosen, best) {
				best = append([]uint32(nil), chosen...)
			}
			return
		}
		for i := start; i < len(cands); i++ {
			if mask|cands[i].mask == mask {
				continue
			}
			visit(i+1, append(chosen, cands[i].j), mask|cands[i].mask)
		}
	}
	visit(0, nil, 0)
	return best
}

// lexLess 比较两个升序向量序列的字典序。
func lexLess(a, b []uint32) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// randTests 生成随机公开测试集。
func randTests(r *rand.Rand, space, max int) []uint32 {
	n := r.Intn(max + 1)
	tests := make([]uint32, n)
	for i := range tests {
		tests[i] = uint32(r.Intn(space))
	}
	return tests
}
