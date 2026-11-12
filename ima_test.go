package netsec

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func makeAttribute(typ, version, algo uint8, keyID uint32, payload []byte) []byte {
	return makeAttributeWithLength(typ, version, algo, keyID, uint16(len(payload)), payload)
}

func makeAttributeWithLength(typ, version, algo uint8, keyID uint32, declared uint16, payload []byte) []byte {
	raw := make([]byte, IMAHeaderSize+len(payload))
	raw[0] = typ
	raw[1] = version
	raw[2] = algo
	binary.BigEndian.PutUint32(raw[3:7], keyID)
	binary.BigEndian.PutUint16(raw[7:9], declared)
	copy(raw[IMAHeaderSize:], payload)
	return raw
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestParseAttributeRejectsLengthOverrun(t *testing.T) {
	payload := bytesOfLen(8)

	tests := []struct {
		name string
		raw  []byte
		want error
	}{
		{
			name: "declared longer than actual",
			raw:  makeAttributeWithLength(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, 0xffff, payload),
			want: ErrLengthMismatch,
		},
		{
			name: "declared shorter than actual",
			raw:  makeAttributeWithLength(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, uint16(len(payload)-1), payload),
			want: ErrLengthMismatch,
		},
		{
			name: "truncated header",
			raw:  []byte{TypeSignature, IMAVersion2, AlgorithmSHA256, 0, 0, 0, 7},
			want: ErrShortAttribute,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseAttribute(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("ParseAttribute error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseAttributeRejectsUnsupportedFields(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want error
	}{
		{
			name: "version other than two",
			raw:  makeAttribute(TypeSignature, 1, AlgorithmSHA256, 7, []byte("sig")),
			want: ErrBadVersion,
		},
		{
			name: "unknown algorithm",
			raw:  makeAttribute(TypeSignature, IMAVersion2, 0xff, 7, []byte("sig")),
			want: ErrUnknownAlgorithm,
		},
		{
			name: "unknown type",
			raw:  makeAttribute(0xff, IMAVersion2, AlgorithmSHA256, 7, []byte("sig")),
			want: ErrUnknownType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseAttribute(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("ParseAttribute error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestDigestOnlyAttributeRejectedOnSignedPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "data")

	store := NewMapAttributeStore()
	store.Set(path, XattrName, makeAttribute(TypeDigest, IMAVersion2, AlgorithmSHA256, 7, bytesOfLen(32)))

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(path); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("digest attribute error = %v, want ErrSignatureRequired", err)
	}
	if err := eval.Evaluate(path); !errors.Is(err, ErrDigestNotAllowed) {
		t.Fatalf("cached digest attribute error = %v, want ErrDigestNotAllowed", err)
	}

	// A non-signed path may carry a digest-only attribute.
	permissive := NewEvaluatorWithReader(NewPolicy(false, 7), store)
	if err := permissive.Evaluate(path); err != nil {
		t.Fatalf("digest on unsigned path: %v", err)
	}
}

func TestLengthOverrunIsRejectedByEvaluator(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "data")

	store := NewMapAttributeStore()
	store.Set(path, XattrName,
		makeAttributeWithLength(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, 0xffff, []byte("short")))

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(path); !errors.Is(err, ErrLengthMismatch) {
		t.Fatalf("overrun attribute error = %v, want ErrLengthMismatch", err)
	}
}

func TestUntrustedKeyIDIsRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "data")

	store := NewMapAttributeStore()
	store.Set(path, XattrName, makeAttribute(TypeSignature, IMAVersion2, AlgorithmSHA256, 42, []byte("sig")))

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(path); !errors.Is(err, ErrUntrustedKeyID) {
		t.Fatalf("untrusted key error = %v, want ErrUntrustedKeyID", err)
	}

	trusted := NewEvaluatorWithReader(RequireSignatures(42), store)
	if err := trusted.Evaluate(path); err != nil {
		t.Fatalf("trusted key: %v", err)
	}
}

func TestWriteInvalidatesCachedDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "first")

	store := NewMapAttributeStore()
	store.Set(path, XattrName, makeAttribute(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, []byte("sig")))

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(path); err != nil {
		t.Fatalf("first evaluation: %v", err)
	}
	if err := eval.Evaluate(path); err != nil {
		t.Fatalf("second evaluation: %v", err)
	}
	stats := eval.Stats()
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("cache stats before write = %+v, want 1 hit and 1 miss", stats)
	}

	// The attribute is unchanged. Only the file write can invalidate the
	// cached decision.
	writeFile(t, path, "second content that is larger")
	if err := eval.Evaluate(path); err != nil {
		t.Fatalf("evaluation after write: %v", err)
	}
	stats = eval.Stats()
	if stats.Misses != 2 {
		t.Fatalf("cache stats after write = %+v, want 2 misses", stats)
	}
}

func TestAttributeReplacementInvalidatesCachedDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "data")

	store := NewMapAttributeStore()
	store.Set(path, XattrName, makeAttribute(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, []byte("sig")))

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(path); err != nil {
		t.Fatalf("first evaluation: %v", err)
	}

	store.Set(path, XattrName, makeAttribute(TypeDigest, IMAVersion2, AlgorithmSHA256, 7, bytesOfLen(32)))
	if err := eval.Evaluate(path); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("after attribute replacement error = %v, want ErrSignatureRequired", err)
	}
}

func TestHardLinkSharesCacheAndInvalidatesTogether(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	writeFile(t, first, "data")
	if err := os.Link(first, second); err != nil {
		t.Fatalf("hard link: %v", err)
	}

	store := NewMapAttributeStore()
	attr := makeAttribute(TypeSignature, IMAVersion2, AlgorithmSHA256, 7, []byte("sig"))
	store.Set(first, XattrName, attr)
	store.Set(second, XattrName, attr)

	eval := NewEvaluatorWithReader(RequireSignatures(7), store)
	if err := eval.Evaluate(first); err != nil {
		t.Fatalf("first path: %v", err)
	}
	if err := eval.Evaluate(second); err != nil {
		t.Fatalf("hard-linked path: %v", err)
	}
	stats := eval.Stats()
	if stats.Hits != 1 || stats.Misses != 1 {
		t.Fatalf("hard-link cache stats = %+v, want 1 hit and 1 miss", stats)
	}

	// Writing through the second name changes the inode shared by both paths,
	// so the decision cached through the first name must no longer be reused.
	writeFile(t, second, "new data written through the other link")
	if err := eval.Evaluate(first); err != nil {
		t.Fatalf("first path after write through hard link: %v", err)
	}
	stats = eval.Stats()
	if stats.Misses != 2 {
		t.Fatalf("hard-link cache stats after write = %+v, want 2 misses", stats)
	}
}

func TestHardLinkCacheKeepsPerPathPolicy(t *testing.T) {
	dir := t.TempDir()
	exempt := filepath.Join(dir, "exempt-tool")
	required := filepath.Join(dir, "required-tool")
	writeFile(t, exempt, "data")
	if err := os.Link(exempt, required); err != nil {
		t.Fatalf("hard link: %v", err)
	}

	store := NewMapAttributeStore()
	digest := makeAttribute(TypeDigest, IMAVersion2, AlgorithmSHA256, 7, bytesOfLen(32))
	store.Set(exempt, XattrName, digest)
	store.Set(required, XattrName, digest)

	policy := NewPolicy(true, 7)
	policy.ExemptPaths = []string{exempt}
	eval := NewEvaluatorWithReader(policy, store)

	if err := eval.Evaluate(exempt); err != nil {
		t.Fatalf("exempt path: %v", err)
	}
	// The two paths share an inode, but only one requires a signature. A
	// digest-only decision cached for the exempt path must not authorize the
	// required path.
	if err := eval.Evaluate(required); !errors.Is(err, ErrSignatureRequired) {
		t.Fatalf("required hard link error = %v, want ErrSignatureRequired", err)
	}
}

func TestMissingAttribute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payload")
	writeFile(t, path, "data")

	eval := NewEvaluatorWithReader(RequireSignatures(7), NewMapAttributeStore())
	if err := eval.Evaluate(path); !errors.Is(err, ErrAttributeMissing) {
		t.Fatalf("missing attribute error = %v, want ErrAttributeMissing", err)
	}
}

func bytesOfLen(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}
