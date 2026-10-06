package inspect

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

var errCorrupt = errors.New("corrupt data")

// corruptedIf reports data containing "bad" as corrupted.
func corruptedIf(mark []byte) Validator {
	return func(data []byte) error {
		if bytes.Contains(data, mark) {
			return errCorrupt
		}
		return nil
	}
}

func inspectResult(in *Inspector, key string) (Result, error) {
	return in.Inspect(context.Background(), key)
}

// An update landing between the read and the atomic status update makes
// the inspection result stale: it must be discarded and the new object
// version must stay untouched.
func TestUpdateDuringInspectionDoesNotQuarantineNewVersion(t *testing.T) {
	mem := NewMemoryBackend()
	mem.Put("obj", []byte("bad v1"))

	backend := NewPausableBackend(mem)
	in := NewInspector(backend, corruptedIf([]byte("bad")))

	parked, release := backend.ArmRead()

	type outcome struct {
		res Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := inspectResult(in, "obj")
		done <- outcome{res, err}
	}()

	<-parked // inspector holds v1 ("bad v1") and is about to validate it

	newVersion := mem.Put("obj", []byte("good v2"))
	release()

	out := <-done
	if out.err != nil {
		t.Fatalf("inspect: %v", out.err)
	}
	if out.res.Applied {
		t.Fatalf("stale result for version %d was applied", out.res.Version)
	}
	if out.res.Version != newVersion-1 {
		t.Fatalf("inspected version = %d, want %d", out.res.Version, newVersion-1)
	}
	if out.res.Status != StatusCorrupted {
		t.Fatalf("stale read should have looked corrupted, got %v", out.res.Status)
	}
	if status, _ := mem.StatusOf("obj"); status != StatusUnknown {
		t.Fatalf("new version was marked %v; must stay unknown", status)
	}
}

// A delete landing between read and status update also makes the result
// stale; nothing may be recreated or marked.
func TestDeleteDuringInspectionDiscardsResult(t *testing.T) {
	mem := NewMemoryBackend()
	mem.Put("obj", []byte("bad v1"))

	backend := NewPausableBackend(mem)
	in := NewInspector(backend, corruptedIf([]byte("bad")))

	parked, release := backend.ArmRead()

	done := make(chan error, 1)
	go func() {
		_, err := inspectResult(in, "obj")
		done <- err
	}()

	<-parked
	if !mem.Delete("obj") {
		t.Fatal("object should have existed")
	}
	release()

	if err := <-done; err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if _, ok := mem.StatusOf("obj"); ok {
		t.Fatal("deleted object must not be resurrected by a stale result")
	}
	if _, err := mem.Get(context.Background(), "obj"); err != ErrNotFound {
		t.Fatalf("object should stay deleted, got %v", err)
	}
}

// Repeated inspections are idempotent, and a healthy mark never sticks to
// a version it was not computed from.
func TestRepeatedInspections(t *testing.T) {
	mem := NewMemoryBackend()
	backend := NewPausableBackend(mem)
	in := NewInspector(backend, corruptedIf([]byte("bad")))

	mem.Put("obj", []byte("bad v1"))

	// First inspection quarantines the corrupted version.
	res, err := inspectResult(in, "obj")
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if !res.Applied || res.Status != StatusCorrupted {
		t.Fatalf("first inspection = %+v, want applied corrupted", res)
	}

	// Re-inspecting the unchanged version reaches the same verdict.
	res, err = inspectResult(in, "obj")
	if err != nil {
		t.Fatalf("re-inspect: %v", err)
	}
	if !res.Applied || res.Status != StatusCorrupted {
		t.Fatalf("re-inspection = %+v, want applied corrupted", res)
	}
	if status, _ := mem.StatusOf("obj"); status != StatusCorrupted {
		t.Fatalf("status = %v, want corrupted", status)
	}

	// Updating the object resets the status of the new version.
	mem.Put("obj", []byte("good v2"))
	if status, _ := mem.StatusOf("obj"); status != StatusUnknown {
		t.Fatalf("status after update = %v, want unknown", status)
	}
	res, err = inspectResult(in, "obj")
	if err != nil {
		t.Fatalf("inspect v2: %v", err)
	}
	if !res.Applied || res.Status != StatusHealthy {
		t.Fatalf("inspection of v2 = %+v, want applied healthy", res)
	}

	// A healthy verdict computed for v2 must not be applied to a
	// corrupted v3 written concurrently.
	parked, release := backend.ArmRead()
	done := make(chan Result, 1)
	go func() {
		res, err := inspectResult(in, "obj")
		if err != nil {
			t.Errorf("inspect: %v", err)
		}
		done <- res
	}()
	<-parked
	mem.Put("obj", []byte("bad v3"))
	release()
	if res := <-done; res.Applied {
		t.Fatalf("stale healthy result applied to corrupted v3: %+v", res)
	}
	if status, _ := mem.StatusOf("obj"); status != StatusUnknown {
		t.Fatalf("v3 marked %v by stale result, want unknown", status)
	}

	// The next inspection sees v3 and quarantines it.
	res, err = inspectResult(in, "obj")
	if err != nil {
		t.Fatalf("inspect v3: %v", err)
	}
	if !res.Applied || res.Status != StatusCorrupted {
		t.Fatalf("inspection of v3 = %+v, want applied corrupted", res)
	}
}

// Concurrent writers and inspectors: the recorded status must always
// match the data of the version it was computed from.
func TestConcurrentInspectAndUpdate(t *testing.T) {
	mem := NewMemoryBackend()
	in := NewInspector(mem, corruptedIf([]byte("bad")))
	ctx := context.Background()

	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				data := []byte(fmt.Sprintf("good w%d i%d", w, i))
				if i%3 == 0 {
					data = []byte(fmt.Sprintf("bad w%d i%d", w, i))
				}
				mem.Put("obj", data)
			}
		}(w)
	}
	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if _, err := in.Inspect(ctx, "obj"); err != nil && err != ErrNotFound {
					t.Errorf("inspect: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()

	data, _, status, ok := mem.Snapshot("obj")
	if !ok {
		t.Fatal("object missing")
	}
	corrupt := corruptedIf([]byte("bad"))(data) != nil
	switch {
	case status == StatusCorrupted && !corrupt:
		t.Fatalf("healthy data %q quarantined", data)
	case status == StatusHealthy && corrupt:
		t.Fatalf("corrupted data %q marked healthy", data)
	}
}

// Inspecting a missing object reports ErrNotFound.
func TestInspectMissingObject(t *testing.T) {
	in := NewInspector(NewMemoryBackend(), corruptedIf([]byte("bad")))
	if _, err := inspectResult(in, "nope"); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
