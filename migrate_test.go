package bpmigrate

import "testing"

func bp(lines ...int) []Breakpoint {
	var bps []Breakpoint
	for i, l := range lines {
		bps = append(bps, Breakpoint{ID: i + 1, Line: l})
	}
	return bps
}

func TestUnchanged(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	rs := Migrate(src, src, bp(4))
	if rs[0].Status != StatusUnchanged || rs[0].Line != 4 {
		t.Fatalf("期望 unchanged@4, 得到 %v@%d", rs[0].Status, rs[0].Line)
	}
}

func TestMoved(t *testing.T) {
	oldSrc := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	newSrc := "package main\n\n// 新增注释\n// 又一注释\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	rs := Migrate(oldSrc, newSrc, bp(4))
	if rs[0].Status != StatusMoved || rs[0].Line != 7 {
		t.Fatalf("期望 moved@7, 得到 %v@%d (%s)", rs[0].Status, rs[0].Line, rs[0].Reason)
	}
}

func TestDeleted(t *testing.T) {
	oldSrc := "package main\n\nfunc main() {\n\tresult := computeExpensiveValue(42)\n\tprintln(result)\n}\n"
	newSrc := "package main\n\nfunc main() {\n\tprintln(0)\n}\n"
	rs := Migrate(oldSrc, newSrc, bp(4))
	if rs[0].Status != StatusDeleted {
		t.Fatalf("期望 deleted, 得到 %v (%s)", rs[0].Status, rs[0].Reason)
	}
}

func TestSplit(t *testing.T) {
	oldSrc := "package main\n\nfunc main() {\n\tdoSomething(argumentOne, argumentTwo, argumentThree)\n}\n"
	newSrc := `package main

func main() {
	doSomething(
		argumentOne,
		argumentTwo,
		argumentThree,
	)
}
`
	rs := Migrate(oldSrc, newSrc, bp(4))
	if rs[0].Status != StatusSplit {
		t.Fatalf("期望 split, 得到 %v (%s)", rs[0].Status, rs[0].Reason)
	}
	if rs[0].Line != 4 || rs[0].Candidates[0].EndLine != 8 {
		t.Fatalf("期望拆分范围 4-8, 得到 %d-%d", rs[0].Line, rs[0].Candidates[0].EndLine)
	}
}

// 相同行文本：两处相同的语句，邻域可区分时应唯一命中。
func TestIdenticalTextDisambiguatedByContext(t *testing.T) {
	oldSrc := `package main

func alpha() {
	x := 1
	total += x
	println(x)
}

func beta() {
	y := 2
	total += y
	println(y)
}
`
	// 新文件在 alpha 前插入内容，两个 println 文本相同但邻域不同。
	newSrc := `package main

// 新增说明
func alpha() {
	x := 1
	total += x
	println(x)
}

func beta() {
	y := 2
	total += y
	println(y)
}
`
	rs := Migrate(oldSrc, newSrc, bp(6)) // alpha 里的 println(x)
	if rs[0].Status != StatusMoved || rs[0].Line != 7 {
		t.Fatalf("期望 moved@7, 得到 %v@%d (%s)", rs[0].Status, rs[0].Line, rs[0].Reason)
	}
}

// 复制出的相同代码：整段复制后邻域也无法区分，必须返回候选列表而非随意选择。
func TestDuplicatedBlockIsAmbiguous(t *testing.T) {
	oldSrc := `package main

func alpha() {
	x := 1
	total += x
	println(x)
}
`
	// 新文件中把 alpha 原样复制为 alpha2，两处语句与邻域完全一致。
	newSrc := `package main

func alpha() {
	x := 1
	total += x
	println(x)
}

func alpha2() {
	x := 1
	total += x
	println(x)
}
`
	rs := Migrate(oldSrc, newSrc, bp(5)) // total += x
	if rs[0].Status != StatusAmbiguous {
		t.Fatalf("期望 ambiguous, 得到 %v@%d (%s)", rs[0].Status, rs[0].Line, rs[0].Reason)
	}
	if len(rs[0].Candidates) != 2 {
		t.Fatalf("期望 2 个候选, 得到 %d", len(rs[0].Candidates))
	}
	lines := map[int]bool{}
	for _, c := range rs[0].Candidates {
		lines[c.Line] = true
	}
	if !lines[5] || !lines[11] {
		t.Fatalf("候选应为第 5 和 11 行, 得到 %v", lines)
	}
}

// 相同行文本且其中一个被删除：剩下的应唯一命中。
func TestIdenticalTextOneRemoved(t *testing.T) {
	oldSrc := `package main

func alpha() {
	x := 1
	total += x
}

func beta() {
	y := 2
	total += y
}
`
	newSrc := `package main

func beta() {
	y := 2
	total += y
}
`
	rs := Migrate(oldSrc, newSrc, bp(10)) // beta 里的 total += y
	if rs[0].Status != StatusMoved || rs[0].Line != 5 {
		t.Fatalf("期望 moved@5, 得到 %v@%d (%s)", rs[0].Status, rs[0].Line, rs[0].Reason)
	}
}

// 语句被修改（重命名变量）：模糊匹配应跟随到修改后的行。
func TestModifiedStatementFuzzyMatch(t *testing.T) {
	oldSrc := `package main

func main() {
	count := computeTotal(items)
	println(count)
}
`
	newSrc := `package main

func main() {
	total := computeTotal(items)
	println(total)
}
`
	rs := Migrate(oldSrc, newSrc, bp(4))
	if (rs[0].Status != StatusMoved && rs[0].Status != StatusUnchanged) || rs[0].Line != 4 {
		t.Fatalf("期望唯一匹配@4, 得到 %v@%d (%s)", rs[0].Status, rs[0].Line, rs[0].Reason)
	}
}
