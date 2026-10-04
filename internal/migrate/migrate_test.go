package migrate

import (
	"strings"
	"testing"
)

func mustMigrate(t *testing.T, oldSrc, newSrc string, line int) Result {
	t.Helper()
	res, err := Migrate(oldSrc, newSrc, line)
	if err != nil {
		t.Fatalf("Migrate 出错: %v", err)
	}
	return res
}

func TestUnchangedFile(t *testing.T) {
	src := "package main\n\nfunc main() {\n\tx := 1\n\tprintln(x)\n}\n"
	res := mustMigrate(t, src, src, 4)
	if res.Status != StatusUnique || res.Line != 4 {
		t.Fatalf("期望 unique@4，得到 %s@%d", res.Status, res.Line)
	}
}

func TestStatementMoved(t *testing.T) {
	oldSrc := "package main\n\nfunc f() {\n\tx := 1\n\ty := 2\n\tprintln(x, y)\n}\n"
	newSrc := "package main\n\nfunc f() {\n\ty := 2\n\tx := 1\n\tprintln(x, y)\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 5) // y := 2
	if res.Status != StatusUnique || res.Line != 4 {
		t.Fatalf("期望 unique@4，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
	joined := strings.Join(res.Reasons, " ")
	if !strings.Contains(joined, "移动") {
		t.Fatalf("期望理由中包含移动说明，得到 %v", res.Reasons)
	}
}

func TestStatementMovedAcrossFunctions(t *testing.T) {
	oldSrc := "func a() {\n\twork()\n}\n\nfunc b() {\n\tidle()\n}\n"
	newSrc := "func a() {\n}\n\nfunc b() {\n\twork()\n\tidle()\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 2) // work() 从 a 移到 b
	if res.Status != StatusUnique || res.Line != 5 {
		t.Fatalf("期望 unique@5，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
}

func TestStatementSplit(t *testing.T) {
	oldSrc := "package main\n\nfunc f() {\n\tresult := compute(a, b, c)\n\tprintln(result)\n}\n"
	newSrc := "package main\n\nfunc f() {\n\tresult := compute(a,\n\t\tb,\n\t\tc)\n\tprintln(result)\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 4)
	if res.Status != StatusUnique || res.Line != 4 || res.Span != 3 {
		t.Fatalf("期望 unique@4 span=3，得到 %s@%d span=%d (%v)",
			res.Status, res.Line, res.Span, res.Reasons)
	}
	joined := strings.Join(res.Reasons, " ")
	if !strings.Contains(joined, "拆分") {
		t.Fatalf("期望理由中包含拆分说明，得到 %v", res.Reasons)
	}
}

func TestStatementDeleted(t *testing.T) {
	oldSrc := "package main\n\nfunc f() {\n\tdebug := computeVerboseDiagnostics()\n\tx := 1\n\tprintln(x)\n}\n"
	newSrc := "package main\n\nfunc f() {\n\tx := 1\n\tprintln(x)\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 4)
	if res.Status != StatusDeleted {
		t.Fatalf("期望 deleted，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
}

func TestIdenticalTextDisambiguatedByContext(t *testing.T) {
	// 两处 foo() 文本相同，但邻域不同，应凭邻域唯一定位。
	oldSrc := "if a {\n\tfoo()\n}\nif b {\n\tfoo()\n}\n"
	res := mustMigrate(t, oldSrc, oldSrc, 2)
	if res.Status != StatusUnique || res.Line != 2 {
		t.Fatalf("期望 unique@2，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
}

func TestIdenticalCopiesNotPickedArbitrarily(t *testing.T) {
	// 三个完全相同的代码块：第 1、2 个的内容与邻域完全一致，
	// 工具必须返回候选列表而不是随意选一个。
	oldSrc := "if a {\n\tfoo()\n}\n"
	newSrc := "if a {\n\tfoo()\n}\nif a {\n\tfoo()\n}\nif a {\n\tfoo()\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 2)
	if res.Status != StatusAmbiguous {
		t.Fatalf("期望 ambiguous，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
	if len(res.Candidates) < 2 {
		t.Fatalf("期望至少 2 个候选，得到 %d", len(res.Candidates))
	}
	lines := map[int]bool{}
	for _, c := range res.Candidates {
		lines[c.Line] = true
	}
	if !lines[2] || !lines[5] {
		t.Fatalf("候选应包含第 2、5 行，得到 %v", lines)
	}
	joined := strings.Join(res.Reasons, " ")
	if !strings.Contains(joined, "不随意选择") {
		t.Fatalf("期望理由中说明不随意选择，得到 %v", res.Reasons)
	}
}

func TestEditedStatementFuzzyMatch(t *testing.T) {
	oldSrc := "func f() {\n\tcount := computeTotal(items)\n\tprintln(count)\n}\n"
	newSrc := "func f() {\n\tcount := computeTotal(items, options)\n\tprintln(count)\n}\n"
	res := mustMigrate(t, oldSrc, newSrc, 2)
	if res.Status != StatusUnique || res.Line != 2 {
		t.Fatalf("期望 unique@2，得到 %s@%d (%v)", res.Status, res.Line, res.Reasons)
	}
}

func TestBreakpointOnBlankLine(t *testing.T) {
	src := "package main\n\nfunc f() {}\n"
	if _, err := Migrate(src, src, 2); err == nil {
		t.Fatal("空行断点应返回错误")
	}
}
