package netsec

import "testing"

// buildPairLoop builds:
//
//	i = 0; j = 0;
//	head:
//	  if i <= hi { if 0 <= i-j <= 1 { buf[i]; buf[j]; i += 1; j += 1; goto head } }
//	  else exit
//
// j trails i by at most one, so both indices stay in [0, hi].
func buildPairLoop(hi int64, bufLen int64) (*Graph, int) {
	g := NewGraph()
	g.Array("buf", bufLen)
	entry := g.Entry()
	initI := g.Node()
	g.Assume(entry, initI, "i", 0, 0)
	head := g.Node()
	g.Assume(initI, head, "j", 0, 0)

	body := g.Node()
	g.Assume(head, body, "i", -Inf, hi)
	rel := g.Node()
	g.Guard(body, rel, "i", "j", 0, 1)
	g.AccessAt(rel, "buf", "i", 0)
	g.AccessAt(rel, "buf", "j", 0)
	incI := g.Node()
	g.Assign(rel, incI, "i", 1)
	g.Assign(incI, head, "j", 1)

	exit := g.Node()
	g.Assume(head, exit, "i", hi+1, Inf)
	return g, exit
}

func TestRelatedIndicesProven(t *testing.T) {
	g, _ := buildPairLoop(15, 16)
	res := Analyze(g, DefaultOptions())
	if len(res.Accesses) != 2 {
		t.Fatalf("want 2 access results, got %d", len(res.Accesses))
	}
	for _, ar := range res.Accesses {
		if !ar.Proven {
			t.Errorf("access %v not proven: lower=%v upper=%v have %s / %s",
				ar.Access.Index, ar.LowerOK, ar.UpperOK, ar.HaveLower, ar.HaveUpper)
		}
		if len(ar.Basis) == 0 {
			t.Errorf("access %v proven without constraint basis", ar.Access.Index)
		}
		if !ar.Concrete.Reached {
			t.Errorf("access %v never reached by concrete interpreter", ar.Access.Index)
		}
		if ar.Concrete.Violations != 0 {
			t.Errorf("proven access %v has %d concrete violations",
				ar.Access.Index, ar.Concrete.Violations)
		}
		if ar.Concrete.MaxIndex > 15 {
			t.Errorf("concrete max index %d exceeds 15", ar.Concrete.MaxIndex)
		}
	}
}

func TestOutOfBoundsUnknown(t *testing.T) {
	// Loop runs while i <= 16, but buf has length 16: i can reach 16.
	g, _ := buildPairLoop(16, 16)
	res := Analyze(g, DefaultOptions())
	ar := res.Accesses[0] // buf[i]
	if ar.Proven {
		t.Fatalf("buf[i] wrongly proven with i up to 16 and len 16")
	}
	if ar.UpperOK {
		t.Errorf("upper bound should fail: have %s need %s", ar.HaveUpper, ar.NeedUpper)
	}
	if ar.Concrete.Violations == 0 {
		t.Errorf("concrete interpreter should observe an out-of-bounds access")
	}
	if ar.Concrete.MaxIndex != 16 {
		t.Errorf("concrete max index = %d, want 16", ar.Concrete.MaxIndex)
	}
}
