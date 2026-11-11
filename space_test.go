package netsec

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// forEachPoint enumerates every integer grid point of b.
func forEachPoint(b Box, fn func(p [MaxDims]int64)) {
	var p [MaxDims]int64
	var rec func(d int)
	rec = func(d int) {
		if d == b.Dims {
			fn(p)
			return
		}
		for v := b.Lo[d]; v < b.Hi[d]; v++ {
			p[d] = v
			rec(d + 1)
		}
	}
	rec(0)
}

// refModel is a pointwise reference implementation of the marking rules:
// unknown points take the new state, evidenced points keep their established
// state, and every claim's evidence is recorded on the covered points.
type refModel struct {
	state map[[MaxDims]int64]State
	ev    map[[MaxDims]int64][]string
}

func newRefModel() *refModel {
	return &refModel{
		state: make(map[[MaxDims]int64]State),
		ev:    make(map[[MaxDims]int64][]string),
	}
}

func (m *refModel) mark(claim Box, st State, id string) {
	forEachPoint(claim, func(p [MaxDims]int64) {
		if m.state[p] == Unknown {
			m.state[p] = st
		}
		if !containsStr(m.ev[p], id) {
			m.ev[p] = append(m.ev[p], id)
		}
	})
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func evidenceIDs(ev []Evidence) []string {
	ids := make([]string, len(ev))
	for i, e := range ev {
		ids[i] = e.ID
	}
	sort.Strings(ids)
	return ids
}

// checkAgainstGrid verifies, for every grid point of the root, that the
// space agrees with the reference model on state and evidence, and that the
// point is covered by exactly one region.
func checkAgainstGrid(t *testing.T, s *Space, m *refModel) {
	t.Helper()
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	covered := 0
	forEachPoint(s.Root(), func(p [MaxDims]int64) {
		covered++
		r, ok := s.RegionAt(p)
		if !ok {
			t.Fatalf("point %v not covered by any region (gap)", p)
		}
		if want := m.state[p]; r.State != want {
			t.Fatalf("point %v: state %v, want %v", p, r.State, want)
		}
		gotIDs := evidenceIDs(r.Evidence)
		wantIDs := append([]string(nil), m.ev[p]...)
		sort.Strings(wantIDs)
		if fmt.Sprint(gotIDs) != fmt.Sprint(wantIDs) {
			t.Fatalf("point %v: evidence %v, want %v", p, gotIDs, wantIDs)
		}
	})
	if covered != int(s.Root().Volume()) {
		t.Fatalf("enumerated %d points, root volume is %d", covered, s.Root().Volume())
	}
	// Every coverage region must contain at least one grid point and all its
	// points must agree with the model.
	for _, reg := range s.Coverage() {
		n := 0
		forEachPoint(reg.Box, func(p [MaxDims]int64) {
			n++
			if st, _ := s.StateAt(p); st != reg.State {
				t.Fatalf("point %v inside %s region %s has state %v", p, reg.State, reg.Box, st)
			}
		})
		if n != int(reg.Box.Volume()) {
			t.Fatalf("region %s holds %d points, volume is %d", reg.Box, n, reg.Box.Volume())
		}
	}
}

func mustBox(t *testing.T, lo, hi []int64) Box {
	t.Helper()
	b, err := NewBox(lo, hi)
	if err != nil {
		t.Fatalf("NewBox(%v,%v): %v", lo, hi, err)
	}
	return b
}

func mustMark(t *testing.T, s *Space, claim Box, st State, id string) {
	t.Helper()
	if err := s.Mark(claim, st, Evidence{ID: id}); err != nil {
		t.Fatalf("Mark(%s,%v,%s): %v", claim, st, id, err)
	}
}

func TestConflictSplitKeepsBothEvidence(t *testing.T) {
	root := mustBox(t, []int64{0, 0}, []int64{4, 4})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newRefModel()

	safe := mustBox(t, []int64{0, 0}, []int64{3, 3})
	danger := mustBox(t, []int64{2, 2}, []int64{4, 4})
	mustMark(t, s, safe, Safe, "ev-safe")
	m.mark(safe, Safe, "ev-safe")
	mustMark(t, s, danger, Danger, "ev-danger")
	m.mark(danger, Danger, "ev-danger")

	// Overlap [2,3)x[2,3): established Safe wins, both evidence kept.
	r, ok := s.RegionAt([MaxDims]int64{2, 2})
	if !ok {
		t.Fatal("overlap point not covered")
	}
	if r.State != Safe {
		t.Fatalf("overlap state = %v, want Safe (no last-write-wins)", r.State)
	}
	if got := evidenceIDs(r.Evidence); fmt.Sprint(got) != "[ev-danger ev-safe]" {
		t.Fatalf("overlap evidence = %v, want both records", got)
	}
	// Danger-only corner keeps its claim.
	if st, _ := s.StateAt([MaxDims]int64{3, 3}); st != Danger {
		t.Fatalf("corner state = %v, want Danger", st)
	}
	// Untouched area stays unknown and pending.
	if st, _ := s.StateAt([MaxDims]int64{3, 0}); st != Unknown {
		t.Fatalf("untouched state = %v, want Unknown", st)
	}
	checkAgainstGrid(t, s, m)
}

func TestAdjacentMarksTileExactly(t *testing.T) {
	root := mustBox(t, []int64{0, 0}, []int64{4, 4})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newRefModel()
	// Quadrants sharing faces: boundaries must neither gap nor overlap.
	quads := []struct {
		lo, hi []int64
		st     State
	}{
		{[]int64{0, 0}, []int64{2, 2}, Safe},
		{[]int64{2, 0}, []int64{4, 2}, Danger},
		{[]int64{0, 2}, []int64{2, 4}, Danger},
		{[]int64{2, 2}, []int64{4, 4}, Safe},
	}
	for i, q := range quads {
		b := mustBox(t, q.lo, q.hi)
		id := fmt.Sprintf("ev-%d", i)
		mustMark(t, s, b, q.st, id)
		m.mark(b, q.st, id)
	}
	checkAgainstGrid(t, s, m)
	if got := len(s.Coverage()); got != 4 {
		t.Fatalf("coverage has %d regions, want exactly 4", got)
	}
}

func TestRandomMarksAgainstGrid3D(t *testing.T) {
	root := mustBox(t, []int64{0, 0, 0}, []int64{6, 5, 4})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newRefModel()
	rng := rand.New(rand.NewSource(42))
	states := []State{Safe, Danger}
	for i := 0; i < 300; i++ {
		var lo, hi []int64
		for d := 0; d < 3; d++ {
			a := rng.Int63n(root.Hi[d])
			b := rng.Int63n(root.Hi[d])
			if a > b {
				a, b = b, a
			}
			lo = append(lo, a)
			hi = append(hi, b+1)
		}
		claim := mustBox(t, lo, hi)
		st := states[rng.Intn(2)]
		id := fmt.Sprintf("ev-%d", i%17) // reuse IDs to exercise dedup
		mustMark(t, s, claim, st, id)
		m.mark(claim, st, id)
		if i%25 == 0 {
			checkAgainstGrid(t, s, m)
		}
	}
	checkAgainstGrid(t, s, m)
}

func TestFourDimensional(t *testing.T) {
	root := mustBox(t, []int64{0, 0, 0, 0}, []int64{2, 3, 2, 3})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	m := newRefModel()
	marks := []struct {
		lo, hi []int64
		st     State
	}{
		{[]int64{0, 0, 0, 0}, []int64{2, 3, 2, 3}, Safe},
		{[]int64{1, 1, 0, 1}, []int64{2, 3, 2, 3}, Danger},
		{[]int64{0, 0, 1, 0}, []int64{1, 2, 2, 2}, Danger},
		{[]int64{0, 1, 0, 0}, []int64{2, 2, 1, 1}, Safe},
	}
	for i, mk := range marks {
		b := mustBox(t, mk.lo, mk.hi)
		id := fmt.Sprintf("ev-%d", i)
		mustMark(t, s, b, mk.st, id)
		m.mark(b, mk.st, id)
	}
	checkAgainstGrid(t, s, m)
}

func TestReportPartitionsSpace(t *testing.T) {
	root := mustBox(t, []int64{0}, []int64{6})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	mustMark(t, s, mustBox(t, []int64{0}, []int64{2}), Safe, "ev-a")
	mustMark(t, s, mustBox(t, []int64{3}, []int64{4}), Danger, "ev-b")
	rep := s.Report()
	var safeVol, dangerVol, pendVol int64
	for _, r := range rep.Safe {
		safeVol += r.Box.Volume()
	}
	for _, r := range rep.Danger {
		dangerVol += r.Box.Volume()
	}
	for _, r := range rep.Pending {
		pendVol += r.Box.Volume()
		if len(r.Evidence) != 0 {
			t.Fatalf("pending region %s unexpectedly carries evidence", r.Box)
		}
	}
	if safeVol != 2 || dangerVol != 1 || pendVol != 3 {
		t.Fatalf("volumes safe=%d danger=%d pending=%d, want 2/1/3", safeVol, dangerVol, pendVol)
	}
	if safeVol+dangerVol+pendVol != root.Volume() {
		t.Fatal("report does not cover the whole space")
	}
}

func TestMarkValidation(t *testing.T) {
	root := mustBox(t, []int64{0, 0}, []int64{3, 3})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	inside := mustBox(t, []int64{0, 0}, []int64{1, 1})
	cases := []struct {
		name  string
		claim Box
		st    State
		ev    Evidence
	}{
		{"no evidence", inside, Safe, Evidence{}},
		{"mark unknown", inside, Unknown, Evidence{ID: "ev"}},
		{"escapes root", mustBox(t, []int64{2, 2}, []int64{4, 4}), Safe, Evidence{ID: "ev"}},
		{"wrong dims", mustBox(t, []int64{0}, []int64{1}), Safe, Evidence{ID: "ev"}},
	}
	for _, c := range cases {
		if err := s.Mark(c.claim, c.st, c.ev); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
	// Failed marks must not disturb the partition.
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate after rejected marks: %v", err)
	}
	if got := len(s.Coverage()); got != 1 {
		t.Fatalf("coverage has %d regions after rejected marks, want 1", got)
	}
}

func TestNewBoxValidation(t *testing.T) {
	if _, err := NewBox([]int64{0}, []int64{0}); err == nil {
		t.Error("empty range accepted")
	}
	if _, err := NewBox([]int64{0, 0, 0, 0, 0}, []int64{1, 1, 1, 1, 1}); err == nil {
		t.Error("5D box accepted")
	}
	if _, err := NewBox([]int64{0, 0}, []int64{1}); err == nil {
		t.Error("length mismatch accepted")
	}
	if _, err := NewSpace(Box{}); err == nil {
		t.Error("empty root accepted")
	}
}

func TestStateAtOutsideRoot(t *testing.T) {
	root := mustBox(t, []int64{0, 0}, []int64{2, 2})
	s, err := NewSpace(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.StateAt([MaxDims]int64{5, 0}); ok {
		t.Error("StateAt outside root reported coverage")
	}
	if _, ok := s.StateAt([MaxDims]int64{0, -1}); ok {
		t.Error("StateAt below root reported coverage")
	}
}
