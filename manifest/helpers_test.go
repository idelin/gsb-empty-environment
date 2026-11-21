package manifest

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

const testModel = "device-alpha"

type keyPair struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newKeyPair(t *testing.T, id string) keyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key %s: %v", id, err)
	}
	return keyPair{id: id, priv: priv, pub: pub}
}

type testEnv struct {
	t     *testing.T
	roots map[string]keyPair
	host  *Host
	store *MemoryStore
	base  time.Time
}

func newTestEnv(t *testing.T, rootIDs []string, rootThreshold, minFirmwareThreshold uint64) *testEnv {
	t.Helper()
	roots := make(map[string]keyPair, len(rootIDs))
	var pinned []RootKey
	for _, id := range rootIDs {
		kp := newKeyPair(t, id)
		roots[id] = kp
		pinned = append(pinned, RootKey{ID: id, Public: kp.pub})
	}
	store := NewMemoryStore()
	base := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	host := &Host{
		Model:                testModel,
		Roots:                pinned,
		RootThreshold:        rootThreshold,
		MinFirmwareThreshold: minFirmwareThreshold,
		Store:                store,
		Now:                  func() time.Time { return base },
	}
	return &testEnv{t: t, roots: roots, host: host, store: store, base: base}
}

func (e *testEnv) signManifest(m Manifest, signerIDs []string) SignedManifest {
	e.t.Helper()
	encoded, err := EncodeManifest(m)
	if err != nil {
		e.t.Fatalf("encode manifest: %v", err)
	}
	var sigs []RootSignature
	for _, id := range signerIDs {
		signer := e.roots[id]
		sigs = append(sigs, RootSignature{RootID: id, Signature: ed25519.Sign(signer.priv, encoded)})
	}
	return SignedManifest{Manifest: m, Signatures: sigs}
}

func firmwareEntry(kp keyPair, start, end uint64, purposes ...string) FirmwareKey {
	if len(purposes) == 0 {
		purposes = []string{PurposeFirmware}
	}
	return FirmwareKey{
		KeyID:           kp.id,
		Public:          kp.pub,
		Purposes:        purposes,
		StartGeneration: start,
		EndGeneration:   end,
	}
}

func baseManifest(keys []FirmwareKey, gen, threshold uint64, revoked ...string) Manifest {
	return Manifest{
		DeviceModel:       testModel,
		Generation:        gen,
		NotBefore:         time.Date(2026, 11, 22, 11, 0, 0, 0, time.UTC),
		NotAfter:          time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC),
		FirmwareThreshold: threshold,
		Keys:              keys,
		RevokedKeyIDs:     revoked,
	}
}

func makeKeys(t *testing.T, ids ...string) []keyPair {
	t.Helper()
	var out []keyPair
	for _, id := range ids {
		out = append(out, newKeyPair(t, id))
	}
	return out
}

func entries(keys []keyPair, start, end uint64) []FirmwareKey {
	var out []FirmwareKey
	for _, kp := range keys {
		out = append(out, firmwareEntry(kp, start, end))
	}
	return out
}

func mustEncode(t *testing.T, m Manifest) []byte {
	t.Helper()
	raw, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return raw
}

func mustLoad(t *testing.T, s Store) *State {
	t.Helper()
	st, err := s.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return st
}
