package dbx

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

type testChain struct {
	root         *x509.Certificate
	intermediate *x509.Certificate
	leaf         *x509.Certificate
}

func makeCert(t *testing.T, tmpl *x509.Certificate, parent *x509.Certificate, parentKey ed25519.PrivateKey) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if parent == nil {
		parent = tmpl
		parentKey = priv
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, priv
}

func newTestChain(t *testing.T) testChain {
	t.Helper()
	now := time.Now()
	base := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		BasicConstraintsValid: true,
	}
	rootTmpl := *base
	rootTmpl.Subject = pkix.Name{CommonName: "test root CA"}
	rootTmpl.IsCA = true
	rootTmpl.KeyUsage = x509.KeyUsageCertSign
	root, rootKey := makeCert(t, &rootTmpl, nil, nil)

	interTmpl := *base
	interTmpl.SerialNumber = big.NewInt(2)
	interTmpl.Subject = pkix.Name{CommonName: "test intermediate CA"}
	interTmpl.IsCA = true
	interTmpl.KeyUsage = x509.KeyUsageCertSign
	intermediate, interKey := makeCert(t, &interTmpl, root, rootKey)

	leafTmpl := *base
	leafTmpl.SerialNumber = big.NewInt(3)
	leafTmpl.Subject = pkix.Name{CommonName: "bootloader signer"}
	leafTmpl.KeyUsage = x509.KeyUsageDigitalSignature
	leaf, _ := makeCert(t, &leafTmpl, intermediate, interKey)

	return testChain{root: root, intermediate: intermediate, leaf: leaf}
}

// dbAllows models the loader's db check: the signer's certificate is in the
// allowed-signature database.
func dbAllows(dbAllowed map[[sha256.Size]byte]bool, chain []*x509.Certificate) bool {
	for _, c := range chain {
		if dbAllowed[CertTBSHash(c)] {
			return true
		}
	}
	return false
}

func TestIntermediateCertRevocation(t *testing.T) {
	chain := newTestChain(t)
	imageHash := sha256.Sum256([]byte("bootloader image"))

	// db trusts the leaf signer; dbx revokes only the intermediate.
	dbAllowed := map[[sha256.Size]byte]bool{CertTBSHash(chain.leaf): true}
	d, err := New(1, []Entry{{Kind: EntryCertTBSHash, Hash: CertTBSHash(chain.intermediate)}})
	if err != nil {
		t.Fatal(err)
	}

	fullChain := []*x509.Certificate{chain.leaf, chain.intermediate, chain.root}
	if !dbAllows(dbAllowed, fullChain) {
		t.Fatal("precondition: db should allow the signer")
	}
	if d.ImageRevoked(imageHash) {
		t.Fatal("precondition: image hash must not be revoked")
	}
	if !d.IsRevoked(imageHash, fullChain) {
		t.Fatal("intermediate certificate revoked in dbx: image must be rejected even though db allows it")
	}
}

func TestImageHashRevocation(t *testing.T) {
	chain := newTestChain(t)
	imageHash := sha256.Sum256([]byte("known-bad bootloader"))

	dbAllowed := map[[sha256.Size]byte]bool{CertTBSHash(chain.leaf): true}
	d, err := New(1, []Entry{{Kind: EntryImageHash, Hash: imageHash}})
	if err != nil {
		t.Fatal(err)
	}

	fullChain := []*x509.Certificate{chain.leaf, chain.intermediate, chain.root}
	if !dbAllows(dbAllowed, fullChain) {
		t.Fatal("precondition: db should allow the signer")
	}
	if d.ChainRevoked(fullChain) {
		t.Fatal("precondition: no certificate in the chain is revoked")
	}
	if !d.IsRevoked(imageHash, fullChain) {
		t.Fatal("image hash revoked in dbx: image must be rejected even though db allows it")
	}
}

func TestCleanImageAccepted(t *testing.T) {
	chain := newTestChain(t)
	imageHash := sha256.Sum256([]byte("good bootloader"))
	d, err := New(1, []Entry{
		{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("some other image"))},
		{Kind: EntryCertTBSHash, Hash: sha256.Sum256([]byte("some other cert"))},
	})
	if err != nil {
		t.Fatal(err)
	}
	if d.IsRevoked(imageHash, []*x509.Certificate{chain.leaf, chain.intermediate, chain.root}) {
		t.Fatal("clean image with clean chain must not be revoked")
	}
}

func newUpdateKeys(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func TestReplayOldDBXRejected(t *testing.T) {
	pub, priv := newUpdateKeys(t)
	store := NewMemStore(100, []Entry{{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("revoked-v1"))}})
	u := NewUpdater(store, pub)

	// Attacker replays an older dbx (timestamp 50) that no longer revokes
	// the bootloader, trying to resurrect it.
	oldEntries := []Entry{{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("ancient"))}}
	sig := Sign(priv, 50, oldEntries)
	if err := u.Apply(50, oldEntries, sig); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("replay of old dbx: got %v, want ErrStaleTimestamp", err)
	}

	ts, entries, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ts != 100 || len(entries) != 1 {
		t.Fatalf("stored dbx changed after rejected replay: ts=%d entries=%d", ts, len(entries))
	}
}

func TestSameTimestampRejected(t *testing.T) {
	pub, priv := newUpdateKeys(t)
	store := NewMemStore(100, nil)
	u := NewUpdater(store, pub)

	entries := []Entry{{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("new revocation"))}}
	sig := Sign(priv, 100, entries)
	if err := u.Apply(100, entries, sig); !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("equal timestamp: got %v, want ErrStaleTimestamp (must be strictly greater)", err)
	}

	// A strictly newer timestamp must succeed.
	sig = Sign(priv, 101, entries)
	if err := u.Apply(101, entries, sig); err != nil {
		t.Fatalf("strictly newer timestamp must be accepted: %v", err)
	}
	ts, got, _ := store.Load()
	if ts != 101 || len(got) != 1 {
		t.Fatalf("update not committed: ts=%d entries=%d", ts, len(got))
	}
}

func TestForgedUpdateRejected(t *testing.T) {
	pub, _ := newUpdateKeys(t)
	_, attackerPriv := newUpdateKeys(t)
	store := NewMemStore(100, nil)
	u := NewUpdater(store, pub)

	entries := []Entry{{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("x"))}}
	sig := Sign(attackerPriv, 200, entries)
	if err := u.Apply(200, entries, sig); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("forged update: got %v, want ErrBadSignature", err)
	}
	if ts, _, _ := store.Load(); ts != 100 {
		t.Fatalf("stored dbx changed after forged update: ts=%d", ts)
	}
}

// crashStore simulates a power loss during the variable write: Replace
// always fails before anything is committed.
type crashStore struct {
	inner *MemStore
	err   error
}

func (c *crashStore) Load() (uint64, []Entry, error) { return c.inner.Load() }

func (c *crashStore) Replace(uint64, []Entry) error { return c.err }

func TestHalfWriteLeavesPreviousDBXIntact(t *testing.T) {
	pub, priv := newUpdateKeys(t)
	revokedHash := sha256.Sum256([]byte("revoked bootloader"))
	original := []Entry{{Kind: EntryImageHash, Hash: revokedHash}}
	inner := NewMemStore(100, original)
	store := &crashStore{inner: inner, err: errors.New("simulated flash write failure")}
	u := NewUpdater(store, pub)

	newEntries := []Entry{
		{Kind: EntryImageHash, Hash: sha256.Sum256([]byte("additional revocation"))},
		{Kind: EntryCertTBSHash, Hash: sha256.Sum256([]byte("additional cert"))},
	}
	sig := Sign(priv, 200, newEntries)
	if err := u.Apply(200, newEntries, sig); err == nil {
		t.Fatal("commit failure must surface as an error")
	}

	// The failed write must not partially take effect: timestamp and entry
	// set are exactly the pre-update dbx.
	ts, entries, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if ts != 100 {
		t.Fatalf("timestamp advanced despite failed commit: %d", ts)
	}
	if len(entries) != 1 || entries[0] != original[0] {
		t.Fatalf("entries partially written: %+v", entries)
	}

	// Revocation decisions still follow the old dbx.
	d, err := New(ts, entries)
	if err != nil {
		t.Fatal(err)
	}
	if !d.ImageRevoked(revokedHash) {
		t.Fatal("previously revoked image must stay revoked after failed update")
	}
	if d.ImageRevoked(sha256.Sum256([]byte("additional revocation"))) {
		t.Fatal("entries from the failed update must not be visible")
	}
}

func TestInvalidEntryRejectedBeforeCommit(t *testing.T) {
	pub, priv := newUpdateKeys(t)
	store := NewMemStore(100, nil)
	u := NewUpdater(store, pub)

	entries := []Entry{{Kind: EntryKind(99), Hash: sha256.Sum256([]byte("bogus"))}}
	sig := Sign(priv, 200, entries)
	if err := u.Apply(200, entries, sig); !errors.Is(err, ErrInvalidEntry) {
		t.Fatalf("invalid entry: got %v, want ErrInvalidEntry", err)
	}
	if ts, got, _ := store.Load(); ts != 100 || len(got) != 0 {
		t.Fatalf("store touched by invalid update: ts=%d entries=%d", ts, len(got))
	}
}
