package testsel

import (
	"os"
	"path/filepath"
	"testing"
)

const fixtureTests = `package calc
import "testing"
func TestAdd(t *testing.T) {
	if Add(1, 2) != 3 {
		t.Fail()
	}
}
func TestMul(t *testing.T) {
	if Mul(2, 3) != 6 {
		t.Fail()
	}
}
`

// TestRecordCoverage runs "go test" per test function in a fixture module
// and checks the recorded function coverage. Skipped when the go toolchain
// is unavailable.
func TestRecordCoverage(t *testing.T) {
	if os.Getenv("GOCACHE") == "" {
		os.Setenv("GOCACHE", filepath.Join(os.TempDir(), "testsel-gocache"))
	}
	dir, cleanup := writeFixture(t, map[string]string{
		"go.mod":       "module example.com/calc\n",
		"calc.go":      fixtureV1Main,
		"calc_test.go": fixtureTests,
	})
	defer cleanup()
	_, idx, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := RecordCoverage(dir, "example.com/calc", idx)
	if err != nil {
		t.Skipf("go toolchain unavailable: %v", err)
	}
	add := coverage["example.com/calc.TestAdd"]
	if !containsFunc(add, "example.com/calc.Add") {
		t.Fatalf("TestAdd coverage %v, want Add", add)
	}
	if containsFunc(add, "example.com/calc.Mul") {
		t.Fatalf("TestAdd unexpectedly covers Mul: %v", add)
	}
	mul := coverage["example.com/calc.TestMul"]
	if !containsFunc(mul, "example.com/calc.Mul") {
		t.Fatalf("TestMul coverage %v, want Mul", mul)
	}
}

func containsFunc(list []FuncID, id FuncID) bool {
	for _, f := range list {
		if f == id {
			return true
		}
	}
	return false
}
