package netsec

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func resolveForSigning(t *testing.T) (CallerIdentity, Resolution, PublicKey) {
	t.Helper()
	store := NewMemoryStore()
	pub, _ := testKey(t, store, acctA, "key-a")

	entry := ref(acctA, regionCN, aliasSign)
	mid := ref(acctA, regionCN, "rotated")
	mustBind(t, store, mid, keyTarget(pub.ID))
	mustBind(t, store, entry, aliasTarget(mid))

	caller := NewCallerIdentity("builder-sign", []AccountID{acctA})
	txn, err := NewResolver(store).Begin(context.Background(), caller)
	if err != nil {
		t.Fatal(err)
	}
	res, err := txn.Resolve(context.Background(), entry)
	if err != nil {
		t.Fatal(err)
	}
	return caller, res, pub
}

func TestBuildSignRequestCarriesFullyQualifiedIdentity(t *testing.T) {
	caller, res, pub := resolveForSigning(t)

	issued := time.Date(2026, 11, 12, 6, 0, 0, 0, time.UTC)
	req, err := BuildSignRequest(caller, res,
		WithRequestID("req-1"),
		WithPayload([]byte("build-artifact-7")),
		WithIssuedAt(issued),
		WithKeyDER(pub.DER),
	)
	if err != nil {
		t.Fatalf("BuildSignRequest: %v", err)
	}

	if req.CallerID != caller.ID || req.Entry != res.Entry {
		t.Fatalf("request not bound to caller/entry: %+v", req)
	}
	if req.FinalFingerprint != res.FinalFingerprint ||
		req.FinalAccount != acctA || req.FinalKey != pub.ID {
		t.Fatalf("request terminus does not match resolution: %+v", req)
	}
	if req.EntryVersion != 1 || len(req.Hops) != 2 {
		t.Fatalf("request lost pinned version/hops: %+v", req)
	}
	if err := req.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	digest1, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	digest2, err := req.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest1 != digest2 {
		t.Fatal("canonical digest must be deterministic")
	}

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var decoded SignRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	decodedDigest, err := decoded.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if decodedDigest != digest1 {
		t.Fatal("digest changed across JSON roundtrip; encoding not canonical")
	}
	if err := decoded.Verify(); err != nil {
		t.Fatalf("decoded request failed Verify: %v", err)
	}
}

func TestSignRequestDetectsFingerprintSwap(t *testing.T) {
	caller, res, pub := resolveForSigning(t)
	req, err := BuildSignRequest(caller, res, WithKeyDER(pub.DER))
	if err != nil {
		t.Fatal(err)
	}

	tampered := req
	tampered.FinalFingerprint = SHA256Fingerprint([]byte("attacker-key"))
	if err := tampered.Verify(); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("fingerprint swap: err = %v, want ErrFingerprintMismatch", err)
	}

	tamperedDER := req
	tamperedDER.KeyDER = append([]byte(nil), pub.DER...)
	tamperedDER.KeyDER[0] ^= 0xFF
	if err := tamperedDER.Verify(); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("DER tamper: err = %v, want ErrFingerprintMismatch", err)
	}
}

func TestSignRequestDetectsHopAndEntryTampering(t *testing.T) {
	caller, res, _ := resolveForSigning(t)
	req, err := BuildSignRequest(caller, res)
	if err != nil {
		t.Fatal(err)
	}

	versionTampered := req
	versionTampered.EntryVersion = 99
	if err := versionTampered.Verify(); !errors.Is(err, ErrInvalidSignRequest) {
		t.Fatalf("version tamper: err = %v, want ErrInvalidSignRequest", err)
	}

	keyTampered := req
	keyTampered.FinalKey = "key-evil"
	if err := keyTampered.Verify(); !errors.Is(err, ErrInvalidSignRequest) {
		t.Fatalf("final key tamper: err = %v, want ErrInvalidSignRequest", err)
	}

	cyclic := req
	cyclic.Hops = append(append([]Hop(nil), req.Hops...), req.Hops[0])
	if err := cyclic.Verify(); !errors.Is(err, ErrCycle) {
		t.Fatalf("cyclic hops: err = %v, want ErrCycle", err)
	}

	emptyEntry := req
	emptyEntry.Entry.Name = ""
	if err := emptyEntry.Verify(); !errors.Is(err, ErrInvalidSignRequest) {
		t.Fatalf("empty entry: err = %v, want ErrInvalidSignRequest", err)
	}
}

func TestBuildSignRequestRejectsUnresolvedInput(t *testing.T) {
	caller := NewCallerIdentity("builder", []AccountID{acctA})
	if _, err := BuildSignRequest(caller, Resolution{}); !errors.Is(err, ErrInvalidSignRequest) {
		t.Fatalf("err = %v, want ErrInvalidSignRequest", err)
	}
}

func TestPutKeyRejectsInconsistentFingerprint(t *testing.T) {
	store := NewMemoryStore()
	pub := PublicKey{
		Account:     acctA,
		ID:          "bad",
		Region:      regionCN,
		DER:         []byte("real-material"),
		Fingerprint: SHA256Fingerprint([]byte("different-material")),
	}
	if _, err := store.PutKey(pub); !errors.Is(err, ErrFingerprintMismatch) {
		t.Fatalf("err = %v, want ErrFingerprintMismatch", err)
	}
}

func TestMemoryStoreRebindVersions(t *testing.T) {
	store := NewMemoryStore()
	entry := ref(acctA, regionCN, aliasSign)
	first := mustBind(t, store, entry, keyTarget("k1"))
	second := mustBind(t, store, entry, keyTarget("k2"))
	if first.Version != 1 || second.Version != 2 {
		t.Fatalf("versions = %d,%d, want 1,2", first.Version, second.Version)
	}
	if _, err := store.PutBinding(Binding{Ref: entry, Version: 1, Target: keyTarget("k3")}); !errors.Is(err, ErrStaleVersion) {
		t.Fatalf("stale write not rejected")
	}
	current, ok := store.LookupAlias(entry)
	if !ok || current.Target.Key != "k2" {
		t.Fatalf("stale write mutated store: %+v", current)
	}
}
