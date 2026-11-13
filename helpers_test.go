package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"fmt"
	"time"
)

var testNow = time.Date(2026, time.November, 14, 0, 0, 0, 0, time.UTC)

type testFixture struct {
	roots       []Root
	rootKeys    []ed25519.PrivateKey
	firmware    map[string]ed25519.PrivateKey
	firmwareIDs []string
}

func testPrivateKey(seedByte byte) ed25519.PrivateKey {
	seed := bytes.Repeat([]byte{seedByte}, ed25519.SeedSize)
	return ed25519.NewKeyFromSeed(seed)
}

func testPublicKey(privateKey ed25519.PrivateKey) ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), privateKey.Public().(ed25519.PublicKey)...)
}

func newTestFixture() *testFixture {
	root1 := testPrivateKey(0x11)
	root2 := testPrivateKey(0x22)
	fw1 := testPrivateKey(0xA1)
	fw2 := testPrivateKey(0xA2)
	fw3 := testPrivateKey(0xA3)
	return &testFixture{
		roots: []Root{
			{ID: "root-1", PublicKey: testPublicKey(root1)},
			{ID: "root-2", PublicKey: testPublicKey(root2)},
		},
		rootKeys: []ed25519.PrivateKey{root1, root2},
		firmware: map[string]ed25519.PrivateKey{
			"fw-1": fw1,
			"fw-2": fw2,
			"fw-3": fw3,
		},
		firmwareIDs: []string{"fw-1", "fw-2", "fw-3"},
	}
}

func (f *testFixture) manifest(generation uint64) Manifest {
	return Manifest{
		Model:             "router-x",
		Generation:        generation,
		ValidFrom:         testNow.Add(-time.Hour),
		ValidUntil:        testNow.Add(time.Hour),
		FirmwareThreshold: 2,
		Keys: []FirmwareKey{
			{
				KeyID:           "fw-1",
				PublicKey:       testPublicKey(f.firmware["fw-1"]),
				Usages:          []string{UsageFirmware},
				StartGeneration: 1,
			},
			{
				KeyID:           "fw-2",
				PublicKey:       testPublicKey(f.firmware["fw-2"]),
				Usages:          []string{UsageFirmware},
				StartGeneration: 1,
			},
			{
				KeyID:           "fw-3",
				PublicKey:       testPublicKey(f.firmware["fw-3"]),
				Usages:          []string{UsageFirmware},
				StartGeneration: 1,
			},
		},
	}
}

func (f *testFixture) signRoots(manifest *Manifest, rootIndexes ...int) {
	if len(rootIndexes) == 0 {
		rootIndexes = []int{0, 1}
	}
	for _, index := range rootIndexes {
		root := f.roots[index]
		if err := manifest.SignRoot(root.ID, f.rootKeys[index]); err != nil {
			panic(err)
		}
	}
}

func (f *testFixture) newAttestation(manifest Manifest, version string) FirmwareAttestation {
	image := sha256.Sum256([]byte(fmt.Sprintf("image:%s:%s:%d", manifest.Model, version, manifest.Generation)))
	return FirmwareAttestation{
		Model:              manifest.Model,
		ImageDigest:        image,
		Version:            version,
		ManifestGeneration: manifest.Generation,
	}
}

func (f *testFixture) attestation(manifest Manifest, version string, keyIDs ...string) FirmwareAttestation {
	attestation := f.newAttestation(manifest, version)
	for _, keyID := range keyIDs {
		privateKey, ok := f.firmware[keyID]
		if !ok {
			panic("unknown test firmware key " + keyID)
		}
		if err := attestation.Sign(keyID, privateKey); err != nil {
			panic(err)
		}
	}
	return attestation
}

func (f *testFixture) host(store TrustedStore) *Host {
	host, err := NewHost(HostConfig{
		Model:                    "router-x",
		Roots:                    f.roots,
		RootThreshold:            2,
		MinimumFirmwareThreshold: 2,
		Store:                    store,
		Now:                      func() time.Time { return testNow },
	})
	if err != nil {
		panic(err)
	}
	return host
}
