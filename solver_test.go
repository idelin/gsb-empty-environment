package main

import (
	"strconv"
	"strings"
	"testing"
)

func runCase(t *testing.T, input string) string {
	t.Helper()
	out, err := run(strings.NewReader(input))
	if err != nil {
		t.Fatalf("run(%q) error: %v", input, err)
	}
	return out
}

func TestUnique(t *testing.T) {
	out := runCase(t, `
2 2
0 5
0 5
1 1 3
1 -1 1
`)
	if out != "UNIQUE\n2 1\n" {
		t.Fatalf("got %q", out)
	}
}

func TestMultiple(t *testing.T) {
	out := runCase(t, `
2 1
0 5
0 5
1 1 3
`)
	if !strings.HasPrefix(out, "MULTIPLE\n") {
		t.Fatalf("got %q", out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected two signals, got %q", out)
	}
	if lines[1] == lines[2] {
		t.Fatalf("signals must differ, got %q", out)
	}
}

// Rank over the reals is 1 < n = 2, yet the integer box pins down a single
// feasible signal. Rank deficiency must not be reported as MULTIPLE.
func TestRankDeficientButUnique(t *testing.T) {
	out := runCase(t, `
2 1
0 1
1 1
2 2 4
`)
	if out != "UNIQUE\n1 1\n" {
		t.Fatalf("got %q", out)
	}
}

// Rationally feasible but no integer point: 2x = 3.
func TestParityInfeasible(t *testing.T) {
	out := runCase(t, `
1 1
0 10
2 3
`)
	if out != "INFEASIBLE\nCONFLICT 1\n" {
		t.Fatalf("got %q", out)
	}
}

func TestContradictoryObservations(t *testing.T) {
	out := runCase(t, `
1 2
0 10
1 1
1 2
`)
	if out != "INFEASIBLE\nCONFLICT 1 2\n" {
		t.Fatalf("got %q", out)
	}
}

// Observation conflicts with the bounds, not with another observation.
func TestBoundConflict(t *testing.T) {
	out := runCase(t, `
1 1
0 3
1 5
`)
	if out != "INFEASIBLE\nCONFLICT 1\n" {
		t.Fatalf("got %q", out)
	}
}

// The conflict core must be minimal: observation 2 (x=5) contradicts both
// observation 1 (x=1) and observation 3 (2x=2), so the core is a pair.
func TestMinimalConflictCore(t *testing.T) {
	out := runCase(t, `
1 3
0 10
1 1
1 5
2 2
`)
	// Both {1,2} (x=1 vs x=5) and {2,3} (x=5 vs 2x=2) are minimal cores;
	// either is acceptable, but row pairs must be exactly one of these.
	if out != "INFEASIBLE\nCONFLICT 1 2\n" && out != "INFEASIBLE\nCONFLICT 2 3\n" {
		t.Fatalf("got %q", out)
	}
}

func TestRationalCoefficients(t *testing.T) {
	out := runCase(t, `
2 2
0 10
0 10
1/2 1/4 2
0.5 -0.25 1
`)
	// x1/2 + x2/4 = 2 and x1/2 - x2/4 = 1  =>  x1 = 3, x2 = 2
	if out != "UNIQUE\n3 2\n" {
		t.Fatalf("got %q", out)
	}
}

// Non-integer bounds are tightened to the enclosing integers.
func TestBoundTightening(t *testing.T) {
	out := runCase(t, `
1 1
0.5 3.5
1 3
`)
	if out != "UNIQUE\n3\n" {
		t.Fatalf("got %q", out)
	}
}

func TestTwelveDimUnique(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("12 12\n")
	for i := 0; i < 12; i++ {
		sb.WriteString("0 20\n")
	}
	for i := 0; i < 12; i++ {
		for j := 0; j < 12; j++ {
			if i == j {
				sb.WriteString("1 ")
			} else {
				sb.WriteString("0 ")
			}
		}
		sb.WriteString(strconv.Itoa(i + 1))
		sb.WriteString("\n")
	}
	out := runCase(t, sb.String())
	want := "UNIQUE\n1 2 3 4 5 6 7 8 9 10 11 12\n"
	if out != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestNoObservations(t *testing.T) {
	// Fixed box: unique.
	if out := runCase(t, "2 0\n3 3\n4 4\n"); out != "UNIQUE\n3 4\n" {
		t.Fatalf("got %q", out)
	}
	// Non-trivial box: multiple.
	if out := runCase(t, "1 0\n0 5\n"); !strings.HasPrefix(out, "MULTIPLE\n") {
		t.Fatalf("got %q", out)
	}
}

func TestEmptyDomainRejected(t *testing.T) {
	_, err := run(strings.NewReader("1 0\n2 1\n"))
	if err == nil {
		t.Fatal("expected error for empty domain")
	}
}
