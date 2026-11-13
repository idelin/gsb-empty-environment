package netsec

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newProducerKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return pub, priv
}

func writeOutputTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}
	return root
}

func signedFixture(t *testing.T) (ActionInputs, *CacheEntry, ed25519.PublicKey, time.Time) {
	t.Helper()
	inputs := baseInputs()
	root := writeOutputTree(t, map[string]string{
		"bin/tool":      "#!/bin/sh\necho tool\n",
		"lib/data.txt":  "artifact data",
		"lib/empty.txt": "",
	})
	digest, err := HashOutputTree(root)
	if err != nil {
		t.Fatalf("HashOutputTree: %v", err)
	}
	issued := time.Date(2026, 11, 13, 12, 0, 0, 123456789, time.UTC)
	entry, err := NewCacheEntry(inputs, testHostKey, "builder-1", root, digest, issued)
	if err != nil {
		t.Fatalf("NewCacheEntry: %v", err)
	}
	pub, priv := newProducerKey(t)
	if err := entry.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return inputs, entry, pub, issued
}

func authorizedPolicy(pub ed25519.PublicKey, issued time.Time) HitPolicy {
	return HitPolicy{
		SecretHMACKey: testHostKey,
		ProducerKeys:  map[string]ed25519.PublicKey{"builder-1": pub},
		Now:           func() time.Time { return issued.Add(time.Minute) },
	}
}

func TestHashOutputTreeDeterminism(t *testing.T) {
	files := map[string]string{
		"a.txt":     "alpha",
		"dir/b.txt": "beta",
		"dir/c.txt": "",
	}
	rootA := writeOutputTree(t, files)
	rootB := writeOutputTree(t, files)
	digestA, err := HashOutputTree(rootA)
	if err != nil {
		t.Fatalf("HashOutputTree A: %v", err)
	}
	digestB, err := HashOutputTree(rootB)
	if err != nil {
		t.Fatalf("HashOutputTree B: %v", err)
	}
	if digestA != digestB {
		t.Fatalf("identical trees produced different digests: %s != %s", digestA, digestB)
	}

	if err := os.WriteFile(filepath.Join(rootA, "a.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	changed, err := HashOutputTree(rootA)
	if err != nil {
		t.Fatalf("HashOutputTree changed: %v", err)
	}
	if digestA == changed {
		t.Fatal("content change did not change the output tree digest")
	}

	if err := os.WriteFile(filepath.Join(rootB, "new.txt"), []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	added, err := HashOutputTree(rootB)
	if err != nil {
		t.Fatalf("HashOutputTree added: %v", err)
	}
	if digestB == added {
		t.Fatal("added file did not change the output tree digest")
	}
}

func TestSignedCacheHit(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	result := authorizedPolicy(pub, issued).Check(inputs, entry)
	if !result.Hit {
		t.Fatalf("expected hit, got %s", result)
	}
}

func TestMappingReorderRemainsCacheHit(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)

	reordered := inputs
	reordered.Env = []EnvVar{
		{Name: "LANG", Value: "C"},
		{Name: "CGO_ENABLED", Value: "0"},
	}
	reordered.DependencyOutputs = []DependencyOutput{
		{Name: "dep-codegen", Digest: "sha256:codegen-output-v1"},
		{Name: "dep-lib", Digest: "sha256:dep-output-v1"},
	}
	reordered.Platform = []Attribute{
		{Name: "os", Value: "linux"},
		{Name: "arch", Value: "amd64"},
	}

	result := authorizedPolicy(pub, issued).Check(reordered, entry)
	if !result.Hit {
		t.Fatalf("semantically identical mapping reorder should hit, got %s", result)
	}
}

func TestSecretRotationMissesCacheHit(t *testing.T) {
	inputs := baseInputs()
	inputs.Env = append(inputs.Env, EnvVar{Name: "DEPLOY_TOKEN", Value: "token-round-1", Secret: true})
	root := writeOutputTree(t, map[string]string{"out.bin": "artifact"})
	digest, err := HashOutputTree(root)
	if err != nil {
		t.Fatalf("HashOutputTree: %v", err)
	}
	issued := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	entry, err := NewCacheEntry(inputs, testHostKey, "builder-1", root, digest, issued)
	if err != nil {
		t.Fatalf("NewCacheEntry: %v", err)
	}
	pub, priv := newProducerKey(t)
	if err := entry.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	policy := authorizedPolicy(pub, issued)
	if result := policy.Check(inputs, entry); !result.Hit {
		t.Fatalf("precondition failed: %s", result)
	}

	rotated := inputs
	rotated.Env = append([]EnvVar(nil), inputs.Env...)
	rotated.Env[len(rotated.Env)-1].Value = "token-round-2"
	result := policy.Check(rotated, entry)
	if result.Hit || result.Reason != MissActionKeyMismatch {
		t.Fatalf("secret rotation must miss, got %s", result)
	}
}

func TestMissingEntryFieldsAreMisses(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)

	tests := []struct {
		name string
		edit func(*CacheEntry)
	}{
		{"output root", func(e *CacheEntry) { e.OutputRoot = "" }},
		{"producer", func(e *CacheEntry) { e.Producer = "" }},
		{"action key", func(e *CacheEntry) { e.ActionKey = ActionKey{} }},
		{"issuance time", func(e *CacheEntry) { e.IssuedAt = time.Time{} }},
		{"output digest", func(e *CacheEntry) { e.OutputDigest = "" }},
		{"signature", func(e *CacheEntry) { e.Signature = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copyEntry := *entry
			tt.edit(&copyEntry)
			result := policy.Check(inputs, &copyEntry)
			if result.Hit || result.Reason != MissMissingField {
				t.Fatalf("expected MissMissingField, got %s", result)
			}
		})
	}

	if result := policy.Check(inputs, nil); result.Hit || result.Reason != MissNoEntry {
		t.Fatalf("expected MissNoEntry, got %s", result)
	}
}

func TestTamperedCacheEntryMisses(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)

	tests := []struct {
		name   string
		edit   func(*CacheEntry)
		reason MissReason
	}{
		{
			name:   "action key",
			edit:   func(e *CacheEntry) { e.ActionKey = ActionKey(sha256.Sum256([]byte("other"))) },
			reason: MissActionKeyMismatch,
		},
		{
			name:   "output root",
			edit:   func(e *CacheEntry) { e.OutputRoot = e.OutputRoot + "-other" },
			reason: MissBadSignature,
		},
		{
			name:   "producer identity",
			edit:   func(e *CacheEntry) { e.Producer = "builder-2" },
			reason: MissUnauthorizedProducer,
		},
		{
			name:   "issuance time",
			edit:   func(e *CacheEntry) { e.IssuedAt = e.IssuedAt.Add(time.Second) },
			reason: MissBadSignature,
		},
		{
			name:   "output digest",
			edit:   func(e *CacheEntry) { e.OutputDigest = strings.Repeat("0", 64) },
			reason: MissBadSignature,
		},
		{
			name:   "signature",
			edit:   func(e *CacheEntry) { e.Signature = append([]byte(nil), e.Signature...); e.Signature[0] ^= 0xff },
			reason: MissBadSignature,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copyEntry := *entry
			copyEntry.Signature = append([]byte(nil), entry.Signature...)
			tt.edit(&copyEntry)
			result := policy.Check(inputs, &copyEntry)
			if result.Hit || result.Reason != tt.reason {
				t.Fatalf("expected %s, got %s", tt.reason, result)
			}
		})
	}
}

func TestChangedInputsMiss(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)

	tests := []struct {
		name string
		edit func(*ActionInputs)
	}{
		{"toolchain upgrade", func(in *ActionInputs) { in.ToolchainDigest = "sha256:toolchain-v2" }},
		{"policy tightening", func(in *ActionInputs) { in.SecurityPolicyDigest = "sha256:policy-v2" }},
		{"dependency output", func(in *ActionInputs) { in.DependencyOutputs[0].Digest = "sha256:new-dep" }},
		{"platform", func(in *ActionInputs) { in.Platform[0].Value = "arm64" }},
		{"source root", func(in *ActionInputs) { in.SourceRoot = "/src/other" }},
		{"working directory", func(in *ActionInputs) { in.WorkDir = "/src/other" }},
		{"command arguments", func(in *ActionInputs) { in.Args = append(in.Args, "-race") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := inputs
			changed.Args = append([]string(nil), inputs.Args...)
			changed.Env = append([]EnvVar(nil), inputs.Env...)
			changed.DependencyOutputs = append([]DependencyOutput(nil), inputs.DependencyOutputs...)
			changed.Platform = append([]Attribute(nil), inputs.Platform...)
			tt.edit(&changed)
			result := policy.Check(changed, entry)
			if result.Hit || result.Reason != MissActionKeyMismatch {
				t.Fatalf("expected MissActionKeyMismatch, got %s", result)
			}
		})
	}
}

func TestOutputTreeChangedAfterSigningMisses(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)
	if result := policy.Check(inputs, entry); !result.Hit {
		t.Fatalf("precondition failed: %s", result)
	}
	if err := os.WriteFile(filepath.Join(entry.OutputRoot, "lib", "data.txt"), []byte("tampered"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	result := policy.Check(inputs, entry)
	if result.Hit || result.Reason != MissOutputDigestMismatch {
		t.Fatalf("expected MissOutputDigestMismatch, got %s", result)
	}
}

func TestMissingOutputTreeMisses(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)
	if err := os.RemoveAll(entry.OutputRoot); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	result := policy.Check(inputs, entry)
	if result.Hit || result.Reason != MissUnreadableOutput {
		t.Fatalf("expected MissUnreadableOutput, got %s", result)
	}
}

func TestUnauthorizedProducerMisses(t *testing.T) {
	inputs, entry, _, issued := signedFixture(t)
	policy := HitPolicy{
		SecretHMACKey: testHostKey,
		ProducerKeys:  map[string]ed25519.PublicKey{},
		Now:           func() time.Time { return issued.Add(time.Minute) },
	}
	result := policy.Check(inputs, entry)
	if result.Hit || result.Reason != MissUnauthorizedProducer {
		t.Fatalf("expected MissUnauthorizedProducer, got %s", result)
	}
}

func TestExpectedOutputRootAndExpiry(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)
	policy.ExpectedOutputRoot = entry.OutputRoot + "-other"
	if result := policy.Check(inputs, entry); result.Reason != MissOutputRootMismatch {
		t.Fatalf("expected MissOutputRootMismatch, got %s", result)
	}

	policy = authorizedPolicy(pub, issued)
	policy.MaxAge = time.Minute
	policy.Now = func() time.Time { return issued.Add(2 * time.Minute) }
	if result := policy.Check(inputs, entry); result.Reason != MissExpired {
		t.Fatalf("expected MissExpired, got %s", result)
	}
}

func TestInvalidCurrentInputsMiss(t *testing.T) {
	inputs, entry, pub, issued := signedFixture(t)
	policy := authorizedPolicy(pub, issued)
	inputs.ToolchainDigest = ""
	result := policy.Check(inputs, entry)
	if result.Hit || result.Reason != MissInvalidActionInputs {
		t.Fatalf("expected MissInvalidActionInputs, got %s", result)
	}
}

func TestNoDowngradeToWeakKey(t *testing.T) {
	inputs := baseInputs()
	root := writeOutputTree(t, map[string]string{"out.bin": "artifact"})
	digest, err := HashOutputTree(root)
	if err != nil {
		t.Fatalf("HashOutputTree: %v", err)
	}
	issued := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

	// Simulate an entry from a host that keyed only source root and args.
	weakMaterial := inputs.SourceRoot + "\x00" + strings.Join(inputs.Args, "\x00")
	entry := &CacheEntry{
		OutputRoot:   root,
		Producer:     "builder-1",
		ActionKey:    ActionKey(sha256.Sum256([]byte(weakMaterial))),
		IssuedAt:     issued,
		OutputDigest: digest,
	}
	pub, priv := newProducerKey(t)
	if err := entry.Sign(priv); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	result := authorizedPolicy(pub, issued).Check(inputs, entry)
	if result.Hit || result.Reason != MissActionKeyMismatch {
		t.Fatalf("weak-key entry must miss rather than degrade, got %s", result)
	}
}

func TestSignatureBindsEveryMetadataField(t *testing.T) {
	_, entry, pub, _ := signedFixture(t)
	if err := entry.VerifySignature(pub); err != nil {
		t.Fatalf("precondition: %v", err)
	}

	fields := []struct {
		name string
		edit func(*CacheEntry)
	}{
		{"output root", func(e *CacheEntry) { e.OutputRoot += "-x" }},
		{"producer", func(e *CacheEntry) { e.Producer += "-x" }},
		{"action key", func(e *CacheEntry) { e.ActionKey[0] ^= 0xff }},
		{"issued at", func(e *CacheEntry) { e.IssuedAt = e.IssuedAt.Add(time.Nanosecond) }},
		{"output digest", func(e *CacheEntry) { e.OutputDigest = strings.Repeat("1", 64) }},
	}
	for _, field := range fields {
		t.Run(field.name, func(t *testing.T) {
			copyEntry := *entry
			field.edit(&copyEntry)
			if err := copyEntry.VerifySignature(pub); err == nil {
				t.Fatalf("modified %s still verified", field.name)
			}
		})
	}
}

func TestSignWithSeed(t *testing.T) {
	_, entry, pub, _ := signedFixture(t)
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if err := entry.Sign(seed); err != nil {
		t.Fatalf("Sign(seed): %v", err)
	}
	if err := entry.VerifySignature(priv.Public().(ed25519.PublicKey)); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	_ = pub
}

func TestOutputDigestFormat(t *testing.T) {
	root := writeOutputTree(t, map[string]string{"a": "b"})
	digest, err := HashOutputTree(root)
	if err != nil {
		t.Fatalf("HashOutputTree: %v", err)
	}
	if len(digest) != sha256.Size*2 {
		t.Fatalf("digest length = %d, want %d", len(digest), sha256.Size*2)
	}
	if _, err := hex.DecodeString(digest); err != nil {
		t.Fatalf("digest is not hex: %v", err)
	}
}
