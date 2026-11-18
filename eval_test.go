package netsec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

const testAttrName = "user.netsec.ima.test"

func signatureAttribute(signature []byte, keyID uint32, algo Algorithm) []byte {
	raw := make([]byte, HeaderSize+len(signature))
	raw[0] = byte(TypeSignature)
	raw[1] = HeaderVersion
	raw[2] = byte(algo)
	binary.BigEndian.PutUint32(raw[3:7], keyID)
	binary.BigEndian.PutUint16(raw[7:9], uint16(len(signature)))
	copy(raw[HeaderSize:], signature)
	return raw
}

func digestAttribute(algo Algorithm, digest []byte) []byte {
	raw := make([]byte, 2+len(digest))
	raw[0] = byte(TypeDigestNG)
	raw[1] = byte(algo)
	copy(raw[2:], digest)
	return raw
}

func writeTestFile(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func setTestAttr(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := SetXAttr(path, testAttrName, value); err != nil {
		if errors.Is(err, ErrXAttrUnsupported) {
			t.Skip("extended attributes are unavailable")
		}
		t.Skipf("cannot set test extended attribute: %v", err)
	}
}

func testEvaluator(trusted []uint32, require func(string) bool) *Evaluator {
	evaluator := NewEvaluator(Policy{
		TrustedKeyIDs:    TrustedKeys(trusted...),
		RequireSignature: require,
	})
	evaluator.XAttrName = testAttrName
	return evaluator
}

func TestParseSignature(t *testing.T) {
	raw := signatureAttribute([]byte("signature-bytes"), 0x01020304, AlgoSHA256)
	attr, err := ParseSignature(raw)
	if err != nil {
		t.Fatalf("ParseSignature: %v", err)
	}
	if attr.Type != TypeSignature || attr.Version != HeaderVersion {
		t.Fatalf("unexpected type/version: %#v", attr)
	}
	if attr.Algorithm != AlgoSHA256 {
		t.Fatalf("algorithm = %v, want sha256", attr.Algorithm)
	}
	if attr.KeyID != 0x01020304 {
		t.Fatalf("keyid = 0x%08x", attr.KeyID)
	}
	if attr.SignatureLength != uint16(len("signature-bytes")) {
		t.Fatalf("signature length = %d", attr.SignatureLength)
	}
	if !bytes.Equal(attr.Signature, []byte("signature-bytes")) {
		t.Fatalf("signature = %q", attr.Signature)
	}
}

func TestParseRejectsMalformedAttributes(t *testing.T) {
	valid := signatureAttribute([]byte("abcdef"), 7, AlgoSHA256)

	tests := []struct {
		name string
		raw  []byte
		want error
	}{
		{
			name: "truncated header",
			raw:  valid[:HeaderSize-1],
			want: ErrLengthMismatch,
		},
		{
			name: "declared beyond actual",
			raw: func() []byte {
				raw := append([]byte(nil), valid...)
				binary.BigEndian.PutUint16(raw[7:9], uint16(len(valid)))
				return raw
			}(),
			want: ErrLengthMismatch,
		},
		{
			name: "declared u16 max",
			raw: func() []byte {
				raw := append([]byte(nil), valid...)
				binary.BigEndian.PutUint16(raw[7:9], ^uint16(0))
				return raw
			}(),
			want: ErrLengthMismatch,
		},
		{
			name: "trailing bytes",
			raw:  append(append([]byte(nil), valid...), 0x00),
			want: ErrLengthMismatch,
		},
		{
			name: "empty signature",
			raw:  signatureAttribute(nil, 7, AlgoSHA256),
			want: ErrEmptySignature,
		},
		{
			name: "wrong version",
			raw: func() []byte {
				raw := append([]byte(nil), valid...)
				raw[1] = 1
				return raw
			}(),
			want: ErrUnsupportedVersion,
		},
		{
			name: "unknown algorithm",
			raw: func() []byte {
				raw := append([]byte(nil), valid...)
				raw[2] = 0xff
				return raw
			}(),
			want: ErrUnknownAlgorithm,
		},
		{
			name: "unknown type",
			raw:  []byte{0x7f, 2, 4, 0, 0, 0, 7, 0, 0},
			want: ErrUnknownAttributeType,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSignature(test.raw)
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseDigestOnlyFormat(t *testing.T) {
	digest := bytes.Repeat([]byte{0xab}, digestSizes[AlgoSHA256])
	attr, err := Parse(digestAttribute(AlgoSHA256, digest))
	if err != nil {
		t.Fatalf("Parse digest: %v", err)
	}
	if attr.Type != TypeDigestNG {
		t.Fatalf("type = %v, want digest-ng", attr.Type)
	}
	if !bytes.Equal(attr.Digest, digest) {
		t.Fatal("digest was not decoded")
	}
}

func TestDigestOnlyRejectedWhenSignatureRequired(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "digest-only", "executable")
	digest := bytes.Repeat([]byte{0x42}, digestSizes[AlgoSHA256])
	setTestAttr(t, path, digestAttribute(AlgoSHA256, digest))

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	result, err := evaluator.Evaluate(path)
	if !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("error = %v, want %v", err, ErrSignatureRequired)
	}
	if result.Allowed {
		t.Fatal("digest-only attribute was allowed on a signature-required path")
	}
}

func TestDigestOnlyAllowedWhenSignatureNotRequired(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "digest-ok", "data")
	digest := bytes.Repeat([]byte{0x42}, digestSizes[AlgoSHA256])
	setTestAttr(t, path, digestAttribute(AlgoSHA256, digest))

	evaluator := testEvaluator(nil, NoPaths)
	result, err := evaluator.Evaluate(path)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !result.Allowed || result.Type != TypeDigestNG {
		t.Fatalf("unexpected result: %#v", result)
	}
}

func TestDeclaredLengthBeyondActualRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "too-long", "executable")
	raw := signatureAttribute([]byte("short"), 7, AlgoSHA256)
	binary.BigEndian.PutUint16(raw[7:9], 4096)
	setTestAttr(t, path, raw)

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	result, err := evaluator.Evaluate(path)
	if !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("error = %v, want %v", err, ErrLengthMismatch)
	}
	if result.Allowed {
		t.Fatal("out-of-bounds length was allowed")
	}
}

func TestUntrustedKeyIDRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "untrusted", "executable")
	setTestAttr(t, path, signatureAttribute([]byte("signed"), 0xdeadbeef, AlgoSHA256))

	evaluator := testEvaluator([]uint32{0x12345678}, AllPaths)
	result, err := evaluator.Evaluate(path)
	if !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("error = %v, want %v", err, ErrUntrustedKeyID)
	}
	if result.Allowed {
		t.Fatal("signature from an untrusted key was allowed")
	}
}

func TestTrustedSignatureIsAllowedAndCached(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "trusted", "executable")
	setTestAttr(t, path, signatureAttribute([]byte("signed"), 7, AlgoSHA256))

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	first, err := evaluator.Evaluate(path)
	if err != nil {
		t.Fatalf("first Evaluate: %v", err)
	}
	if !first.Allowed || first.Cached {
		t.Fatalf("unexpected first result: %#v", first)
	}

	second, err := evaluator.Evaluate(path)
	if err != nil {
		t.Fatalf("second Evaluate: %v", err)
	}
	if !second.Allowed || !second.Cached {
		t.Fatalf("unexpected second result: %#v", second)
	}
	if evaluator.CacheSize() != 1 {
		t.Fatalf("cache size = %d, want 1", evaluator.CacheSize())
	}
}

func TestWriteInvalidatesCachedEvaluation(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "rewritten", "first-content")
	setTestAttr(t, path, signatureAttribute([]byte("signed"), 7, AlgoSHA256))

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	if result, err := evaluator.Evaluate(path); err != nil || !result.Allowed {
		t.Fatalf("initial Evaluate = %#v, %v", result, err)
	}
	if result, err := evaluator.Evaluate(path); err != nil || !result.Cached {
		t.Fatalf("expected cached allow, got %#v, %v", result, err)
	}

	// Same-size rewrite: size alone must not be used to validate the cache.
	if err := os.WriteFile(path, []byte("other-content"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	result, err := evaluator.Evaluate(path)
	if err != nil {
		t.Fatalf("Evaluate after write: %v", err)
	}
	if !result.Allowed || result.Cached {
		t.Fatalf("write did not invalidate cache: %#v", result)
	}

	// Changing only the attribute must also invalidate the cached allow.
	digest := bytes.Repeat([]byte{0x11}, digestSizes[AlgoSHA256])
	setTestAttr(t, path, digestAttribute(AlgoSHA256, digest))
	result, err = evaluator.Evaluate(path)
	if !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("error after attribute replacement = %v, want %v", err, ErrSignatureRequired)
	}
	if result.Allowed || result.Cached {
		t.Fatalf("attribute replacement did not invalidate cache: %#v", result)
	}
}

func TestHardLinkSharesInodeInvalidation(t *testing.T) {
	dir := t.TempDir()
	original := writeTestFile(t, dir, "original", "executable")
	link := filepath.Join(dir, "hardlink")
	if err := os.Link(original, link); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	setTestAttr(t, original, signatureAttribute([]byte("signed"), 7, AlgoSHA256))

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	if result, err := evaluator.Evaluate(original); err != nil || !result.Allowed {
		t.Fatalf("evaluate original = %#v, %v", result, err)
	}
	if result, err := evaluator.Evaluate(link); err != nil || !result.Allowed || result.Cached {
		t.Fatalf("evaluate hard link = %#v, %v", result, err)
	}
	if evaluator.CacheSize() != 2 {
		t.Fatalf("cache size = %d, want 2 path entries", evaluator.CacheSize())
	}

	// A write through the original path changes the shared inode and must
	// invalidate the decision cached for the other path too.
	if err := os.WriteFile(original, []byte("modified"), 0o600); err != nil {
		t.Fatalf("write through original: %v", err)
	}
	result, err := evaluator.Evaluate(link)
	if err != nil {
		t.Fatalf("evaluate hard link after write: %v", err)
	}
	if result.Cached {
		t.Fatalf("hard-link cache survived a write: %#v", result)
	}

	// Replacing the shared attribute through one path must affect the other.
	setTestAttr(t, link, signatureAttribute([]byte("signed"), 99, AlgoSHA256))
	if _, err := evaluator.Evaluate(original); !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("original error = %v, want %v", err, ErrUntrustedKeyID)
	}
	if _, err := evaluator.Evaluate(link); !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("hard link error = %v, want %v", err, ErrUntrustedKeyID)
	}
}

func TestHardLinkPathsUseIndependentPolicy(t *testing.T) {
	dir := t.TempDir()
	protected := writeTestFile(t, dir, "protected", "executable")
	unprotected := filepath.Join(dir, "unprotected")
	if err := os.Link(protected, unprotected); err != nil {
		t.Fatalf("hard link: %v", err)
	}
	digest := bytes.Repeat([]byte{0x33}, digestSizes[AlgoSHA256])
	setTestAttr(t, protected, digestAttribute(AlgoSHA256, digest))

	evaluator := testEvaluator(nil, PathMatcher(protected))
	if _, err := evaluator.Evaluate(protected); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("protected path error = %v, want %v", err, ErrSignatureRequired)
	}
	result, err := evaluator.Evaluate(unprotected)
	if err != nil || !result.Allowed {
		t.Fatalf("unprotected hard link = %#v, %v", result, err)
	}
}

func TestAttributeReplacementInvalidatesCachedDenial(t *testing.T) {
	dir := t.TempDir()
	path := writeTestFile(t, dir, "promoted", "executable")
	digest := bytes.Repeat([]byte{0x55}, digestSizes[AlgoSHA256])
	setTestAttr(t, path, digestAttribute(AlgoSHA256, digest))

	evaluator := testEvaluator([]uint32{7}, AllPaths)
	if _, err := evaluator.Evaluate(path); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("initial error = %v, want %v", err, ErrSignatureRequired)
	}
	if _, err := evaluator.Evaluate(path); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("cached error = %v, want %v", err, ErrSignatureRequired)
	}

	setTestAttr(t, path, signatureAttribute([]byte("signed"), 7, AlgoSHA256))
	result, err := evaluator.Evaluate(path)
	if err != nil {
		t.Fatalf("Evaluate after attribute replacement: %v", err)
	}
	if !result.Allowed || result.Cached {
		t.Fatalf("cached denial survived attribute replacement: %#v", result)
	}
}

func TestPathMatcher(t *testing.T) {
	matcher := PathMatcher("/usr/bin/", "*.so", "/opt/secret")
	tests := map[string]bool{
		"/usr/bin/ls":       true,
		"/usr/bin":          false,
		"/lib/libcrypto.so": true,
		"/opt/secret":       true,
		"/opt/secret/other": false,
		"/tmp/other":        false,
	}
	for path, want := range tests {
		if got := matcher(path); got != want {
			t.Errorf("matcher(%q) = %v, want %v", path, got, want)
		}
	}
}

func FuzzParseNeverPanics(f *testing.F) {
	valid := signatureAttribute([]byte("signed"), 7, AlgoSHA256)
	f.Add([]byte{})
	f.Add(valid[:HeaderSize-1])
	f.Add(valid)
	f.Add(append(append([]byte(nil), valid...), 0x00))
	f.Add(digestAttribute(AlgoSHA256, bytes.Repeat([]byte{0xaa}, 32)))

	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = Parse(raw)
		_, _ = ParseSignature(raw)
	})
}
