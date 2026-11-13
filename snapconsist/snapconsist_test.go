package snapconsist

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

var annViewer = Tuple{Object: "doc:1", Relation: "viewer", Subject: "user:ann"}

func grantedSetup(t *testing.T) (*Leader, *Replica) {
	t.Helper()
	leader := NewLeader()
	replica := NewReplica(leader)
	leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer}})
	if _, err := replica.CatchUp(context.Background()); err != nil {
		t.Fatalf("initial catch up: %v", err)
	}
	return leader, replica
}

func defaultCheckReq() CheckRequest {
	return CheckRequest{Object: annViewer.Object, Relation: annViewer.Relation, Subject: annViewer.Subject}
}

func TestZookieMonotonicAndRoundTrip(t *testing.T) {
	leader := NewLeader()
	var previous uint64
	for i := 0; i < 5; i++ {
		var z Zookie
		if i%2 == 0 {
			z = leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer}})
		} else {
			z = leader.SaveContent("doc:1")
		}
		if z.Revision() <= previous {
			t.Fatalf("zookie not monotonic: got %d after %d", z.Revision(), previous)
		}
		parsed, err := ParseZookie(z.String())
		if err != nil {
			t.Fatalf("parse own token %q: %v", z.String(), err)
		}
		if parsed.Revision() != z.Revision() {
			t.Fatalf("round trip mismatch: %d != %d", parsed.Revision(), z.Revision())
		}
		previous = z.Revision()
	}
	if leader.Latest().Revision() != 5 {
		t.Fatalf("latest = %d, want 5", leader.Latest().Revision())
	}
}

func TestParseInvalidZookie(t *testing.T) {
	invalid := []string{
		"",
		"GSBZ1-",
		"GSBZ1-0",
		"GSBZ2-1",
		"GSB-1",
		"GSBZ1-01",  // non-canonical leading zero
		"GSBZ1-zz",  // non-hex payload
		"GSBZ1-1 ",  // trailing space
		" GSBZ1-1",  // leading space
		"GSBZ1-1\n", // newline
		"GSBZ1-1-extra",
	}
	for _, token := range invalid {
		if _, err := ParseZookie(token); !errors.Is(err, ErrInvalidZookie) {
			t.Errorf("ParseZookie(%q) err = %v, want ErrInvalidZookie", token, err)
		}
	}
}

func TestInvalidZookieRejectedByCheck(t *testing.T) {
	leader, replica := grantedSetup(t)
	checker := NewChecker(leader, replica)

	req := defaultCheckReq()
	req.AtLeastAsFresh = "GSBZ1-01"
	if _, err := checker.Check(context.Background(), req); !errors.Is(err, ErrInvalidZookie) {
		t.Fatalf("malformed token err = %v, want ErrInvalidZookie", err)
	}
}

func TestFutureZookieRejected(t *testing.T) {
	leader, replica := grantedSetup(t)
	head := leader.Latest().Revision()

	// Leader rejects out-of-range revisions directly.
	if _, err := leader.Check(context.Background(), head+1, annViewer); !errors.Is(err, ErrFutureZookie) {
		t.Fatalf("leader.Check future rev err = %v, want ErrFutureZookie", err)
	}

	checker := NewChecker(leader, replica)
	req := defaultCheckReq()
	req.AtLeastAsFresh = fmt.Sprintf("GSBZ1-%x", head+1)
	if _, err := checker.Check(context.Background(), req); !errors.Is(err, ErrFutureZookie) {
		t.Fatalf("future token err = %v, want ErrFutureZookie", err)
	}

	// A token at the current head is accepted.
	req.AtLeastAsFresh = fmt.Sprintf("GSBZ1-%x", head)
	result, err := checker.Check(context.Background(), req)
	if err != nil {
		t.Fatalf("head token should be accepted: %v", err)
	}
	if !result.Allowed {
		t.Fatal("head token check should allow the granted tuple")
	}
}

func TestRevocationOnStaleReplica(t *testing.T) {
	leader, replica := grantedSetup(t)
	blocking := NewChecker(leader, replica)
	forwarding := NewChecker(leader, replica, WithForwarding())

	// Revoke ann's viewer relation; the replica deliberately stays at rev 1.
	revokeZookie := leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer, Remove: true}})
	if replica.AppliedRevision() != 1 {
		t.Fatalf("replica rev = %d, want stale rev 1", replica.AppliedRevision())
	}
	// The stale snapshot would wrongly allow the revoked member: this is the
	// "new enemy" hazard the freshness guard must prevent.
	if !replica.checkLocal(annViewer) {
		t.Fatal("stale replica snapshot unexpectedly denies before catch up")
	}

	// Default check must not answer from the stale snapshot, only wait.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := blocking.Check(ctx, defaultCheckReq()); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("stale default check err = %v, want ErrStaleWait", err)
	}

	// Even an explicit old token cannot go below the resource's ACL floor.
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	oldReq := defaultCheckReq()
	oldReq.AtLeastAsFresh = "GSBZ1-1"
	if _, err := blocking.Check(ctx, oldReq); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("old-token check err = %v, want ErrStaleWait", err)
	}

	// Forwarding replica asks the leader and gets the post-revocation deny
	// without exposing the stale allow.
	result, err := forwarding.Check(context.Background(), defaultCheckReq())
	if err != nil {
		t.Fatalf("forwarded check: %v", err)
	}
	if result.Allowed || !result.Forwarded || result.UsedZookie.Revision() != revokeZookie.Revision() {
		t.Fatalf("forwarded result = %+v, want deny at rev %d", result, revokeZookie.Revision())
	}

	// Once the replica applies the revocation, checks deny immediately using
	// its own fresh snapshot.
	if _, err := replica.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	result, err = blocking.Check(context.Background(), defaultCheckReq())
	if err != nil {
		t.Fatalf("post-catchup check: %v", err)
	}
	if result.Allowed || result.Waited || result.UsedZookie.Revision() < revokeZookie.Revision() {
		t.Fatalf("post-catchup result = %+v, want immediate deny at >= rev %d", result, revokeZookie.Revision())
	}
}

func TestContentZookieFloor(t *testing.T) {
	leader, replica := grantedSetup(t)
	blocking := NewChecker(leader, replica)
	forwarding := NewChecker(leader, replica, WithForwarding())

	// A content save records its zookie as the resource floor; the replica is
	// one revision behind.
	contentZookie := leader.SaveContent("doc:1")

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := blocking.Check(ctx, defaultCheckReq()); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("check below content floor err = %v, want ErrStaleWait", err)
	}

	// The forwarded check evaluates at the content revision and still allows
	// the unchanged ACL, proving the floor is about freshness not denial.
	result, err := forwarding.Check(context.Background(), defaultCheckReq())
	if err != nil {
		t.Fatalf("forwarded content-floor check: %v", err)
	}
	if !result.Allowed || result.UsedZookie.Revision() != contentZookie.Revision() {
		t.Fatalf("forwarded result = %+v, want allow at rev %d", result, contentZookie.Revision())
	}

	// A later content write also invalidates the previously returned token.
	leader.SaveContent("doc:1")
	req := defaultCheckReq()
	req.AtLeastAsFresh = contentZookie.String()
	ctx, cancel = context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := blocking.Check(ctx, req); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("stale content token err = %v, want ErrStaleWait", err)
	}

	if _, err := replica.CatchUp(context.Background()); err != nil {
		t.Fatalf("catch up: %v", err)
	}
	result, err = blocking.Check(context.Background(), defaultCheckReq())
	if err != nil {
		t.Fatalf("post-catchup content check: %v", err)
	}
	if !result.Allowed || result.UsedZookie.Revision() < leader.ResourceFloor("doc:1").Revision() {
		t.Fatalf("result = %+v, want snapshot at least the resource floor", result)
	}
}

func TestReplicaLagWaitsThenAnswers(t *testing.T) {
	leader, replica := grantedSetup(t)
	checker := NewChecker(leader, replica)
	leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer, Remove: true}})

	// Simulate asynchronous replication that lands shortly after the check.
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = replica.CatchUp(context.Background())
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	result, err := checker.Check(ctx, defaultCheckReq())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("waiting check: %v", err)
	}
	if result.Allowed || !result.Waited || result.UsedZookie.Revision() != 2 {
		t.Fatalf("result = %+v, want waited deny at rev 2", result)
	}
	if elapsed < 15*time.Millisecond {
		t.Fatalf("check returned in %v, must have waited for replication", elapsed)
	}
}

func TestDefaultCheckUsesLatestAppliedButNotBeforeACLFloor(t *testing.T) {
	leader, replica := grantedSetup(t)

	// A second resource gets an ACL write; the replica lags. Its default check
	// on doc:1 is still servable because doc:1's floor is rev 1, showing the
	// default timestamp is the replica's applied revision, not the leader head.
	leader.WriteACL("doc:2", []ACLChange{{Tuple: Tuple{Object: "doc:2", Relation: "viewer", Subject: "user:bob"}}})
	result, err := NewChecker(leader, replica).Check(context.Background(), defaultCheckReq())
	if err != nil {
		t.Fatalf("unrelated resource lag should not block doc:1: %v", err)
	}
	if !result.Allowed || result.UsedZookie.Revision() != 1 {
		t.Fatalf("result = %+v, want allow from applied rev 1", result)
	}

	// Revocation on doc:1 immediately becomes part of its floor, so the same
	// default check can no longer use rev 1.
	leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer, Remove: true}})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := NewChecker(leader, replica).Check(ctx, defaultCheckReq()); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("post-revocation default check must wait, got %v", err)
	}
}

func TestCancellationAndHelpers(t *testing.T) {
	leader, replica := grantedSetup(t)
	leader.WriteACL("doc:1", []ACLChange{{Tuple: annViewer, Remove: true}})

	// An already-cancelled context makes the wait fail with ErrStaleWait and
	// CatchUp propagate the cancellation without touching the stale snapshot.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := replica.CatchUp(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("CatchUp err = %v, want context.Canceled", err)
	}
	if _, err := NewChecker(leader, replica).Check(ctx, defaultCheckReq()); !errors.Is(err, ErrStaleWait) {
		t.Fatalf("cancelled check err = %v, want ErrStaleWait", err)
	}

	// CatchUpTo drives the replica to an explicit target via polling.
	catchCtx, catchCancel := context.WithTimeout(context.Background(), time.Second)
	defer catchCancel()
	rev, err := replica.CatchUpTo(catchCtx, 2)
	if err != nil || rev != 2 || replica.AppliedZookie().Revision() != 2 {
		t.Fatalf("CatchUpTo rev=%d err=%v, want rev 2", rev, err)
	}

	// Zookie zero value and leader snapshot helpers behave at the boundary.
	var zero Zookie
	if !zero.IsZero() || !NewLeader().Latest().IsZero() {
		t.Fatal("zero zookie helpers misbehave")
	}
	if _, err := leader.Check(context.Background(), 0, annViewer); err != nil {
		t.Fatalf("leader check at rev 0: %v", err)
	}
	if _, err := leader.Check(ctx, 1, annViewer); !errors.Is(err, context.Canceled) {
		t.Fatalf("leader.Check cancelled err = %v, want context.Canceled", err)
	}
}
