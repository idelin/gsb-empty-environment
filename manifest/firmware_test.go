package manifest

import (
	"errors"
	"testing"
)

func signedBundle(t *testing.T, env *testEnv, gen uint64, signers []keyPair) FirmwareBundle {
	t.Helper()
	image := []byte("firmware-image-v1")
	digest := ImageDigest(image)
	bundle := FirmwareBundle{
		DeviceModel:        testModel,
		Image:              image,
		ImageDigest:        digest,
		Version:            "1.2.3",
		ManifestGeneration: gen,
	}
	for _, kp := range signers {
		bundle.Signatures = append(bundle.Signatures, FirmwareSignature{
			KeyID:     kp.id,
			Signature: SignFirmware(kp.priv, testModel, digest, bundle.Version, gen),
		})
	}
	return bundle
}

func TestFirmwareAdmitHappyPath(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2", "k3")
	m := baseManifest(entries(keys, 1, 10), 1, 2)
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	bundle := signedBundle(t, env, 1, []keyPair{keys[0], keys[1]})
	if err := env.host.AdmitFirmware(bundle); err != nil {
		t.Fatalf("firmware should boot: %v", err)
	}

	// One signature is below t=2.
	if err := env.host.AdmitFirmware(signedBundle(t, env, 1, []keyPair{keys[0]})); !errors.Is(err, ErrFirmwareThresholdNotMet) {
		t.Fatalf("expected ErrFirmwareThresholdNotMet, got %v", err)
	}
}

func TestFirmwareAliasKeyIDsCountOnce(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	k1 := newKeyPair(t, "k1")
	k2 := newKeyPair(t, "k2")

	// Two keyIDs sharing k1's public material, plus an independent k2.
	alias := FirmwareKey{
		KeyID: "alias-of-k1", Public: k1.pub, Purposes: []string{PurposeFirmware},
		StartGeneration: 1, EndGeneration: 10,
	}
	m := baseManifest([]FirmwareKey{firmwareEntry(k1, 1, 10), alias, firmwareEntry(k2, 1, 10)}, 1, 2)
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	image := []byte("firmware-image-alias")
	digest := ImageDigest(image)
	bundle := FirmwareBundle{
		DeviceModel: testModel, Image: image, ImageDigest: digest,
		Version: "9", ManifestGeneration: 1,
	}
	bundle.Signatures = append(bundle.Signatures,
		FirmwareSignature{KeyID: "k1", Signature: SignFirmware(k1.priv, testModel, digest, "9", 1)},
		FirmwareSignature{KeyID: "alias-of-k1", Signature: SignFirmware(k1.priv, testModel, digest, "9", 1)},
	)
	// Same key material twice: only one distinct vote, threshold not met.
	if err := env.host.AdmitFirmware(bundle); !errors.Is(err, ErrFirmwareThresholdNotMet) {
		t.Fatalf("expected ErrFirmwareThresholdNotMet for alias-only votes, got %v", err)
	}

	bundle.Signatures = append(bundle.Signatures,
		FirmwareSignature{KeyID: "k2", Signature: SignFirmware(k2.priv, testModel, digest, "9", 1)})
	if err := env.host.AdmitFirmware(bundle); err != nil {
		t.Fatalf("firmware with independent second key should boot: %v", err)
	}
}

func TestFirmwareRevokedPurposeAndWindowExclusions(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	active := newKeyPair(t, "active")
	revoked := newKeyPair(t, "revoked")
	notFirmware := newKeyPair(t, "metadata-only")
	outOfWindow := newKeyPair(t, "future-window")

	m := baseManifest([]FirmwareKey{
		firmwareEntry(active, 1, 10),
		firmwareEntry(revoked, 1, 10),
		firmwareEntry(notFirmware, 1, 10, "metadata"),
		firmwareEntry(outOfWindow, 10, 20),
	}, 1, 2, "revoked")
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	cases := []struct {
		name string
		key  keyPair
	}{
		{"revoked key cannot vote", revoked},
		{"non-firmware purpose cannot vote", notFirmware},
		{"key outside generation window cannot vote", outOfWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bundle := signedBundle(t, env, 1, []keyPair{active, tc.key})
			err := env.host.AdmitFirmware(bundle)
			if !errors.Is(err, ErrFirmwareThresholdNotMet) {
				t.Fatalf("expected ErrFirmwareThresholdNotMet, got %v", err)
			}
		})
	}

	// Unknown keyID signatures never count either.
	unknown := newKeyPair(t, "unknown")
	bundle := signedBundle(t, env, 1, []keyPair{active, unknown})
	if err := env.host.AdmitFirmware(bundle); !errors.Is(err, ErrFirmwareThresholdNotMet) {
		t.Fatalf("expected ErrFirmwareThresholdNotMet with unknown keyID, got %v", err)
	}
}

func TestFirmwareGenerationBindingAndBadSignature(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")
	m := baseManifest(entries(keys, 1, 10), 1, 2)
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	// Firmware signed for a different manifest generation is rejected.
	stale := signedBundle(t, env, 99, keys)
	if err := env.host.AdmitFirmware(stale); !errors.Is(err, ErrFirmwareGenerationMismatch) {
		t.Fatalf("expected ErrFirmwareGenerationMismatch, got %v", err)
	}

	// A corrupted signature is ignored rather than counted.
	bad := signedBundle(t, env, 1, keys)
	bad.Signatures[0].Signature[0] ^= 0xFF
	if err := env.host.AdmitFirmware(bad); !errors.Is(err, ErrFirmwareThresholdNotMet) {
		t.Fatalf("expected ErrFirmwareThresholdNotMet after corruption, got %v", err)
	}

	// Conflicting signatures under one keyID disqualify that keyID entirely.
	conflict := signedBundle(t, env, 1, keys)
	dup := conflict.Signatures[0]
	dup.Signature = append([]byte(nil), dup.Signature...)
	dup.Signature[0] ^= 0x01
	conflict.Signatures = append(conflict.Signatures, dup)
	if err := env.host.AdmitFirmware(conflict); !errors.Is(err, ErrFirmwareThresholdNotMet) {
		t.Fatalf("expected ErrFirmwareThresholdNotMet with conflicting keyID, got %v", err)
	}

	// Wrong model is rejected.
	wrong := signedBundle(t, env, 1, keys)
	wrong.DeviceModel = "other-device"
	if err := env.host.AdmitFirmware(wrong); !errors.Is(err, ErrModelMismatch) {
		t.Fatalf("expected ErrModelMismatch, got %v", err)
	}
}

func TestFirmwareRequiresTrustedManifest(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")
	bundle := signedBundle(t, env, 1, keys)
	if err := env.host.AdmitFirmware(bundle); !errors.Is(err, ErrNoTrustedManifest) {
		t.Fatalf("expected ErrNoTrustedManifest, got %v", err)
	}
}

func TestFirmwareDigestTamperingRejected(t *testing.T) {
	env := newTestEnv(t, []string{"r1", "r2"}, 2, 1)
	keys := makeKeys(t, "k1", "k2")
	m := baseManifest(entries(keys, 1, 10), 1, 2)
	if _, err := env.host.Admit(env.signManifest(m, []string{"r1", "r2"})); err != nil {
		t.Fatalf("admit: %v", err)
	}

	bundle := signedBundle(t, env, 1, keys)
	bundle.Image = append(bundle.Image, 'x')
	if err := env.host.AdmitFirmware(bundle); !errors.Is(err, ErrInvalidFirmwareBundle) {
		t.Fatalf("expected ErrInvalidFirmwareBundle on digest mismatch, got %v", err)
	}
}
