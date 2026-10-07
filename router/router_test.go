package router

import (
	"testing"
	"time"
)

func drain(s *Subscriber) []Delivery {
	var out []Delivery
	for {
		select {
		case d := <-s.C:
			out = append(out, d)
		default:
			return out
		}
	}
}

func mustNoDelivery(t *testing.T, s *Subscriber) {
	t.Helper()
	select {
	case d := <-s.C:
		t.Fatalf("subscriber %s: unexpected delivery %+v", s.ID, d)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestPrefixMatch(t *testing.T) {
	r := New()
	a := r.AddSubscriber("a", 4)
	b := r.AddSubscriber("b", 4)
	r.Apply(
		SubscribeOp("a", "sensors/"),
		SubscribeOp("b", "sensors/temp/"),
	)
	r.Publish("sensors/temp/room1", nil)
	if got := drain(a); len(got) != 1 {
		t.Fatalf("a: want 1 delivery, got %d", len(got))
	}
	if got := drain(b); len(got) != 1 {
		t.Fatalf("b: want 1 delivery, got %d", len(got))
	}

	r.Publish("sensors/hum/room1", nil)
	if got := drain(a); len(got) != 1 {
		t.Fatalf("a: want 1 delivery, got %d", len(got))
	}
	mustNoDelivery(t, b)

	// Prefix must really be a prefix: "sensors/" does not match "sensors2/x".
	c := r.AddSubscriber("c", 4)
	r.Apply(SubscribeOp("c", "sensors/"))
	r.Publish("sensors2/x", nil)
	mustNoDelivery(t, c)
	// But it still matches deeper topics under "sensors/".
	r.Publish("sensors/temp/room2", nil)
	if got := drain(c); len(got) != 1 {
		t.Fatalf("c: want 1 delivery, got %d", len(got))
	}
}

func TestMultiPrefixHitDeliversOnce(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	// Both prefixes match the published topic.
	r.Apply(SubscribeOp("s", "a/"), SubscribeOp("s", "a/b/"))
	r.Publish("a/b/c", []byte("x"))
	got := drain(s)
	if len(got) != 1 {
		t.Fatalf("want exactly 1 delivery for multi-prefix hit, got %d", len(got))
	}
}

func TestDuplicateSubscribeDeliversOnce(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	// Duplicate within one batch and across batches.
	r.Apply(SubscribeOp("s", "a/"), SubscribeOp("s", "a/"))
	r.Apply(SubscribeOp("s", "a/"))
	r.Publish("a/1", nil)
	if got := drain(s); len(got) != 1 {
		t.Fatalf("want 1 delivery after duplicate subscribes, got %d", len(got))
	}
	// One unsubscribe removes the single logical subscription.
	r.Apply(UnsubscribeOp("s", "a/"))
	r.Publish("a/2", nil)
	mustNoDelivery(t, s)
}

func TestUnsubscribe(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	r.Apply(SubscribeOp("s", "x/"), SubscribeOp("s", "y/"))
	r.Publish("x/1", nil)
	if got := drain(s); len(got) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(got))
	}
	r.Apply(UnsubscribeOp("s", "x/"))
	r.Publish("x/2", nil)
	mustNoDelivery(t, s)
	r.Publish("y/2", nil)
	if got := drain(s); len(got) != 1 {
		t.Fatalf("want 1 delivery on remaining prefix, got %d", len(got))
	}
	// Unsubscribing something never subscribed is a no-op.
	r.Apply(UnsubscribeOp("s", "z/"), UnsubscribeOp("ghost", "x/"))
	r.Publish("y/3", nil)
	if got := drain(s); len(got) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(got))
	}
}

func TestCommitSequenceOnDeliveries(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	seq1 := r.Apply(SubscribeOp("s", "a/"))
	if seq1 != 1 {
		t.Fatalf("first commit seq = %d, want 1", seq1)
	}
	if got := r.Publish("a/1", nil); got != seq1 {
		t.Fatalf("publish seq = %d, want %d", got, seq1)
	}
	for _, d := range drain(s) {
		if d.Seq != seq1 {
			t.Fatalf("delivery seq = %d, want %d", d.Seq, seq1)
		}
	}
	seq2 := r.Apply(SubscribeOp("s", "b/"))
	if seq2 != seq1+1 {
		t.Fatalf("seq not monotonic: %d then %d", seq1, seq2)
	}
	if got := r.Publish("b/1", nil); got != seq2 {
		t.Fatalf("publish seq = %d, want %d", got, seq2)
	}
	// Empty batch does not advance the sequence.
	if got := r.Apply(); got != seq2 {
		t.Fatalf("empty apply seq = %d, want %d", got, seq2)
	}
}

func TestRemoveSubscriberPurgesSubscriptions(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	r.Apply(SubscribeOp("s", "a/"), SubscribeOp("s", "b/"))
	r.RemoveSubscriber("s")
	r.Publish("a/1", nil)
	r.Publish("b/1", nil)
	mustNoDelivery(t, s)
	if r.Seq() != 2 {
		t.Fatalf("purge should commit a new snapshot, seq = %d, want 2", r.Seq())
	}
	// Unknown subscriber: no new commit.
	if got := r.RemoveSubscriber("ghost"); got != r.Seq() {
		t.Fatalf("remove unknown committed: seq = %d", got)
	}
}

func TestEmptyPrefixMatchesEverything(t *testing.T) {
	r := New()
	s := r.AddSubscriber("s", 4)
	r.Apply(SubscribeOp("s", ""))
	r.Publish("anything/at/all", nil)
	if got := drain(s); len(got) != 1 {
		t.Fatalf("empty prefix should match everything, got %d deliveries", len(got))
	}
}
