package snapshot

import (
	"encoding/binary"
	"fmt"
	"sync"
	"testing"
	"time"
)

// Versioned content model: version v has fragsPerVersion fragments, and
// fragment i encodes (v, i) plus a checksum so a checker can verify that
// every delivered snapshot is complete and single-version.
const fragsPerVersion = 4

func modelPayload(version uint64, index int) []byte {
	b := make([]byte, 20)
	binary.BigEndian.PutUint64(b[0:8], version)
	binary.BigEndian.PutUint64(b[8:16], uint64(index))
	binary.BigEndian.PutUint32(b[16:20], uint32(version*2654435761+uint64(index)))
	return b
}

func publishModel(t *testing.T, p *Publisher, want uint64) {
	t.Helper()
	id, err := p.PublishFunc(fragsPerVersion, modelPayload)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if id != want {
		t.Fatalf("version id = %d, want %d", id, want)
	}
}

// checkSnapshot verifies the snapshot against the content model: complete
// fragment set, every fragment tagged with the snapshot's own version,
// valid checksum, and version newer than the previously received one.
func checkSnapshot(s *Snapshot, prevVersion uint64) error {
	v := s.Version()
	if v <= prevVersion {
		return fmt.Errorf("non-monotonic version: got %d after %d", v, prevVersion)
	}
	frags := s.Fragments()
	if len(frags) != fragsPerVersion {
		return fmt.Errorf("version %d: got %d fragments, want %d", v, len(frags), fragsPerVersion)
	}
	for i, f := range frags {
		if len(f.Data) != 20 {
			return fmt.Errorf("version %d fragment %d: bad length %d", v, i, len(f.Data))
		}
		fv := binary.BigEndian.Uint64(f.Data[0:8])
		fi := binary.BigEndian.Uint64(f.Data[8:16])
		sum := binary.BigEndian.Uint32(f.Data[16:20])
		if fv != v {
			return fmt.Errorf("spliced fragment: snapshot v%d contains fragment of v%d", v, fv)
		}
		if fi != uint64(i) {
			return fmt.Errorf("version %d: fragment index %d at position %d", v, fi, i)
		}
		if sum != uint32(v*2654435761+uint64(i)) {
			return fmt.Errorf("version %d fragment %d: checksum mismatch", v, i)
		}
	}
	return nil
}

// Publish much faster than the consumer drains: intermediate versions
// must be skipped, every delivered snapshot complete and single-version,
// and the final version must eventually arrive.
func TestPublishFasterThanConsume(t *testing.T) {
	p := NewPublisher()
	defer p.Close()
	sub, err := p.Subscribe(2)
	if err != nil {
		t.Fatal(err)
	}

	const total = 2000
	done := make(chan struct{})
	go func() {
		defer close(done)
		for v := uint64(1); v <= total; v++ {
			if _, err := p.PublishFunc(fragsPerVersion, modelPayload); err != nil {
				return
			}
		}
	}()

	var prev uint64
	received := 0
	for prev < total {
		s, ok := sub.Next()
		if !ok {
			t.Fatalf("subscription closed after %d versions", received)
		}
		if err := checkSnapshot(s, prev); err != nil {
			t.Fatal(err)
		}
		prev = s.Version()
		s.Release()
		received++
		// Slow consumer: force backlog so versions get coalesced.
		time.Sleep(time.Millisecond)
	}
	<-done
	if received >= total {
		t.Fatalf("expected skipped versions under backlog, received all %d", received)
	}
	t.Logf("received %d of %d versions (skipped %d)", received, total, total-received)
	sub.Cancel()
	if live := p.LiveFragments(); live != 0 {
		t.Fatalf("leaked fragments: %d live", live)
	}
}

// Capacity 1 with many subscribers: every enqueue displaces the previous
// version. Model invariants must still hold and nothing may leak.
func TestCapacityPressure(t *testing.T) {
	p := NewPublisher()
	defer p.Close()

	const subs = 8
	const total = 500
	subscriptions := make([]*Subscription, subs)
	for i := range subscriptions {
		s, err := p.Subscribe(1)
		if err != nil {
			t.Fatal(err)
		}
		subscriptions[i] = s
	}

	var wg sync.WaitGroup
	for i, s := range subscriptions {
		wg.Add(1)
		go func(i int, s *Subscription) {
			defer wg.Done()
			var prev uint64
			for prev < total {
				snap, ok := s.Next()
				if !ok {
					t.Errorf("sub %d closed early at v%d", i, prev)
					return
				}
				if err := checkSnapshot(snap, prev); err != nil {
					t.Errorf("sub %d: %v", i, err)
					snap.Release()
					return
				}
				prev = snap.Version()
				snap.Release()
			}
		}(i, s)
	}

	for v := uint64(1); v <= total; v++ {
		publishModel(t, p, v)
	}
	wg.Wait()
	for _, s := range subscriptions {
		s.Cancel()
	}
	if live := p.LiveFragments(); live != 0 {
		t.Fatalf("leaked fragments: %d live", live)
	}
}

// Cancelling a subscription must immediately dereference everything it
// has queued, even if the receiver never consumed anything.
func TestCancelDereferencesImmediately(t *testing.T) {
	p := NewPublisher()
	defer p.Close()
	sub, _ := p.Subscribe(4)

	for v := uint64(1); v <= 4; v++ {
		publishModel(t, p, v)
	}
	if live := p.LiveFragments(); live != 4*fragsPerVersion {
		t.Fatalf("before cancel: %d live, want %d", live, 4*fragsPerVersion)
	}
	sub.Cancel()
	if live := p.LiveFragments(); live != 0 {
		t.Fatalf("after cancel: %d live, want 0", live)
	}
	if _, ok := sub.Next(); ok {
		t.Fatal("Next after Cancel should report closed")
	}
}

// Fragments stay alive while a receiver holds a snapshot and are freed
// exactly when it acks (Release).
func TestAckReleasesFragments(t *testing.T) {
	p := NewPublisher()
	defer p.Close()
	sub, _ := p.Subscribe(8)
	defer sub.Cancel()

	publishModel(t, p, 1)
	s1, ok := sub.Next()
	if !ok {
		t.Fatal("no snapshot")
	}
	publishModel(t, p, 2) // queued, not yet consumed
	if live := p.LiveFragments(); live != 2*fragsPerVersion {
		t.Fatalf("with held snapshot + queued version: %d live, want %d", live, 2*fragsPerVersion)
	}
	s1.Release()
	if live := p.LiveFragments(); live != fragsPerVersion {
		t.Fatalf("after ack: %d live, want %d", live, fragsPerVersion)
	}
	s1.Release() // idempotent
	if live := p.LiveFragments(); live != fragsPerVersion {
		t.Fatalf("after double ack: %d live, want %d", live, fragsPerVersion)
	}
}

// A snapshot handed out before Cancel stays valid afterwards.
func TestInFlightSnapshotSurvivesCancel(t *testing.T) {
	p := NewPublisher()
	defer p.Close()
	sub, _ := p.Subscribe(1)

	publishModel(t, p, 1)
	s, ok := sub.Next()
	if !ok {
		t.Fatal("no snapshot")
	}
	sub.Cancel()
	if err := checkSnapshot(s, 0); err != nil {
		t.Fatal(err)
	}
	s.Release()
	if live := p.LiveFragments(); live != 0 {
		t.Fatalf("leaked fragments: %d live", live)
	}
}

// Concurrent publishers and subscribers under -race.
func TestConcurrentStress(t *testing.T) {
	p := NewPublisher()
	const publishers = 4
	const perPublisher = 250

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		sub, err := p.Subscribe(3)
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func(s *Subscription) {
			defer wg.Done()
			var prev uint64
			for {
				snap, ok := s.Next()
				if !ok {
					return
				}
				if err := checkSnapshot(snap, prev); err != nil {
					t.Error(err)
					snap.Release()
					return
				}
				prev = snap.Version()
				snap.Release()
			}
		}(sub)
	}

	var pw sync.WaitGroup
	for i := 0; i < publishers; i++ {
		pw.Add(1)
		go func() {
			defer pw.Done()
			for j := 0; j < perPublisher; j++ {
				if _, err := p.PublishFunc(fragsPerVersion, modelPayload); err != nil {
					return
				}
			}
		}()
	}
	pw.Wait()
	p.Close() // cancels all subscriptions, unblocking consumers
	wg.Wait()
	if live := p.LiveFragments(); live != 0 {
		t.Fatalf("leaked fragments: %d live", live)
	}
}

func ExamplePublisher() {
	p := NewPublisher()
	defer p.Close()
	sub, _ := p.Subscribe(1)
	defer sub.Cancel()

	p.Publish([]byte("hello"), []byte("world"))
	p.Publish([]byte("newer"), []byte("version")) // displaces v1 (capacity 1)

	s, _ := sub.Next()
	fmt.Printf("v%d: %s %s\n", s.Version(), s.Fragments()[0].Data, s.Fragments()[1].Data)
	s.Release()
	// Output: v2: newer version
}
