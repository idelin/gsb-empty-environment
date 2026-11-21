package manifest_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"prescreen.local/ps-d4797ec147/manifest"
)

func mustRootKey() ed25519.PrivateKey {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	return priv
}

func ExampleHost() {
	root1, root2 := mustRootKey(), mustRootKey()
	_, fwPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}

	store := manifest.NewMemoryStore()
	host := &manifest.Host{
		Model: "device-alpha",
		Roots: []manifest.RootKey{
			{ID: "root-1", Public: root1.Public().(ed25519.PublicKey)},
			{ID: "root-2", Public: root2.Public().(ed25519.PublicKey)},
		},
		RootThreshold:        2,
		MinFirmwareThreshold: 1,
		Store:                store,
		Now:                  func() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) },
	}

	m := manifest.Manifest{
		DeviceModel:       "device-alpha",
		Generation:        1,
		NotBefore:         time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC),
		NotAfter:          time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC),
		FirmwareThreshold: 1,
		Keys: []manifest.FirmwareKey{{
			KeyID:           "fw-1",
			Public:          fwPriv.Public().(ed25519.PublicKey),
			Purposes:        []string{manifest.PurposeFirmware},
			StartGeneration: 1,
			EndGeneration:   10,
		}},
	}
	sig1, _ := manifest.SignManifest(root1, m)
	sig2, _ := manifest.SignManifest(root2, m)

	if _, err := host.Admit(manifest.SignedManifest{
		Manifest: m,
		Signatures: []manifest.RootSignature{
			{RootID: "root-1", Signature: sig1},
			{RootID: "root-2", Signature: sig2},
		},
	}); err != nil {
		panic(err)
	}

	image := []byte("firmware image bytes")
	bundle := manifest.FirmwareBundle{
		DeviceModel:        "device-alpha",
		Image:              image,
		ImageDigest:        manifest.ImageDigest(image),
		Version:            "1.0.0",
		ManifestGeneration: 1,
		Signatures: []manifest.FirmwareSignature{{
			KeyID: "fw-1",
			Signature: manifest.SignFirmware(
				fwPriv, "device-alpha", manifest.ImageDigest(image), "1.0.0", 1),
		}},
	}

	if err := host.AdmitFirmware(bundle); err != nil {
		panic(err)
	}
	fmt.Println("firmware admitted")

	// A replayed older generation is rejected.
	old := m
	old.Generation = 0
	old.Keys[0].StartGeneration = 0
	oldSig1, _ := manifest.SignManifest(root1, old)
	oldSig2, _ := manifest.SignManifest(root2, old)
	_, err = host.Admit(manifest.SignedManifest{
		Manifest: old,
		Signatures: []manifest.RootSignature{
			{RootID: "root-1", Signature: oldSig1},
			{RootID: "root-2", Signature: oldSig2},
		},
	})
	fmt.Println(errors.Is(err, manifest.ErrStaleGeneration))
	// Output:
	// firmware admitted
	// true
}
