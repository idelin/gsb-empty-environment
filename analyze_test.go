package testsel

import (
	"io/ioutil"
	"path/filepath"
	"testing"
)

const fixtureV1Main = `package calc
func Add(a, b int) int { return a + b }
func Mul(a, b int) int { return a * b }
func Eval(op string, a, b int) int {
	if op == "add" {
		return Add(a, b)
	}
	return Mul(a, b)
}
`

const fixtureV1Iface = `package calc
type Op interface {
	Apply(a, b int) int
}
type Adder struct{}
func (Adder) Apply(a, b int) int { return a + b }
func RunOp(o Op, a, b int) int {
	return o.Apply(a, b)
}
`

func writeFixture(t *testing.T, files map[string]string) (string, func()) {
	t.Helper()
	dir, cleanup := tmpDir(t)
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := ioutil.WriteFile(p, []byte(content), 0644); err != nil {
			cleanup()
			t.Fatal(err)
		}
	}
	return dir, cleanup
}

func TestAnalyzeHashesAndEdges(t *testing.T) {
	dir, cleanup := writeFixture(t, map[string]string{
		"go.mod":   "module example.com/calc\n",
		"calc.go":  fixtureV1Main,
		"iface.go": fixtureV1Iface,
	})
	defer cleanup()
	s, idx, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []FuncID{
		"example.com/calc.Add",
		"example.com/calc.Mul",
		"example.com/calc.Eval",
		"example.com/calc.RunOp",
		"example.com/calc.(Adder).Apply",
	} {
		if s.Funcs[id] == "" {
			t.Fatalf("missing function %s in %v", id, s.Funcs)
		}
	}
	want := map[Edge]bool{
		{"example.com/calc.Eval", "example.com/calc.Add"}: true,
		{"example.com/calc.Eval", "example.com/calc.Mul"}: true,
	}
	for _, e := range s.Edges {
		delete(want, e)
	}
	if len(want) != 0 {
		t.Fatalf("missing static edges %v in %v", want, s.Edges)
	}
	if got := s.DynCalls["example.com/calc.RunOp"]; len(got) != 1 || got[0] != "Apply" {
		t.Fatalf("DynCalls: %v", s.DynCalls)
	}
	if got := s.DynImpls["Apply"]; len(got) != 1 || got[0] != "example.com/calc.(Adder).Apply" {
		t.Fatalf("DynImpls: %v", s.DynImpls)
	}
	if idx == nil || len(idx.files) == 0 {
		t.Fatal("empty FuncIndex")
	}
}

func TestAnalyzeHashStability(t *testing.T) {
	files := map[string]string{
		"go.mod":  "module example.com/calc\n",
		"calc.go": fixtureV1Main,
	}
	dir1, c1 := writeFixture(t, files)
	defer c1()
	dir2, c2 := writeFixture(t, files)
	defer c2()
	s1, _, err := Analyze(dir1, "")
	if err != nil {
		t.Fatal(err)
	}
	s2, _, err := Analyze(dir2, "")
	if err != nil {
		t.Fatal(err)
	}
	for id, h := range s1.Funcs {
		if s2.Funcs[id] != h {
			t.Fatalf("hash of %s unstable: %q vs %q", id, h, s2.Funcs[id])
		}
	}
}

func TestEndToEnd(t *testing.T) {
	dir, cleanup := writeFixture(t, map[string]string{
		"go.mod":  "module example.com/calc\n",
		"calc.go": fixtureV1Main,
	})
	defer cleanup()
	old, _, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	old.Coverage["example.com/calc.TestAdd"] = []FuncID{"example.com/calc.Add"}
	old.Coverage["example.com/calc.TestEval"] = []FuncID{
		"example.com/calc.Eval", "example.com/calc.Add", "example.com/calc.Mul",
	}
	v2main := `package calc
func Add(a, b int) int { return a + b }
func Mul(a, b int) int { return (a * b) + 0 }
func Eval(op string, a, b int) int {
	if op == "add" {
		return Add(a, b)
	}
	return Mul(a, b)
}
`
	if err := ioutil.WriteFile(filepath.Join(dir, "calc.go"), []byte(v2main), 0644); err != nil {
		t.Fatal(err)
	}
	new, _, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	dec := Select(old, new)
	if dec.RunAll {
		t.Fatalf("unexpected RunAll: %v", dec.Reasons)
	}
	if len(dec.Selected) != 1 || dec.Selected[0] != "example.com/calc.TestEval" {
		t.Fatalf("selected %v, want only TestEval (Mul is only reachable from Eval)", dec.Selected)
	}
	v3main := v2main + "\nfunc Sub(a, b int) int { return a - b }\n"
	if err := ioutil.WriteFile(filepath.Join(dir, "calc.go"), []byte(v3main), 0644); err != nil {
		t.Fatal(err)
	}
	newer, _, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if dec := Select(new, newer); !dec.RunAll {
		t.Fatalf("expected RunAll after adding Sub, got %+v", dec)
	}
}
