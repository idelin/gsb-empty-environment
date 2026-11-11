package wafdecision

import "testing"

func benchSpecs(n int) ([]Formula, []LeafSpec) {
	kids := make([]Formula, n)
	specs := make([]LeafSpec, n)
	for i := 0; i < n; i++ {
		kids[i] = NewLeaf(i)
		specs[i] = spec(i, uint64(1+i), 0.2, 0.8)
	}
	return kids, specs
}

func BenchmarkCompile12(b *testing.B) {
	kids, specs := benchSpecs(12)
	rule := OrOf(
		AndOf(kids[0:4]...),
		AndOf(kids[4:8]...),
		OrOf(kids[8:12]...),
	)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Compile(rule, specs); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerify12(b *testing.B) {
	kids, specs := benchSpecs(12)
	rule := OrOf(
		AndOf(kids[0:4]...),
		AndOf(kids[4:8]...),
		OrOf(kids[8:12]...),
	)
	g, err := Compile(rule, specs)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := g.Verify(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecide(b *testing.B) {
	kids, specs := benchSpecs(8)
	rule := OrOf(AndOf(kids[0:4]...), AndOf(kids[4:8]...))
	g, err := Compile(rule, specs)
	if err != nil {
		b.Fatal(err)
	}
	det := map[int]Detector{}
	for _, s := range specs {
		det[s.ID] = func(id int) Detector {
			return func() bool { return id%2 == 0 }
		}(s.ID)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := g.Decide(det)
		if out.Verdict != Allow && out.Verdict != Deny {
			b.Fatal("invalid verdict")
		}
	}
}
