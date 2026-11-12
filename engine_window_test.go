package netsec

import (
	"errors"
	"math/big"
	"sync"
	"testing"
)

func TestDoubleBroadcastDisqualifies(t *testing.T) {
	e, _ := NewEngine(2)
	if err := e.RegisterReceiver("r1", big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterReceiver("r2", big.NewInt(2)); err != nil {
		t.Fatal(err)
	}
	m := genDealer("sneaky", 2)
	if err := e.RegisterCommitment("sneaky", m.commit); err != nil {
		t.Fatal(err)
	}
	alt := genDealer("alt", 2)

	h1 := m.commit.Hash()
	h2 := alt.commit.Hash()
	if err := e.RegisterReceipt("sneaky", "r1", h1); err != nil {
		t.Fatal(err)
	}
	err := e.RegisterReceipt("sneaky", "r2", h2)
	if !errors.Is(err, ErrDoubleBroadcast) {
		t.Fatalf("want ErrDoubleBroadcast, got %v", err)
	}
	status, ok := e.DealerStatus("sneaky")
	if !ok || !status.Disqualified || status.Reason != ReasonDoubleBroadcast {
		t.Fatalf("dealer not marked double broadcast: %+v", status)
	}
}

func TestConstantCommitmentClash(t *testing.T) {
	e, _ := NewEngine(1)
	if err := e.RegisterReceiver("r1", big.NewInt(1)); err != nil {
		t.Fatal(err)
	}

	m1 := genDealer("d1", 2)
	m2 := genDealer("d2", 2)
	m2.commit.Points[0] = cloneCommit(m1.commit).Points[0]
	for _, m := range []*dealerMaterial{m1, m2} {
		if err := e.RegisterCommitment(m.id, m.commit); err != nil {
			t.Fatal(err)
		}
		h := m.commit.Hash()
		if err := e.RegisterReceipt(m.id, "r1", h); err != nil {
			t.Fatal(err)
		}
		if err := e.RegisterShare(m.id, "r1", m.shareAt(big.NewInt(1))); err != nil {
			t.Fatal(err)
		}
	}
	closeWindow(e, 1)
	_, err := e.Finalize()
	if !errors.Is(err, ErrNoQualifiedDealers) {
		t.Fatalf("want ErrNoQualifiedDealers, got %v", err)
	}
}

func TestIdentityConstantRejected(t *testing.T) {
	e, _ := NewEngine(1)
	c := Commitment{Points: []Point{infinityPoint(), generatorPoint()}}
	err := e.RegisterCommitment("d", c)
	if !errors.Is(err, ErrInvalidCommitmentPoint) {
		t.Fatalf("want invalid commitment point, got %v", err)
	}
}

func TestWindowBoundaries(t *testing.T) {
	const window = 2
	e, _ := NewEngine(window)
	materials, _ := mustRegisterRun(t, e, []string{"d1"}, []string{"r1", "r2"}, 2)
	hash := materials["d1"].commit.Hash()
	bogus := new(big.Int).Add(materials["d1"].shares["r1"], big.NewInt(1))
	bogus.Mod(bogus, Curve.Params().N)

	if _, err := e.Finalize(); !errors.Is(err, ErrWindowStillOpen) {
		t.Fatalf("want ErrWindowStillOpen, got %v", err)
	}
	if err := e.FileComplaint("d1", "r1", bogus, hash); err != nil {
		t.Fatalf("round 1 complaint: %v", err)
	}
	if e.Round() != 1 {
		t.Fatalf("unexpected round %d", e.Round())
	}
	e.Advance()
	if !e.WindowOpen() {
		t.Fatal("window should still be open at round 2")
	}
	if err := e.DiscloseShare("d1", "r1", materials["d1"].shares["r1"], hash); err != nil {
		t.Fatalf("round 2 disclosure: %v", err)
	}
	e.Advance()
	if e.WindowOpen() {
		t.Fatal("window should be closed at round 3")
	}
	late := new(big.Int).Add(materials["d1"].shares["r2"], big.NewInt(1))
	late.Mod(late, Curve.Params().N)
	if err := e.FileComplaint("d1", "r2", late, hash); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("late complaint: want ErrWindowClosed, got %v", err)
	}
	if err := e.DiscloseShare("d1", "r2", late, hash); !errors.Is(err, ErrWindowClosed) {
		t.Fatalf("late disclosure: want ErrWindowClosed, got %v", err)
	}

	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res.Disqualified) != 0 || len(res.FalseAccusers) != 1 {
		t.Fatalf("late inputs altered the result: %+v", res)
	}

	if err := e.FileComplaint("d1", "r2", late, hash); !errors.Is(err, ErrFinalized) {
		t.Fatalf("post-finalize complaint: want ErrFinalized, got %v", err)
	}
	res2, err := e.Finalize()
	if !errors.Is(err, ErrFinalized) || res2 != nil {
		t.Fatalf("second finalize: want ErrFinalized, got %v", err)
	}
}

func TestHashMismatchAndDuplicateXRejected(t *testing.T) {
	e, _ := NewEngine(2)
	if err := e.RegisterReceiver("r1", big.NewInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterReceiver("r2", big.NewInt(1)); !errors.Is(err, ErrDuplicateIndex) {
		t.Fatalf("duplicate x: want ErrDuplicateIndex, got %v", err)
	}
	m := genDealer("d1", 2)
	if err := e.RegisterCommitment("d1", m.commit); err != nil {
		t.Fatal(err)
	}
	observed := m.commit.Hash()
	if err := e.RegisterReceipt("d1", "r1", observed); err != nil {
		t.Fatal(err)
	}
	var foreign [32]byte
	foreign[0] = 1
	if err := e.FileComplaint("d1", "r1", big.NewInt(5), foreign); !errors.Is(err, ErrCommitmentHashMismatch) {
		t.Fatalf("foreign hash: want ErrCommitmentHashMismatch, got %v", err)
	}
	if err := e.FileComplaint("d1", "r1", m.shareAt(big.NewInt(1)), observed); !errors.Is(err, ErrShareMismatch) {
		t.Fatalf("baseless complaint: want ErrShareMismatch, got %v", err)
	}
}

func TestConcurrentComplaints(t *testing.T) {
	const window = 3
	e, _ := NewEngine(window)
	dealers := []string{"d1", "d2", "d3", "d4"}
	receivers := []string{"r1", "r2", "r3", "r4"}
	materials, _ := mustRegisterRun(t, e, dealers, receivers, 2)

	bogusFor := func(did, rid string) *big.Int {
		bogus := new(big.Int).Add(materials[did].shares[rid], big.NewInt(7))
		return bogus.Mod(bogus, Curve.Params().N)
	}

	// Phase 1: every receiver hammers its complaint concurrently; the
	// first write wins and duplicates must be reported cleanly.
	var wg sync.WaitGroup
	for _, did := range dealers {
		for _, rid := range receivers {
			for k := 0; k < 4; k++ {
				wg.Add(1)
				go func(did, rid string) {
					defer wg.Done()
					err := e.FileComplaint(did, rid, bogusFor(did, rid), materials[did].commit.Hash())
					if err != nil && !errors.Is(err, ErrDuplicateComplaint) {
						t.Errorf("complaint %s/%s: %v", did, rid, err)
					}
				}(did, rid)
			}
		}
	}
	wg.Wait()

	// Phase 2: dealers answer concurrently with the verifying share.
	for _, did := range dealers {
		for _, rid := range receivers {
			wg.Add(1)
			go func(did, rid string) {
				defer wg.Done()
				err := e.DiscloseShare(did, rid, materials[did].shares[rid], materials[did].commit.Hash())
				if err != nil && !errors.Is(err, ErrDuplicateDisclosure) {
					t.Errorf("disclosure %s/%s: %v", did, rid, err)
				}
			}(did, rid)
		}
	}
	wg.Wait()

	closeWindow(e, window)
	res, err := e.Finalize()
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if len(res.Disqualified) != 0 {
		t.Fatalf("unexpected disqualifications: %+v", res.Disqualified)
	}
	wantFalse := len(dealers) * len(receivers)
	if len(res.FalseAccusers) != wantFalse {
		t.Fatalf("want %d false accuser entries, got %d", wantFalse, len(res.FalseAccusers))
	}
	if len(res.Qualified) != len(dealers) {
		t.Fatalf("want %d qualified dealers, got %d", len(dealers), len(res.Qualified))
	}
}

func TestConcurrentRegistrationNoCorruption(t *testing.T) {
	e, _ := NewEngine(2)
	const n = 16
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "r" + itoa(i)
			if err := e.RegisterReceiver(id, big.NewInt(int64(i))); err != nil {
				t.Errorf("register %s: %v", id, err)
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i <= n; i++ {
		id := "r" + itoa(i)
		if _, ok := e.receivers[id]; !ok {
			t.Fatalf("receiver %s missing after concurrent registration", id)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}
