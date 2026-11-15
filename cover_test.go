package netsec

import (
	"math/rand"
	"testing"
)

func TestMinTestSetKnown(t *testing.T) {
	c := nand2(t)
	// 公开测试缺少输入 3：向量 3 可同时发现两个危险故障。
	rep, err := MinTestSet(c, []uint32{0, 1, 2}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusFound {
		t.Fatalf("Status = %v, 期望 found", rep.Status)
	}
	if rep.DangerousFaults != 2 || rep.AlreadyCovered != 0 {
		t.Fatalf("危险故障 %d 已覆盖 %d, 期望 2/0", rep.DangerousFaults, rep.AlreadyCovered)
	}
	if len(rep.Vectors) != 1 || rep.Vectors[0] != 3 {
		t.Fatalf("Vectors = %v, 期望 [3]", rep.Vectors)
	}
	// 公开测试已覆盖全部危险故障：无需新增向量。
	rep2, err := MinTestSet(c, []uint32{0, 1, 2, 3}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Status != StatusNone || len(rep2.Vectors) != 0 {
		t.Fatalf("Status = %v, Vectors = %v, 期望 none/[]", rep2.Status, rep2.Vectors)
	}
	if rep2.AlreadyCovered != 2 {
		t.Fatalf("AlreadyCovered = %d, 期望 2", rep2.AlreadyCovered)
	}
}

func TestMinTestSetBudgetExhausted(t *testing.T) {
	c := nand2(t)
	rep, err := MinTestSet(c, nil, Options{Budget: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusUnknown || rep.Reason == "" {
		t.Fatalf("Status = %v, Reason = %q, 期望 unknown 且带原因", rep.Status, rep.Reason)
	}
}

// notChain 构造 1 输入、n 级 NOT 链、输出为末级的电路。
func notChain(t *testing.T, n int) *Circuit {
	t.Helper()
	nodes := []Node{{Kind: KindInput}}
	for i := 0; i < n; i++ {
		nodes = append(nodes, Node{Kind: KindNot, Args: []int{len(nodes) - 1}})
	}
	nodes = append(nodes, Node{Kind: KindOutput, Args: []int{len(nodes) - 1}})
	c, err := NewCircuit(nodes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMinTestSetTooManyDangerous(t *testing.T) {
	// 21 级 NOT 链：每级恰有一个危险故障，共 21 个，超过上限 20。
	c := notChain(t, 21)
	rep, err := MinTestSet(c, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != StatusUnknown {
		t.Fatalf("Status = %v, 期望 unknown", rep.Status)
	}
	if rep.DangerousFaults != 21 {
		t.Fatalf("DangerousFaults = %d, 期望 21", rep.DangerousFaults)
	}
	// 同一电路上 Analyze 仍能完备枚举（21 个逃逸故障）。
	arep, err := Analyze(c, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if arep.Status != StatusFound || len(arep.Findings) != 21 {
		t.Fatalf("Analyze: Status = %v, Findings = %d, 期望 found/21", arep.Status, len(arep.Findings))
	}
	for _, fd := range arep.Findings {
		if fd.EscapeInput != 1 {
			t.Fatalf("故障 %v 逃逸输入 = %d, 期望 1", fd.Fault, fd.EscapeInput)
		}
	}
}

// TestMinTestSetAgainstBruteForce 随机电路上与暴力子集枚举对照，
// 同时验证最小数量与字典序决胜。
func TestMinTestSetAgainstBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(211))
	for trial := 0; trial < 300; trial++ {
		c := randCircuit(r, 1+r.Intn(4), 1+r.Intn(8))
		tests := randTests(r, c.InputSpace(), c.InputSpace()/2)
		rep, err := MinTestSet(c, tests, Options{})
		if err != nil {
			t.Fatal(err)
		}
		// 暴力求全部危险故障及其中未被公开测试发现者。
		var dangerous, remaining []Fault
		for _, f := range c.AllFaults() {
			if isDangerous, _ := bruteDangerous(c, f); isDangerous {
				dangerous = append(dangerous, f)
				if !bruteDetected(c, f, tests) {
					remaining = append(remaining, f)
				}
			}
		}
		if len(dangerous) > MaxDangerousFaults {
			continue // 本用例不覆盖该前提（由专门测试验证）
		}
		if rep.DangerousFaults != len(dangerous) {
			t.Fatalf("trial %d: DangerousFaults = %d, 期望 %d", trial, rep.DangerousFaults, len(dangerous))
		}
		if rep.AlreadyCovered != len(dangerous)-len(remaining) {
			t.Fatalf("trial %d: AlreadyCovered = %d, 期望 %d", trial, rep.AlreadyCovered, len(dangerous)-len(remaining))
		}
		want := bruteMinCover(c, remaining)
		if len(remaining) == 0 {
			if rep.Status != StatusNone || len(rep.Vectors) != 0 {
				t.Fatalf("trial %d: Status = %v, Vectors = %v, 期望 none/[]", trial, rep.Status, rep.Vectors)
			}
			continue
		}
		if rep.Status != StatusFound {
			t.Fatalf("trial %d: Status = %v, 期望 found", trial, rep.Status)
		}
		if len(rep.Vectors) != len(want) {
			t.Fatalf("trial %d: |Vectors| = %d, 暴力最小值 = %d（%v vs %v）",
				trial, len(rep.Vectors), len(want), rep.Vectors, want)
		}
		for i := range want {
			if rep.Vectors[i] != want[i] {
				t.Fatalf("trial %d: Vectors = %v, 字典序最小解 = %v", trial, rep.Vectors, want)
			}
		}
		// 新增向量与公开测试合并后必须发现全部危险故障。
		merged := append(append([]uint32(nil), tests...), rep.Vectors...)
		for _, f := range dangerous {
			if !bruteDetected(c, f, merged) {
				t.Fatalf("trial %d: 合并后仍漏掉危险故障 %v", trial, f)
			}
		}
		// 新增向量不得与公开测试重复。
		pub := make(map[uint32]bool)
		for _, tv := range tests {
			pub[tv] = true
		}
		for _, v := range rep.Vectors {
			if pub[v] {
				t.Fatalf("trial %d: 新增向量 %d 与公开测试重复", trial, v)
			}
		}
	}
}

func TestMinTestSetRejectsBadTests(t *testing.T) {
	c := nand2(t)
	if _, err := MinTestSet(c, []uint32{7}, Options{}); err == nil {
		t.Fatal("越界测试向量应报错")
	}
}
