package scrubber

import (
	"strings"
	"testing"
)

// intactVerifier treats payloads with the "good:" prefix as intact.
func intactVerifier(data []byte) bool {
	return strings.HasPrefix(string(data), "good:")
}

func TestHealthyObjectMarked(t *testing.T) {
	b := NewMemoryBackend()
	if _, err := b.Put("k", []byte("good:payload")); err != nil {
		t.Fatal(err)
	}
	insp := NewInspector(b, intactVerifier)

	out, err := insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if out != Healthy {
		t.Fatalf("outcome = %v, want %v", out, Healthy)
	}
	if b.Quarantined("k") {
		t.Fatal("healthy object must not be quarantined")
	}
}

func TestCorruptObjectQuarantined(t *testing.T) {
	b := NewMemoryBackend()
	if _, err := b.Put("k", []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	insp := NewInspector(b, intactVerifier)

	out, err := insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if out != Corrupt {
		t.Fatalf("outcome = %v, want %v", out, Corrupt)
	}
	if !b.Quarantined("k") {
		t.Fatal("corrupt object must be quarantined")
	}
	if _, err := b.Get("k"); err != ErrQuarantined {
		t.Fatalf("Get after quarantine = %v, want ErrQuarantined", err)
	}

	// A repeated inspection sees the quarantine, not a new verdict.
	out, err = insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if out != AlreadyQuarantined {
		t.Fatalf("re-inspect outcome = %v, want %v", out, AlreadyQuarantined)
	}
}

// An update landing between the read and the mark expires the result:
// the inspection reports Stale and the new version stays untouched.
func TestUpdateDuringInspectionExpiresResult(t *testing.T) {
	inner := NewMemoryBackend()
	if _, err := inner.Put("k", []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	pb := NewPausableBackend(inner)
	insp := NewInspector(pb, intactVerifier)

	readDone := pb.ArmPause()
	type result struct {
		out Outcome
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := insp.Inspect("k")
		done <- result{out, err}
	}()

	<-readDone // inspector holds corrupt v1, frozen before the mark
	v2, err := inner.Put("k", []byte("good:updated"))
	if err != nil {
		t.Fatal(err)
	}
	pb.Resume()

	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.out != Stale {
		t.Fatalf("outcome = %v, want %v", r.out, Stale)
	}
	if inner.Quarantined("k") {
		t.Fatal("updated object must not be quarantined by an expired result")
	}
	cur, err := inner.Get("k")
	if err != nil {
		t.Fatal(err)
	}
	if cur.Version != v2.Version || string(cur.Data) != "good:updated" {
		t.Fatalf("current object = %+v, want version %d good:updated", cur, v2.Version)
	}
}

// A delete landing between the read and the mark means there is nothing
// left to mark: the inspection reports Gone and nothing is resurrected.
func TestDeleteDuringInspectionExpiresResult(t *testing.T) {
	inner := NewMemoryBackend()
	if _, err := inner.Put("k", []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	pb := NewPausableBackend(inner)
	insp := NewInspector(pb, intactVerifier)

	readDone := pb.ArmPause()
	done := make(chan Outcome, 1)
	go func() {
		out, err := insp.Inspect("k")
		if err != nil {
			t.Error(err)
		}
		done <- out
	}()

	<-readDone
	if err := inner.Delete("k"); err != nil {
		t.Fatal(err)
	}
	pb.Resume()

	if out := <-done; out != Gone {
		t.Fatalf("outcome = %v, want %v", out, Gone)
	}
	if inner.Quarantined("k") {
		t.Fatal("deleted object must not be quarantined")
	}
	if _, err := inner.Get("k"); err != ErrNotFound {
		t.Fatalf("Get after delete = %v, want ErrNotFound", err)
	}
}

// Two inspections racing on the same corrupt version both observe it;
// the quarantine applies once and the object stays quarantined.
func TestRepeatedInspectionsSameVersion(t *testing.T) {
	inner := NewMemoryBackend()
	if _, err := inner.Put("k", []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	pb := NewPausableBackend(inner)
	insp := NewInspector(pb, intactVerifier)

	readDone := pb.ArmPause()
	first := make(chan Outcome, 1)
	go func() {
		out, err := insp.Inspect("k")
		if err != nil {
			t.Error(err)
		}
		first <- out
	}()

	<-readDone // first inspection frozen between read and mark

	// Second inspection runs to completion on the same version.
	second, err := insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if second != Corrupt {
		t.Fatalf("second outcome = %v, want %v", second, Corrupt)
	}

	pb.Resume()
	if out := <-first; out != Corrupt {
		t.Fatalf("first outcome = %v, want %v", out, Corrupt)
	}
	if !inner.Quarantined("k") {
		t.Fatal("object must be quarantined")
	}
}

// After a corrupt version is quarantined, a fresh Put creates a new
// version that the old verdict cannot touch.
func TestPutAfterQuarantineStartsClean(t *testing.T) {
	b := NewMemoryBackend()
	if _, err := b.Put("k", []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	insp := NewInspector(b, intactVerifier)

	out, err := insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if out != Corrupt {
		t.Fatalf("outcome = %v, want %v", out, Corrupt)
	}

	if _, err := b.Put("k", []byte("good:rewritten")); err != nil {
		t.Fatal(err)
	}
	if b.Quarantined("k") {
		t.Fatal("new version must not inherit the quarantine")
	}
	out, err = insp.Inspect("k")
	if err != nil {
		t.Fatal(err)
	}
	if out != Healthy {
		t.Fatalf("re-inspect outcome = %v, want %v", out, Healthy)
	}
}

func TestInspectMissingObject(t *testing.T) {
	b := NewMemoryBackend()
	insp := NewInspector(b, intactVerifier)
	out, err := insp.Inspect("missing")
	if err != nil {
		t.Fatal(err)
	}
	if out != Gone {
		t.Fatalf("outcome = %v, want %v", out, Gone)
	}
}
