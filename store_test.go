package netsec_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	netsec "example.org/netsec/q0169"
)

func TestFirstUseAndCaseFoldAliases(t *testing.T) {
	var store netsec.Store
	first := netsec.Artifact{
		Scope:    "Acme-Corp",
		Name:     "Widget",
		Version:  "1.0.0",
		Checksum: "sha256:aa",
		Signer:   "alice",
	}

	record, err := store.Verify(first)
	if err != nil {
		t.Fatalf("first use: %v", err)
	}
	if record.Key != "acme-corp.widget@1.0.0" {
		t.Fatalf("canonical key = %q", record.Key)
	}
	if record.Checksum != first.Checksum || record.Signer != "alice" {
		t.Fatalf("first-use record = %+v", record)
	}

	alias := first
	alias.Scope = "ACME-CORP"
	alias.Name = "wIdGeT"
	record, err = store.Verify(alias)
	if err != nil {
		t.Fatalf("case alias: %v", err)
	}
	if record.Key != "acme-corp.widget@1.0.0" {
		t.Fatalf("alias key = %q", record.Key)
	}

	got, ok, err := store.Lookup("aCmE-cOrP.WIDGET@1.0.0")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !ok || got != record {
		t.Fatalf("lookup = %+v, %v", got, ok)
	}
	if store.Len() != 1 {
		t.Fatalf("Len = %d, want 1", store.Len())
	}
}

func TestSameVersionChecksumTamperRejected(t *testing.T) {
	store := netsec.NewStore()
	original := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa", Signer: "alice",
	}
	if _, err := store.Verify(original); err != nil {
		t.Fatalf("first use: %v", err)
	}

	for _, opts := range []netsec.Options{{}, {TrustSignerChange: true}} {
		tampered := original
		tampered.Checksum = "sha256:bb"
		tampered.Signer = "bob"
		if _, err := store.VerifyWithOptions(tampered, opts); !errors.Is(err, netsec.ErrChecksumMismatch) {
			t.Fatalf("options %+v: err = %v, want ErrChecksumMismatch", opts, err)
		}
	}

	got, ok, err := store.Lookup("acme.widget@1.0.0")
	if err != nil || !ok {
		t.Fatalf("lookup after tamper: %+v, %v, %v", got, ok, err)
	}
	if got.Checksum != original.Checksum || got.Signer != "alice" {
		t.Fatalf("record changed after rejected tamper: %+v", got)
	}
}

func TestSignerSwitchRequiresExplicitTrust(t *testing.T) {
	store := netsec.NewStore()
	base := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa", Signer: "alice",
	}
	if _, err := store.Verify(base); err != nil {
		t.Fatalf("first use: %v", err)
	}

	switched := base
	switched.Version = "2.0.0"
	switched.Checksum = "sha256:bb"
	switched.Signer = "bob"
	if _, err := store.Verify(switched); !errors.Is(err, netsec.ErrSignerChanged) {
		t.Fatalf("signer switch without trust: err = %v", err)
	}
	if _, ok, err := store.Lookup("acme.widget@2.0.0"); err != nil || ok {
		t.Fatalf("rejected version was recorded: ok = %v, err = %v", ok, err)
	}

	if _, err := store.VerifyTrustingSignerChange(switched); err != nil {
		t.Fatalf("explicitly trusted signer switch: %v", err)
	}

	next := switched
	next.Version = "3.0.0"
	next.Checksum = "sha256:cc"
	if _, err := store.Verify(next); err != nil {
		t.Fatalf("signer bob should now be trusted: %v", err)
	}

	revoked := next
	revoked.Version = "4.0.0"
	revoked.Checksum = "sha256:dd"
	revoked.Signer = "alice"
	if _, err := store.Verify(revoked); !errors.Is(err, netsec.ErrSignerChanged) {
		t.Fatalf("switching back without trust: err = %v", err)
	}
	if _, err := store.VerifyTrustingSignerChange(revoked); err != nil {
		t.Fatalf("explicitly trusted switch back: %v", err)
	}

	got, ok, err := store.Lookup("acme.widget@3.0.0")
	if err != nil || !ok || got.Signer != "bob" {
		t.Fatalf("version 3 record = %+v, %v, %v", got, ok, err)
	}
}

func TestSameVersionSignerSwitchRequiresTrust(t *testing.T) {
	store := netsec.NewStore()
	base := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa", Signer: "alice",
	}
	if _, err := store.Verify(base); err != nil {
		t.Fatalf("first use: %v", err)
	}

	switched := base
	switched.Signer = "bob"
	if _, err := store.Verify(switched); !errors.Is(err, netsec.ErrSignerChanged) {
		t.Fatalf("same-version signer switch: err = %v", err)
	}
	if _, err := store.VerifyTrustingSignerChange(switched); err != nil {
		t.Fatalf("trusted same-version signer switch: %v", err)
	}

	got, ok, err := store.Lookup("acme.widget@1.0.0")
	if err != nil || !ok || got.Signer != "bob" {
		t.Fatalf("rewritten record = %+v, %v, %v", got, ok, err)
	}
}

func TestSignedToUnsignedDowngradeAlwaysRejected(t *testing.T) {
	store := netsec.NewStore()
	signed := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa", Signer: "alice",
	}
	if _, err := store.Verify(signed); err != nil {
		t.Fatalf("first use: %v", err)
	}

	unsigned := signed
	unsigned.Signer = ""
	if _, err := store.Verify(unsigned); !errors.Is(err, netsec.ErrUnsignedDowngrade) {
		t.Fatalf("same-version downgrade: err = %v", err)
	}

	unsigned.Version = "2.0.0"
	unsigned.Checksum = "sha256:bb"
	for _, opts := range []netsec.Options{{}, {TrustSignerChange: true}} {
		if _, err := store.VerifyWithOptions(unsigned, opts); !errors.Is(err, netsec.ErrUnsignedDowngrade) {
			t.Fatalf("options %+v: err = %v, want ErrUnsignedDowngrade", opts, err)
		}
	}

	if _, ok, err := store.Lookup("acme.widget@2.0.0"); err != nil || ok {
		t.Fatalf("downgraded version was recorded: ok = %v, err = %v", ok, err)
	}
}

func TestUnsignedPackageCanGainSignatureButNotLoseIt(t *testing.T) {
	store := netsec.NewStore()
	unsigned := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa",
	}
	if _, err := store.Verify(unsigned); err != nil {
		t.Fatalf("first unsigned use: %v", err)
	}

	signed := unsigned
	signed.Version = "2.0.0"
	signed.Checksum = "sha256:bb"
	signed.Signer = "alice"
	if _, err := store.Verify(signed); !errors.Is(err, netsec.ErrSignerChanged) {
		t.Fatalf("unsigned to signed without trust: err = %v", err)
	}
	if _, err := store.VerifyTrustingSignerChange(signed); err != nil {
		t.Fatalf("explicitly trusted unsigned to signed upgrade: %v", err)
	}

	downgrade := signed
	downgrade.Version = "3.0.0"
	downgrade.Checksum = "sha256:cc"
	downgrade.Signer = ""
	if _, err := store.Verify(downgrade); !errors.Is(err, netsec.ErrUnsignedDowngrade) {
		t.Fatalf("downgrade after upgrade: err = %v", err)
	}
}

func TestKeyValidationAndCaseFolding(t *testing.T) {
	key, err := netsec.ParseKey("Scope-1.Name-2@1.2.3")
	if err != nil {
		t.Fatalf("ParseKey: %v", err)
	}
	if got, want := key.String(), "scope-1.name-2@1.2.3"; got != want {
		t.Fatalf("canonical key = %q, want %q", got, want)
	}

	badKeys := []string{
		"",
		"scope.name",
		"scope@1.0.0",
		".name@1.0.0",
		"scope.@1.0.0",
		"sc_ope.name@1.0.0",
		"sc.ope.name@1.0.0",
		"scöpe.name@1.0.0",
		"scope.na_me@1.0.0",
		"scope.name@",
		"scope.name@1 0",
	}
	for _, bad := range badKeys {
		if _, err := netsec.ParseKey(bad); !errors.Is(err, netsec.ErrInvalidKey) {
			t.Errorf("ParseKey(%q) err = %v, want ErrInvalidKey", bad, err)
		}
	}

	store := netsec.NewStore()
	for _, bad := range []netsec.Artifact{
		{Scope: "acme_", Name: "widget", Version: "1.0.0", Checksum: "sha256:aa"},
		{Scope: "acme", Name: "wid.get", Version: "1.0.0", Checksum: "sha256:aa"},
		{Scope: "acme", Name: "widget", Version: "", Checksum: "sha256:aa"},
	} {
		if _, err := store.Verify(bad); !errors.Is(err, netsec.ErrInvalidKey) {
			t.Errorf("Verify(%+v) err = %v, want ErrInvalidKey", bad, err)
		}
	}

	if _, err := store.VerifyKey("acme.widget@1.0.0", " ", "alice"); !errors.Is(err, netsec.ErrEmptyChecksum) {
		t.Fatalf("empty checksum err = %v", err)
	}
}

func TestRecordsSnapshotAndConcurrentUse(t *testing.T) {
	store := netsec.NewStore()
	const n = 32
	artifact := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:aa", Signer: "alice",
	}

	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := artifact
			if i%2 == 1 {
				a.Scope = "ACME"
				a.Name = "WIDGET"
			}
			if _, err := store.Verify(a); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent Verify: %v", err)
	}
	if store.Len() != 1 {
		t.Fatalf("concurrent Len = %d, want 1", store.Len())
	}

	records := store.Records()
	if len(records) != 1 {
		t.Fatalf("Records length = %d", len(records))
	}
	if records[0].Key != "acme.widget@1.0.0" {
		t.Fatalf("Records key = %q", records[0].Key)
	}
}

func TestVerifyKeyAddressing(t *testing.T) {
	store := netsec.NewStore()
	record, err := store.VerifyKey("Acme.Widget@1.0.0", "sha256:aa", "alice")
	if err != nil {
		t.Fatalf("VerifyKey: %v", err)
	}
	if record.Key != "acme.widget@1.0.0" {
		t.Fatalf("VerifyKey record = %+v", record)
	}

	_, err = store.VerifyKey("acme.widget@1.0.0", "sha256:bb", "alice")
	if !errors.Is(err, netsec.ErrChecksumMismatch) {
		t.Fatalf("VerifyKey tamper err = %v", err)
	}
}

func ExampleStore_Verify() {
	store := netsec.NewStore()
	artifact := netsec.Artifact{
		Scope: "acme", Name: "widget", Version: "1.0.0",
		Checksum: "sha256:0123", Signer: "alice@example.org",
	}
	_, _ = store.Verify(artifact)

	artifact.Version = "2.0.0"
	artifact.Checksum = "sha256:4567"
	artifact.Signer = "mallory@example.org"
	_, err := store.Verify(artifact)
	fmt.Println(errors.Is(err, netsec.ErrSignerChanged))

	// Output:
	// true
}
