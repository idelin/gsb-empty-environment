package dfa

import (
	"math/rand"
	"testing"
)

// genRandomProgram 生成结构合法、跳转目标收敛的随机程序，用于差分测试。
// 程序保证末尾是 halt，所有跳转只向前跳，避免无限循环撑爆步数预算。
func genRandomProgram(rng *rand.Rand, width int) *Program {
	n := 6 + rng.Intn(12) // 6..17 条指令
	ops := []Op{OpInp, OpLds, OpAnd, OpNot, OpInc, OpDec, OpJz, OpJt, OpEmit, OpUnlock}
	lines := make([]string, 0, n+4)
	for i := 0; i < n-1; i++ {
		op := ops[rng.Intn(len(ops))]
		switch op {
		case OpInp:
			if width == 0 {
				lines = append(lines, "lds r0")
				continue
			}
			lines = append(lines, "inp r"+itoa(rng.Intn(MaxBoolRegs))+", p"+itoa(rng.Intn(width)))
		case OpLds:
			lines = append(lines, "lds r"+itoa(rng.Intn(MaxBoolRegs)))
		case OpAnd, OpNot:
			lines = append(lines, string(op)+" r"+itoa(rng.Intn(MaxBoolRegs))+", r"+itoa(rng.Intn(MaxBoolRegs)))
		case OpInc, OpDec:
			lines = append(lines, string(op))
		case OpEmit:
			lines = append(lines, "emit "+itoa(rng.Intn(MaxEmitTag+1)))
		case OpUnlock:
			lines = append(lines, "unlock")
		case OpJz, OpJt:
			// 前向跳到 i+1..n-1 之一，目标处放标签。
			target := i + 1 + rng.Intn(n-1-i)
			label := "L" + itoa(target)
			if op == OpJz {
				lines = append(lines, "jz "+label)
			} else {
				lines = append(lines, "jt r"+itoa(rng.Intn(MaxBoolRegs))+", "+label)
			}
		}
	}
	lines = append(lines, "halt")
	// 在被引用的目标前补标签。
	final := make([]string, 0, len(lines)+4)
	for i, ln := range lines {
		if label := "L" + itoa(i); containsLabelRef(lines, label) {
			final = append(final, label+":")
		}
		final = append(final, ln)
	}
	src := ""
	for _, ln := range final {
		src += ln + "\n"
	}
	return MustParse(src, width)
}

func containsLabelRef(lines []string, label string) bool {
	for _, ln := range lines {
		if (len(ln) > 3 && ln[:3] == "jz " && ln[3:] == label) ||
			(len(ln) > 3 && ln[:3] == "jt " && tailLabel(ln) == label) {
			return true
		}
	}
	return false
}

func tailLabel(jtLine string) string {
	for i := len(jtLine) - 1; i >= 0; i-- {
		if jtLine[i] == ' ' {
			return jtLine[i+1:]
		}
	}
	return ""
}

// TestRandomDifferential 在固定种子下生成随机程序，逐输入对照 BFS 与全枚举
// 预言机的可达性结论，并对找到的见证做独立 Replay 复核。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261126))
	for iter := 0; iter < 60; iter++ {
		width := rng.Intn(3) // 0..2 个公开输入位，枚举规模可控
		prog := genRandomProgram(rng, width)
		for _, sigValid := range []bool{false, true} {
			for _, in := range AllInputs(width, sigValid) {
				for _, budget := range []int{0, 1, 2} {
					dis, err := CrossCheck(prog, in, budget)
					if err != nil {
						t.Fatalf("iter=%d 输入=%+v budget=%d: %v", iter, in, budget, err)
					}
					if dis != nil {
						t.Fatalf("iter=%d 检查器分歧: %s", iter, dis.Reason)
					}
				}
			}
		}
	}
}

// TestRandomWitnessReplay 对随机程序的全部攻击见证做重放，杜绝“检查器
// 声称可达但参考 VM 无法复现”的假阳性。
func TestRandomWitnessReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	checked := 0
	for iter := 0; iter < 30 && checked < 25; iter++ {
		prog := genRandomProgram(rng, 1)
		in := Input{Public: []bool{rng.Intn(2) == 1}, SignatureValid: false}
		att, err := Attack(prog, in, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !att.Reachable {
			continue
		}
		tr, err := Replay(prog, in, att.Witness)
		if err != nil {
			t.Fatalf("见证重放失败: %v", err)
		}
		if tr.UnlockCount == 0 {
			t.Fatalf("假阳性见证: %+v", att.Witness)
		}
		checked++
	}
}
