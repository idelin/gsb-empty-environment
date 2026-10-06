package testsel

import (
	"io/ioutil"
	"path/filepath"
	"testing"
)

func TestParseAndMapCoverProfile(t *testing.T) {
	dir, cleanup := writeFixture(t, map[string]string{
		"go.mod":  "module example.com/calc\n",
		"calc.go": fixtureV1Main,
	})
	defer cleanup()

	_, idx, err := Analyze(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	profile := `mode: set
example.com/calc/calc.go:2.26,2.38 1 1
example.com/calc/calc.go:3.26,3.38 1 0
example.com/calc/calc.go:4.36,6.16 1 1
`
	pp := filepath.Join(dir, "cover.out")
	if err := ioutil.WriteFile(pp, []byte(profile), 0644); err != nil {
		t.Fatal(err)
	}
	blocks, err := ParseCoverProfile(pp)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 3 {
		t.Fatalf("blocks: %v", blocks)
	}
	got := idx.MapBlocks(blocks)
	// Line 2 (Add) and line 4-6 (Eval) covered; line 3 (Mul) count 0.
	if !got["example.com/calc.Add"] || !got["example.com/calc.Eval"] {
		t.Fatalf("mapped: %v", got)
	}
	if got["example.com/calc.Mul"] {
		t.Fatalf("Mul has count 0 but was mapped: %v", got)
	}
}
