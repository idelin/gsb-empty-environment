package targetverify

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

const (
	testSerial = "ECU-0001"
	testHW     = "HW-A"
	testTarget = "firmware-v2.bin"
)

var (
	testNow   = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	testLater = testNow.Add(24 * time.Hour)
)

type testKey struct {
	id   string
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

func newTestKey(t *testing.T, id string) testKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key %q: %v", id, err)
	}
	return testKey{id: id, priv: priv, pub: pub}
}

func (k testKey) ringKey(t *testing.T) *Ed25519Key {
	t.Helper()
	key, err := NewEd25519Key(k.id, k.pub)
	if err != nil {
		t.Fatalf("build ring key: %v", err)
	}
	return key
}

func signEnvelope(t *testing.T, signed SignedMeta, signers ...testKey) []byte {
	t.Helper()
	raw, err := json.Marshal(signed)
	if err != nil {
		t.Fatalf("marshal signed payload: %v", err)
	}
	signatures := make([]Signature, 0, len(signers))
	for _, signer := range signers {
		signatures = append(signatures, Signature{
			KeyID: signer.id,
			Sig:   ed25519.Sign(signer.priv, raw),
		})
	}
	envelope := struct {
		Signed     json.RawMessage `json:"signed"`
		Signatures []Signature     `json:"signatures"`
	}{
		Signed:     raw,
		Signatures: signatures,
	}
	out, err := json.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return out
}

func testFile() ([]byte, TargetFile) {
	content := []byte("firmware-image-v2-bytes")
	sum := sha256.Sum256(content)
	return content, TargetFile{
		Length: int64(len(content)),
		Hashes: Hashes{SHA256: sum[:]},
	}
}

func baseSigned(version int, targets map[string]TargetMeta) SignedMeta {
	return SignedMeta{
		Type:    metadataTypeTargets,
		Version: version,
		Expires: testLater,
		Targets: targets,
	}
}

func (s SignedMeta) withExpiry(expiry time.Time) SignedMeta {
	s.Expires = expiry
	return s
}

func directorTargets(file TargetFile) map[string]TargetMeta {
	return map[string]TargetMeta{
		testTarget: {
			File:   file,
			Custom: Custom{ECUIdentifier: testSerial, HardwareIdentifier: testHW},
		},
	}
}

func imageTargets(file TargetFile) map[string]TargetMeta {
	return map[string]TargetMeta{testTarget: {File: file}}
}

type fixture struct {
	dirKeys []testKey
	imgKeys []testKey
	v       *Verifier
	content []byte
	file    TargetFile
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dirKeys := []testKey{newTestKey(t, "dir-1"), newTestKey(t, "dir-2"), newTestKey(t, "dir-3")}
	imgKeys := []testKey{newTestKey(t, "img-1"), newTestKey(t, "img-2"), newTestKey(t, "img-3")}

	var dirRingKeys, imgRingKeys []Key
	for _, key := range dirKeys {
		dirRingKeys = append(dirRingKeys, key.ringKey(t))
	}
	for _, key := range imgKeys {
		imgRingKeys = append(imgRingKeys, key.ringKey(t))
	}
	dirRing, err := NewKeyring(dirRingKeys...)
	if err != nil {
		t.Fatalf("director keyring: %v", err)
	}
	imgRing, err := NewKeyring(imgRingKeys...)
	if err != nil {
		t.Fatalf("image keyring: %v", err)
	}

	content, file := testFile()
	v, err := New(Config{
		ECU:               ECUIdentity{Serial: testSerial, Hardware: testHW},
		DirectorThreshold: 2,
		ImageThreshold:    2,
		DirectorKeys:      dirRing,
		ImageKeys:         imgRing,
		Now:               func() time.Time { return testNow },
	})
	if err != nil {
		t.Fatalf("new verifier: %v", err)
	}
	return &fixture{
		dirKeys: dirKeys,
		imgKeys: imgKeys,
		v:       v,
		content: content,
		file:    file,
	}
}

func (f *fixture) directorEnvelope(t *testing.T, version int, targets map[string]TargetMeta) []byte {
	t.Helper()
	return signEnvelope(t, baseSigned(version, targets), f.dirKeys[:2]...)
}

func (f *fixture) imageEnvelope(t *testing.T, version int, targets map[string]TargetMeta) []byte {
	t.Helper()
	return signEnvelope(t, baseSigned(version, targets), f.imgKeys[:2]...)
}

func (f *fixture) validPair(t *testing.T, version int) ([]byte, []byte) {
	t.Helper()
	return f.directorEnvelope(t, version, directorTargets(f.file)),
		f.imageEnvelope(t, version, imageTargets(f.file))
}

func TestVerifyHappyPathAndArtifact(t *testing.T) {
	f := newFixture(t)
	director, image := f.validPair(t, 2)

	result, err := f.v.Verify(director, image)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if result.Target == nil {
		t.Fatal("expected verified target")
	}
	if result.Target.Name != testTarget || result.Target.Length != f.file.Length {
		t.Fatalf("unexpected target: %+v", result.Target)
	}
	if !bytes.Equal(result.Target.SHA256, f.file.Hashes.SHA256) {
		t.Fatal("verified target sha256 mismatch")
	}
	if result.DirectorVersion != 2 || result.ImageVersion != 2 {
		t.Fatalf("unexpected versions: %+v", result)
	}
	if len(result.SkippedOneSided) != 0 {
		t.Fatalf("expected no one-sided targets, got %v", result.SkippedOneSided)
	}
	if got := f.v.State(); got.DirectorVersion != 2 || got.ImageVersion != 2 {
		t.Fatalf("state not advanced: %+v", got)
	}

	if err := result.Target.VerifyArtifact(bytes.NewReader(f.content)); err != nil {
		t.Fatalf("artifact verification failed: %v", err)
	}

	altered := append([]byte(nil), f.content...)
	altered[0] ^= 0xFF
	if err := result.Target.VerifyArtifact(bytes.NewReader(altered)); !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("altered artifact: want ErrTargetMismatch, got %v", err)
	}
	truncated := f.content[:len(f.content)-1]
	if err := result.Target.VerifyArtifact(bytes.NewReader(truncated)); !errors.Is(err, ErrTargetMismatch) {
		t.Fatalf("truncated artifact: want ErrTargetMismatch, got %v", err)
	}
}

func TestOneSidedTargetsAreSkippedOrRejected(t *testing.T) {
	f := newFixture(t)

	imageWithExtra := imageTargets(f.file)
	imageWithExtra["image-only.bin"] = TargetMeta{File: f.file}
	directorWithExtra := directorTargets(f.file)
	directorWithExtra["director-only.bin"] = TargetMeta{
		File:   f.file,
		Custom: Custom{ECUIdentifier: "ECU-OTHER", HardwareIdentifier: "HW-OTHER"},
	}

	result, err := f.v.Verify(
		f.directorEnvelope(t, 1, directorWithExtra),
		f.imageEnvelope(t, 1, imageWithExtra),
	)
	if err != nil {
		t.Fatalf("verify with one-sided extras: %v", err)
	}
	wantSkipped := map[string]bool{"image-only.bin": false, "director-only.bin": false}
	for _, name := range result.SkippedOneSided {
		if _, ok := wantSkipped[name]; ok {
			wantSkipped[name] = true
		}
	}
	for name, found := range wantSkipped {
		if !found {
			t.Fatalf("expected %q in skipped list, got %v", name, result.SkippedOneSided)
		}
	}

	_, err = f.v.Verify(
		f.directorEnvelope(t, 2, directorTargets(f.file)),
		f.imageEnvelope(t, 2, map[string]TargetMeta{}),
	)
	if !errors.Is(err, ErrImageSideMissing) {
		t.Fatalf("missing image counterpart: want ErrImageSideMissing, got %v", err)
	}
}

func TestDirectorMustNameThisECU(t *testing.T) {
	f := newFixture(t)

	cases := map[string]Custom{
		"wrong serial":   {ECUIdentifier: "ECU-9999", HardwareIdentifier: testHW},
		"wrong hardware": {ECUIdentifier: testSerial, HardwareIdentifier: "HW-Z"},
		"empty binding":  {},
	}
	for name, custom := range cases {
		t.Run(name, func(t *testing.T) {
			targets := map[string]TargetMeta{
				testTarget: {File: f.file, Custom: custom},
			}
			_, err := f.v.Verify(
				f.directorEnvelope(t, 1, targets),
				f.imageEnvelope(t, 1, imageTargets(f.file)),
			)
			if !errors.Is(err, ErrNoTargetForECU) {
				t.Fatalf("want ErrNoTargetForECU, got %v", err)
			}
		})
	}

	targets := directorTargets(f.file)
	targets["duplicate.bin"] = TargetMeta{
		File:   f.file,
		Custom: Custom{ECUIdentifier: testSerial, HardwareIdentifier: testHW},
	}
	_, err := f.v.Verify(
		f.directorEnvelope(t, 1, targets),
		f.imageEnvelope(t, 1, imageTargets(f.file)),
	)
	if !errors.Is(err, ErrECUMismatch) {
		t.Fatalf("duplicate ECU binding: want ErrECUMismatch, got %v", err)
	}
}

func TestFileMetadataMustMatchAcrossRepositories(t *testing.T) {
	f := newFixture(t)

	t.Run("length differs", func(t *testing.T) {
		other := f.file
		other.Length++
		_, err := f.v.Verify(
			f.directorEnvelope(t, 1, directorTargets(f.file)),
			f.imageEnvelope(t, 1, imageTargets(other)),
		)
		if !errors.Is(err, ErrTargetMismatch) {
			t.Fatalf("want ErrTargetMismatch, got %v", err)
		}
	})

	t.Run("sha256 differs", func(t *testing.T) {
		other := f.file
		other.Hashes = Hashes{SHA256: bytes.Repeat([]byte{0x01}, sha256.Size)}
		_, err := f.v.Verify(
			f.directorEnvelope(t, 1, directorTargets(f.file)),
			f.imageEnvelope(t, 1, imageTargets(other)),
		)
		if !errors.Is(err, ErrTargetMismatch) {
			t.Fatalf("want ErrTargetMismatch, got %v", err)
		}
	})
}

func TestRollbackProtection(t *testing.T) {
	f := newFixture(t)

	if err := f.v.SetState(State{DirectorVersion: 5, ImageVersion: 5}); err != nil {
		t.Fatalf("set state: %v", err)
	}

	_, err := f.v.Verify(
		f.directorEnvelope(t, 4, directorTargets(f.file)),
		f.imageEnvelope(t, 5, imageTargets(f.file)),
	)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("director rollback: want ErrRollback, got %v", err)
	}
	_, err = f.v.Verify(
		f.directorEnvelope(t, 5, directorTargets(f.file)),
		f.imageEnvelope(t, 4, imageTargets(f.file)),
	)
	if !errors.Is(err, ErrRollback) {
		t.Fatalf("image rollback: want ErrRollback, got %v", err)
	}

	if got := f.v.State(); got.DirectorVersion != 5 || got.ImageVersion != 5 {
		t.Fatalf("state changed after failed verification: %+v", got)
	}

	if _, err := f.v.Verify(
		f.directorEnvelope(t, 5, directorTargets(f.file)),
		f.imageEnvelope(t, 5, imageTargets(f.file)),
	); err != nil {
		t.Fatalf("same version should be accepted: %v", err)
	}

	if _, err := f.v.Verify(
		f.directorEnvelope(t, 6, directorTargets(f.file)),
		f.imageEnvelope(t, 6, imageTargets(f.file)),
	); err != nil {
		t.Fatalf("version 6: %v", err)
	}
	if _, err := f.v.Verify(
		f.directorEnvelope(t, 5, directorTargets(f.file)),
		f.imageEnvelope(t, 5, imageTargets(f.file)),
	); !errors.Is(err, ErrRollback) {
		t.Fatalf("replay after upgrade: want ErrRollback, got %v", err)
	}
}

func TestExpiryRejected(t *testing.T) {
	f := newFixture(t)

	cases := map[string]time.Time{
		"expired in the past": testNow.Add(-time.Second),
		"expires exactly now": testNow,
	}
	for name, expiry := range cases {
		t.Run(name+" director", func(t *testing.T) {
			director := signEnvelope(t, baseSigned(1, directorTargets(f.file)).withExpiry(expiry), f.dirKeys[:2]...)
			_, err := f.v.Verify(director, f.imageEnvelope(t, 1, imageTargets(f.file)))
			if !errors.Is(err, ErrExpired) {
				t.Fatalf("want ErrExpired, got %v", err)
			}
		})
		t.Run(name+" image", func(t *testing.T) {
			image := signEnvelope(t, baseSigned(1, imageTargets(f.file)).withExpiry(expiry), f.imgKeys[:2]...)
			_, err := f.v.Verify(f.directorEnvelope(t, 1, directorTargets(f.file)), image)
			if !errors.Is(err, ErrExpired) {
				t.Fatalf("want ErrExpired, got %v", err)
			}
		})
	}

	// Expiry one second in the future is still valid.
	fresh := testNow.Add(time.Second)
	director := signEnvelope(t, baseSigned(1, directorTargets(f.file)).withExpiry(fresh), f.dirKeys[:2]...)
	image := signEnvelope(t, baseSigned(1, imageTargets(f.file)).withExpiry(fresh), f.imgKeys[:2]...)
	if _, err := f.v.Verify(director, image); err != nil {
		t.Fatalf("fresh metadata should verify: %v", err)
	}
}

func TestSignatureThreshold(t *testing.T) {
	f := newFixture(t)

	t.Run("only one director signature below threshold", func(t *testing.T) {
		director := signEnvelope(t, baseSigned(1, directorTargets(f.file)), f.dirKeys[0])
		_, err := f.v.Verify(director, f.imageEnvelope(t, 1, imageTargets(f.file)))
		if !errors.Is(err, ErrThresholdNotMet) {
			t.Fatalf("want ErrThresholdNotMet, got %v", err)
		}
	})

	t.Run("untrusted key does not count", func(t *testing.T) {
		stranger := newTestKey(t, "stranger")
		director := signEnvelope(t, baseSigned(1, directorTargets(f.file)), f.dirKeys[0], stranger)
		_, err := f.v.Verify(director, f.imageEnvelope(t, 1, imageTargets(f.file)))
		if !errors.Is(err, ErrThresholdNotMet) {
			t.Fatalf("want ErrThresholdNotMet, got %v", err)
		}
	})

	t.Run("tampered signed payload invalidates signatures", func(t *testing.T) {
		director := f.directorEnvelope(t, 1, directorTargets(f.file))
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(director, &envelope); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		tamperedSigned := bytes.Replace(envelope["signed"], []byte(`"version":1`), []byte(`"version":2`), 1)
		envelope["signed"] = tamperedSigned
		raw, err := json.Marshal(envelope)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		_, err = f.v.Verify(raw, f.imageEnvelope(t, 1, imageTargets(f.file)))
		if !errors.Is(err, ErrThresholdNotMet) {
			t.Fatalf("tampered payload: want ErrThresholdNotMet, got %v", err)
		}
	})
}

func TestParseMetadataRejectsMalformedDocuments(t *testing.T) {
	_, file := testFile()
	valid := baseSigned(1, imageTargets(file))

	cases := map[string]func() []byte{
		"not json": func() []byte {
			return []byte("{nope")
		},
		"missing signatures": func() []byte {
			raw, _ := json.Marshal(valid)
			out, _ := json.Marshal(struct {
				Signed json.RawMessage `json:"signed"`
			}{Signed: raw})
			return out
		},
		"wrong type": func() []byte {
			other := valid
			other.Type = "snapshot"
			return signEnvelope(t, other, newTestKey(t, "k"))
		},
		"zero version": func() []byte {
			other := valid
			other.Version = 0
			return signEnvelope(t, other, newTestKey(t, "k"))
		},
		"bad sha256 length": func() []byte {
			other := baseSigned(1, map[string]TargetMeta{
				testTarget: {File: TargetFile{Length: 1, Hashes: Hashes{SHA256: []byte("short")}}},
			})
			return signEnvelope(t, other, newTestKey(t, "k"))
		},
		"bad hex digest": func() []byte {
			raw, _ := json.Marshal(valid)
			envelope := map[string]any{
				"signed": json.RawMessage(raw),
				"signatures": []map[string]any{
					{"keyid": "k", "sig": "zz-not-hex"},
				},
			}
			out, _ := json.Marshal(envelope)
			return out
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMetadata(build()); !errors.Is(err, ErrMalformed) {
				t.Fatalf("want ErrMalformed, got %v", err)
			}
		})
	}

	// A well-formed document round-trips and keeps the raw signed bytes used
	// for signature verification.
	key := newTestKey(t, "k")
	raw := signEnvelope(t, valid, key)
	metadata, err := ParseMetadata(raw)
	if err != nil {
		t.Fatalf("parse valid document: %v", err)
	}
	if metadata.Signed.Version != 1 || len(metadata.Signatures) != 1 {
		t.Fatalf("unexpected parsed metadata: %+v", metadata)
	}
	if !bytes.Equal(metadata.RawSigned, mustMarshal(t, valid)) {
		t.Fatal("raw signed payload not preserved")
	}
}

func mustMarshal(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	key := newTestKey(t, "k")
	ring, err := NewKeyring(key.ringKey(t))
	if err != nil {
		t.Fatalf("keyring: %v", err)
	}

	cases := map[string]Config{
		"missing ecu identity": {
			ECU:               ECUIdentity{Serial: "", Hardware: testHW},
			DirectorThreshold: 1,
			ImageThreshold:    1,
			DirectorKeys:      ring,
			ImageKeys:         ring,
		},
		"zero director threshold": {
			ECU:               ECUIdentity{Serial: testSerial, Hardware: testHW},
			DirectorThreshold: 0,
			ImageThreshold:    1,
			DirectorKeys:      ring,
			ImageKeys:         ring,
		},
		"empty image keyring": {
			ECU:               ECUIdentity{Serial: testSerial, Hardware: testHW},
			DirectorThreshold: 1,
			ImageThreshold:    1,
			DirectorKeys:      ring,
		},
	}
	for name, config := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}

	if _, err := NewKeyring(key.ringKey(t), key.ringKey(t)); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("duplicate keys: want ErrInvalidConfig, got %v", err)
	}
}

func TestSetStateRejectsNegativeVersions(t *testing.T) {
	f := newFixture(t)
	if err := f.v.SetState(State{DirectorVersion: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("negative version: want ErrInvalidConfig, got %v", err)
	}
}
