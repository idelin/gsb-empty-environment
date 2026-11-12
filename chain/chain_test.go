package chain_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"example.org/netsec/q0618/chain"
)

const testPayload = "signed-payload"

var baseTime = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

func testKey(i int) chain.Key {
	return chain.Key(fmt.Sprintf("key-%d", i))
}

// fakeOp is a stand-in for a decoded did:plc operation.
type fakeOp struct {
	prev         chain.CID
	rotationKeys []chain.Key
	signingKey   chain.Key
	sig          []byte
	payload      string
}

func (o fakeOp) Prev() chain.CID           { return o.prev }
func (o fakeOp) RotationKeys() []chain.Key { return o.rotationKeys }
func (o fakeOp) SignedBytes() []byte {
	if o.payload == "" {
		return []byte(testPayload)
	}
	return []byte(o.payload)
}
func (o fakeOp) Signature() []byte { return o.sig }

// fakeHasher derives a deterministic CID from prev and signing key, standing
// in for DAG-CBOR multihash hashing.
type fakeHasher struct{}

func (fakeHasher) Hash(op chain.Operation) (chain.CID, error) {
	return chain.CID(fmt.Sprintf("cid(%s,%s,%s)", op.Prev(), op.Signature(), op.SignedBytes())), nil
}

type errHasher struct{}

func (errHasher) Hash(chain.Operation) (chain.CID, error) {
	return "", errors.New("dag-cbor encoder exploded")
}

type errVerifier struct{}

var errVerifierSentinel = errors.New("unsupported key type")

func (errVerifier) Verify(chain.Operation, chain.Key) (bool, error) {
	return false, errVerifierSentinel
}

// fakeVerifier treats a non-empty signature as the key that produced it.
type fakeVerifier struct{}

func (fakeVerifier) Verify(op chain.Operation, key chain.Key) (bool, error) {
	return len(op.Signature()) > 0 && chain.Key(op.Signature()) == key, nil
}

type manualClock struct{ t time.Time }

func (c *manualClock) now() time.Time { return c.t }

func newValidator(clock *manualClock) *chain.Validator {
	return chain.NewValidator(fakeHasher{}, fakeVerifier{}, chain.WithClock(clock.now))
}

func genesisOp(keys ...chain.Key) fakeOp {
	return fakeOp{rotationKeys: keys, signingKey: keys[0], sig: []byte(keys[0])}
}

func childOp(prev chain.CID, keys []chain.Key, signingKey chain.Key) fakeOp {
	return fakeOp{prev: prev, rotationKeys: keys, signingKey: signingKey, sig: []byte(signingKey)}
}

func TestGenesisAcceptedAndHeadTracked(t *testing.T) {
	clock := &manualClock{t: baseTime}
	v := newValidator(clock)

	id, err := v.Submit(genesisOp(testKey(0), testKey(1)))
	if err != nil {
		t.Fatalf("genesis submit: %v", err)
	}
	if v.Head() != id {
		t.Fatalf("head = %q, want %q", v.Head(), id)
	}
	rec, ok := v.Lookup(id)
	if !ok || rec.Status != chain.StatusValid || rec.SigningKey != testKey(0) {
		t.Fatalf("unexpected genesis record: %+v ok=%v", rec, ok)
	}
	second := genesisOp(testKey(0))
	second.payload = "different-genesis"
	if _, err := v.Submit(second); !errors.Is(err, chain.ErrGenesisExists) {
		t.Fatalf("second genesis error = %v, want ErrGenesisExists", err)
	}
}

func TestRotationKeysRange(t *testing.T) {
	cases := []struct {
		name string
		n    int
		want error
	}{
		{"zero keys", 0, chain.ErrRotationKeysRange},
		{"six keys", 6, chain.ErrRotationKeysRange},
		{"one key", 1, nil},
		{"five keys", 5, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newValidator(&manualClock{t: baseTime})
			keys := make([]chain.Key, tc.n)
			for i := range keys {
				keys[i] = testKey(i)
			}
			var op fakeOp
			if tc.n > 0 {
				op = genesisOp(keys...)
			} else {
				op = fakeOp{rotationKeys: keys}
			}
			_, err := v.Submit(op)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestOrphanPrevRejected(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	keys := []chain.Key{testKey(0), testKey(1)}
	op := childOp("cid(missing)", keys, testKey(0))

	if _, err := v.Submit(op); !errors.Is(err, chain.ErrOrphanPrev) {
		t.Fatalf("error = %v, want ErrOrphanPrev", err)
	}
	if len(v.Audit()) != 0 {
		t.Fatalf("rejected op must not be audited, got %d records", len(v.Audit()))
	}
}

func TestNonGenesisWithoutPrevRejected(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	genesisID, err := v.Submit(genesisOp(testKey(0)))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	op := fakeOp{
		rotationKeys: []chain.Key{testKey(0)},
		signingKey:   testKey(0),
		sig:          []byte(testKey(0)),
		payload:      "second-empty-prev",
	}
	_ = genesisID
	if _, err := v.Submit(op); !errors.Is(err, chain.ErrGenesisExists) {
		t.Fatalf("empty prev after genesis error = %v, want ErrGenesisExists", err)
	}
}

func TestSignatureMustBePrevRotationKey(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	genesisID, err := v.Submit(genesisOp(testKey(0), testKey(1)))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}

	unknown := chain.Key("attacker-key")
	op := childOp(genesisID, []chain.Key{testKey(0), testKey(1)}, unknown)
	if _, err := v.Submit(op); !errors.Is(err, chain.ErrUnauthorizedKey) {
		t.Fatalf("error = %v, want ErrUnauthorizedKey", err)
	}
}

func TestLowPriorityForkRejected(t *testing.T) {
	clock := &manualClock{t: baseTime}
	v := newValidator(clock)
	keys := []chain.Key{testKey(0), testKey(1), testKey(2)}

	genesisID, err := v.Submit(genesisOp(keys...))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	firstID, err := v.Submit(childOp(genesisID, keys, testKey(1)))
	if err != nil {
		t.Fatalf("first child: %v", err)
	}

	// Same prev, signed by a strictly lower-priority key.
	if _, err := v.Submit(childOp(genesisID, keys, testKey(2))); !errors.Is(err, chain.ErrSamePriorityFork) {
		t.Fatalf("low-priority fork error = %v, want ErrSamePriorityFork", err)
	}
	if v.Head() != firstID {
		t.Fatalf("head changed to %q, want %q", v.Head(), firstID)
	}
}

func TestSamePriorityCompetingForkRejected(t *testing.T) {
	clock := &manualClock{t: baseTime}
	v := newValidator(clock)
	keys := []chain.Key{testKey(0), testKey(1)}

	genesisID, err := v.Submit(genesisOp(keys...))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	firstID, err := v.Submit(childOp(genesisID, keys, testKey(1)))
	if err != nil {
		t.Fatalf("first child: %v", err)
	}

	// A distinct operation signed by the same key: equal priority, no replace.
	competitor := childOp(genesisID, keys, testKey(1))
	competitor.payload = "same-key-competitor"
	if _, err := v.Submit(competitor); !errors.Is(err, chain.ErrSamePriorityFork) {
		t.Fatalf("same-priority fork error = %v, want ErrSamePriorityFork", err)
	}
	if v.Head() != firstID {
		t.Fatalf("head changed to %q, want %q", v.Head(), firstID)
	}
}

func TestHigherPriorityForkInvalidatesBranchWithinWindow(t *testing.T) {
	clock := &manualClock{t: baseTime}
	v := newValidator(clock)
	keys := []chain.Key{testKey(0), testKey(1), testKey(2)}

	genesisID, err := v.Submit(genesisOp(keys...))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	oldID, err := v.Submit(childOp(genesisID, keys, testKey(2)))
	if err != nil {
		t.Fatalf("old child: %v", err)
	}
	grandchildID, err := v.Submit(childOp(oldID, keys, testKey(0)))
	if err != nil {
		t.Fatalf("grandchild: %v", err)
	}

	clock.t = baseTime.Add(48 * time.Hour)
	forkID, err := v.Submit(childOp(genesisID, keys, testKey(0)))
	if err != nil {
		t.Fatalf("higher-priority fork within window: %v", err)
	}

	if v.Head() != forkID {
		t.Fatalf("head = %q, want fork %q", v.Head(), forkID)
	}
	for _, id := range []chain.CID{oldID, grandchildID} {
		rec, ok := v.Lookup(id)
		if !ok {
			t.Fatalf("record %q missing from audit log", id)
		}
		if rec.Status != chain.StatusInvalid || rec.SupersededBy != forkID {
			t.Fatalf("record %q = %+v, want invalid superseded by %q", id, rec, forkID)
		}
	}

	// Nothing may be appended to the invalidated branch.
	if _, err := v.Submit(childOp(grandchildID, keys, testKey(0))); !errors.Is(err, chain.ErrPrevInvalidated) {
		t.Fatalf("append to dead branch error = %v, want ErrPrevInvalidated", err)
	}

	audit := v.Audit()
	wantOrder := []chain.CID{genesisID, oldID, grandchildID, forkID}
	if len(audit) != len(wantOrder) {
		t.Fatalf("audit len = %d, want %d", len(audit), len(wantOrder))
	}
	for i, id := range wantOrder {
		if audit[i].CID != id {
			t.Fatalf("audit[%d] = %q, want %q (invalidated records retained)", i, audit[i].CID, id)
		}
	}
}

func TestRecoveryWindowBoundary(t *testing.T) {
	cases := []struct {
		name string
		age  time.Duration
		want error
	}{
		{"just inside window", chain.RecoveryWindow - time.Nanosecond, nil},
		{"exactly 72 hours", chain.RecoveryWindow, nil},
		{"just outside window", chain.RecoveryWindow + time.Nanosecond, chain.ErrRecoveryWindowClose},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &manualClock{t: baseTime}
			v := newValidator(clock)
			keys := []chain.Key{testKey(0), testKey(1)}

			genesisID, err := v.Submit(genesisOp(keys...))
			if err != nil {
				t.Fatalf("genesis: %v", err)
			}
			oldID, err := v.Submit(childOp(genesisID, keys, testKey(1)))
			if err != nil {
				t.Fatalf("old child: %v", err)
			}

			clock.t = baseTime.Add(tc.age)
			forkID, err := v.Submit(childOp(genesisID, keys, testKey(0)))
			if !errors.Is(err, tc.want) {
				t.Fatalf("fork error = %v, want %v", err, tc.want)
			}
			old, _ := v.Lookup(oldID)
			if tc.want == nil {
				if v.Head() != forkID || old.Status != chain.StatusInvalid {
					t.Fatalf("fork should be accepted: head=%q old=%+v", v.Head(), old)
				}
			} else if old.Status != chain.StatusValid || v.Head() != oldID {
				t.Fatalf("rejected fork must leave log unchanged: head=%q old=%+v", v.Head(), old)
			}
		})
	}
}

func TestDuplicateCIDRejected(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	keys := []chain.Key{testKey(0)}
	genesisID, err := v.Submit(genesisOp(keys...))
	if err != nil {
		t.Fatalf("genesis: %v", err)
	}
	// Identical fields hash to the same CID.
	dup := childOp(genesisID, keys, testKey(0))
	if _, err := v.Submit(dup); err != nil {
		t.Fatalf("first identical submit should be accepted: %v", err)
	}
	if _, err := v.Submit(dup); !errors.Is(err, chain.ErrDuplicateCID) {
		t.Fatalf("duplicate error = %v, want ErrDuplicateCID", err)
	}
}

func TestHasherErrorPropagates(t *testing.T) {
	v := chain.NewValidator(errHasher{}, fakeVerifier{})
	if _, err := v.Submit(genesisOp(testKey(0))); err == nil {
		t.Fatal("expected hasher error to propagate")
	}
}

func TestVerifierErrorPropagates(t *testing.T) {
	v := chain.NewValidator(fakeHasher{}, errVerifier{})
	if _, err := v.Submit(genesisOp(testKey(0))); err == nil || !errors.Is(err, errVerifierSentinel) {
		t.Fatalf("error = %v, want wrapped verifier failure", err)
	}
}

func TestNilOperationRejected(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	if _, err := v.Submit(nil); !errors.Is(err, chain.ErrNilOperation) {
		t.Fatalf("error = %v, want ErrNilOperation", err)
	}
}

func TestLookupUnknownCID(t *testing.T) {
	v := newValidator(&manualClock{t: baseTime})
	if _, ok := v.Lookup("cid(ghost)"); ok {
		t.Fatal("unknown CID must not be found")
	}
	if head := v.Head(); head != "" {
		t.Fatalf("empty log head = %q, want empty", head)
	}
}
