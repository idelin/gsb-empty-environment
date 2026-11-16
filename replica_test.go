package netsec

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRevocationStaleReplicaRead(t *testing.T) {
	store := NewStore(testSecret)
	replica := NewReplica(store, WithWaitTimeout(15*time.Millisecond))
	ctx := context.Background()

	grant, err := store.WriteTuple(RelationTuple{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}
	replica.ApplyThrough(grant.Timestamp())

	res, err := replica.Check(ctx, CheckRequest{Object: "doc:s", Relation: "viewer", Subject: "user:erin"})
	if err != nil || !res.Allowed {
		t.Fatalf("replica should serve grant: allowed=%v err=%v", res.Allowed, err)
	}

	// Revoke on the leader while the replica stays at the old snapshot.
	revoke, err := store.WriteTuple(RelationTuple{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
		Present:  false,
	})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if replica.Applied() >= revoke.Timestamp() {
		t.Fatalf("replica unexpectedly caught up: applied=%d revoke=%d", replica.Applied(), revoke.Timestamp())
	}

	// Default check must not use the stale snapshot: the resource ACL floor
	// now points at the revocation, so the replica is behind.
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
	}); !errors.Is(err, ErrStaleReplica) {
		t.Fatalf("stale default check: want ErrStaleReplica, got %v", err)
	}

	// An explicit at_least_as_fresh token at the revocation is likewise refused.
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
		Token:    revoke.Token(),
	}); !errors.Is(err, ErrStaleReplica) {
		t.Fatalf("stale explicit check: want ErrStaleReplica, got %v", err)
	}

	// The old grant token cannot legitimize a stale answer either.
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
		Token:    grant.Token(),
	}); !errors.Is(err, ErrStaleReplica) {
		t.Fatalf("stale check pinned to grant: want ErrStaleReplica, got %v", err)
	}

	// After replication catches up, the revocation is immediately enforced.
	replica.ApplyThrough(revoke.Timestamp())
	res, err = replica.Check(ctx, CheckRequest{
		Object:   "doc:s",
		Relation: "viewer",
		Subject:  "user:erin",
		Token:    revoke.Token(),
	})
	if err != nil {
		t.Fatalf("caught-up check: %v", err)
	}
	if res.Allowed {
		t.Fatalf("revoked relation still allowed after applying revocation")
	}
	if res.ServedAt.Timestamp() < revoke.Timestamp() {
		t.Fatalf("served below revocation zookie: %d < %d", res.ServedAt.Timestamp(), revoke.Timestamp())
	}
}

func TestContentChangeZookie(t *testing.T) {
	store := NewStore(testSecret)
	replica := NewReplica(store, WithWaitTimeout(15*time.Millisecond))
	ctx := context.Background()

	grant, err := store.WriteTuple(RelationTuple{
		Object:   "doc:c",
		Relation: "viewer",
		Subject:  "user:fay",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	// A content save returns the zookie that subsequent checks must respect.
	contentZK, err := store.SaveContent("doc:c")
	if err != nil {
		t.Fatalf("SaveContent: %v", err)
	}
	if contentZK.Timestamp() <= grant.Timestamp() {
		t.Fatalf("content zookie %d not newer than grant %d", contentZK.Timestamp(), grant.Timestamp())
	}

	// Replica has only applied the grant, not the content write.
	replica.ApplyThrough(grant.Timestamp())

	// A default check must not read a snapshot older than the content zookie.
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:c",
		Relation: "viewer",
		Subject:  "user:fay",
	}); !errors.Is(err, ErrStaleReplica) {
		t.Fatalf("default check on stale replica: want ErrStaleReplica, got %v", err)
	}

	// An explicit token at the content zookie is also refused until applied.
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:c",
		Relation: "viewer",
		Subject:  "user:fay",
		Token:    contentZK.Token(),
	}); !errors.Is(err, ErrStaleReplica) {
		t.Fatalf("content-token check on stale replica: want ErrStaleReplica, got %v", err)
	}

	// Once the content entry is applied, the check succeeds at a fresh snapshot.
	replica.ApplyThrough(contentZK.Timestamp())
	res, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:c",
		Relation: "viewer",
		Subject:  "user:fay",
	})
	if err != nil {
		t.Fatalf("caught-up content check: %v", err)
	}
	if !res.Allowed || res.ServedAt.Timestamp() < contentZK.Timestamp() {
		t.Fatalf("unexpected result: %+v want fresh >= %d", res, contentZK.Timestamp())
	}

	// The content floor is scoped per resource: another resource is unaffected.
	other, err := store.WriteTuple(RelationTuple{
		Object:   "doc:other",
		Relation: "viewer",
		Subject:  "user:gus",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("other grant: %v", err)
	}
	replica.ApplyThrough(other.Timestamp())
	otherRes, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:other",
		Relation: "viewer",
		Subject:  "user:gus",
	})
	if err != nil {
		t.Fatalf("per-resource floor leaked across objects: %v", err)
	}
	if !otherRes.Allowed {
		t.Fatalf("other resource check denied: %+v", otherRes)
	}
}

func TestReplicaBehindWaitThenForward(t *testing.T) {
	store := NewStore(testSecret)
	ctx := context.Background()

	zk, err := store.WriteTuple(RelationTuple{
		Object:   "doc:w",
		Relation: "viewer",
		Subject:  "user:hal",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}

	t.Run("forward to leader", func(t *testing.T) {
		forwarded := false
		replica := NewReplica(store,
			WithWaitTimeout(10*time.Millisecond),
			WithForwarder(func(ctx context.Context, req CheckRequest) (CheckResult, error) {
				forwarded = true
				return store.Check(ctx, req)
			}),
		)

		// Replica never applies anything, so waiting times out and it forwards.
		res, err := replica.Check(ctx, CheckRequest{
			Object:   "doc:w",
			Relation: "viewer",
			Subject:  "user:hal",
			Token:    zk.Token(),
		})
		if err != nil {
			t.Fatalf("forwarded check: %v", err)
		}
		if !forwarded {
			t.Fatal("expected request to be forwarded to leader")
		}
		if !res.Allowed {
			t.Fatalf("forwarded result denied: %+v", res)
		}
	})

	t.Run("wait resolves after replication", func(t *testing.T) {
		replica := NewReplica(store, WithWaitTimeout(time.Second))

		done := make(chan CheckResult, 1)
		errCh := make(chan error, 1)
		go func() {
			res, err := replica.Check(ctx, CheckRequest{
				Object:   "doc:w",
				Relation: "viewer",
				Subject:  "user:hal",
				Token:    zk.Token(),
			})
			if err != nil {
				errCh <- err
				return
			}
			done <- res
		}()

		// Give the check time to start waiting, then deliver the entry.
		time.Sleep(20 * time.Millisecond)
		if replica.Applied() != 0 {
			t.Fatalf("replica should still be at zero: %d", replica.Applied())
		}
		replica.ApplyThrough(zk.Timestamp())

		select {
		case res := <-done:
			if !res.Allowed {
				t.Fatalf("waited check denied: %+v", res)
			}
			if res.ServedAt.Timestamp() < zk.Timestamp() {
				t.Fatalf("served below requested zookie: %+v", res)
			}
		case err := <-errCh:
			t.Fatalf("waited check failed: %v", err)
		case <-time.After(time.Second):
			t.Fatal("wait was not woken by ApplyThrough")
		}
	})
}

func TestReplicaMalformedAndFutureRejection(t *testing.T) {
	store := NewStore(testSecret)
	replica := NewReplica(store, WithWaitTimeout(10*time.Millisecond))
	ctx := context.Background()

	zk, err := store.WriteTuple(RelationTuple{
		Object:   "doc:j",
		Relation: "viewer",
		Subject:  "user:ivy",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}
	replica.CatchUp()

	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:j",
		Relation: "viewer",
		Subject:  "user:ivy",
		Token:    "not-a-zookie",
	}); !errors.Is(err, ErrMalformedZookie) {
		t.Fatalf("malformed replica token: want ErrMalformedZookie, got %v", err)
	}

	future := mintZookie(testSecret, zk.Timestamp()+5)
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:j",
		Relation: "viewer",
		Subject:  "user:ivy",
		Token:    future.Token(),
	}); !errors.Is(err, ErrFutureZookie) {
		t.Fatalf("future replica token: want ErrFutureZookie, got %v", err)
	}
}
