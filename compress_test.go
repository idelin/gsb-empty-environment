package timemap

import "testing"

func buildSample(t *testing.T) (*Result, Spec) {
	t.Helper()
	spec := Spec{
		MinIdleGap:         RatFromInt(30),
		CompressedDuration: RatFromInt(2),
		Events: []Rat{
			RatFromInt(0),
			RatFromInt(5),
			RatFromInt(5), // simultaneous pair
			RatFromInt(100),
		},
		Protected: []Interval{
			{ID: "drag1", Kind: "drag", Start: RatFromInt(10), End: RatFromInt(20)},
			{ID: "audio1", Kind: "audio", Start: RatFromInt(60), End: RatFromInt(80)},
		},
	}
	res, err := Build(spec)
	if err != nil {
		t.Fatal(err)
	}
	return res, spec
}

func TestBuildCompressesIdleAndProtects(t *testing.T) {
	res, _ := buildSample(t)
	// cuts: 0,5,10,20,60,80,100
	// [20,60) idle 40 >= 30 -> compressed to 2; other idle pieces < 30 kept.
	var compressed, kept, protected int
	for _, s := range res.Segments {
		switch s.Kind {
		case SegIdleCompressed:
			compressed++
			if s.Start.String() != "20" || s.End.String() != "60" {
				t.Errorf("compressed segment = [%s,%s)", s.Start, s.End)
			}
			if s.MappedEnd.Sub(s.MappedStart).String() != "2" {
				t.Errorf("compressed duration = %s", s.MappedEnd.Sub(s.MappedStart))
			}
		case SegIdleKept:
			kept++
		case SegProtected:
			protected++
			if !s.Slope().Equal(RatFromInt(1)) {
				t.Errorf("protected segment %q slope != 1", s.ProtectedID)
			}
		}
	}
	if compressed != 1 || kept != 3 || protected != 2 {
		t.Errorf("segments: compressed=%d kept=%d protected=%d", compressed, kept, protected)
	}
}

func TestVerifyPasses(t *testing.T) {
	res, spec := buildSample(t)
	for _, c := range Verify(res, spec) {
		if !c.OK {
			t.Errorf("check %q failed: %s", c.Name, c.Detail)
		}
	}
}

func TestSimultaneityAndOrder(t *testing.T) {
	res, _ := buildSample(t)
	m := res.Mapping
	a := m.Forward(RatFromInt(5))
	b := m.Forward(RatFromInt(5))
	if !a.Equal(b) {
		t.Errorf("simultaneous events diverged: %s vs %s", a, b)
	}
	if m.Forward(RatFromInt(0)).Cmp(a) >= 0 {
		t.Errorf("order broken: f(0)=%s >= f(5)=%s", m.Forward(RatFromInt(0)), a)
	}
}

func TestProtectedOverlapRejected(t *testing.T) {
	spec := Spec{
		MinIdleGap:         RatFromInt(30),
		CompressedDuration: RatFromInt(2),
		Events:             []Rat{RatFromInt(0), RatFromInt(100)},
		Protected: []Interval{
			{ID: "bad", Start: RatFromInt(50), End: RatFromInt(40)},
		},
	}
	if _, err := Build(spec); err == nil {
		t.Error("expected error for empty protected interval")
	}
}
