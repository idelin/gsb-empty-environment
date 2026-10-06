package receipt

import (
	"encoding/json"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func dump(t *testing.T, s *Store) string {
	t.Helper()
	b, err := json.Marshal(s.items)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func applyEvent(t *testing.T, s *Store, e testEvent) {
	t.Helper()
	var err error
	if e.decision != nil {
		err = s.Supersede(*e.decision)
	} else {
		err = s.Apply(*e.receipt)
	}
	if err != nil {
		t.Fatalf("apply %+v: %v", e, err)
	}
}

func shuffledScenario(rng *rand.Rand) []testEvent {
	rc := func(item string, epoch, rev uint64, st Status) testEvent {
		return testEvent{receipt: &Receipt{ItemID: item, Epoch: epoch, Revision: rev, Status: st}}
	}
	var receipts []testEvent
	for _, e := range []testEvent{
		rc("x", 1, 1, StatusAccepted), rc("x", 1, 2, StatusRunning), rc("x", 1, 3, StatusSucceeded),
		rc("x", 2, 1, StatusAccepted), rc("x", 2, 2, StatusRunning), rc("x", 2, 3, StatusFailed),
		rc("x", 3, 1, StatusAccepted), rc("x", 3, 2, StatusRunning), rc("x", 3, 3, StatusRunning), rc("x", 3, 4, StatusSucceeded),
		rc("y", 5, 1, StatusAccepted), rc("y", 5, 2, StatusRunning), rc("y", 5, 3, StatusFailed), rc("y", 5, 4, StatusRunning),
	} {
		receipts = append(receipts, e)
	}
	rng.Shuffle(len(receipts), func(i, j int) { receipts[i], receipts[j] = receipts[j], receipts[i] })

	d12 := testEvent{decision: &SupersedeDecision{ItemID: "x", From: 1, To: 2}}
	d23 := testEvent{decision: &SupersedeDecision{ItemID: "x", From: 2, To: 3}}
	i := rng.Intn(len(receipts) + 1)
	j := rng.Intn(len(receipts) + 2)
	if j < i {
		i, j = j, i
	}
	var out []testEvent
	out = append(out, receipts[:i]...)
	out = append(out, d12)
	out = append(out, receipts[i:j-1]...)
	out = append(out, d23)
	out = append(out, receipts[j-1:]...)
	return out
}

func TestCheckpointRecovery(t *testing.T) {
	dir, err := ioutil.TempDir("", "receipt-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	logPath := filepath.Join(dir, "events.log")
	cpPath := filepath.Join(dir, "checkpoint.json")

	rng := rand.New(rand.NewSource(42))
	events := shuffledScenario(rng)

	ref := New()
	mustRegister(t, ref, "x", 1)
	mustRegister(t, ref, "y", 5)

	ps, err := Open(logPath, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, ps, "x", 1)
	mustRegister(t, ps, "y", 5)

	for i, e := range events {
		applyEvent(t, ref, e)
		applyEvent(t, ps, e)
		if i%4 == 1 {
			if err := ps.Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := ps.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := ps.Close(); err != nil {
		t.Fatal(err)
	}

	rec, err := Open(logPath, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	if got, want := dump(t, rec), dump(t, ref); got != want {
		t.Fatalf("checkpoint recovery mismatch:\n got %s\nwant %s", got, want)
	}

	full, err := Open(logPath, "")
	if err != nil {
		t.Fatal(err)
	}
	defer full.Close()
	if got, want := dump(t, full), dump(t, ref); got != want {
		t.Fatalf("full replay mismatch:\n got %s\nwant %s", got, want)
	}

	if epoch, ok := rec.CurrentEpoch("x"); !ok || epoch != 3 {
		t.Fatalf("current epoch of x = %d, want unique current epoch 3", epoch)
	}
	headIs(t, rec, "x", 3, 4, StatusSucceeded)
	headIs(t, rec, "y", 5, 3, StatusFailed)
	comps := rec.PendingCompensations("x")
	want := []Compensation{{ItemID: "x", Epoch: 1, Status: StatusSucceeded}}
	if !reflect.DeepEqual(comps, want) {
		t.Fatalf("compensations = %+v, want %+v", comps, want)
	}
	if n := len(rec.Evidence("x")); n != 10 {
		t.Fatalf("evidence of x = %d, want 10", n)
	}
	if n := len(rec.Evidence("y")); n != 4 {
		t.Fatalf("evidence of y = %d, want 4", n)
	}
}

func TestRecoveredStoreContinues(t *testing.T) {
	dir, err := ioutil.TempDir("", "receipt-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	logPath := filepath.Join(dir, "events.log")
	cpPath := filepath.Join(dir, "checkpoint.json")

	ps, err := Open(logPath, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	mustRegister(t, ps, "a", 1)
	mustApply(t, ps, Receipt{ItemID: "a", Epoch: 1, Revision: 1, Status: StatusRunning})
	if err := ps.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := ps.Close(); err != nil {
		t.Fatal(err)
	}

	rec, err := Open(logPath, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Close()
	mustApply(t, rec, Receipt{ItemID: "a", Epoch: 1, Revision: 2, Status: StatusSucceeded})
	if err := rec.Supersede(SupersedeDecision{ItemID: "a", From: 1, To: 2}); err != nil {
		t.Fatal(err)
	}
	mustApply(t, rec, Receipt{ItemID: "a", Epoch: 2, Revision: 1, Status: StatusAccepted})
	if err := rec.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	headIs(t, rec, "a", 2, 1, StatusAccepted)

	again, err := Open(logPath, cpPath)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	headIs(t, again, "a", 2, 1, StatusAccepted)
	if epoch, _ := again.CurrentEpoch("a"); epoch != 2 {
		t.Fatalf("current epoch = %d, want 2", epoch)
	}
	comps := again.PendingCompensations("a")
	if len(comps) != 1 || comps[0].Epoch != 1 {
		t.Fatalf("compensations = %+v, want one for epoch 1", comps)
	}
}
