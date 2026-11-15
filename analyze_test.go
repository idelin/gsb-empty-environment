package netsec

import (
	"math/rand"
	"testing"
)

// nand2 返回样例电路：a, b 输入；n2 = a AND b；n3 = NOT n2；out = n3。
func nand2(t *testing.T) *Circuit {
	t.Helper()
	c, err := NewCircuit([]Node{
		{Kind: KindInput},
		{Kind: KindInput},
		{Kind: KindAnd, Args: []int{0, 1}},
		{Kind: KindNot, Args: []int{2}},
		{Kind: KindOutput, Args: []int{3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAnalyzeKnownEscapes(t *testing.T) {
	c := nand2(t)
	// 公开测试缺少输入 3（a=b=1），两个危险故障均逃逸。
	rep, err := Analyze(c, []uint32{0, 1, 2}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusFound {
		t.Fatalf("Status = %v, 期望 found", rep.Status)
	}
	if rep.FaultsTotal != 4 || rep.FaultsAnalyzed != 4 {
		t.Fatalf("故障计数 = %d/%d, 期望 4/4", rep.FaultsAnalyzed, rep.FaultsTotal)
	}
	if len(rep.Findings) != 2 {
		t.Fatalf("Findings = %d, 期望 2", len(rep.Findings))
	}
	wantFaults := []Fault{{Node: 2, StuckAt: false}, {Node: 3, StuckAt: true}}
	for i, fd := range rep.Findings {
		if fd.Fault != wantFaults[i] {
			t.Errorf("Findings[%d].Fault = %v, 期望 %v", i, fd.Fault, wantFaults[i])
		}
		if fd.EscapeInput != 3 {
			t.Errorf("Findings[%d].EscapeInput = %d, 期望 3", i, fd.EscapeInput)
		}
		if c.Output(fd.NormalStates) || !c.Output(fd.FaultyStates) {
			t.Errorf("Findings[%d]: 逃逸输入未复现 0→1", i)
		}
	}
	// 第一个故障的逐节点状态：正常 a=1,b=1,n2=1,n3=0,out=0；
	// 故障（n2 卡 0）：n2=0,n3=1,out=1。
	fd := rep.Findings[0]
	wantNormal := []bool{true, true, true, false, false}
	wantFaulty := []bool{true, true, false, true, true}
	for i := range wantNormal {
		if fd.NormalStates[i] != wantNormal[i] {
			t.Errorf("NormalStates[%d] = %v, 期望 %v", i, fd.NormalStates[i], wantNormal[i])
		}
		if fd.FaultyStates[i] != wantFaulty[i] {
			t.Errorf("FaultyStates[%d] = %v, 期望 %v", i, fd.FaultyStates[i], wantFaulty[i])
		}
	}
	// 公开测试包含输入 3 后，两个故障都被发现。
	rep2, err := Analyze(c, []uint32{0, 1, 2, 3}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusNone || len(rep2.Findings) != 0 {
		t.Fatalf("Status = %v, Findings = %d, 期望 none/0", rep2.Status, len(rep2.Findings))
	}
	if len(rep2.Dangerous) != 2 {
		t.Fatalf("Dangerous = %d, 期望 2（危险但已被发现）", len(rep2.Dangerous))
	}
}

func TestAnalyzeBudgetExhausted(t *testing.T) {
	c := nand2(t)
	rep, err := Analyze(c, nil, Options{Budget: 2})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusUnknown {
		t.Fatalf("Status = %v, 期望 unknown", rep.Status)
	}
	if rep.FaultsAnalyzed != 2 || rep.FaultsTotal != 4 {
		t.Fatalf("故障计数 = %d/%d, 期望 2/4", rep.FaultsAnalyzed, rep.FaultsTotal)
	}
	if rep.Reason == "" {
		t.Error("Unknown 时应给出原因")
	}
}

// TestAnalyzeAgainstBruteForce 随机电路上与暴力参考实现对照。
func TestAnalyzeAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(112))
	for trial := 0; trial < 300; trial++ {
		c := randCircuit(r, 1+r.Intn(4), 1+r.Intn(10))
		tests := randTests(r, c.InputSpace(), c.InputSpace()/2)
		rep, err := Analyze(c, tests, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if rep.FaultsAnalyzed != rep.FaultsTotal {
			t.Fatalf("trial %d: 未限预算却未枚举全部故障", trial)
		}
		findings := make(map[Fault]Finding)
		for _, fd := range rep.Findings {
			findings[fd.Fault] = fd
		}
		dangerous := make(map[Fault]bool)
		for _, f := range rep.Dangerous {
			dangerous[f] = true
		}
		wantFindings := 0
		for _, f := range c.AllFaults() {
			isDangerous, esc := bruteDangerous(c, f)
			if isDangerous != dangerous[f] {
				t.Fatalf("trial %d 故障 %v: 危险性判定与暴力不符", trial, f)
			}
			isDetected := bruteDetected(c, f, tests)
			fd, found := findings[f]
			if isDangerous && !isDetected {
				wantFindings++
				if !found {
					t.Fatalf("trial %d 故障 %v: 漏报逃逸故障", trial, f)
				}
				if int(fd.EscapeInput) != esc {
					t.Fatalf("trial %d 故障 %v: 逃逸输入 = %d, 暴力期望 %d", trial, f, fd.EscapeInput, esc)
				}
				// 独立回放状态必须与逐节点标量仿真一致。
				for i := range fd.NormalStates {
					if fd.NormalStates[i] != c.Eval(fd.EscapeInput)[i] {
						t.Fatalf("trial %d 故障 %v: 正常状态回放错误", trial, f)
					}
					if fd.FaultyStates[i] != c.EvalWithFault(fd.EscapeInput, f)[i] {
						t.Fatalf("trial %d 故障 %v: 故障状态回放错误", trial, f)
					}
				}
			} else if found {
				t.Fatalf("trial %d 故障 %v: 误报（危险=%v 被发现=%v）", trial, f, isDangerous, isDetected)
			}
		}
		if wantFindings > 0 && rep.Status != StatusFound {
			t.Fatalf("trial %d: Status = %v, 期望 found", trial, rep.Status)
		}
		if wantFindings == 0 && rep.Status != StatusNone {
			t.Fatalf("trial %d: Status = %v, 期望 none", trial, rep.Status)
		}
	}
}

func TestAnalyzeRejectsBadTests(t *testing.T) {
	c := nand2(t)
	if _, err := Analyze(c, []uint32{4}, Options{}); err == nil {
		t.Fatal("越界测试向量应报错")
	}
}
