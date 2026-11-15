package netsec

import (
	"math/rand"
	"testing"
)

// TestWideInputSpace 覆盖 12 位输入（4096 输入空间、64 字真值表、
// bit>=6 的输入模式分支），逐输入逐节点对照位并行与标量仿真，
// 并用暴力参考校验 Analyze 与 MinTestSet 的结论。
func TestWideInputSpace(t *testing.T) {
	r := rand.New(rand.NewSource(4096))
	for trial := 0; trial < 20; trial++ {
		c := randCircuit(r, MaxInputs, 2+r.Intn(8))
		// 位并行与标量逐输入一致（含全部故障）。
		for _, f := range c.AllFaults() {
			f := f
			tab := c.simulate(&f)
			for j := 0; j < c.InputSpace(); j++ {
				states := c.EvalWithFault(uint32(j), f)
				for i := range states {
					if bitOf(tab[i], uint32(j)) != states[i] {
						t.Fatalf("trial %d 故障 %v 输入 %d 节点 %d: 位并行与标量不一致",
							trial, f, j, i)
					}
				}
			}
		}
		// Analyze 与暴力参考一致。
		tests := randTests(r, c.InputSpace(), 8)
		rep, err := Analyze(c, tests, Options{})
		if err != nil {
			t.Fatal(err)
		}
		findings := make(map[Fault]Finding)
		for _, fd := range rep.Findings {
			findings[fd.Fault] = fd
		}
		var remaining []Fault
		for _, f := range c.AllFaults() {
			isDangerous, esc := bruteDangerous(c, f)
			isDetected := bruteDetected(c, f, tests)
			fd, found := findings[f]
			if isDangerous && !isDetected {
				if !found {
					t.Fatalf("trial %d 故障 %v: 漏报逃逸故障", trial, f)
				}
				if int(fd.EscapeInput) != esc {
					t.Fatalf("trial %d 故障 %v: 逃逸输入 = %d, 期望 %d", trial, f, fd.EscapeInput, esc)
				}
			} else if found {
				t.Fatalf("trial %d 故障 %v: 误报", trial, f)
			}
			if isDangerous && !isDetected {
				remaining = append(remaining, f)
			}
		}
		// MinTestSet：覆盖性与最小数量（用 DP 自检：数量不超过
		// 剩余故障数，且合并后可发现全部危险故障）。
		crep, err := MinTestSet(c, tests, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if crep.Status == StatusUnknown {
			continue // 危险故障超上限，前提不适用
		}
		if len(remaining) == 0 && crep.Status != StatusNone {
			t.Fatalf("trial %d: 无剩余故障但 Status = %v", trial, crep.Status)
		}
		if len(crep.Vectors) > len(remaining) {
			t.Fatalf("trial %d: |Vectors| = %d 超过剩余故障数 %d", trial, len(crep.Vectors), len(remaining))
		}
		merged := append(append([]uint32(nil), tests...), crep.Vectors...)
		for _, f := range c.AllFaults() {
			if isDangerous, _ := bruteDangerous(c, f); isDangerous && !bruteDetected(c, f, merged) {
				t.Fatalf("trial %d: 合并后仍漏掉危险故障 %v", trial, f)
			}
		}
	}
}
