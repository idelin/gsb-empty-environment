package keyusage

import (
	"errors"
	"testing"
)

func newKey(id string, alg Algorithm, purposes Purpose, parent *Key) *Key {
	return &Key{ID: id, Algorithm: alg, Purposes: purposes, Parent: parent}
}

func TestPurposeMisuse(t *testing.T) {
	policy := NewPolicy()

	signingKey := newKey("sig-1", AlgECDSAP256SHA256, PurposeSign|PurposeVerify, nil)
	encryptionKey := newKey("enc-1", AlgAES256GCM, PurposeEncrypt|PurposeDecrypt, nil)

	if err := policy.Check(signingKey, Operation{Purpose: PurposeSign, Target: TargetBusinessData}); err != nil {
		t.Fatalf("signing with a sign-purpose key must be allowed, got %v", err)
	}
	if err := policy.Check(encryptionKey, Operation{Purpose: PurposeEncrypt, Target: TargetBusinessData}); err != nil {
		t.Fatalf("encrypting with an encryption-purpose key must be allowed, got %v", err)
	}

	// A signing key must never be used for encryption.
	err := policy.Check(signingKey, Operation{Purpose: PurposeEncrypt, Target: TargetBusinessData})
	if !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("expected ErrPurposeNotAllowed for sign->encrypt, got %v", err)
	}

	// An encryption key must never be used for signing.
	err = policy.Check(encryptionKey, Operation{Purpose: PurposeSign, Target: TargetBusinessData})
	if !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("expected ErrPurposeNotAllowed for encrypt->sign, got %v", err)
	}

	// A key without the decrypt purpose cannot decrypt, even if it may encrypt.
	encryptOnly := newKey("enc-2", AlgRSA2048OAEPSHA256, PurposeEncrypt, nil)
	err = policy.Check(encryptOnly, Operation{Purpose: PurposeDecrypt, Target: TargetBusinessData})
	if !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("expected ErrPurposeNotAllowed for missing decrypt purpose, got %v", err)
	}

	// Every rejection must be audited; allowed calls must not be.
	sink := policy.AuditSink().(*MemoryAuditSink)
	if sink.Len() != 3 {
		t.Fatalf("expected 3 audited rejections, got %d", sink.Len())
	}
	for _, event := range sink.Events() {
		if !errors.Is(event.Err, ErrPurposeNotAllowed) {
			t.Fatalf("expected purpose violations in audit, got %v", event.Err)
		}
	}
}

func TestWeakAlgorithmRejectedAndRecorded(t *testing.T) {
	policy := NewPolicy()
	key := newKey("sig-weak", AlgRSA1024PKCS1v15SHA1, PurposeSign, nil)

	err := policy.Check(key, Operation{Purpose: PurposeSign, Target: TargetBusinessData})
	if !errors.Is(err, ErrWeakAlgorithm) {
		t.Fatalf("expected ErrWeakAlgorithm for RSA-1024/SHA-1, got %v", err)
	}

	// Per-call algorithm overrides are validated as well.
	strongKey := newKey("mac-1", AlgHMACSHA256, PurposeSign, nil)
	err = policy.Check(strongKey, Operation{
		Purpose:   PurposeSign,
		Target:    TargetBusinessData,
		Algorithm: AlgHMACSHA1,
	})
	if !errors.Is(err, ErrWeakAlgorithm) {
		t.Fatalf("expected ErrWeakAlgorithm for HMAC-SHA1 override, got %v", err)
	}

	// Weak algorithms stay rejected even if a custom whitelist includes them.
	loose := NewPolicy(WithWhitelist(AlgHMACSHA1))
	err = loose.Check(strongKey, Operation{
		Purpose:   PurposeSign,
		Target:    TargetBusinessData,
		Algorithm: AlgHMACSHA1,
	})
	if !errors.Is(err, ErrWeakAlgorithm) {
		t.Fatalf("weak classification must take precedence over whitelist, got %v", err)
	}

	// Unknown, non-weak algorithms fail the whitelist instead.
	err = policy.Check(strongKey, Operation{
		Purpose:   PurposeSign,
		Target:    TargetBusinessData,
		Algorithm: Algorithm("AES-128-GCM"),
	})
	if !errors.Is(err, ErrAlgorithmNotAllowed) {
		t.Fatalf("expected ErrAlgorithmNotAllowed for unknown algorithm, got %v", err)
	}

	sink := policy.AuditSink().(*MemoryAuditSink)
	events := sink.Events()
	if len(events) != 3 {
		t.Fatalf("expected 3 audited rejections, got %d", len(events))
	}
	if events[0].Reason == "" || events[0].Algorithm != AlgRSA1024PKCS1v15SHA1 {
		t.Fatalf("audit event must carry algorithm and explanation: %+v", events[0])
	}
}

func TestKeyDerivationInheritance(t *testing.T) {
	policy := NewPolicy()
	parent := newKey("root", AlgHKDFSHA256,
		PurposeDeriveKey|PurposeSign|PurposeEncrypt|PurposeDecrypt, nil)

	// A child may inherit only a subset of the parent's purposes.
	child, err := policy.Derive(parent, "child-sign", AlgECDSAP256SHA256, PurposeSign)
	if err != nil {
		t.Fatalf("subset derivation must succeed, got %v", err)
	}
	if !child.IsDerived() || child.Parent != parent {
		t.Fatal("derived key must reference its parent")
	}
	if err := policy.Check(child, Operation{Purpose: PurposeSign, Target: TargetBusinessData}); err != nil {
		t.Fatalf("inherited sign purpose must be usable, got %v", err)
	}

	// The child did not inherit encryption: using it for that is rejected.
	err = policy.Check(child, Operation{Purpose: PurposeEncrypt, Target: TargetBusinessData})
	if !errors.Is(err, ErrPurposeNotAllowed) {
		t.Fatalf("derived child must not use non-inherited purpose, got %v", err)
	}

	// Requesting purposes outside the parent's set is derivation abuse.
	_, err = policy.Derive(parent, "child-wrap", AlgAES256GCM, PurposeWrapKey)
	if !errors.Is(err, ErrDerivedPurposeExceedsParent) {
		t.Fatalf("expected ErrDerivedPurposeExceedsParent, got %v", err)
	}

	// A parent without derive_key cannot derive anything.
	signingKey := newKey("sig-only", AlgECDSAP256SHA256, PurposeSign, nil)
	_, err = policy.Derive(signingKey, "child", AlgECDSAP256SHA256, PurposeSign)
	if !errors.Is(err, ErrDerivationNotAllowed) {
		t.Fatalf("expected ErrDerivationNotAllowed, got %v", err)
	}

	// Weak algorithms are rejected at derivation time.
	_, err = policy.Derive(parent, "child-weak", Alg3DESCBC, PurposeEncrypt)
	if !errors.Is(err, ErrWeakAlgorithm) {
		t.Fatalf("expected ErrWeakAlgorithm at derivation, got %v", err)
	}

	// Defense in depth: a tampered child claiming a purpose beyond its parent
	// is rejected at call time even though it was not built via Derive.
	tampered := newKey("forged", AlgAES256GCM,
		PurposeEncrypt|PurposeWrapKey, parent)
	err = policy.Check(tampered, Operation{Purpose: PurposeWrapKey, Target: TargetKeyMaterial})
	if !errors.Is(err, ErrDerivedPurposeExceedsParent) {
		t.Fatalf("expected ErrDerivedPurposeExceedsParent for tampered child, got %v", err)
	}
}

func TestWrappingKeyMisuse(t *testing.T) {
	policy := NewPolicy()
	wrappingKey := newKey("kek-1", AlgAES256GCM, PurposeWrapKey|PurposeUnwrapKey, nil)

	// Wrapping and unwrapping key material is the intended use.
	if err := policy.Check(wrappingKey, Operation{
		Purpose: PurposeWrapKey, Target: TargetKeyMaterial,
	}); err != nil {
		t.Fatalf("wrapping key material must be allowed, got %v", err)
	}
	if err := policy.Check(wrappingKey, Operation{
		Purpose: PurposeUnwrapKey, Target: TargetKeyMaterial,
	}); err != nil {
		t.Fatalf("unwrapping key material must be allowed, got %v", err)
	}

	// Wrap/unwrap against business data is misuse.
	err := policy.Check(wrappingKey, Operation{
		Purpose: PurposeWrapKey, Target: TargetBusinessData,
	})
	if !errors.Is(err, ErrWrappingKeyMisuse) {
		t.Fatalf("expected ErrWrappingKeyMisuse wrapping data, got %v", err)
	}

	// A wrapping-only key must not encrypt or decrypt business data, even
	// though wrapping itself is an encryption operation.
	err = policy.Check(wrappingKey, Operation{
		Purpose: PurposeEncrypt, Target: TargetBusinessData,
	})
	if !errors.Is(err, ErrWrappingKeyMisuse) {
		t.Fatalf("expected ErrWrappingKeyMisuse encrypting data, got %v", err)
	}
	err = policy.Check(wrappingKey, Operation{
		Purpose: PurposeDecrypt, Target: TargetBusinessData,
	})
	if !errors.Is(err, ErrWrappingKeyMisuse) {
		t.Fatalf("expected ErrWrappingKeyMisuse decrypting data, got %v", err)
	}

	sink := policy.AuditSink().(*MemoryAuditSink)
	if sink.Len() != 3 {
		t.Fatalf("expected 3 wrapping-misuse audit events, got %d", sink.Len())
	}
	for _, event := range sink.Events() {
		if !errors.Is(event.Err, ErrWrappingKeyMisuse) {
			t.Fatalf("expected wrapping misuse in audit, got %v", event.Err)
		}
	}
}

func TestInvalidOperations(t *testing.T) {
	policy := NewPolicy()
	key := newKey("k", AlgAES256GCM, PurposeEncrypt, nil)

	if err := policy.Check(nil, Operation{Purpose: PurposeEncrypt}); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("expected ErrInvalidKey for nil key, got %v", err)
	}
	if err := policy.Check(key, Operation{}); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("expected ErrInvalidOperation for empty purpose, got %v", err)
	}
	if err := policy.Check(key, Operation{
		Purpose: PurposeEncrypt | PurposeDecrypt,
	}); !errors.Is(err, ErrInvalidOperation) {
		t.Fatalf("expected ErrInvalidOperation for combined purpose, got %v", err)
	}
}
