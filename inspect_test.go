package hexgrid

import "testing"

func cleanSnapshot(t *testing.T) Snapshot {
	t.Helper()
	u3, _ := Universe(3)
	u4, _ := Universe(4)
	return NewSnapshot(map[int][]Cell{3: u3, 4: u4})
}

func TestInspectClean(t *testing.T) {
	rep := Inspect(cleanSnapshot(t), InspectOptions{})
	if !rep.OK() {
		t.Fatalf("clean snapshot has findings: %v", rep.Findings)
	}
	if rep.Components != 1 {
		t.Fatalf("clean snapshot has %d components, want 1", rep.Components)
	}
	if rep.Skew.Buckets == 0 {
		t.Fatal("expected skew statistics to be computed")
	}
}

func TestInspectDanglingRef(t *testing.T) {
	snap := cleanSnapshot(t)
	u3, _ := Universe(3)
	victim := u3[0]
	delete(snap.Levels[3], victim)
	rep := Inspect(snap, InspectOptions{})
	if rep.OK() {
		t.Fatal("expected dangling-ref findings")
	}
	dangling := rep.FindingsOfKind(FindingDanglingRef)
	if len(dangling) != 4 {
		t.Fatalf("got %d dangling refs, want 4 (aperture-4 children)", len(dangling))
	}
	for _, f := range dangling {
		if f.Cell.Resolution() != 4 {
			t.Fatalf("dangling finding at wrong resolution: %s", f.Cell)
		}
	}
}

func TestInspectOrphanLoop(t *testing.T) {
	snap := cleanSnapshot(t)
	u4, _ := Universe(4)
	// Pick an interior cell and cut the shell around its 1-ring,
	// stranding an island of cells.
	var center Cell
	for _, c := range u4 {
		if !c.IsReachable() {
			continue
		}
		interior := true
		for _, nb := range Neighbors(c) {
			if nb.Face() != c.Face() {
				interior = false
				break
			}
		}
		if interior {
			center = c
			break
		}
	}
	island := KRing(center, 1)
	inIsland := map[Cell]bool{}
	for _, c := range island {
		inIsland[c] = true
	}
	for _, c := range KRing(center, 2) {
		if !inIsland[c] {
			delete(snap.Levels[4], c)
		}
	}
	rep := Inspect(snap, InspectOptions{})
	orphans := rep.FindingsOfKind(FindingOrphanLoop)
	if len(orphans) != 1 {
		t.Fatalf("got %d orphan loops, want 1", len(orphans))
	}
	if rep.Components != 2 {
		t.Fatalf("got %d components, want 2", rep.Components)
	}
	if rep.LargestComponent <= len(island) {
		t.Fatalf("largest component %d should exceed island %d", rep.LargestComponent, len(island))
	}
}

func TestInspectDataSkew(t *testing.T) {
	snap := cleanSnapshot(t)
	u4, _ := Universe(4)
	hot := u4[1234]
	snap.Weights = map[Cell]int64{hot: 100000}
	rep := Inspect(snap, InspectOptions{})
	skew := rep.FindingsOfKind(FindingDataSkew)
	if len(skew) != 1 {
		t.Fatalf("got %d skew findings, want 1", len(skew))
	}
	parent, _ := hot.Parent()
	if skew[0].Cell != parent {
		t.Fatalf("skew bucket = %s, want hot parent %s", skew[0].Cell, parent)
	}
	if rep.Skew.Max != 100003 { // 100000 payload + 3 siblings of weight 1
		t.Fatalf("skew max = %d, want 100003", rep.Skew.Max)
	}
}

func TestInspectSkewThreshold(t *testing.T) {
	snap := cleanSnapshot(t)
	u4, _ := Universe(4)
	// A mild imbalance below the default threshold stays clean.
	snap.Weights = map[Cell]int64{u4[0]: 5}
	rep := Inspect(snap, InspectOptions{})
	if len(rep.FindingsOfKind(FindingDataSkew)) != 0 {
		t.Fatal("mild imbalance must not be flagged at default threshold")
	}
	// A very strict threshold flags it.
	rep = Inspect(snap, InspectOptions{SkewThreshold: 1.0001})
	if len(rep.FindingsOfKind(FindingDataSkew)) == 0 {
		t.Fatal("strict threshold should flag the imbalance")
	}
}

func TestVerifyUniverse(t *testing.T) {
	rep, err := VerifyUniverse(3)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("universe res 3 failed verification: %v", rep.Findings)
	}
	if rep.Cells != 20*64 {
		t.Fatalf("cells = %d, want %d", rep.Cells, 20*64)
	}
	if rep.Reachable+rep.Unreachable != rep.Cells {
		t.Fatal("reachability accounting does not add up")
	}
	if rep.Reachable == 0 {
		t.Fatal("no reachable cells found")
	}
}
