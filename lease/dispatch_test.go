package lease

import (
	"errors"
	"testing"
	"time"
)

func newTestDispatcher(overlap time.Duration) (*Dispatcher, *MemInbox, *FakeClock) {
	clock := NewFakeClock(time.Unix(1_700_000_000, 0))
	inbox := NewMemInbox()
	d := NewDispatcher(NewMemStore(), inbox, Options{Now: clock.Now, Overlap: overlap})
	return d, inbox, clock
}

func TestReceiveDeliverAndDuplicateOnce(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	out, err := d.Receive(100, 4, 1005, []byte("alpha"))
	if err != nil || out.Disposition != Accepted || out.State != StateCommitted {
		t.Fatalf("first: %+v, err %v", out, err)
	}
	if out.Ack == nil || out.Ack.Seq != 1005 {
		t.Fatalf("want transport ack for 1005, got %+v", out.Ack)
	}
	// Same logical sequence, same payload: delivered exactly once.
	out, err = d.Receive(100, 4, 1005, []byte("alpha"))
	if err != nil || out.Disposition != Duplicate {
		t.Fatalf("duplicate: %+v, err %v", out, err)
	}
	if out.Ack == nil {
		t.Fatal("duplicates still get a transport ack")
	}
	if inbox.Len() != 1 || inbox.Applies != 1 {
		t.Fatalf("inbox: len %d applies %d, want 1/1", inbox.Len(), inbox.Applies)
	}
}

func TestDualPathDuplicateAcrossGenerations(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	// During the overlap the same logical datagram arrives on both
	// paths; the business identity collapses them to one delivery.
	if out, _ := d.Receive(100, 4, 1007, []byte("x")); out.Disposition != Accepted {
		t.Fatalf("old path: %+v", out)
	}
	out, _ := d.Receive(200, 4, 1007, []byte("x"))
	if out.Disposition != Duplicate {
		t.Fatalf("new path duplicate: %+v", out)
	}
	if inbox.Len() != 1 {
		t.Fatalf("inbox len %d, want 1", inbox.Len())
	}
}

func TestConflictFreezesAndKeepsBothSources(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if out, _ := d.Receive(100, 4, 1005, []byte("old-copy")); out.Disposition != Accepted {
		t.Fatalf("first: %+v", out)
	}
	out, _ := d.Receive(200, 4, 1005, []byte("new-copy"))
	if out.Disposition != Frozen || out.Conflict == nil {
		t.Fatalf("conflict: %+v", out)
	}
	c := out.Conflict
	// Both sources are retained; nothing is decided by arrival time.
	if c.First.Generation != 1 || string(c.First.Payload) != "old-copy" {
		t.Fatalf("first source: %+v", c.First)
	}
	if c.Second.Generation != 2 || string(c.Second.Payload) != "new-copy" {
		t.Fatalf("second source: %+v", c.Second)
	}
	if !d.Frozen(1, 9) {
		t.Fatal("request must be frozen")
	}
	// While frozen, further datagrams are held back.
	if out, _ := d.Receive(200, 4, 1006, []byte("later")); out.Disposition != Frozen {
		t.Fatalf("frozen receive: %+v", out)
	}
	// The host picks the winner explicitly by generation. The first
	// copy is already committed to the inbox, so only it is consistent.
	if err := d.ResolveConflict(1, 9, 1005, 2); !errors.Is(err, ErrConflictDelivered) {
		t.Fatalf("contradicting choice: want ErrConflictDelivered, got %v", err)
	}
	if err := d.ResolveConflict(1, 9, 1005, 1); err != nil {
		t.Fatalf("consistent choice: %v", err)
	}
	if d.Frozen(1, 9) {
		t.Fatal("request must unfreeze after resolution")
	}
	if got, _, _ := inbox.Lookup(RecordKey{Tenant: 1, Auth: 9, Seq: 1005}); string(got.Payload) != "old-copy" {
		t.Fatalf("inbox payload %q", got.Payload)
	}
}

func TestConflictReverseArrivalOrder(t *testing.T) {
	d, _, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	// New path first this time: arrival order must not pick a winner.
	if out, _ := d.Receive(200, 4, 1005, []byte("new-copy")); out.Disposition != Accepted {
		t.Fatalf("first: %+v", out)
	}
	out, _ := d.Receive(100, 4, 1005, []byte("old-copy"))
	if out.Disposition != Frozen || out.Conflict == nil {
		t.Fatalf("conflict: %+v", out)
	}
	if out.Conflict.First.Generation != 2 || out.Conflict.Second.Generation != 1 {
		t.Fatalf("sources: %+v", out.Conflict)
	}
}

func TestResolveConflictDeliversChosenWhenUndelivered(t *testing.T) {
	clock := NewFakeClock(time.Unix(1_700_000_000, 0))
	inbox := NewMemInbox()
	paused := NewPausableInbox(inbox)
	d := NewDispatcher(NewMemStore(), paused, Options{Now: clock.Now, Overlap: time.Minute})
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	// First copy arrives but the host inbox crashes before applying it.
	paused.Pause(PauseBefore)
	entered := paused.Entered()
	done := make(chan Outcome, 1)
	go func() {
		out, _ := d.Receive(100, 4, 1005, []byte("old-copy"))
		done <- out
	}()
	<-entered
	paused.Release(ErrInjected)
	if out := <-done; out.Disposition != Accepted || out.State != StatePending {
		t.Fatalf("crashed delivery: %+v", out)
	}
	// The second, different copy freezes the request.
	if out, _ := d.Receive(200, 4, 1005, []byte("new-copy")); out.Disposition != Frozen {
		t.Fatalf("conflict: %+v", out)
	}
	// Host chooses the new-path copy; it is delivered now.
	if err := d.ResolveConflict(1, 9, 1005, 2); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := inbox.Lookup(RecordKey{Tenant: 1, Auth: 9, Seq: 1005}); string(got.Payload) != "new-copy" {
		t.Fatalf("inbox payload %q", got.Payload)
	}
	if st, _ := d.Receiver().State(RecordKey{Tenant: 1, Auth: 9, Seq: 1005}); st != StateCommitted {
		t.Fatalf("state %v, want committed", st)
	}
}

func TestOldPathAckCannotReleaseNewGenerationStaging(t *testing.T) {
	d, _, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Receive(100, 4, 1009, []byte("g1")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Receive(200, 4, 1010, []byte("g2")); err != nil {
		t.Fatal(err)
	}
	// An ack arriving on the old connection for the new generation's
	// sequence must not release the new staging area.
	if d.OnNetAck(100, 4, 1010) {
		t.Fatal("old-path ack released new-generation staging")
	}
	if got := d.Staged(1, 9, 2); len(got) != 1 || got[0] != 1010 {
		t.Fatalf("gen2 staging: %v", got)
	}
	// The same sequence acked on the new path releases it.
	if !d.OnNetAck(200, 4, 1010) {
		t.Fatal("new-path ack should release its staging")
	}
	if got := d.Staged(1, 9, 2); len(got) != 0 {
		t.Fatalf("gen2 staging after ack: %v", got)
	}
	// Old-generation staging is released by old-path acks as usual.
	if !d.OnNetAck(100, 4, 1009) {
		t.Fatal("old-path ack should release old-generation staging")
	}
	if got := d.Staged(1, 9, 1); len(got) != 0 {
		t.Fatalf("gen1 staging after ack: %v", got)
	}
}

func TestLateOldPacketAfterMigrationCompletes(t *testing.T) {
	d, inbox, clock := newTestDispatcher(30 * time.Second)
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Receive(200, 4, 1010, []byte("g2")); err != nil {
		t.Fatal(err)
	}
	// Migration completes: the overlap window closes.
	clock.Advance(31 * time.Second)
	out, err := d.Receive(100, 4, 1011, []byte("late"))
	if out.Disposition != Rejected || !errors.Is(err, ErrOverlapExpired) {
		t.Fatalf("late packet: %+v, err %v", out, err)
	}
	if out.Ack != nil {
		t.Fatal("rejected datagrams get no ack")
	}
	// New generation staging is untouched by the late packet.
	if got := d.Staged(1, 9, 2); len(got) != 1 || got[0] != 1010 {
		t.Fatalf("gen2 staging: %v", got)
	}
	if inbox.Len() != 1 {
		t.Fatalf("inbox len %d, want 1", inbox.Len())
	}
	// Once the expired lease is pruned, late packets are plain unroutable.
	if _, err := d.Receive(100, 4, 1012, []byte("later")); !errors.Is(err, ErrNoLease) {
		t.Fatalf("after pruning: want ErrNoLease, got %v", err)
	}
}

func TestLateOldPacketAfterClose(t *testing.T) {
	d, _, _ := newTestDispatcher(time.Hour)
	old, err := d.Issue(1, 100, 4, 9, 1000, 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(1, 9, 200, 4, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if err := d.Manager().Close(old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Receive(100, 4, 1011, []byte("late")); !errors.Is(err, ErrNoLease) {
		t.Fatalf("late after close: want ErrNoLease, got %v", err)
	}
}

func TestStreamNumberReuseEndToEnd(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	// Stream number 4 reused on two connections: independent requests.
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Issue(1, 200, 4, 10, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if out, _ := d.Receive(100, 4, 1005, []byte("a")); out.Disposition != Accepted {
		t.Fatalf("conn 100: %+v", out)
	}
	out, _ := d.Receive(200, 4, 1005, []byte("a"))
	if out.Disposition != Accepted {
		t.Fatalf("conn 200 must not dedup against conn 100: %+v", out)
	}
	if inbox.Len() != 2 {
		t.Fatalf("inbox len %d, want 2", inbox.Len())
	}
}

func TestSameAuthNumberAcrossTenantsEndToEnd(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	// Both tenants use business authorization number 9.
	if _, err := d.Issue(1, 100, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Issue(2, 200, 4, 9, 1000, 100); err != nil {
		t.Fatal(err)
	}
	if out, _ := d.Receive(100, 4, 1005, []byte("same")); out.Disposition != Accepted {
		t.Fatalf("tenant 1: %+v", out)
	}
	out, _ := d.Receive(200, 4, 1005, []byte("same"))
	if out.Disposition != Accepted {
		t.Fatalf("tenant 2 must be isolated from tenant 1: %+v", out)
	}
	if inbox.Len() != 2 {
		t.Fatalf("inbox len %d, want 2", inbox.Len())
	}
	// A duplicate on tenant 1's path does not leak into tenant 2.
	if out, _ := d.Receive(100, 4, 1005, []byte("same")); out.Disposition != Duplicate {
		t.Fatalf("tenant 1 duplicate: %+v", out)
	}
	if d.Frozen(2, 9) {
		t.Fatal("tenant 2 must not be affected")
	}
}

func TestHalfRingDatagramRejected(t *testing.T) {
	d, inbox, _ := newTestDispatcher(time.Minute)
	if _, err := d.Issue(1, 100, 4, 9, 1000, uint32(SeqHalf-1)); err != nil {
		t.Fatal(err)
	}
	wire := uint32((1000 + SeqHalf) & SeqMask)
	out, err := d.Receive(100, 4, wire, []byte("ambiguous"))
	if out.Disposition != Rejected || !errors.Is(err, ErrAmbiguousSeq) {
		t.Fatalf("half-ring datagram: %+v, err %v", out, err)
	}
	if inbox.Len() != 0 {
		t.Fatalf("inbox len %d, want 0", inbox.Len())
	}
}
