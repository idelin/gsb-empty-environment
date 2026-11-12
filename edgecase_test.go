package netsec

import (
	"context"
	"errors"
	"testing"
)

func TestCrossRegionHopWithinAuthorizedAccount(t *testing.T) {
	store := NewMemoryStore()
	usKey, err := store.PutKey(PublicKey{
		Account: acctA,
		ID:      "us-key",
		Region:  regionUS,
		DER:     []byte("der/us-key"),
	})
	if err != nil {
		t.Fatal(err)
	}

	entry := ref(acctA, regionCN, aliasSign)
	target := ref(acctA, regionUS, "us-alias")
	mustBind(t, store, target, keyTarget(usKey.ID))
	mustBind(t, store, entry, aliasTarget(target))

	caller := NewCallerIdentity("builder-region", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	res, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if res.FinalRegion != regionUS || res.FinalFingerprint != usKey.Fingerprint {
		t.Fatalf("cross-region hop not followed correctly: %+v", res)
	}
}

func TestResolutionRejectsFingerprintMismatch(t *testing.T) {
	store := NewMemoryStore()
	pub := PublicKey{
		Account:     acctA,
		ID:          "key-mismatch",
		Region:      regionCN,
		DER:         []byte("actual-material"),
		Fingerprint: SHA256Fingerprint([]byte("recorded-something-else")),
	}
	store.mu.Lock()
	store.keys[storedKey{account: acctA, id: pub.ID}] = pub
	store.mu.Unlock()

	entry := ref(acctA, regionCN, aliasSign)
	mustBind(t, store, entry, keyTarget(pub.ID))

	caller := NewCallerIdentity("builder-fp", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := txn.Resolve(context.Background(), entry); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
	}
}

func TestCachedCycleErrorIsStableWithinTransaction(t *testing.T) {
	store := NewMemoryStore()
	entry := ref(acctA, regionCN, "loop")
	mustBind(t, store, entry, aliasTarget(entry))

	caller := NewCallerIdentity("builder-cache", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := txn.Resolve(context.Background(), entry); !errors.Is(err, ErrCycle) {
			t.Fatalf("attempt %d: err = %v, want ErrCycle", i, err)
		}
	}
}
