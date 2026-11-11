package wafdecision

import "testing"

func TestConcurrentDecide(t *testing.T) {
	rule := OrOf(
		AndOf(NewLeaf(0), NewLeaf(1)),
		AndOf(NewLeaf(0), NewLeaf(2)),
	)
	specs := []LeafSpec{
		spec(0, 3, 0.4, 0.6),
		spec(1, 5, 0.2, 0.3),
		spec(2, 7, 0.7, 0.9),
	}
	g, err := Compile(rule, specs)
	if err != nil {
		t.Fatal(err)
	}
	base := map[int]bool{0: true, 1: false, 2: true}
	det := map[int]Detector{}
	for id := range base {
		leafID := id
		det[leafID] = func() bool { return base[leafID] }
	}

	done := make(chan struct{})
	for worker := 0; worker < 32; worker++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for i := 0; i < 200; i++ {
				out := g.Decide(det)
				if out.Panicked {
					t.Error("unexpected panic")
					return
				}
			}
		}()
	}
	for worker := 0; worker < 32; worker++ {
		<-done
	}
}
