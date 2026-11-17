package netsec

import (
	"math/rand"
	"reflect"
	"testing"
)

// 示例电路：
//
//	0: INPUT a (pin 0)
//	1: INPUT b (pin 1)
//	2: NOT  b        -> nb
//	3: AND  a, nb    -> s
//	4: OUTPUT s      (安全输出位)
//
// 故障 (2, stuck-1) 使 s = a，在输入 11（向量 3）处安全位 0->1；
// 公开测试 {00, 01} 无法观测到它。
func exampleCircuit() *Circuit {
	return &Circuit{
		NumInputs: 2,
		Secure:    4,
		Nodes: []Node{
			{Op: GateInput, A: -1, B: -1, Pin: 0},
			{Op: GateInput, A: -1, B: -1, Pin: 1},
			{Op: GateNot, A: 1, B: -1},
			{Op: GateAnd, A: 0, B: 2},
			{Op: GateOutput, A: 3, B: -1},
		},
	}
}

func TestAnalyzeFindsEscape(t *testing.T) {
	rep := Analyze(exampleCircuit(), []uint32{0, 1}, -1)
	if rep.Status != StatusFound {
		t.Fatalf("status = %v, want found (err=%v)", rep.Status, rep.Err)
	}
	if rep.FaultsTotal != 4 || rep.FaultsDone != 4 {
		t.Fatalf("faults total/done = %d/%d, want 4/4", rep.FaultsTotal, rep.FaultsDone)
	}
	if len(rep.Escapes) != 1 {
		t.Fatalf("escapes = %d, want 1: %+v", len(rep.Escapes), rep.Escapes)
	}
	esc := rep.Escapes[0]
	if esc.Fault != (Fault{Node: 2, Stuck: 1}) {
		t.Fatalf("fault = %+v, want node 2 stuck 1", esc.Fault)
	}
	if esc.Input != 3 {
		t.Fatalf("escape input = %d, want 3", esc.Input)
	}
	wantNormal := []byte{1, 1, 0, 0, 0}
	wantFaulty := []byte{1, 1, 1, 1, 1}
	if !reflect.DeepEqual(esc.NormalState, wantNormal) {
		t.Fatalf("normal state = %v, want %v", esc.NormalState, wantNormal)
	}
	if !reflect.DeepEqual(esc.FaultyState, wantFaulty) {
		t.Fatalf("faulty state = %v, want %v", esc.FaultyState, wantFaulty)
	}
}

func TestAnalyzeExhaustiveNone(t *testing.T) {
	// 公开测试覆盖全部输入空间：任何危险故障都会被观测到。
	rep := Analyze(exampleCircuit(), []uint32{0, 1, 2, 3}, -1)
	if rep.Status != StatusNone {
		t.Fatalf("status = %v, want none", rep.Status)
	}
	if len(rep.Escapes) != 0 {
		t.Fatalf("escapes = %v, want empty", rep.Escapes)
	}
}

func TestAnalyzeBudgetUnknown(t *testing.T) {
	rep := Analyze(exampleCircuit(), []uint32{0, 1}, 2)
	if rep.Status != StatusUnknown {
		t.Fatalf("status = %v, want unknown", rep.Status)
	}
	if rep.FaultsDone != 2 || rep.FaultsTotal != 4 {
		t.Fatalf("faults done/total = %d/%d, want 2/4", rep.FaultsDone, rep.FaultsTotal)
	}
}

func TestAnalyzeInvalid(t *testing.T) {
	rep := Analyze(exampleCircuit(), []uint32{4}, -1)
	if rep.Status != StatusError || rep.Err == nil {
		t.Fatalf("status = %v err = %v, want error", rep.Status, rep.Err)
	}
	bad := &Circuit{NumInputs: 1, Secure: 0, Nodes: []Node{{Op: GateInput, Pin: 0, A: -1, B: -1}}}
	if rep := Analyze(bad, nil, -1); rep.Status != StatusError {
		t.Fatalf("status = %v, want error for non-OUTPUT secure node", rep.Status)
	}
}

func TestMinimalAdditionalTestsGap(t *testing.T) {
	// 公开测试 {0,1}：危险故障为 (2,stuck1)（仅向量 3 可观测）
	// 与 (3,stuck1)（向量 {0,2,3} 可观测，已被向量 0 覆盖），
	// 只有 (2,stuck1) 待覆盖。
	res := MinimalAdditionalTests(exampleCircuit(), []uint32{0, 1}, -1)
	if res.Status != StatusFound {
		t.Fatalf("status = %v, want found (err=%v)", res.Status, res.Err)
	}
	if !reflect.DeepEqual(res.Vectors, []uint32{3}) {
		t.Fatalf("vectors = %v, want [3]", res.Vectors)
	}
	if len(res.Dangerous) != 2 {
		t.Fatalf("dangerous = %v, want 2 faults", res.Dangerous)
	}
}

// 字典序决胜电路：
//
//	0: INPUT a (pin 0，未使用)
//	1: INPUT b (pin 1)
//	2: AND b, b   -> r  = b
//	3: NOT b      -> r2 = ~b
//	4: AND r, r2  -> s  = 0（恒为 0）
//	5: OUTPUT s   (安全输出位)
//
// 危险故障：(2,stuck1) 在向量 {0,1} 可观测，(3,stuck1) 在 {2,3} 可观测，
// (4,stuck1) 在 {0,1,2,3} 可观测。
func tieBreakCircuit() *Circuit {
	return &Circuit{
		NumInputs: 2,
		Secure:    5,
		Nodes: []Node{
			{Op: GateInput, A: -1, B: -1, Pin: 0},
			{Op: GateInput, A: -1, B: -1, Pin: 1},
			{Op: GateAnd, A: 1, B: 1},
			{Op: GateNot, A: 1, B: -1},
			{Op: GateAnd, A: 2, B: 3},
			{Op: GateOutput, A: 4, B: -1},
		},
	}
}

func TestMinimalAdditionalTestsStandalone(t *testing.T) {
	// 无公开测试：最小规模为 2，候选 {0,2},{0,3},{1,2},{1,3}，
	// 字典序最小为 [0 2]。
	res := MinimalAdditionalTests(tieBreakCircuit(), nil, -1)
	if res.Status != StatusFound {
		t.Fatalf("status = %v, want found (err=%v)", res.Status, res.Err)
	}
	if !reflect.DeepEqual(res.Vectors, []uint32{0, 2}) {
		t.Fatalf("vectors = %v, want [0 2]", res.Vectors)
	}
	if len(res.Dangerous) != 3 {
		t.Fatalf("dangerous = %v, want 3 faults", res.Dangerous)
	}
}

func TestMinimalAdditionalTestsPublicExcluded(t *testing.T) {
	// 公开测试 {0}：故障 (2,stuck1) 与 (4,stuck1) 已被观测，
	// 仅剩 (3,stuck1) 待覆盖，候选向量 {2,3}，取最小者。
	res := MinimalAdditionalTests(tieBreakCircuit(), []uint32{0}, -1)
	if res.Status != StatusFound {
		t.Fatalf("status = %v, want found (err=%v)", res.Status, res.Err)
	}
	if !reflect.DeepEqual(res.Vectors, []uint32{2}) {
		t.Fatalf("vectors = %v, want [2]", res.Vectors)
	}
}

func TestMinimalAdditionalTestsNothingToCover(t *testing.T) {
	res := MinimalAdditionalTests(exampleCircuit(), []uint32{0, 1, 2, 3}, -1)
	if res.Status != StatusFound {
		t.Fatalf("status = %v, want found", res.Status)
	}
	if len(res.Vectors) != 0 {
		t.Fatalf("vectors = %v, want empty", res.Vectors)
	}
}

func TestMinSetCoverLexicographic(t *testing.T) {
	// 4 个故障；最优规模 2，解 {0,3} 与 {1,2}，字典序最小为 [0 3]。
	cover := []uint64{
		0b0011, // 向量 0 覆盖故障 0,1
		0b0101, // 向量 1 覆盖故障 0,2
		0b1010, // 向量 2 覆盖故障 1,3
		0b1100, // 向量 3 覆盖故障 2,3
	}
	vecs, feasible, aborted := minSetCover(cover, 0b1111, nil)
	if aborted || !feasible {
		t.Fatalf("feasible=%v aborted=%v", feasible, aborted)
	}
	if !reflect.DeepEqual(vecs, []int{0, 3}) {
		t.Fatalf("vecs = %v, want [0 3]", vecs)
	}
}

func TestMinSetCoverBudget(t *testing.T) {
	cover := []uint64{0b0001, 0b0010, 0b0100, 0b1000}
	budget := int64(0)
	_, _, aborted := minSetCover(cover, 0b1111, &budget)
	if !aborted {
		t.Fatal("want aborted with zero budget")
	}
}

// 随机小规模实例上与暴力枚举对拍：规模最小且字典序最小。
func TestMinSetCoverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(115))
	for trial := 0; trial < 200; trial++ {
		nf := 1 + rng.Intn(6)
		nv := 1 + rng.Intn(8)
		universe := uint64(1)<<uint(nf) - 1
		cover := make([]uint64, nv)
		for v := range cover {
			cover[v] = uint64(rng.Intn(1 << uint(nf)))
		}
		// 保证可行：随机指定一个向量覆盖全部故障。
		cover[rng.Intn(nv)] = universe

		got, feasible, aborted := minSetCover(cover, universe, nil)
		if aborted || !feasible {
			t.Fatalf("trial %d: feasible=%v aborted=%v", trial, feasible, aborted)
		}
		want := bruteForceMinCover(cover, universe)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: got %v, want %v (cover=%v)", trial, got, want, cover)
		}
	}
}

// bruteForceMinCover 枚举全部组合，返回规模最小、字典序最小的升序下标组合。
func bruteForceMinCover(cover []uint64, universe uint64) []int {
	nv := len(cover)
	var best []int
	for k := 1; k <= nv; k++ {
		var comb []int
		var found []int
		var dfs func(start int)
		dfs = func(start int) {
			if found != nil {
				return
			}
			if len(comb) == k {
				var u uint64
				for _, v := range comb {
					u |= cover[v]
				}
				if u&universe == universe {
					found = append([]int(nil), comb...)
				}
				return
			}
			for i := start; i < nv; i++ {
				comb = append(comb, i)
				dfs(i + 1)
				comb = comb[:len(comb)-1]
			}
		}
		dfs(0)
		if found != nil {
			best = found
			break
		}
	}
	return best
}

// 规模上限压力测试：20 个危险故障、4096 个候选向量。
func TestMinSetCoverScale(t *testing.T) {
	rng := rand.New(rand.NewSource(116))
	cover := make([]uint64, MaxSpace)
	for v := range cover {
		cover[v] = uint64(rng.Int63n(1 << 20))
	}
	universe := uint64(1<<20 - 1)
	vecs, feasible, aborted := minSetCover(cover, universe, nil)
	if aborted || !feasible {
		t.Fatalf("feasible=%v aborted=%v", feasible, aborted)
	}
	var u uint64
	for _, v := range vecs {
		u |= cover[v]
	}
	if u&universe != universe {
		t.Fatalf("solution %v does not cover universe", vecs)
	}
	t.Logf("covered 20 faults with %d vectors", len(vecs))
}

// 深搜索：每个故障仅一个专属向量可覆盖，最小覆盖为全部 10 个向量。
func TestMinSetCoverDeep(t *testing.T) {
	cover := make([]uint64, 10)
	for i := range cover {
		cover[i] = 1 << uint(i)
	}
	vecs, feasible, aborted := minSetCover(cover, 1<<10-1, nil)
	if aborted || !feasible {
		t.Fatalf("feasible=%v aborted=%v", feasible, aborted)
	}
	want := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	if !reflect.DeepEqual(vecs, want) {
		t.Fatalf("vecs = %v, want %v", vecs, want)
	}
}

// 随机电路位并行仿真与独立标量回放的全输入交叉验证。
func TestBitParallelMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(112))
	for trial := 0; trial < 20; trial++ {
		numInputs := 1 + rng.Intn(MaxInputs)
		c := randomCircuit(rng, numInputs, 8+rng.Intn(30))
		space := c.space()
		words := wordsOf(space)
		valid := validMask(space)
		pats := inputPatterns(c.NumInputs, space)
		vals := newBitsets(len(c.Nodes), words)
		simulate(c, pats, valid, vals, 0)
		for x := 0; x < space; x++ {
			scalar := evalScalar(c, uint32(x), nil)
			for i := range c.Nodes {
				if got := vals[i].bit(x); got != int(scalar[i]) {
					t.Fatalf("trial %d input %d node %d: parallel=%d scalar=%d",
						trial, x, i, got, scalar[i])
				}
			}
		}
	}
}

// 随机电路故障仿真与标量回放交叉验证。
func TestFaultSimMatchesScalar(t *testing.T) {
	rng := rand.New(rand.NewSource(113))
	for trial := 0; trial < 10; trial++ {
		numInputs := 1 + rng.Intn(8)
		c := randomCircuit(rng, numInputs, 10+rng.Intn(20))
		space := c.space()
		words := wordsOf(space)
		valid := validMask(space)
		pats := inputPatterns(c.NumInputs, space)
		normal := newBitsets(len(c.Nodes), words)
		simulate(c, pats, valid, normal, 0)
		for _, n := range c.faultableNodes() {
			for stuck := 0; stuck <= 1; stuck++ {
				vals := cloneBitsets(normal)
				setConst(vals[n], valid, stuck)
				simulate(c, pats, valid, vals, n+1)
				fault := Fault{Node: n, Stuck: stuck}
				for x := 0; x < space; x++ {
					scalar := evalScalar(c, uint32(x), &fault)
					for _, o := range c.outputNodes() {
						if got := vals[o].bit(x); got != int(scalar[o]) {
							t.Fatalf("trial %d fault %+v input %d out %d: parallel=%d scalar=%d",
								trial, fault, x, o, got, scalar[o])
						}
					}
				}
			}
		}
	}
}

// randomCircuit 生成随机合法电路：numInputs 个输入后跟 numGates 个门，
// 末节点为安全输出，另随机挂若干输出。
func randomCircuit(rng *rand.Rand, numInputs, numGates int) *Circuit {
	c := &Circuit{NumInputs: numInputs}
	for p := 0; p < numInputs; p++ {
		c.Nodes = append(c.Nodes, Node{Op: GateInput, A: -1, B: -1, Pin: p})
	}
	for g := 0; g < numGates; g++ {
		i := len(c.Nodes)
		a := rng.Intn(i)
		switch rng.Intn(3) {
		case 0:
			c.Nodes = append(c.Nodes, Node{Op: GateAnd, A: a, B: rng.Intn(i)})
		case 1:
			c.Nodes = append(c.Nodes, Node{Op: GateXor, A: a, B: rng.Intn(i)})
		default:
			c.Nodes = append(c.Nodes, Node{Op: GateNot, A: a, B: -1})
		}
	}
	c.Nodes = append(c.Nodes, Node{Op: GateOutput, A: len(c.Nodes) - 1, B: -1})
	c.Secure = len(c.Nodes) - 1
	for k := 0; k < 2; k++ {
		c.Nodes = append(c.Nodes, Node{Op: GateOutput, A: rng.Intn(len(c.Nodes)), B: -1})
	}
	return c
}

// 端到端：随机电路上 Analyze 的逃逸结论与暴力标量枚举一致。
func TestAnalyzeMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(114))
	for trial := 0; trial < 10; trial++ {
		numInputs := 1 + rng.Intn(6)
		c := randomCircuit(rng, numInputs, 6+rng.Intn(10))
		space := c.space()
		var tests []uint32
		for x := 0; x < space; x++ {
			if rng.Intn(2) == 0 {
				tests = append(tests, uint32(x))
			}
		}
		rep := Analyze(c, tests, -1)
		if rep.Status != StatusFound && rep.Status != StatusNone {
			t.Fatalf("trial %d: status = %v", trial, rep.Status)
		}
		want := bruteForceEscapes(c, tests)
		if len(rep.Escapes) != len(want) {
			t.Fatalf("trial %d: escapes = %d, brute force = %d", trial, len(rep.Escapes), len(want))
		}
		for i, esc := range rep.Escapes {
			if esc.Fault != want[i] {
				t.Fatalf("trial %d: escape %d = %+v, want %+v", trial, i, esc.Fault, want[i])
			}
		}
	}
}

// bruteForceEscapes 用纯标量逐输入枚举逃逸故障，作为参照实现。
func bruteForceEscapes(c *Circuit, tests []uint32) []Fault {
	var out []Fault
	outputs := c.outputNodes()
	space := uint32(c.space())
	for _, n := range c.faultableNodes() {
		for stuck := 0; stuck <= 1; stuck++ {
			f := Fault{Node: n, Stuck: stuck}
			detected := false
			for _, t := range tests {
				ns, fs := evalScalar(c, t, nil), evalScalar(c, t, &f)
				for _, o := range outputs {
					if ns[o] != fs[o] {
						detected = true
					}
				}
			}
			if detected {
				continue
			}
			for x := uint32(0); x < space; x++ {
				if evalScalar(c, x, nil)[c.Secure] == 0 && evalScalar(c, x, &f)[c.Secure] == 1 {
					out = append(out, f)
					break
				}
			}
		}
	}
	return out
}
