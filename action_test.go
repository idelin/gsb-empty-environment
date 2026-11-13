package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"testing"
)

var testHostKey = []byte("host-hmac-key-for-netsec-tests-0123456789")

func baseInputs() ActionInputs {
	return ActionInputs{
		SourceRoot: "/src/project",
		Args:       []string{"go", "build", "./cmd/tool"},
		WorkDir:    "/src/project",
		Env: []EnvVar{
			{Name: "CGO_ENABLED", Value: "0"},
			{Name: "LANG", Value: "C"},
		},
		ToolchainDigest: "sha256:toolchain-v1",
		DependencyOutputs: []DependencyOutput{
			{Name: "dep-lib", Digest: "sha256:dep-output-v1"},
			{Name: "dep-codegen", Digest: "sha256:codegen-output-v1"},
		},
		Platform: []Attribute{
			{Name: "arch", Value: "amd64"},
			{Name: "os", Value: "linux"},
		},
		SecurityPolicyDigest: "sha256:policy-v1",
	}
}

func mustKey(t *testing.T, in ActionInputs, secretHMACKey []byte) ActionKey {
	t.Helper()
	key, err := in.Key(secretHMACKey)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	return key
}

func requireDifferentKey(t *testing.T, a, b ActionInputs, secretHMACKey []byte) {
	t.Helper()
	keyA := mustKey(t, a, secretHMACKey)
	keyB := mustKey(t, b, secretHMACKey)
	if keyA == keyB {
		t.Fatalf("expected distinct action keys, both were %s", keyA.Hex())
	}
}

func TestActionKeySHA256OfCanonicalBytes(t *testing.T) {
	in := baseInputs()
	canonical, err := in.CanonicalBytes(testHostKey)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	sum := sha256.Sum256(canonical)
	key := mustKey(t, in, testHostKey)
	if key != ActionKey(sum) {
		t.Fatalf("key %s is not SHA-256 of canonical bytes", key.Hex())
	}
}

func TestArgumentBoundaries(t *testing.T) {
	in := baseInputs()

	noArgs := in
	noArgs.Args = nil
	emptyArgs := in
	emptyArgs.Args = []string{}
	if mustKey(t, noArgs, nil) != mustKey(t, emptyArgs, nil) {
		t.Fatal("nil and empty argument arrays must encode identically")
	}

	oneEmptyArg := in
	oneEmptyArg.Args = []string{""}
	if mustKey(t, noArgs, nil) == mustKey(t, oneEmptyArg, nil) {
		t.Fatal("zero arguments collided with one empty argument")
	}

	ab := in
	ab.Args = []string{"a", "b"}
	joined := in
	joined.Args = []string{"ab"}
	requireDifferentKey(t, ab, joined, nil)

	splitA := in
	splitA.Args = []string{"a", "bc"}
	splitB := in
	splitB.Args = []string{"ab", "c"}
	requireDifferentKey(t, splitA, splitB, nil)

	reversed := in
	reversed.Args = []string{"b", "a"}
	requireDifferentKey(t, ab, reversed, nil)

	nulArg := in
	nulArg.Args = []string{"a\x00b"}
	requireDifferentKey(t, ab, nulArg, nil)

	longA := in
	longA.Args = []string{strings.Repeat("x", 4096)}
	longB := in
	longB.Args = []string{strings.Repeat("x", 4095) + "y"}
	requireDifferentKey(t, longA, longB, nil)
}

func TestEnvironmentAndMappingOrdering(t *testing.T) {
	in := baseInputs()
	reordered := in
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

	keyA := mustKey(t, in, nil)
	keyB := mustKey(t, reordered, nil)
	if keyA != keyB {
		t.Fatalf("semantically identical mapping reorder changed the key")
	}

	// Sorting is by raw UTF-8 bytes, not by locale or Unicode collation.
	unicodeEnvA := in
	unicodeEnvA.Env = []EnvVar{
		{Name: "é", Value: "1"},
		{Name: "Z", Value: "2"},
		{Name: "a", Value: "3"},
	}
	unicodeEnvB := in
	unicodeEnvB.Env = []EnvVar{
		{Name: "a", Value: "3"},
		{Name: "é", Value: "1"},
		{Name: "Z", Value: "2"},
	}
	if mustKey(t, unicodeEnvA, nil) != mustKey(t, unicodeEnvB, nil) {
		t.Fatal("raw UTF-8 byte ordering is not stable across reorderings")
	}
}

func TestDuplicateMappingKeysRejected(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ActionInputs)
	}{
		{
			name: "duplicate public env",
			edit: func(in *ActionInputs) {
				in.Env = append(in.Env, EnvVar{Name: "LANG", Value: "en_US.UTF-8"})
			},
		},
		{
			name: "duplicate public and secret env",
			edit: func(in *ActionInputs) {
				in.Env = append(in.Env, EnvVar{Name: "LANG", Value: "secret", Secret: true})
			},
		},
		{
			name: "duplicate dependency",
			edit: func(in *ActionInputs) {
				in.DependencyOutputs = append(in.DependencyOutputs, DependencyOutput{Name: "dep-lib", Digest: "different"})
			},
		},
		{
			name: "duplicate platform attribute",
			edit: func(in *ActionInputs) {
				in.Platform = append(in.Platform, Attribute{Name: "os", Value: "darwin"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInputs()
			tt.edit(&in)
			_, err := in.CanonicalBytes(testHostKey)
			if !errors.Is(err, ErrDuplicateKey) {
				t.Fatalf("expected ErrDuplicateKey, got %v", err)
			}
		})
	}

	for _, mapping := range []struct {
		name string
		edit func(*ActionInputs)
	}{
		{"empty env name", func(in *ActionInputs) { in.Env = append(in.Env, EnvVar{Name: "", Value: "x"}) }},
		{"empty dependency name", func(in *ActionInputs) {
			in.DependencyOutputs = append(in.DependencyOutputs, DependencyOutput{Name: "", Digest: "x"})
		}},
		{"empty platform name", func(in *ActionInputs) {
			in.Platform = append(in.Platform, Attribute{Name: "", Value: "x"})
		}},
	} {
		t.Run(mapping.name, func(t *testing.T) {
			in := baseInputs()
			mapping.edit(&in)
			_, err := in.CanonicalBytes(testHostKey)
			if !errors.Is(err, ErrEmptyMappingKey) {
				t.Fatalf("expected ErrEmptyMappingKey, got %v", err)
			}
		})
	}
}

func TestMissingRequiredFieldsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		edit func(*ActionInputs)
	}{
		{"source root", func(in *ActionInputs) { in.SourceRoot = "" }},
		{"working directory", func(in *ActionInputs) { in.WorkDir = "" }},
		{"toolchain digest", func(in *ActionInputs) { in.ToolchainDigest = "" }},
		{"security policy digest", func(in *ActionInputs) { in.SecurityPolicyDigest = "" }},
		{"platform attributes", func(in *ActionInputs) { in.Platform = nil }},
		{"dependency output digest", func(in *ActionInputs) { in.DependencyOutputs[0].Digest = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := baseInputs()
			tt.edit(&in)
			_, err := in.Key(nil)
			if !errors.Is(err, ErrMissingInput) {
				t.Fatalf("expected ErrMissingInput, got %v", err)
			}
		})
	}
}

func TestSecretEnvironmentVersions(t *testing.T) {
	in := baseInputs()
	secret := "super-secret-token-value"
	in.Env = append(in.Env, EnvVar{Name: "DEPLOY_TOKEN", Value: secret, Secret: true})

	canonical, err := in.CanonicalBytes(testHostKey)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if bytes.Contains(canonical, []byte(secret)) {
		t.Fatal("canonical action encoding leaked a secret value")
	}
	if !bytes.Contains(canonical, []byte("DEPLOY_TOKEN")) {
		t.Fatal("secret environment variable name must remain in the encoding")
	}

	rotated := in
	rotated.Env = append([]EnvVar(nil), in.Env...)
	rotated.Env[len(rotated.Env)-1].Value = "rotated-secret-token-value"
	requireDifferentKey(t, in, rotated, testHostKey)

	renamed := in
	renamed.Env = append([]EnvVar(nil), in.Env...)
	renamed.Env[len(renamed.Env)-1].Name = "OTHER_TOKEN"
	requireDifferentKey(t, in, renamed, testHostKey)

	sameValueDifferentHostKey := mustKey(t, in, testHostKey)
	otherHostKey := mustKey(t, in, []byte("different-host-hmac-key"))
	if sameValueDifferentHostKey == otherHostKey {
		t.Fatal("host HMAC key rotation must change the secret version identifier")
	}

	stable, err := in.Key(testHostKey)
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	stableAgain, err := in.Key(append([]byte(nil), testHostKey...))
	if err != nil {
		t.Fatalf("Key: %v", err)
	}
	if stable != stableAgain {
		t.Fatal("secret version is not deterministic")
	}

	if _, err := in.Key(nil); !errors.Is(err, ErrSecretKeyRequired) {
		t.Fatalf("expected ErrSecretKeyRequired, got %v", err)
	}

	// A public variable carrying the secret-looking string must not collide
	// with the HMAC-protected secret entry.
	publicLookalike := baseInputs()
	publicLookalike.Env = append(publicLookalike.Env, EnvVar{Name: "DEPLOY_TOKEN", Value: secret})
	requireDifferentKey(t, in, publicLookalike, testHostKey)
}

func TestFormattingRedactsSecretValues(t *testing.T) {
	in := baseInputs()
	secret := "formatting-must-not-leak-this"
	in.Env = append(in.Env, EnvVar{Name: "DEPLOY_TOKEN", Value: secret, Secret: true})

	for _, format := range []string{"%v", "%+v", "%#v"} {
		rendered := fmt.Sprintf(format, in)
		if strings.Contains(rendered, secret) {
			t.Fatalf("%s exposed a secret value: %s", format, rendered)
		}
	}
}

func TestHostHMACKeyDoesNotAffectPublicActions(t *testing.T) {
	in := baseInputs()
	withoutKey, err := in.CanonicalBytes(nil)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	withKey, err := in.CanonicalBytes(testHostKey)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if !bytes.Equal(withoutKey, withKey) {
		t.Fatal("host HMAC key leaked into an action with no secrets")
	}
}

func TestSecurityInputMutationsChangeKey(t *testing.T) {
	upgradedToolchain := baseInputs()
	upgradedToolchain.ToolchainDigest = "sha256:toolchain-v2"
	requireDifferentKey(t, baseInputs(), upgradedToolchain, nil)

	tightenedPolicy := baseInputs()
	tightenedPolicy.SecurityPolicyDigest = "sha256:policy-v2-strict"
	requireDifferentKey(t, baseInputs(), tightenedPolicy, nil)

	changedDependency := baseInputs()
	changedDependency.DependencyOutputs = append([]DependencyOutput(nil), baseInputs().DependencyOutputs...)
	changedDependency.DependencyOutputs[0].Digest = "sha256:dep-output-v2"
	requireDifferentKey(t, baseInputs(), changedDependency, nil)

	changedPlatform := baseInputs()
	changedPlatform.Platform = append([]Attribute(nil), baseInputs().Platform...)
	changedPlatform.Platform[0].Value = "arm64"
	requireDifferentKey(t, baseInputs(), changedPlatform, nil)

	changedWorkDir := baseInputs()
	changedWorkDir.WorkDir = "/src/project/other"
	requireDifferentKey(t, baseInputs(), changedWorkDir, nil)

	changedEnv := baseInputs()
	changedEnv.Env = append([]EnvVar(nil), baseInputs().Env...)
	changedEnv.Env[0].Value = "1"
	requireDifferentKey(t, baseInputs(), changedEnv, nil)

	changedSource := baseInputs()
	changedSource.SourceRoot = "/src/project-fork"
	requireDifferentKey(t, baseInputs(), changedSource, nil)
}

func TestActionKeyEncodingIsDeterministicAcrossCopies(t *testing.T) {
	in := baseInputs()
	keyA := mustKey(t, in, testHostKey)
	copyInputs := in
	copyInputs.Args = append([]string(nil), in.Args...)
	copyInputs.Env = append([]EnvVar(nil), in.Env...)
	copyInputs.DependencyOutputs = append([]DependencyOutput(nil), in.DependencyOutputs...)
	copyInputs.Platform = append([]Attribute(nil), in.Platform...)
	keyB := mustKey(t, copyInputs, testHostKey)
	if keyA != keyB {
		t.Fatalf("copied inputs produced different keys: %s != %s", keyA.Hex(), keyB.Hex())
	}
}

func TestCanonicalEncodingUsesFixedTagsAndBigEndianLengths(t *testing.T) {
	in := baseInputs()
	canonical, err := in.CanonicalBytes(nil)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if len(canonical) < 8+len(ActionFormat)+1+8 {
		t.Fatalf("canonical encoding too short: %d", len(canonical))
	}
	formatLen := binary.BigEndian.Uint64(canonical[:8])
	if formatLen != uint64(len(ActionFormat)) {
		t.Fatalf("format length = %d, want %d", formatLen, len(ActionFormat))
	}
	offset := 8 + int(formatLen)
	if canonical[offset] != actionTagVersion {
		t.Fatalf("version tag = %#x, want %#x", canonical[offset], actionTagVersion)
	}
	versionLen := binary.BigEndian.Uint64(canonical[offset+1 : offset+9])
	if versionLen != 1 || canonical[offset+9] != '1' {
		t.Fatalf("version field length/value = %d/%q, want 1/%q", versionLen, canonical[offset+9], '1')
	}
	offset += 10
	if canonical[offset] != actionTagSourceRoot {
		t.Fatalf("source root tag = %#x, want %#x", canonical[offset], actionTagSourceRoot)
	}
	sourceLen := binary.BigEndian.Uint64(canonical[offset+1 : offset+9])
	if sourceLen != uint64(len(in.SourceRoot)) {
		t.Fatalf("source length = %d, want %d", sourceLen, len(in.SourceRoot))
	}
}

func TestActionKeyParseRoundTrip(t *testing.T) {
	key := mustKey(t, baseInputs(), testHostKey)
	parsed, err := ParseActionKey("sha256:" + key.Hex())
	if err != nil {
		t.Fatalf("ParseActionKey: %v", err)
	}
	if parsed != key {
		t.Fatalf("parse round trip: %s != %s", parsed.Hex(), key.Hex())
	}
	if _, err := ParseActionKey(strings.Repeat("0", 31)); err == nil {
		t.Fatal("short action key accepted")
	}
}

func ExampleActionInputs_Key() {
	inputs := ActionInputs{
		SourceRoot:           "/src",
		Args:                 []string{"go", "build", "./..."},
		WorkDir:              "/src",
		ToolchainDigest:      "sha256:toolchain",
		Platform:             []Attribute{{Name: "os", Value: "linux"}},
		SecurityPolicyDigest: "sha256:policy",
	}
	key, _ := inputs.Key(nil)
	fmt.Println(len(key.Hex()))
	// Output: 64
}
