package netsec

import (
	"math/rand"
	"testing"
)

// progGen 生成结构受限、规模有界的随机程序：
// 变量固定为 o0..o5（对象位）与 s0..s5（字符串位），语句 8-12 条，
// 嵌套最多两层，保证具体穷举的路径数可控。
type progGen struct {
	rng       *rand.Rand
	seq       int
	allocs    int
	objVars   []string
	strVars   []string
	allStrs   []string
	body      []Node
	extCount  int
	branchCnt int
	loopCnt   int
}

func newProgGen(seed int64) *progGen {
	return &progGen{
		rng:     rand.New(rand.NewSource(seed)),
		objVars: []string{"o0", "o1", "o2", "o3", "o4", "o5"},
		strVars: []string{"s0", "s1", "s2", "s3", "s4", "s5"},
		allStrs: append([]string(nil), AllowedStrings...),
	}
}

func (g *progGen) id(prefix string) string {
	g.seq++
	return prefix + itoa(g.seq)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func (g *progGen) pickObj() string { return g.objVars[g.rng.Intn(len(g.objVars))] }
func (g *progGen) pickStr() string { return g.strVars[g.rng.Intn(len(g.strVars))] }
func (g *progGen) pickConst() string {
	return g.allStrs[g.rng.Intn(len(g.allStrs))]
}

func (g *progGen) generate() *Program {
	n := 8 + g.rng.Intn(5)
	for i := 0; i < n; i++ {
		g.body = append(g.body, g.genStmt(0))
	}
	return &Program{ID: "prog", Body: g.body}
}

func (g *progGen) genStmt(depth int) Node {
	if depth < 2 && g.branchCnt < 3 && g.rng.Intn(100) < 18 {
		return g.genIf(depth)
	}
	if depth < 2 && g.loopCnt < 2 && g.rng.Intn(100) < 12 {
		return g.genLoop(depth)
	}
	return g.genFlat()
}

func (g *progGen) genFlat() Node {
	switch g.rng.Intn(6) {
	case 0:
		if g.allocs >= MaxObjects {
			return g.genConst()
		}
		g.allocs++
		return &Alloc{ID: g.id("a"), Dest: g.pickObj()}
	case 1:
		return g.genConst()
	case 2:
		if g.extCount < 2 {
			g.extCount++
			return &External{ID: g.id("e"), Dest: g.pickStr()}
		}
		return g.genConst()
	case 3:
		return &Alias{ID: g.id("al"), Dest: g.pickObj(), Src: g.pickObj()}
	case 4:
		return &Load{ID: g.id("ld"), Dest: g.maybeStrDest(), Obj: g.pickObj(), Key: g.pickStr()}
	default:
		return &Store{ID: g.id("st"), Obj: g.pickObj(), Key: g.pickStr(), Val: g.maybeValue()}
	}
}

func (g *progGen) genConst() Node {
	return &Const{ID: g.id("c"), Dest: g.pickStr(), Value: g.pickConst()}
}

func (g *progGen) maybeStrDest() string {
	if g.rng.Intn(2) == 0 {
		return g.pickStr()
	}
	return g.pickObj()
}

func (g *progGen) maybeValue() string {
	if g.rng.Intn(2) == 0 {
		return g.pickObj()
	}
	return g.pickStr()
}

func (g *progGen) genIf(depth int) Node {
	g.branchCnt++
	thenN := 1 + g.rng.Intn(2)
	elseN := 1 + g.rng.Intn(2)
	then := make([]Node, 0, thenN)
	elseb := make([]Node, 0, elseN)
	for i := 0; i < thenN; i++ {
		then = append(then, g.genStmt(depth+1))
	}
	for i := 0; i < elseN; i++ {
		elseb = append(elseb, g.genStmt(depth+1))
	}
	return &If{ID: g.id("if"), Then: then, Else: elseb}
}

func (g *progGen) genLoop(depth int) Node {
	g.loopCnt++
	count := 1 + g.rng.Intn(4)
	bodyN := 1 + g.rng.Intn(2)
	body := make([]Node, 0, bodyN)
	for i := 0; i < bodyN; i++ {
		body = append(body, g.genStmt(depth+1))
	}
	return &Loop{ID: g.id("lp"), Count: count, Body: body}
}

// TestExhaustiveNoFalseNegative 对数百个随机程序同时运行抽象分析与
// 具体穷举执行，断言：具体执行中真实发生的每个共享原型写，
// 静态分析必须报告（无漏报）。
func TestExhaustiveNoFalseNegative(t *testing.T) {
	const programs = 1000
	const pathCap = 20000

	totalReports := 0
	concreteDangerPrograms := 0
	falsePositiveReports := 0
	for seed := int64(1); seed <= programs; seed++ {
		gen := newProgGen(seed)
		prog := gen.generate()
		if err := prog.Validate(); err != nil {
			t.Fatalf("seed %d produced invalid program: %v", seed, err)
		}

		res, err := Analyze(prog)
		if err != nil {
			t.Fatalf("seed %d analyze error: %v", seed, err)
		}
		reported := map[string]bool{}
		for _, r := range res.DangerousWrites {
			reported[r.NodeID] = true
		}
		totalReports += len(reported)

		conc, err := ExploreConcrete(prog, pathCap)
		if err != nil {
			t.Fatalf("seed %d explore error: %v", seed, err)
		}
		if conc.Truncated {
			t.Fatalf("seed %d hit concrete path cap %d; shrink the generator", seed, pathCap)
		}
		if len(conc.DangerNodes) > 0 {
			concreteDangerPrograms++
		}
		for id := range reported {
			if !contains(conc.DangerNodes, id) {
				falsePositiveReports++
			}
		}
		for _, dangerID := range conc.DangerNodes {
			if !reported[dangerID] {
				t.Fatalf("seed %d FALSE NEGATIVE: concrete danger %s not reported", seed, dangerID)
			}
		}
	}
	t.Logf("programs=%d reports=%d concrete-danger-programs=%d conservative(possible-over-approx)reports=%d",
		programs, totalReports, concreteDangerPrograms, falsePositiveReports)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// TestValidationLimits 覆盖结构性校验。
func TestValidationLimits(t *testing.T) {
	badConst := &Program{ID: "p", Body: []Node{
		&Const{ID: "c1", Dest: "s", Value: "evil"},
	}}
	if _, err := Analyze(badConst); err == nil {
		t.Fatal("illegal constant must fail validation")
	}

	badLoop := &Program{ID: "p", Body: []Node{
		&Loop{ID: "lp1", Count: MaxLoops + 1, Body: nil},
	}}
	if _, err := Analyze(badLoop); err == nil {
		t.Fatal("loop over 16 must fail validation")
	}

	manyAllocs := &Program{ID: "p", Body: func() []Node {
		out := make([]Node, 0, MaxObjects+1)
		for i := 0; i < MaxObjects+1; i++ {
			out = append(out, &Alloc{ID: "a" + itoa(i+1), Dest: "o"})
		}
		return out
	}()}
	if _, err := Analyze(manyAllocs); err == nil {
		t.Fatal("more than 32 allocations must fail validation")
	}

	dup := &Program{ID: "p", Body: []Node{
		&Alloc{ID: "x", Dest: "o"},
		&Const{ID: "x", Dest: "s", Value: StrA},
	}}
	if _, err := Analyze(dup); err == nil {
		t.Fatal("duplicate node id must fail validation")
	}
}
