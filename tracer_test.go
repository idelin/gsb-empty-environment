package tracer

import (
	"math/big"
	"strings"
	"testing"
)

func rat(t *testing.T, s string) *big.Rat {
	t.Helper()
	r, ok := new(big.Rat).SetString(s)
	if !ok {
		t.Fatalf("bad rational %q", s)
	}
	return r
}

func checkMass(t *testing.T, rep ContainerReport, solute, want string) {
	t.Helper()
	got, ok := rep.Masses[solute]
	w := rat(t, want)
	if !ok {
		if w.Sign() != 0 {
			t.Fatalf("%s: mass[%s] missing, want %s", rep.ID, solute, want)
		}
		return
	}
	if got.Cmp(w) != 0 {
		t.Fatalf("%s: mass[%s]=%s, want %s", rep.ID, solute, got, want)
	}
}

func checkVolume(t *testing.T, rep ContainerReport, want string) {
	t.Helper()
	if rep.Volume.Cmp(rat(t, want)) != 0 {
		t.Fatalf("%s: volume=%s, want %s", rep.ID, rep.Volume, want)
	}
}

func checkShare(t *testing.T, rep ContainerReport, batch, wantFraction string) {
	t.Helper()
	for _, sh := range rep.Shares {
		if sh.Batch == batch {
			if sh.Fraction.Cmp(rat(t, wantFraction)) != 0 {
				t.Fatalf("%s: share of %s=%s, want %s", rep.ID, batch, sh.Fraction, wantFraction)
			}
			return
		}
	}
	t.Fatalf("%s: no share of batch %s", rep.ID, batch)
}

func mustReport(t *testing.T, s *System, id string) ContainerReport {
	t.Helper()
	rep, err := s.Report(id)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func mustVerify(t *testing.T, s *System) {
	t.Helper()
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestTransferProportionalExact(t *testing.T) {
	s := NewSystem()
	if err := s.AddContainer("A", 3, map[string]string{"x": "1/3"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddContainer("B", 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer("A", "B", 1); err != nil {
		t.Fatal(err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "2")
	checkMass(t, a, "x", "2/9")
	b := mustReport(t, s, "B")
	checkVolume(t, b, "1")
	checkMass(t, b, "x", "1/9")
	checkShare(t, b, "A#init", "1")
	mustVerify(t, s)
}

func TestOverdrawRejectedUnchanged(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "5"})
	_ = s.AddContainer("B", 0, nil)
	if err := s.Transfer("A", "B", 101); err == nil {
		t.Fatal("expected rejection")
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "100")
	checkMass(t, a, "x", "5")
	b := mustReport(t, s, "B")
	checkVolume(t, b, "0")
	checkMass(t, b, "x", "0")
	mustVerify(t, s)
}

func TestBatchUsesSameOldState(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 100, map[string]string{"y": "1"})
	_ = s.AddContainer("C", 0, nil)
	// B->C must be computed from B's pre-batch contents, i.e. it must NOT
	// carry any of the x that flows A->B within the same batch.
	err := s.TransferBatch([]TransferOp{
		{Src: "A", Dst: "B", Volume: 50},
		{Src: "B", Dst: "C", Volume: 50},
	})
	if err != nil {
		t.Fatal(err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "50")
	checkMass(t, a, "x", "1/2")
	b := mustReport(t, s, "B")
	checkVolume(t, b, "100")
	checkMass(t, b, "x", "1/2")
	checkMass(t, b, "y", "1/2")
	c := mustReport(t, s, "C")
	checkVolume(t, c, "50")
	checkMass(t, c, "x", "0")
	checkMass(t, c, "y", "1/2")
	mustVerify(t, s)
}

func TestBatchOverdrawRejectedAtomically(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 0, nil)
	_ = s.AddContainer("C", 0, nil)
	err := s.TransferBatch([]TransferOp{
		{Src: "A", Dst: "B", Volume: 60},
		{Src: "A", Dst: "C", Volume: 50},
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("expected batch rejection, got %v", err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "100")
	checkMass(t, a, "x", "1")
	for _, id := range []string{"B", "C"} {
		rep := mustReport(t, s, id)
		checkVolume(t, rep, "0")
		checkMass(t, rep, "x", "0")
	}
	mustVerify(t, s)
}

func TestEmptyContainer(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("E", 0, nil)
	_ = s.AddContainer("A", 10, map[string]string{"x": "1"})
	if err := s.Transfer("E", "A", 5); err == nil {
		t.Fatal("expected rejection drawing from empty container")
	}
	if err := s.Transfer("E", "A", 0); err != nil {
		t.Fatalf("zero-volume transfer should be a no-op: %v", err)
	}
	if err := s.AddContainer("Z", 0, map[string]string{"x": "1"}); err == nil {
		t.Fatal("expected rejection of mass with zero volume")
	}
	rep := mustReport(t, s, "E")
	checkVolume(t, rep, "0")
	if len(rep.Shares) != 0 {
		t.Fatalf("empty container should have no shares, got %v", rep.Shares)
	}
	label, err := s.AddWater("E", 10)
	if err != nil {
		t.Fatal(err)
	}
	if label != "E#water1" {
		t.Fatalf("water batch label = %q", label)
	}
	if err := s.Transfer("A", "E", 5); err != nil {
		t.Fatal(err)
	}
	rep = mustReport(t, s, "E")
	checkVolume(t, rep, "15")
	checkMass(t, rep, "x", "1/2")
	checkShare(t, rep, "E#water1", "2/3")
	checkShare(t, rep, "A#init", "1/3")
	mustVerify(t, s)
}

func TestBackflow(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 100, map[string]string{"y": "2"})
	if err := s.Transfer("A", "B", 50); err != nil {
		t.Fatal(err)
	}
	if err := s.Transfer("B", "A", 25); err != nil { // reflux
		t.Fatal(err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "75")
	checkMass(t, a, "x", "7/12")
	checkMass(t, a, "y", "1/3")
	checkShare(t, a, "A#init", "7/9")
	checkShare(t, a, "B#init", "2/9")
	b := mustReport(t, s, "B")
	checkVolume(t, b, "125")
	checkMass(t, b, "x", "5/12")
	checkMass(t, b, "y", "5/3")
	checkShare(t, b, "A#init", "1/3")
	checkShare(t, b, "B#init", "2/3")
	mustVerify(t, s)
}

func TestMultiSourceMerge(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 100, map[string]string{"y": "2"})
	_ = s.AddContainer("C", 0, nil)
	_ = s.AddContainer("D", 0, nil)
	if err := s.TransferBatch([]TransferOp{
		{Src: "A", Dst: "C", Volume: 50},
		{Src: "B", Dst: "C", Volume: 50},
	}); err != nil {
		t.Fatal(err)
	}
	c := mustReport(t, s, "C")
	checkVolume(t, c, "100")
	checkMass(t, c, "x", "1/2")
	checkMass(t, c, "y", "1")
	checkShare(t, c, "A#init", "1/2")
	checkShare(t, c, "B#init", "1/2")
	if err := s.Transfer("C", "D", 25); err != nil {
		t.Fatal(err)
	}
	d := mustReport(t, s, "D")
	checkVolume(t, d, "25")
	checkMass(t, d, "x", "1/8")
	checkMass(t, d, "y", "1/4")
	checkShare(t, d, "A#init", "1/2")
	checkShare(t, d, "B#init", "1/2")
	mustVerify(t, s)
}

func TestAddWaterDilution(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 0, nil)
	if _, err := s.AddWater("A", 100); err != nil {
		t.Fatal(err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "200")
	checkMass(t, a, "x", "1")
	checkShare(t, a, "A#init", "1/2")
	checkShare(t, a, "A#water1", "1/2")
	if err := s.Transfer("A", "B", 100); err != nil {
		t.Fatal(err)
	}
	b := mustReport(t, s, "B")
	checkMass(t, b, "x", "1/2")
	mustVerify(t, s)
}

func TestMixAndSelfTransfer(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 10, map[string]string{"x": "1"})
	if err := s.Mix("A"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mix("nope"); err == nil {
		t.Fatal("expected error for unknown container")
	}
	if err := s.Transfer("A", "A", 4); err != nil {
		t.Fatal(err)
	}
	a := mustReport(t, s, "A")
	checkVolume(t, a, "10")
	checkMass(t, a, "x", "1")
	mustVerify(t, s)
}

func TestVerifyDetectsCorruption(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 100, map[string]string{"x": "1"})
	_ = s.AddContainer("B", 0, nil)
	_ = s.Transfer("A", "B", 40)
	mustVerify(t, s)

	s.containers["A"].masses["x"].Add(s.containers["A"].masses["x"], big.NewRat(1, 10))
	if err := s.Verify(); err == nil {
		t.Fatal("Verify should detect mass non-conservation")
	}
	s.containers["A"].masses["x"].Sub(s.containers["A"].masses["x"], big.NewRat(1, 10))
	mustVerify(t, s)

	s.containers["B"].batchVol["A#init"].Add(s.containers["B"].batchVol["A#init"], big.NewRat(1, 1))
	if err := s.Verify(); err == nil {
		t.Fatal("Verify should detect batch-share non-conservation")
	}
}

func TestReportAllAndString(t *testing.T) {
	s := NewSystem()
	_ = s.AddContainer("A", 10, map[string]string{"x": "1/2"})
	_ = s.AddContainer("B", 0, nil)
	_ = s.Transfer("A", "B", 5)
	reps := s.ReportAll()
	if len(reps) != 2 || reps[0].ID != "A" || reps[1].ID != "B" {
		t.Fatalf("unexpected ReportAll: %+v", reps)
	}
	out := reps[1].String()
	if !strings.Contains(out, "container B: volume=5") ||
		!strings.Contains(out, "mass[x]=1/4") ||
		!strings.Contains(out, "batch A#init: fraction=1") {
		t.Fatalf("unexpected report string:\n%s", out)
	}
	if len(s.Log()) == 0 {
		t.Fatal("audit log should not be empty")
	}
	mustVerify(t, s)
}
