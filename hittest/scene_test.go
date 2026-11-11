package hittest

import "testing"

// buildFixture constructs the shared verification scene:
//
//	root
//	├─ C  cross-shaped window (bottom of the stack)
//	├─ A  scaled L-shaped window with its own clip
//	│  └─ B  secure confirmation button, nested-clipped by A and itself
//	└─ T  transparent intercept overlay (thin strip, top of the stack)
func buildFixture() *Scene {
	cross := NewRegion(
		R(40, 0, 60, 100), // vertical bar
		R(0, 40, 100, 60), // horizontal bar
	)
	c := &Node{
		ID:        "C",
		Transform: Translate(120, 30),
		Shape:     cross,
	}

	lshape := NewRegion(
		R(0, 0, 20, 60),   // vertical bar
		R(20, 40, 60, 60), // foot; upper-right quadrant is a hole
	)
	b := &Node{
		ID:        "B",
		Transform: Translate(30, 10),
		Shape:     NewRegion(R(0, 0, 20, 20)),
		// B clips itself to the left 12 local units; the rest must pass
		// through to windows below despite lying inside B's shape.
		Clip:   NewRegion(R(0, 0, 12, 20)),
		Secure: true,
	}
	a := &Node{
		ID:        "A",
		Transform: Scale(1.5, 1.5).Then(Translate(100, 50)),
		Shape:     lshape,
		// Generous clip: trims a margin off the shape's far edge while
		// keeping both L bars present.
		Clip:     NewRegion(R(-5, -5, 60, 65)),
		Children: []*Node{b},
	}

	// T is opaque only inside a thin strip; everywhere else it is a
	// transparent click-through surface.
	t := &Node{
		ID:        "T",
		Transform: Translate(0, 120),
		Shape:     NewRegion(R(0, 0, 500, 8)),
	}

	root := &Node{ID: "root", Children: []*Node{c, a, t}}
	return NewScene(root)
}

func TestHitTestOverlappingNonRectangularRegions(t *testing.T) {
	snap := buildFixture().Snapshot()

	cases := []struct {
		name string
		p    Point
		want string // "" means no receiver
	}{
		{"scaled L window vertical bar", Point{X: 110, Y: 60}, "A"},
		{"secure button inside nested clip", Point{X: 140, Y: 70}, "B"},
		{"nested clip falls through to cross", Point{X: 150, Y: 80}, "C"},
		{"half-open A edge passes through to B beneath", Point{X: 130, Y: 80}, "B"},
		{"just inside scaled edge hits A", Point{X: 129, Y: 80}, "A"},
		{"cross horizontal arm", Point{X: 210, Y: 80}, "C"},
		{"overlay strip intercepts", Point{X: 170, Y: 124}, "T"},
		{"below strip falls through to A", Point{X: 170, Y: 130}, "A"},
		{"button clip edge is half-open, falls through", Point{X: 148, Y: 80}, "C"},
		{"just inside button clip edge", Point{X: 147.999, Y: 80}, "B"},
		{"empty screen area", Point{X: 250, Y: 100}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hit, ok := snap.HitTest(tc.p)
			if tc.want == "" {
				if ok {
					t.Fatalf("HitTest(%v) = %s, want no receiver", tc.p, hit.Node.ID)
				}
				return
			}
			if !ok {
				t.Fatalf("HitTest(%v) = no hit, want %s", tc.p, tc.want)
			}
			if hit.Node.ID != tc.want {
				t.Fatalf("HitTest(%v) = %s, want %s", tc.p, hit.Node.ID, tc.want)
			}
		})
	}
}

func TestVisibleRegionAppliesNestedClips(t *testing.T) {
	snap := buildFixture().Snapshot()

	// B chain is scale 1.5 then translate (130,60); its own 12-unit clip
	// makes the visible screen rect 12*1.5 = 18 wide and 20*1.5 = 30 tall.
	gotB := snap.VisibleRegion("B")
	wantB := NewRegion(R(130, 60, 148, 90))
	if !gotB.Equals(wantB) {
		t.Fatalf("VisibleRegion(B) = %v, want %v", gotB.Rects(), wantB.Rects())
	}

	// A: both L bars survive the generous clip.
	gotA := snap.VisibleRegion("A")
	wantA := NewRegion(
		R(100, 50, 130, 140),
		R(130, 110, 190, 140),
	)
	if !gotA.Equals(wantA) {
		t.Fatalf("VisibleRegion(A) = %v, want %v", gotA.Rects(), wantA.Rects())
	}

	if snap.VisibleRegion("missing").IsEmpty() == false {
		t.Fatal("missing node must report an empty visible region")
	}
}

func TestVisibleRegionClipsDescendantBeyondParent(t *testing.T) {
	// Child shape extends past the parent clip; only the overlapping slice is
	// visible and capable of receiving events.
	child := &Node{
		ID:        "child",
		Transform: Identity(),
		Shape:     NewRegion(R(0, 0, 100, 100)),
		Secure:    true,
	}
	parent := &Node{
		ID:        "parent",
		Transform: Translate(10, 10),
		Clip:      NewRegion(R(0, 0, 40, 40)),
		Children:  []*Node{child},
	}
	snap := NewScene(parent).Snapshot()

	want := NewRegion(R(10, 10, 50, 50))
	if got := snap.VisibleRegion("child"); !got.Equals(want) {
		t.Fatalf("visible = %v, want %v", got.Rects(), want.Rects())
	}
	if _, ok := snap.HitTest(Point{X: 60, Y: 30}); ok {
		t.Fatal("point beyond parent clip must not reach the child")
	}
	hit, ok := snap.HitTest(Point{X: 30, Y: 30})
	if !ok || hit.Node.ID != "child" {
		t.Fatalf("point inside clip must reach child, got %v/%v", hit, ok)
	}
}

func TestLocalPointRunsTransformChainBackwards(t *testing.T) {
	snap := buildFixture().Snapshot()

	origin, ok := snap.LocalPoint("B", Point{X: 130, Y: 60})
	if !ok {
		t.Fatal("B must exist")
	}
	if !approxEq(origin.X, 0) || !approxEq(origin.Y, 0) {
		t.Fatalf("local origin = %v, want (0,0)", origin)
	}

	mid, _ := snap.LocalPoint("B", Point{X: 145, Y: 75})
	if !approxEq(mid.X, 10) || !approxEq(mid.Y, 10) {
		t.Fatalf("local mid = %v, want (10,10)", mid)
	}

	if _, ok := snap.LocalPoint("ghost", Point{}); ok {
		t.Fatal("missing node must fail local mapping")
	}
}

func TestSnapshotIsImmutableAcrossSceneMutation(t *testing.T) {
	scene := buildFixture()
	snap := scene.Snapshot()
	versionBefore := snap.Version()

	hit, ok := snap.HitTest(Point{X: 140, Y: 70})
	if !ok || hit.Node.ID != "B" {
		t.Fatalf("baseline hit = %v/%v", hit, ok)
	}

	// Mutate the live scene: remove the secure button entirely.
	if !scene.RemoveChild("A", "B") {
		t.Fatal("RemoveChild failed")
	}

	// The captured snapshot still resolves exactly as before ...
	hit2, ok := snap.HitTest(Point{X: 140, Y: 70})
	if !ok || hit2.Node.ID != "B" {
		t.Fatalf("snapshot mutated underneath us: %v/%v", hit2, ok)
	}
	if snap.Version() != versionBefore {
		t.Fatal("snapshot version must be stable")
	}

	// ... while a fresh snapshot reflects the new hierarchy and version.
	fresh := scene.Snapshot()
	if fresh.Version() <= versionBefore {
		t.Fatalf("new snapshot version %d must exceed %d", fresh.Version(), versionBefore)
	}
	if _, ok := fresh.Node("B"); ok {
		t.Fatal("B must be gone from the fresh snapshot")
	}
	// The removed button's point now passes through to whatever lies beneath
	// (the cross window), never back to B.
	if hit, ok := fresh.HitTest(Point{X: 140, Y: 70}); ok && hit.Node.ID == "B" {
		t.Fatal("removed button must no longer receive events at its old position")
	}
}

func TestEveryMutationAdvancesVersion(t *testing.T) {
	scene := buildFixture()
	base := scene.Snapshot().Version()

	mutations := []func(){
		func() { scene.AddChild("root", &Node{ID: "N"}) },
		func() { scene.RemoveChild("root", "N") },
		func() { scene.Raise("root", "T") },
		func() { scene.SetTransform("C", Translate(1, 1)) },
		func() { scene.SetShape("C", NewRegion(R(0, 0, 1, 1))) },
		func() { scene.SetClip("A", NewRegion(R(0, 0, 1, 1))) },
	}
	for i, mutate := range mutations {
		mutate()
		if got := scene.Snapshot().Version(); got <= base+uint64(i) {
			t.Fatalf("mutation %d did not advance version: %d", i, got)
		}
	}
}
