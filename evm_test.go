package netsec

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"testing"
)

var testHMACKey = []byte("test-only host evm hmac key 32b!")

func testAttributes() FileAttributes {
	return FileAttributes{
		IMA:        []byte{0x01, 0x04, 0x00, 0xde, 0xad, 0xbe, 0xef},
		SELinux:    []byte("system_u:object_r:etc_t:s0"),
		Inode:      0x1122334455667788,
		Generation: 0xaabbccdd,
		UID:        1000,
		GID:        1000,
		Mode:       0o100644,
	}
}

func cloneAttributes(attrs FileAttributes) FileAttributes {
	attrs.IMA = append([]byte(nil), attrs.IMA...)
	attrs.SELinux = append([]byte(nil), attrs.SELinux...)
	return attrs
}

func newHMACEvaluator(t *testing.T, key []byte) *Evaluator {
	t.Helper()
	evaluator, err := NewEvaluator(Config{HMACKey: key})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	return evaluator
}

func newPortableKey(t *testing.T, seedText string) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	seed := sha256.Sum256([]byte(seedText))
	priv := ed25519.NewKeyFromSeed(seed[:])
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("private key did not yield an Ed25519 public key")
	}
	return pub, priv
}

func signPortable(t *testing.T, priv ed25519.PrivateKey, attrs FileAttributes) []byte {
	t.Helper()
	xattr, err := SignPortableXattr(priv, attrs)
	if err != nil {
		t.Fatalf("SignPortableXattr: %v", err)
	}
	return xattr
}

func TestHMACAcceptsMatchingAttributes(t *testing.T) {
	attrs := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)
	xattr := NewHMACXattr(testHMACKey, attrs)

	result := evaluator.Evaluate(attrs, xattr)
	if !result.Allowed() {
		t.Fatalf("Evaluate() = %s (%s), want allowed", result.Status, result.Detail)
	}
	if result.EVMType != EVMTypeHMAC {
		t.Fatalf("EVMType = 0x%02x, want 0x%02x", result.EVMType, EVMTypeHMAC)
	}
	if err := evaluator.Verify(attrs, xattr); err != nil {
		t.Fatalf("Verify() = %v, want nil", err)
	}
}

func TestZeroValueResultIsDenied(t *testing.T) {
	var result Result
	if result.Allowed() {
		t.Fatal("zero-value Result must not allow access")
	}
	if err := result.Err(); !errors.Is(err, ErrNotEvaluated) {
		t.Fatalf("zero-value Result.Err() = %v, want ErrNotEvaluated", err)
	}
}

func TestHMACRejectsUIDChange(t *testing.T) {
	original := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)
	xattr := NewHMACXattr(testHMACKey, original)

	changed := cloneAttributes(original)
	changed.UID++

	result := evaluator.Evaluate(changed, xattr)
	if result.Status != StatusMismatch {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMismatch)
	}
	if err := evaluator.Verify(changed, xattr); !errors.Is(err, ErrIntegrityMismatch) {
		t.Fatalf("Verify() = %v, want ErrIntegrityMismatch", err)
	}
}

func TestHMACRejectsCopiedValueOnDifferentInode(t *testing.T) {
	original := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)
	xattr := NewHMACXattr(testHMACKey, original)

	copyOnAnotherInode := cloneAttributes(original)
	copyOnAnotherInode.Inode++
	copyOnAnotherInode.Generation++

	result := evaluator.Evaluate(copyOnAnotherInode, xattr)
	if result.Status != StatusMismatch {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMismatch)
	}
	if result.Allowed() {
		t.Fatal("a type-2 HMAC copied to another inode must not be accepted")
	}
}

func TestHMACAcceptsHardLinkWithSameInode(t *testing.T) {
	attrs := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)
	xattr := NewHMACXattr(testHMACKey, attrs)

	// A hard link has a different path but the same inode and i_generation,
	// so its EVM metadata is identical to the original path's metadata.
	viaOriginalPath := cloneAttributes(attrs)
	viaHardLinkPath := cloneAttributes(attrs)

	for name, candidate := range map[string]FileAttributes{
		"original path": viaOriginalPath,
		"hard link":     viaHardLinkPath,
	} {
		if result := evaluator.Evaluate(candidate, xattr); !result.Allowed() {
			t.Fatalf("%s: Evaluate() = %s (%s), want allowed", name, result.Status, result.Detail)
		}
	}
}

func TestHMACRejectsEveryBoundFieldChange(t *testing.T) {
	original := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)
	xattr := NewHMACXattr(testHMACKey, original)

	tests := []struct {
		name   string
		mutate func(*FileAttributes)
	}{
		{
			name: "ima value",
			mutate: func(attrs *FileAttributes) {
				attrs.IMA[0] ^= 0xff
			},
		},
		{
			name: "selinux value",
			mutate: func(attrs *FileAttributes) {
				attrs.SELinux = []byte("system_u:object_r:tmp_t:s0")
			},
		},
		{
			name: "gid",
			mutate: func(attrs *FileAttributes) {
				attrs.GID++
			},
		},
		{
			name: "mode",
			mutate: func(attrs *FileAttributes) {
				attrs.Mode ^= 0o002
			},
		},
		{
			name: "inode",
			mutate: func(attrs *FileAttributes) {
				attrs.Inode++
			},
		},
		{
			name: "i_generation",
			mutate: func(attrs *FileAttributes) {
				attrs.Generation++
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneAttributes(original)
			test.mutate(&changed)
			result := evaluator.Evaluate(changed, xattr)
			if result.Status != StatusMismatch {
				t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMismatch)
			}
		})
	}
}

func TestHMACFailsClosedWithoutKey(t *testing.T) {
	attrs := testAttributes()
	validXattr := NewHMACXattr(testHMACKey, attrs)

	evaluator, err := NewEvaluator(Config{})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	result := evaluator.Evaluate(attrs, validXattr)
	if result.Status != StatusKeyUnavailable {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusKeyUnavailable)
	}
	if result.Allowed() {
		t.Fatal("missing HMAC key must never allow access")
	}
	// Having security.ima present must not trigger an IMA-only fallback.
	if len(attrs.IMA) == 0 {
		t.Fatal("test setup must include a security.ima value")
	}
	if err := evaluator.Verify(attrs, validXattr); !errors.Is(err, ErrKeyUnavailable) {
		t.Fatalf("Verify() = %v, want ErrKeyUnavailable", err)
	}

	evaluator.SetHMACKey(testHMACKey)
	if result := evaluator.Evaluate(attrs, validXattr); !result.Allowed() {
		t.Fatalf("after loading key Evaluate() = %s, want allowed", result.Status)
	}

	evaluator.SetHMACKey(nil)
	if result := evaluator.Evaluate(attrs, validXattr); result.Status != StatusKeyUnavailable {
		t.Fatalf("after clearing key Evaluate() = %s, want %s", result.Status, StatusKeyUnavailable)
	}
}

func TestHMACRejectsWrongKey(t *testing.T) {
	attrs := testAttributes()
	evaluator := newHMACEvaluator(t, []byte("another host key"))
	xattr := NewHMACXattr(testHMACKey, attrs)

	if result := evaluator.Evaluate(attrs, xattr); result.Status != StatusMismatch {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMismatch)
	}
}

func TestHMACRejectsMalformedValues(t *testing.T) {
	attrs := testAttributes()
	evaluator := newHMACEvaluator(t, testHMACKey)

	tests := []struct {
		name  string
		xattr []byte
	}{
		{name: "empty", xattr: nil},
		{name: "type only", xattr: []byte{EVMTypeHMAC}},
		{name: "short digest", xattr: append([]byte{EVMTypeHMAC}, make([]byte, HMACDigestSize-1)...)},
		{name: "long digest", xattr: append([]byte{EVMTypeHMAC}, make([]byte, HMACDigestSize+1)...)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := evaluator.Evaluate(attrs, test.xattr)
			if result.Status != StatusMalformed {
				t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMalformed)
			}
		})
	}
}

func TestUnknownTypeRejected(t *testing.T) {
	attrs := testAttributes()
	evaluator, err := NewEvaluator(Config{})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	for _, typ := range []byte{0x00, 0x01, 0x03, 0x04, 0xff} {
		result := evaluator.Evaluate(attrs, []byte{typ, 0x00})
		if result.Status != StatusUnknownType {
			t.Fatalf("type 0x%02x: Evaluate() = %s, want %s", typ, result.Status, StatusUnknownType)
		}
		if err := evaluator.Verify(attrs, []byte{typ, 0x00}); !errors.Is(err, ErrUnknownType) {
			t.Fatalf("type 0x%02x: Verify() = %v, want ErrUnknownType", typ, err)
		}
	}
}

func TestPortableSignatureAcceptsMatchingAttributes(t *testing.T) {
	attrs := testAttributes()
	pub, priv := newPortableKey(t, "portable signing key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{pub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	xattr := signPortable(t, priv, attrs)

	if len(xattr) != 1+PortableSignatureSize || xattr[0] != EVMTypePortableSignature {
		t.Fatalf("portable xattr has length %d and type 0x%02x", len(xattr), xattr[0])
	}
	result := evaluator.Evaluate(attrs, xattr)
	if !result.Allowed() {
		t.Fatalf("Evaluate() = %s (%s), want allowed", result.Status, result.Detail)
	}
}

func TestPortableSignatureIgnoresInodeAndGeneration(t *testing.T) {
	original := testAttributes()
	pub, priv := newPortableKey(t, "portable signing key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{pub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	xattr := signPortable(t, priv, original)

	moved := cloneAttributes(original)
	moved.Inode += 4096
	moved.Generation++

	if result := evaluator.Evaluate(moved, xattr); !result.Allowed() {
		t.Fatalf("portable signature must survive inode/generation changes; got %s (%s)",
			result.Status, result.Detail)
	}
}

func TestPortableSignatureRejectsAttributeChange(t *testing.T) {
	original := testAttributes()
	pub, priv := newPortableKey(t, "portable signing key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{pub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	xattr := signPortable(t, priv, original)

	changed := cloneAttributes(original)
	changed.UID++

	result := evaluator.Evaluate(changed, xattr)
	if result.Status != StatusInvalidSignature {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusInvalidSignature)
	}
	if err := evaluator.Verify(changed, xattr); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("Verify() = %v, want ErrInvalidSignature", err)
	}
}

func TestPortableSignatureRejectsModifiedSignature(t *testing.T) {
	attrs := testAttributes()
	pub, priv := newPortableKey(t, "portable signing key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{pub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	xattr := signPortable(t, priv, attrs)
	xattr[len(xattr)-1] ^= 0x01

	if result := evaluator.Evaluate(attrs, xattr); result.Status != StatusInvalidSignature {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusInvalidSignature)
	}
}

func TestPortableSignatureFailsClosedWithoutTrustAnchor(t *testing.T) {
	attrs := testAttributes()
	_, priv := newPortableKey(t, "portable signing key")
	xattr := signPortable(t, priv, attrs)

	// Even with an HMAC key loaded, a type-5 value needs a trust anchor.
	evaluator := newHMACEvaluator(t, testHMACKey)
	result := evaluator.Evaluate(attrs, xattr)
	if result.Status != StatusKeyUnavailable {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusKeyUnavailable)
	}
	if result.Allowed() {
		t.Fatal("missing portable-signature trust key must never allow access")
	}

	pub, _ := newPortableKey(t, "portable signing key")
	if err := evaluator.AddTrustedPublicKey(pub); err != nil {
		t.Fatalf("AddTrustedPublicKey: %v", err)
	}
	if result := evaluator.Evaluate(attrs, xattr); !result.Allowed() {
		t.Fatalf("after loading trust anchor Evaluate() = %s, want allowed", result.Status)
	}
}

func TestPortableSignatureRejectsUntrustedKey(t *testing.T) {
	attrs := testAttributes()
	trustedPub, _ := newPortableKey(t, "trusted key")
	_, untrustedPriv := newPortableKey(t, "untrusted key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{trustedPub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	result := evaluator.Evaluate(attrs, signPortable(t, untrustedPriv, attrs))
	if result.Status != StatusInvalidSignature {
		t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusInvalidSignature)
	}
}

func TestPortableSignatureRejectsMalformedValues(t *testing.T) {
	attrs := testAttributes()
	pub, _ := newPortableKey(t, "portable signing key")
	evaluator, err := NewEvaluator(Config{TrustedPublicKeys: []ed25519.PublicKey{pub}})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}

	tests := []struct {
		name  string
		xattr []byte
	}{
		{name: "type only", xattr: []byte{EVMTypePortableSignature}},
		{name: "short signature", xattr: append([]byte{EVMTypePortableSignature}, make([]byte, PortableSignatureSize-1)...)},
		{name: "long signature", xattr: append([]byte{EVMTypePortableSignature}, make([]byte, PortableSignatureSize+1)...)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if result := evaluator.Evaluate(attrs, test.xattr); result.Status != StatusMalformed {
				t.Fatalf("Evaluate() = %s, want %s", result.Status, StatusMalformed)
			}
		})
	}
}

func TestEvaluatorCopiesKeyMaterial(t *testing.T) {
	attrs := testAttributes()
	key := append([]byte(nil), testHMACKey...)
	evaluator, err := NewEvaluator(Config{HMACKey: key})
	if err != nil {
		t.Fatalf("NewEvaluator: %v", err)
	}
	xattr := NewHMACXattr(key, attrs)

	// Mutating the caller's slice after construction must not change the key
	// used by the evaluator.
	for i := range key {
		key[i] ^= 0xff
	}
	if result := evaluator.Evaluate(attrs, xattr); !result.Allowed() {
		t.Fatalf("Evaluate() = %s, want allowed", result.Status)
	}
}

func TestHMACPayloadOrdering(t *testing.T) {
	attrs := FileAttributes{
		IMA:        []byte{0xaa, 0xbb},
		SELinux:    []byte{0xcc},
		Inode:      0x0102030405060708,
		Generation: 0x11121314,
		UID:        0x21222324,
		GID:        0x31323334,
		Mode:       0x41424344,
	}

	want := []byte{
		0x02, 0x00, 0x00, 0x00, 0xaa, 0xbb,
		0x01, 0x00, 0x00, 0x00, 0xcc,
		0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01,
		0x14, 0x13, 0x12, 0x11,
		0x24, 0x23, 0x22, 0x21,
		0x34, 0x33, 0x32, 0x31,
		0x44, 0x43, 0x42, 0x41,
	}
	if got := HMACPayload(attrs); !bytes.Equal(got, want) {
		t.Fatalf("HMACPayload() = %x, want %x", got, want)
	}

	portableWant := []byte{
		0x02, 0x00, 0x00, 0x00, 0xaa, 0xbb,
		0x01, 0x00, 0x00, 0x00, 0xcc,
		0x24, 0x23, 0x22, 0x21,
		0x34, 0x33, 0x32, 0x31,
		0x44, 0x43, 0x42, 0x41,
	}
	if got := PortableSignaturePayload(attrs); !bytes.Equal(got, portableWant) {
		t.Fatalf("PortableSignaturePayload() = %x, want %x", got, portableWant)
	}
}
