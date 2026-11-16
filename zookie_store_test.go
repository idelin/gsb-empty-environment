package netsec

import (
	"context"
	"errors"
	"strings"
	"testing"
)

var testSecret = []byte("cluster-secret-key")

func TestZookieMonotonicAndAuth(t *testing.T) {
	store := NewStore(testSecret)
	ctx := context.Background()

	var prev uint64
	for i := 0; i < 5; i++ {
		zk, err := store.WriteTuple(RelationTuple{
			Object:   "doc:1",
			Relation: "viewer",
			Subject:  "user:alice",
			Present:  true,
		})
		if err != nil {
			t.Fatalf("WriteTuple: %v", err)
		}
		if zk.Timestamp() <= prev {
			t.Fatalf("zookie not monotonic: got %d after %d", zk.Timestamp(), prev)
		}
		if zk.Token() == "" || zk.Token() != zk.String() {
			t.Fatalf("empty/inconsistent token: %#v", zk)
		}
		prev = zk.Timestamp()
	}

	// Content writes share the same monotonic clock.
	contentZK, err := store.SaveContent("doc:1")
	if err != nil {
		t.Fatalf("SaveContent: %v", err)
	}
	if contentZK.Timestamp() <= prev {
		t.Fatalf("content zookie not monotonic: %d <= %d", contentZK.Timestamp(), prev)
	}

	// The issued token round-trips through the authenticated parser.
	parsed, err := ParseZookie(testSecret, contentZK.Token())
	if err != nil {
		t.Fatalf("ParseZookie: %v", err)
	}
	if parsed.Timestamp() != contentZK.Timestamp() {
		t.Fatalf("parsed timestamp %d != %d", parsed.Timestamp(), contentZK.Timestamp())
	}

	// A token signed with a different cluster secret must be rejected.
	if _, err := ParseZookie([]byte("other-secret"), contentZK.Token()); !errors.Is(err, ErrMalformedZookie) {
		t.Fatalf("wrong-secret token: want ErrMalformedZookie, got %v", err)
	}

	// The leader serves checks at its newest timestamp.
	res, err := store.Check(ctx, CheckRequest{Object: "doc:1", Relation: "viewer", Subject: "user:alice"})
	if err != nil {
		t.Fatalf("leader Check: %v", err)
	}
	if !res.Allowed || res.ServedAt.Timestamp() != store.Latest().Timestamp() {
		t.Fatalf("unexpected leader result: %+v", res)
	}
}

func TestMalformedZookieRejected(t *testing.T) {
	store := NewStore(testSecret)
	valid, err := store.WriteTuple(RelationTuple{
		Object:   "doc:m",
		Relation: "viewer",
		Subject:  "user:bob",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}
	ctx := context.Background()

	malformed := []string{
		"garbage",
		"1:2",
		"1:2:3:4",
		"2:1:abc",
		"1:0:" + strings.Split(valid.Token(), ":")[2],
		"1:abc:zzz",
		"1:1:deadbeef",
		valid.Token() + "x",
	}

	// The empty string is not a wire token: the parser rejects it, while a
	// check treats an empty Token field as "no explicit bound".
	if _, err := ParseZookie(testSecret, ""); !errors.Is(err, ErrMalformedZookie) {
		t.Errorf("ParseZookie(\"\"): want ErrMalformedZookie, got %v", err)
	}

	for _, token := range malformed {
		if _, err := ParseZookie(testSecret, token); !errors.Is(err, ErrMalformedZookie) {
			t.Errorf("ParseZookie(%q): want ErrMalformedZookie, got %v", token, err)
		}
		if _, err := store.Check(ctx, CheckRequest{
			Object:   "doc:m",
			Relation: "viewer",
			Subject:  "user:bob",
			Token:    token,
		}); !errors.Is(err, ErrMalformedZookie) {
			t.Errorf("leader Check(%q): want ErrMalformedZookie, got %v", token, err)
		}
	}
}

func TestFutureZookieRejected(t *testing.T) {
	store := NewStore(testSecret)
	replica := NewReplica(store)
	ctx := context.Background()

	zk, err := store.WriteTuple(RelationTuple{
		Object:   "doc:f",
		Relation: "viewer",
		Subject:  "user:carol",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("WriteTuple: %v", err)
	}

	// Forge a well-formed token beyond the leader's latest timestamp.
	future := mintZookie(testSecret, zk.Timestamp()+100)

	if _, err := store.Check(ctx, CheckRequest{
		Object:   "doc:f",
		Relation: "viewer",
		Subject:  "user:carol",
		Token:    future.Token(),
	}); !errors.Is(err, ErrFutureZookie) {
		t.Fatalf("leader future token: want ErrFutureZookie, got %v", err)
	}

	replica.CatchUp()
	if _, err := replica.Check(ctx, CheckRequest{
		Object:   "doc:f",
		Relation: "viewer",
		Subject:  "user:carol",
		Token:    future.Token(),
	}); !errors.Is(err, ErrFutureZookie) {
		t.Fatalf("replica future token: want ErrFutureZookie, got %v", err)
	}

	// Once the leader advances past the timestamp, the token is no longer future.
	for i := 0; i < 100; i++ {
		if _, err := store.SaveContent("doc:f"); err != nil {
			t.Fatalf("SaveContent: %v", err)
		}
	}
	if _, err := store.Check(ctx, CheckRequest{
		Object:   "doc:f",
		Relation: "viewer",
		Subject:  "user:carol",
		Token:    future.Token(),
	}); err != nil {
		t.Fatalf("token became valid after clock advanced: %v", err)
	}
}

func TestLeaderRevocationImmediatelyEffective(t *testing.T) {
	store := NewStore(testSecret)
	ctx := context.Background()

	grant, err := store.WriteTuple(RelationTuple{
		Object:   "doc:r",
		Relation: "editor",
		Subject:  "user:dave",
		Present:  true,
	})
	if err != nil {
		t.Fatalf("grant: %v", err)
	}

	res, err := store.Check(ctx, CheckRequest{Object: "doc:r", Relation: "editor", Subject: "user:dave"})
	if err != nil || !res.Allowed {
		t.Fatalf("post-grant check: allowed=%v err=%v", res.Allowed, err)
	}

	revoke, err := store.WriteTuple(RelationTuple{
		Object:   "doc:r",
		Relation: "editor",
		Subject:  "user:dave",
		Present:  false,
	})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if revoke.Timestamp() <= grant.Timestamp() {
		t.Fatalf("revocation zookie not newer than grant")
	}

	// Any check at or after the revocation zookie sees the removal, including
	// an explicit request pinned to the revocation timestamp itself.
	res, err = store.Check(ctx, CheckRequest{
		Object:   "doc:r",
		Relation: "editor",
		Subject:  "user:dave",
		Token:    revoke.Token(),
	})
	if err != nil {
		t.Fatalf("post-revoke check: %v", err)
	}
	if res.Allowed {
		t.Fatalf("revoked relation still allowed at revocation zookie")
	}
	if res.ServedAt.Timestamp() < revoke.Timestamp() {
		t.Fatalf("served at %d before revocation %d", res.ServedAt.Timestamp(), revoke.Timestamp())
	}
}
