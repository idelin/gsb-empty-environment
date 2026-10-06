package snapshot

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- versioned content model -------------------------------------------------
//
// The model generates deterministic content for every (version, index)
// pair, so a receiver can independently verify each fragment it gets.
// Any splice of fragments from different versions shows up as a content
// mismatch or a broken index sequence.

const modelParts = 4 // fragments per version in model tests

func modelContent(version uint64, index int) []byte {
	return []byte(fmt.Sprintf("v%06d-part%02d", version, index))
}

func modelSnapshot(version uint64) Snapshot {
	parts := make([][]byte, modelParts)
	for i := range parts {
		parts[i] = modelContent(version, i)
	}
	return Snapshot{Version: version, Parts: parts}
}

var modelPartSize = len(modelContent(0, 0))

// reassembler consumes fragments and checks them against the model.
//
// The library guarantees the received stream is a concatenation of
// contiguous version prefixes: every run of same-version fragments starts
// at index 0 with consecutive indices, and run versions strictly increase
// (intermediate versions may be skipped, never revisited or spliced).
type reassembler struct {
	t *testing.T

	runVersion uint64 // version of the current contiguous run
	nextIndex  int    // next expected index within the run
	inRun      bool

	completed []uint64
}

func (r *reassembler) add(f Fragment) {
	r.t.Helper()
	if f.Count != modelParts {
		r.t.Fatalf("fragment %+v: Count = %d, want %d", f, f.Count, modelParts)
	}
	if f.Index < 0 || f.Index >= f.Count {
		r.t.Fatalf("fragment %+v: index out of range", f)
	}
	if want := modelContent(f.Version, f.Index); !bytes.Equal(f.Data, want) {
		r.t.Fatalf("fragment v%d i%d: data %q, want %q (cross-version splice?)",
			f.Version, f.Index, f.Data, want)
	}
	if r.inRun && f.Version == r.runVersion {
		if f.Index != r.nextIndex {
			r.t.Fatalf("v%d: got index %d, want contiguous prefix index %d",
				f.Version, f.Index, r.nextIndex)
		}
	} else {
		// A new run must start at index 0 of a strictly newer version:
		// intermediate versions may be skipped, never revisited.
		if f.Index != 0 {
			r.t.Fatalf("v%d: new run starts at index %d, want 0", f.Version, f.Index)
		}
		if r.inRun && f.Version <= r.runVersion {
			r.t.Fatalf("version went backwards: %d after %d", f.Version, r.runVersion)
		}
		if n := len(r.completed); n > 0 && f.Version <= r.completed[n-1] {
			r.t.Fatalf("version %d after completed %d", f.Version, r.completed[n-1])
		}
		r.runVersion, r.nextIndex, r.inRun = f.Version, 0, true
	}
	r.nextIndex++
	if r.nextIndex == f.Count {
		r.completed = append(r.completed, f.Version)
		r.inRun = false
	}
}

func (r *reassembler) lastCompleted() uint64 {
	if len(r.completed) == 0 {
		return 0
	}
	return r.completed[len(r.completed)-1]
}

// drainUntil receives until the given version has been fully reassembled.
// It always terminates: the latest version is never evicted from the queue.
func drainUntil(t *testing.T, sub *Subscription, r *reassembler, version uint64) {
	t.Helper()
	for r.lastCompleted() < version {
		f, err := sub.Recv()
		if err != nil {
			t.Fatalf("waiting for v%d: %v", version, err)
		}
		r.add(f)
	}
}

// --- tests -------------------------------------------------------------------

// Publish much faster than the subscriber consumes: the subscriber must
// still observe only complete latest versions, with intermediate versions
// skipped wholesale and never spliced.
func TestPublishFasterThanConsume(t *testing.T) {
	const versions = 300
	st := NewStream(modelParts) // queue holds exactly one version
	sub := st.Subscribe()
	defer sub.Cancel()

	go func() {
		for v := uint64(1); v <= versions; v++ {
			if err := st.Publish(modelSnapshot(v)); err != nil {
				t.Errorf("publish v%d: %v", v, err)
				return
			}
			if v%10 == 0 {
				time.Sleep(2 * time.Millisecond) // paced bursts: consumer keeps up within a burst, falls behind across bursts
			}
		}
	}()

	r := &reassembler{t: t}
	for r.lastCompleted() < versions {
		f, err := sub.Recv()
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		r.add(f)
		if f.Index == 0 {
			time.Sleep(200 * time.Microsecond) // slow consumer: force backlog
		}
		sub.Ack(f.Version)
	}
	if got := r.lastCompleted(); got != versions {
		t.Fatalf("last completed version = %d, want %d", got, versions)
	}
	if len(r.completed) < 2 {
		t.Fatalf("completed only %d versions, want several complete reassemblies", len(r.completed))
	}
	t.Logf("completed %d of %d versions (skipped %d)",
		len(r.completed), versions, versions-len(r.completed))
}

// A subscriber that never reads must not grow memory: its queue holds at
// most the latest complete version, however many versions are published.
func TestCapacityPressureBackloggedSubscriber(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()
	defer sub.Cancel()

	for v := uint64(1); v <= 1000; v++ {
		if err := st.Publish(modelSnapshot(v)); err != nil {
			t.Fatalf("publish v%d: %v", v, err)
		}
		if got, want := sub.RetainedBytes(), modelParts*modelPartSize; got != want {
			t.Fatalf("after v%d: retained = %d bytes, want exactly one version (%d)",
				v, got, want)
		}
	}

	// The backlogged subscriber holds the latest complete version only.
	r := &reassembler{t: t}
	drainUntil(t, sub, r, 1000)
	if len(r.completed) != 1 || r.completed[0] != 1000 {
		t.Fatalf("completed = %v, want [1000]", r.completed)
	}
}

// Repeatedly hammer a slow receiver with evictions; every received
// fragment sequence must satisfy the prefix model.
func TestEvictionStressKeepsPrefixInvariant(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		st := NewStream(modelParts)
		sub := st.Subscribe()
		const versions = 60
		go func() {
			for v := uint64(1); v <= versions; v++ {
				if err := st.Publish(modelSnapshot(v)); err != nil {
					return
				}
			}
		}()
		r := &reassembler{t: t}
		drainUntil(t, sub, r, versions)
		sub.Cancel()
	}
}

// Received fragments stay retained until the receiver acks; acking a
// version releases it and everything older, but nothing newer.
func TestAckReleasesOldFragments(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()
	defer sub.Cancel()

	recvVersion := func(v uint64) {
		t.Helper()
		if err := st.Publish(modelSnapshot(v)); err != nil {
			t.Fatalf("publish v%d: %v", v, err)
		}
		for i := 0; i < modelParts; i++ {
			if _, err := sub.Recv(); err != nil {
				t.Fatalf("recv v%d: %v", v, err)
			}
		}
	}

	recvVersion(1)
	recvVersion(2)
	if got, want := sub.RetainedBytes(), 2*modelParts*modelPartSize; got != want {
		t.Fatalf("unacked retained = %d, want %d", got, want)
	}

	sub.Ack(1) // releases v1 only
	if got, want := sub.RetainedBytes(), modelParts*modelPartSize; got != want {
		t.Fatalf("after Ack(1): retained = %d, want %d", got, want)
	}

	recvVersion(3)
	sub.Ack(3) // releases v2 and v3
	if got := sub.RetainedBytes(); got != 0 {
		t.Fatalf("after Ack(3): retained = %d, want 0", got)
	}
}

// Cancel must immediately dereference queued and unacked fragments.
func TestCancelReleasesImmediately(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()

	if err := st.Publish(modelSnapshot(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Recv(); err != nil {
		t.Fatal(err)
	}
	if err := st.Publish(modelSnapshot(2)); err != nil {
		t.Fatal(err)
	}
	if got := sub.RetainedBytes(); got == 0 {
		t.Fatal("expected retained fragments before cancel")
	}

	sub.Cancel()
	if got := sub.RetainedBytes(); got != 0 {
		t.Fatalf("after cancel: retained = %d, want 0", got)
	}
	if _, err := sub.Recv(); err != ErrClosed {
		t.Fatalf("recv after cancel: err = %v, want ErrClosed", err)
	}
	sub.Cancel() // idempotent
	if got := st.Stats().Subscribers; got != 0 {
		t.Fatalf("subscribers = %d, want 0 after cancel", got)
	}
}

// Cancel while Recv is blocked must unblock it with ErrClosed.
func TestCancelUnblocksRecv(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()

	done := make(chan error, 1)
	go func() {
		_, err := sub.Recv()
		done <- err
	}()
	time.Sleep(10 * time.Millisecond) // let Recv block
	sub.Cancel()
	select {
	case err := <-done:
		if err != ErrClosed {
			t.Fatalf("blocked Recv got %v, want ErrClosed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Recv still blocked after Cancel")
	}
}

// A late subscriber starts from the latest complete version, not from
// the beginning of the stream.
func TestLateSubscriberGetsLatestVersion(t *testing.T) {
	st := NewStream(modelParts)
	for v := uint64(1); v <= 5; v++ {
		if err := st.Publish(modelSnapshot(v)); err != nil {
			t.Fatal(err)
		}
	}
	sub := st.Subscribe()
	defer sub.Cancel()

	r := &reassembler{t: t}
	drainUntil(t, sub, r, 5)
	if len(r.completed) != 1 || r.completed[0] != 5 {
		t.Fatalf("completed = %v, want [5]", r.completed)
	}
}

// Deterministic boundary check: with a queue that fits exactly one
// version, receiving one fragment of v1 and then publishing v2 must
// yield a prefix of v1 followed by a prefix of v2 — never interleaved.
func TestNoSpliceAtVersionBoundary(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()
	defer sub.Cancel()

	if err := st.Publish(modelSnapshot(1)); err != nil {
		t.Fatal(err)
	}
	first, err := sub.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != 1 || first.Index != 0 {
		t.Fatalf("first fragment = %+v, want v1 i0", first)
	}
	if err := st.Publish(modelSnapshot(2)); err != nil {
		t.Fatal(err)
	}

	r := &reassembler{t: t}
	r.add(first)
	drainUntil(t, sub, r, 2)
	if got := r.lastCompleted(); got != 2 {
		t.Fatalf("last completed = %d, want 2", got)
	}
}

// Concurrent publishers, subscribers, acks and cancels under the race
// detector, with the model verifying every delivered fragment.
func TestConcurrentModelVerification(t *testing.T) {
	const versions = 200
	st := NewStream(modelParts)

	var wg sync.WaitGroup
	for s := 0; s < 4; s++ {
		sub := st.Subscribe()
		r := &reassembler{t: t}
		wg.Add(1)
		go func(sub *Subscription, r *reassembler, slow bool) {
			defer wg.Done()
			defer sub.Cancel()
			for r.lastCompleted() < versions {
				f, err := sub.Recv()
				if err != nil {
					t.Errorf("recv: %v", err)
					return
				}
				r.add(f)
				if slow && f.Index == 0 {
					time.Sleep(time.Millisecond)
				}
				if f.Index == f.Count-1 {
					sub.Ack(f.Version)
				}
			}
		}(sub, r, s%2 == 1)
	}

	for v := uint64(1); v <= versions; v++ {
		if err := st.Publish(modelSnapshot(v)); err != nil {
			t.Fatalf("publish v%d: %v", v, err)
		}
	}
	wg.Wait()
}

func TestPublishValidation(t *testing.T) {
	st := NewStream(modelParts)
	if err := st.Publish(Snapshot{Version: 1}); err != ErrEmptySnapshot {
		t.Fatalf("empty snapshot: err = %v, want ErrEmptySnapshot", err)
	}
	big := Snapshot{Version: 1, Parts: make([][]byte, modelParts+1)}
	if err := st.Publish(big); err != ErrVersionTooLarge {
		t.Fatalf("oversized snapshot: err = %v, want ErrVersionTooLarge", err)
	}
	if err := st.Publish(modelSnapshot(7)); err != nil {
		t.Fatal(err)
	}
	if err := st.Publish(modelSnapshot(7)); err != ErrVersionRegression {
		t.Fatalf("same version: err = %v, want ErrVersionRegression", err)
	}
	if err := st.Publish(modelSnapshot(3)); err != ErrVersionRegression {
		t.Fatalf("older version: err = %v, want ErrVersionRegression", err)
	}
}

func TestStreamClose(t *testing.T) {
	st := NewStream(modelParts)
	sub := st.Subscribe()
	if err := st.Publish(modelSnapshot(1)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := st.Publish(modelSnapshot(2)); err != ErrClosed {
		t.Fatalf("publish after close: err = %v, want ErrClosed", err)
	}
	if _, err := sub.Recv(); err != ErrClosed {
		t.Fatalf("recv after close: err = %v, want ErrClosed", err)
	}
	if got := sub.RetainedBytes(); got != 0 {
		t.Fatalf("retained after close = %d, want 0", got)
	}
	if got := st.Stats().Subscribers; got != 0 {
		t.Fatalf("subscribers after close = %d, want 0", got)
	}
	st.Close() // idempotent
}
