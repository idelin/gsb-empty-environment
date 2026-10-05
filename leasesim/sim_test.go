package leasesim

import (
	"reflect"
	"strings"
	"testing"
)

// certify runs a full successful campaign for leader at the given epoch and
// returns the sim at the tick where the certificate is formed. With dur=10
// starting at t=0 the certificate expires at tick 11.
func certify(s *Sim, leader string, epoch, dur int) {
	s.Campaign(leader, epoch, dur)
	s.Tick(1)
	s.Flush() // voters grant
	s.Tick(1)
	s.Flush() // leader collects quorum
}

func hasEvent(s *Sim, subs ...string) bool {
	for _, ev := range s.Events() {
		ok := true
		for _, sub := range subs {
			if !strings.Contains(ev, sub) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func assertCommitted(t *testing.T, s *Sim, want ...string) {
	t.Helper()
	got := s.Committed()
	if len(want) == 0 {
		if len(got) != 0 {
			t.Fatalf("committed writes = %v, want none", got)
		}
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("committed writes = %v, want %v", got, want)
	}
}

// A leader that can only reach a minority of voters must never obtain a
// certificate, and its writes must never reach storage.
func TestMinorityCannotObtainCertificate(t *testing.T) {
	s := New()
	s.Pause("v3")
	s.Pause("v4")
	s.Pause("v5")
	s.Campaign("A", 1, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()

	if got := s.GrantCount("A"); got != 2 {
		t.Fatalf("grants = %d, want 2 (minority)", got)
	}
	if cert := s.CertOf("A"); cert != nil {
		t.Fatalf("minority leader got certificate %+v", cert)
	}
	s.Write("A", "w1", "x")
	s.Tick(1)
	s.Flush()
	assertCommitted(t, s)
	if !hasEvent(s, "no certificate") {
		t.Fatalf("expected local rejection of uncertified write")
	}
}

// The reported fault: old primary A is paused, its lease expires, a new
// primary takes over and writes; when A resumes it still flushes its queued
// write to storage. Fencing (and expiry) must reject it.
func TestOldLeaderRecoveryCannotOverwrite(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2, cert epoch=1 expiry=11

	s.Pause("A")
	s.Write("A", "w1", "v1") // queued in A's outbox while paused
	s.Tick(10)               // t=12: A's lease [1,11) long expired

	certify(s, "B", 2, 10) // grants [13,23), cert formed at t=14
	s.Write("B", "w2", "v2")
	s.Tick(1)
	s.Flush() // t=15: storage commits v2, max epoch becomes 2
	assertCommitted(t, s, "v2")

	s.Resume("A") // queued write finally hits the network
	s.Tick(1)
	s.Flush() // t=16: storage must fence the stale write

	assertCommitted(t, s, "v2")
	if !hasEvent(s, "w1", "fenced: cert epoch 1 < storage max epoch 2") {
		t.Fatalf("expected fencing rejection of old leader's write; events:\n%s",
			strings.Join(s.Events(), "\n"))
	}
}

// A renewal that only reaches a minority must not extend the certificate:
// the old expiry stands and writes at/after it are rejected.
func TestPartialRenewalDoesNotExtendCertificate(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2, expiry=11

	s.Pause("v3")
	s.Pause("v4")
	s.Pause("v5")
	s.Campaign("A", 1, 20) // renewal attempt, only v1+v2 reachable
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=4: only 2 grants collected

	if got := s.GrantCount("A"); got != 2 {
		t.Fatalf("renewal grants = %d, want 2", got)
	}
	if cert := s.CertOf("A"); cert == nil || cert.Expiry != 11 {
		t.Fatalf("certificate after partial renewal = %+v, want expiry 11", s.CertOf("A"))
	}

	s.Tick(6) // t=10
	s.Write("A", "w1", "x")
	s.Tick(1)
	s.Flush() // t=11: write processed exactly at expiry
	assertCommitted(t, s)
	if !hasEvent(s, "w1", "expired certificate: tick 11 >= expiry 11") {
		t.Fatalf("expected expiry rejection after partial renewal; events:\n%s",
			strings.Join(s.Events(), "\n"))
	}
}

// The certificate interval is half-open: a write processed at tick == expiry
// is rejected, one processed at expiry-1 commits.
func TestWriteAtExpiryTickExactlyRejected(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2, expiry=11

	s.Tick(7) // t=9
	s.Write("A", "w1", "a")
	s.Tick(1)
	s.Flush() // t=10: processed at expiry-1, commits

	s.Write("A", "w2", "b")
	s.Tick(1)
	s.Flush() // t=11: processed exactly at expiry, rejected

	assertCommitted(t, s, "a")
	if !hasEvent(s, "w2", "expired certificate: tick 11 >= expiry 11") {
		t.Fatalf("expected boundary rejection at tick == expiry; events:\n%s",
			strings.Join(s.Events(), "\n"))
	}
}

// With enough reachable voters and eventual message delivery, a new leader
// waits out the old lease, survives dropped messages, and commits its write.
func TestNewLeaderWritesWhenMessagesEventuallyDelivered(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2, A holds [1,11)
	s.Pause("A")           // old leader vanishes

	// Too early: voters still owe A a valid lease.
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=4
	if s.CertOf("B") != nil {
		t.Fatalf("B certified while A's lease still valid")
	}
	if !hasEvent(s, "conflicting lease held by A until tick 11") {
		t.Fatalf("expected lease-conflict rejections")
	}

	s.Tick(8) // t=12: A's lease expired
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush() // t=13: voters grant [13,23), 5 responses in flight

	// Lose three grant responses: quorum cannot form.
	s.Drop(0)
	s.Drop(0)
	s.Drop(0)
	s.Tick(1)
	s.Flush() // t=14: only 2 grants arrive
	if s.CertOf("B") != nil {
		t.Fatalf("B certified from 2 grants")
	}

	// Retry; this time everything is delivered.
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=16: certified
	if cert := s.CertOf("B"); cert == nil {
		t.Fatalf("B never certified after redelivery")
	}

	s.Write("B", "w1", "new")
	s.Tick(1)
	s.Flush()
	assertCommitted(t, s, "new")
}

// Restarted voters must keep their promises: persisted max epoch and valid
// leases survive a reboot and still block other owners.
func TestRestartPreservesPromises(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2, voters promised A [1,11), max epoch 1

	for _, v := range VoterNames {
		s.Restart(v)
	}

	// Higher epoch, but A's lease is still valid: must be rejected.
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()
	if s.CertOf("B") != nil {
		t.Fatalf("B certified despite valid lease to A surviving restarts")
	}
	if !hasEvent(s, "conflicting lease held by A until tick 11") {
		t.Fatalf("restarted voters forgot lease promises")
	}

	// Lower epoch than the persisted max: must be rejected as stale.
	s.Campaign("B", 0, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()
	if !hasEvent(s, "stale epoch 0 < persisted max epoch 1") {
		t.Fatalf("restarted voters forgot persisted max epoch")
	}
	assertCommitted(t, s)
}

// Retries are deduplicated by request ID; the same ID carrying different
// content is an error.
func TestRetryDedupAndConflictingRequestID(t *testing.T) {
	s := New()
	certify(s, "A", 1, 10) // t=2

	s.Write("A", "w1", "a")
	s.Dup(0) // network duplicates the write
	s.Tick(1)
	s.Flush() // t=3: first copy commits, second is deduped

	assertCommitted(t, s, "a")
	if !hasEvent(s, "dedups retry id=w1") {
		t.Fatalf("expected dedup of retried write")
	}

	s.Write("A", "w1", "b") // same ID, different content
	s.Tick(1)
	s.Flush()
	assertCommitted(t, s, "a")
	if !hasEvent(s, `already committed with different value`) {
		t.Fatalf("expected conflicting-request-ID error")
	}
}

// A full chaotic run - reordering, duplication, pause, restart, drops -
// recorded as an op log and replayed from JSON must reproduce the identical
// event log and committed write sequence.
func TestReplayProducesIdenticalResults(t *testing.T) {
	s := New()
	s.Campaign("A", 1, 10)
	s.Tick(1)
	s.DeliverOne(4) // reorder: v5's request delivered first
	s.DeliverOne(0)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=2: A certified, expiry 11

	s.Pause("v4")
	s.Pause("v5")
	s.Campaign("A", 1, 20) // renewal reaches v1..v3 only, but that is a quorum
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=4: renewed, expiry 23

	s.Write("A", "w1", "x")
	s.Dup(2) // duplicate the write itself (pending: 2 voter reqs + write)
	s.Tick(1)
	s.Flush() // t=5: commit + dedup

	s.Restart("v1")
	s.Campaign("B", 2, 10) // A's renewed lease [3,23) still valid: conflict
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush()
	if s.CertOf("B") != nil {
		t.Fatalf("B certified during A's renewed lease")
	}

	s.Tick(20) // t=27: all leases expired
	s.Campaign("B", 2, 10)
	s.Tick(1)
	s.Flush()
	s.Tick(1)
	s.Flush() // t=29: B certified
	s.Write("B", "w2", "y")
	s.Tick(1)
	s.Flush() // t=30: committed

	assertCommitted(t, s, "x", "y")

	data, err := MarshalOps(s.Ops())
	if err != nil {
		t.Fatalf("marshal ops: %v", err)
	}
	replayed, err := ReplayJSON(data)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !reflect.DeepEqual(s.Events(), replayed.Events()) {
		t.Fatalf("event logs diverge after replay\noriginal:\n%s\nreplayed:\n%s",
			strings.Join(s.Events(), "\n"), strings.Join(replayed.Events(), "\n"))
	}
	if !reflect.DeepEqual(s.Committed(), replayed.Committed()) {
		t.Fatalf("committed writes diverge after replay: %v vs %v",
			s.Committed(), replayed.Committed())
	}
}
