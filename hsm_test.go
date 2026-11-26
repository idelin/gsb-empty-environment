package hsm

import (
	"bytes"
	"errors"
	"testing"
)

func aesSpec(t *testing.T, id ID, fill byte, attrs Attributes) KeySpec {
	t.Helper()
	return KeySpec{
		ID:         id,
		Label:      string(id),
		Class:      ClassSecretKey,
		Type:       TypeAES,
		Material:   bytes.Repeat([]byte{fill}, 32),
		Attributes: attrs,
	}
}

func sensitiveAttrs() Attributes {
	return Attributes{
		Sensitive:       true,
		Extractable:     false,
		AlwaysSensitive: true,
	}
}

func TestWrapUnwrapWithTrackedTemplate(t *testing.T) {
	targetAttrs := Attributes{
		Sensitive:       true,
		Extractable:     true,
		Encrypt:         true,
		AlwaysSensitive: true,
	}
	target := aesSpec(t, "target", 0x11, targetAttrs)
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive:   true,
		Extractable: false,
		Wrap:        true,
		Unwrap:      true,
	})
	h, err := NewHardened(target, wrapper)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	blob, err := h.WrapKey("wrapper", "target", AESKeyWrap)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	meta, ok := h.InspectBlob(blob)
	if !ok || meta.SourceKey != "target" || meta.Mechanism != AESKeyWrap {
		t.Fatalf("unexpected blob metadata: %+v, ok=%t", meta, ok)
	}
	id, err := h.UnwrapKey("wrapper", blob, KeyTemplate{
		Label:      "target-clone",
		Class:      ClassSecretKey,
		Type:       TypeAES,
		Attributes: targetAttrs,
	}, AESKeyWrap)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	info, err := h.GetKey(id)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if info.Attributes != targetAttrs {
		t.Fatalf("unwrapped attributes = %v, want %v", info.Attributes, targetAttrs)
	}
}

func TestAsymmetricKeyPairIsWrappedAsOpaqueMaterial(t *testing.T) {
	privateMaterial := bytes.Repeat([]byte{0x5a}, 64)
	rsaKey := KeySpec{
		ID: "rsa", Label: "rsa", Class: ClassKeyPair, Type: TypeRSA,
		Material:       privateMaterial,
		PublicMaterial: []byte("public"),
		Attributes: Attributes{
			Sensitive: true, Extractable: true, Decrypt: true,
			AlwaysSensitive: true,
		},
	}
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true, Unwrap: true,
	})
	h, err := NewHardened(rsaKey, wrapper)
	if err != nil {
		t.Fatalf("NewHardened: %v", err)
	}
	blob, err := h.WrapKey("wrapper", "rsa", AESCBC)
	if err != nil {
		t.Fatalf("WrapKey CBC: %v", err)
	}
	id, err := h.UnwrapKey("wrapper", blob, KeyTemplate{
		Label: "rsa-clone", Class: ClassKeyPair, Type: TypeRSA,
		Attributes: rsaKey.Attributes,
	}, AESCBC)
	if err != nil {
		t.Fatalf("UnwrapKey CBC: %v", err)
	}
	info, err := h.GetKey(id)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if info.Class != ClassKeyPair || info.Type != TypeRSA || info.MaterialLength != len(privateMaterial) {
		t.Fatalf("unexpected asymmetric key info: %+v", info)
	}
}

func TestNonextractableWrapPolicy(t *testing.T) {
	target := aesSpec(t, "target", 0x11, sensitiveAttrs())
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true,
		Wrap:      true,
	})

	strict, err := NewHardened(target, wrapper)
	if err != nil {
		t.Fatalf("NewHardened: %v", err)
	}
	if _, err := strict.WrapKey("wrapper", "target", AESKeyWrap); !errors.Is(err, ErrNotExtractable) {
		t.Fatalf("strict wrap error = %v, want ErrNotExtractable", err)
	}

	legacy, err := New(InsecurePolicy(), target, wrapper)
	if err != nil {
		t.Fatalf("New insecure: %v", err)
	}
	if _, err := legacy.WrapKey("wrapper", "target", AESKeyWrap); err != nil {
		t.Fatalf("legacy wrap: %v", err)
	}
}

func TestMonotonicAttributeTransitions(t *testing.T) {
	key := aesSpec(t, "k", 0x11, sensitiveAttrs())
	h, err := NewHardened(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := h.SetAttributeValue("k", AttributePatch{Sensitive: BoolPtr(false)}); !errors.Is(err, ErrMonotonicity) {
		t.Fatalf("clear sensitive error = %v, want ErrMonotonicity", err)
	}
	if err := h.SetAttributeValue("k", AttributePatch{Extractable: BoolPtr(true)}); !errors.Is(err, ErrMonotonicity) {
		t.Fatalf("set extractable error = %v, want ErrMonotonicity", err)
	}
	if err := h.SetAttributeValue("k", AttributePatch{AlwaysSensitive: BoolPtr(false)}); !errors.Is(err, ErrMonotonicity) {
		t.Fatalf("clear history flag error = %v, want ErrMonotonicity", err)
	}
	if err := h.SetAttributeValue("k", AttributePatch{WrapWithTrusted: BoolPtr(true)}); err != nil {
		t.Fatalf("set wrap-with-trusted: %v", err)
	}
	if err := h.SetAttributeValue("k", AttributePatch{WrapWithTrusted: BoolPtr(false)}); !errors.Is(err, ErrMonotonicity) {
		t.Fatalf("clear wrap-with-trusted error = %v, want ErrMonotonicity", err)
	}
}

func TestRoleSeparationAndRolePromotion(t *testing.T) {
	target := aesSpec(t, "target", 0x11, Attributes{
		Sensitive: true, Extractable: true, AlwaysSensitive: true,
	})
	dual := aesSpec(t, "dual", 0x22, Attributes{
		Sensitive: true, Wrap: true, Decrypt: true,
	})
	h, err := NewHardened(target, dual)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := h.WrapKey("dual", "target", AESKeyWrap); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("dual wrap error = %v, want ErrRoleConflict", err)
	}
	blob, err := New(InsecurePolicy(), target, dual)
	if err != nil {
		t.Fatalf("New insecure: %v", err)
	}
	wrapped, err := blob.WrapKey("dual", "target", AESKeyWrap)
	if err != nil {
		t.Fatalf("insecure wrap: %v", err)
	}
	if _, err := h.Decrypt("dual", wrapped, AESKeyWrap); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("hardened decrypt error = %v, want ErrRoleConflict", err)
	}

	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	h, err = NewHardened(target, wrapper)
	if err != nil {
		t.Fatalf("New wrapper: %v", err)
	}
	err = h.SetAttributeValue("wrapper", AttributePatch{Decrypt: BoolPtr(true)})
	if !errors.Is(err, ErrRolePromotion) {
		t.Fatalf("role promotion error = %v, want ErrRolePromotion", err)
	}
}

func TestWrapWithTrustedEnforcement(t *testing.T) {
	target := aesSpec(t, "target", 0x11, Attributes{
		Sensitive: true, Extractable: true, WrapWithTrusted: true,
	})
	untrusted := aesSpec(t, "untrusted", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	trusted := aesSpec(t, "trusted", 0x33, Attributes{
		Sensitive: true, Wrap: true, Trusted: true,
	})
	h, err := NewHardened(target, untrusted, trusted)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := h.WrapKey("untrusted", "target", AESKeyWrap); !errors.Is(err, ErrUntrustedWrapper) {
		t.Fatalf("untrusted wrap error = %v, want ErrUntrustedWrapper", err)
	}
	if _, err := h.WrapKey("trusted", "target", AESKeyWrap); err != nil {
		t.Fatalf("trusted wrap: %v", err)
	}
}

func TestCopyObjectCannotExpandRoles(t *testing.T) {
	source := aesSpec(t, "source", 0x11, Attributes{
		Sensitive: true, Extractable: true, Wrap: true,
	})
	h, err := NewHardened(source)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	expanded := source.Attributes
	expanded.Decrypt = true
	_, err = h.CopyObject("source", KeyTemplate{
		ID: "copy", Class: ClassSecretKey, Type: TypeAES, Attributes: expanded,
	})
	if !errors.Is(err, ErrRolePromotion) {
		t.Fatalf("copy role expansion error = %v, want ErrRolePromotion", err)
	}
	id, err := h.CopyObject("source", KeyTemplate{
		ID: "copy", Class: ClassSecretKey, Type: TypeAES, Attributes: source.Attributes,
	})
	if err != nil {
		t.Fatalf("copy same policy: %v", err)
	}
	info, err := h.GetKey(id)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if info.Attributes != source.Attributes {
		t.Fatalf("copied attributes = %v, want %v", info.Attributes, source.Attributes)
	}
}

func TestSessionPermissions(t *testing.T) {
	target := aesSpec(t, "target", 0x11, Attributes{
		Sensitive: true, Extractable: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	h, err := NewHardened(target, wrapper)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	session := h.As(PermissionsFor(APIDecrypt))
	if _, err := session.WrapKey("wrapper", "target", AESKeyWrap); !errors.Is(err, ErrPermission) {
		t.Fatalf("permission error = %v, want ErrPermission", err)
	}
}

func TestMechanismAliasesAreAccepted(t *testing.T) {
	target := aesSpec(t, "target", 0x11, Attributes{
		Sensitive: true, Extractable: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	h, err := NewHardened(target, wrapper)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := h.WrapKey("wrapper", "target", Mechanism("AES-KW")); err != nil {
		t.Fatalf("alias wrap: %v", err)
	}
}

func TestMechanismConfusionRequiresCompatibilityFlag(t *testing.T) {
	target := aesSpec(t, "target", 0x11, Attributes{
		Sensitive: true, Extractable: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true, Unwrap: true,
	})
	safeish := Policy{
		AllowUnwrapTemplateDowngrade:   true,
		AllowRoleMutation:              true,
		AllowUntrustedPermissiveUnwrap: true,
	}
	h, err := New(safeish, target, wrapper)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	blob, err := h.WrapKey("wrapper", "target", AESKeyWrap)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	template := KeyTemplate{
		Class: ClassSecretKey, Type: TypeAES,
		Attributes: Attributes{Sensitive: false, Extractable: true},
	}
	if _, err := h.UnwrapKey("wrapper", blob, template, AESCBC); !errors.Is(err, ErrMechanismMismatch) {
		t.Fatalf("cross-mechanism unwrap error = %v, want ErrMechanismMismatch", err)
	}

	confused := safeish
	confused.AllowMechanismConfusion = true
	h, err = New(confused, target, wrapper)
	if err != nil {
		t.Fatalf("New confused: %v", err)
	}
	blob, err = h.WrapKey("wrapper", "target", AESKeyWrap)
	if err != nil {
		t.Fatalf("WrapKey confused: %v", err)
	}
	if _, err := h.UnwrapKey("wrapper", blob, template, AESCBC); err != nil {
		t.Fatalf("confused unwrap: %v", err)
	}
}

func TestEncryptDecryptMechanisms(t *testing.T) {
	key := aesSpec(t, "k", 0x11, Attributes{
		Sensitive: true, Encrypt: true, Decrypt: true,
	})
	h, err := NewHardened(key)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, mech := range []Mechanism{AESKeyWrap, AESCBC, AESGCM} {
		plaintext := bytes.Repeat([]byte{0x33}, 32)
		ciphertext, err := h.Encrypt("k", plaintext, mech)
		if err != nil {
			t.Fatalf("Encrypt %s: %v", mech, err)
		}
		opened, err := h.Decrypt("k", ciphertext, mech)
		if err != nil {
			t.Fatalf("Decrypt %s: %v", mech, err)
		}
		if !bytes.Equal(opened, plaintext) {
			t.Fatalf("%s round trip mismatch", mech)
		}
	}
}

func TestPolicyValidationHelpers(t *testing.T) {
	conflict := Attributes{
		Sensitive: true, Wrap: true, Decrypt: true,
	}
	if err := HardenedPolicy().ValidateAttributes(conflict); !errors.Is(err, ErrRoleConflict) {
		t.Fatalf("strict validation error = %v, want ErrRoleConflict", err)
	}
	if err := InsecurePolicy().ValidateAttributes(conflict); err != nil {
		t.Fatalf("insecure validation: %v", err)
	}

	old := Attributes{Sensitive: true, AlwaysSensitive: true}
	_, err := HardenedPolicy().ApplyPatch(old, AttributePatch{Sensitive: BoolPtr(false)})
	if !errors.Is(err, ErrMonotonicity) {
		t.Fatalf("ApplyPatch error = %v, want ErrMonotonicity", err)
	}
	next, err := InsecurePolicy().ApplyPatch(old, AttributePatch{Encrypt: BoolPtr(true)})
	if err != nil {
		t.Fatalf("ApplyPatch encrypt: %v", err)
	}
	if !next.Encrypt {
		t.Fatal("Encrypt patch was not applied")
	}

	badKey := KeySpec{
		ID: "bad", Type: TypeAES, Class: ClassSecretKey,
		Material: make([]byte, 7),
	}
	if err := badKey.Validate(); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad key validation error = %v, want ErrInvalidKey", err)
	}
}

func TestHardenedDerivationKeepsMaterialProtected(t *testing.T) {
	base := aesSpec(t, "base", 0x11, Attributes{
		Sensitive: false, Extractable: true, Derive: true,
	})
	h, err := NewHardened(base)
	if err != nil {
		t.Fatalf("NewHardened: %v", err)
	}
	_, err = h.DeriveKey("base", KeyTemplate{
		Class: ClassSecretKey, Type: TypeAES, MaterialLength: 32,
		Attributes: Attributes{Sensitive: false, Extractable: false, Wrap: true},
	}, HKDFSHA256)
	if !errors.Is(err, ErrPolicy) {
		t.Fatalf("permissive derivation error = %v, want ErrPolicy", err)
	}
	id, err := h.DeriveKey("base", KeyTemplate{
		Class: ClassSecretKey, Type: TypeAES, MaterialLength: 32,
		Attributes: Attributes{
			Sensitive: true, Extractable: false, Wrap: true,
			AlwaysSensitive: true, NeverExtractable: true,
		},
	}, HKDFSHA256)
	if err != nil {
		t.Fatalf("protected derivation: %v", err)
	}
	info, err := h.GetKey(id)
	if err != nil {
		t.Fatalf("GetKey: %v", err)
	}
	if !info.Attributes.Sensitive || info.Attributes.Extractable {
		t.Fatalf("derived key is not protected: %+v", info.Attributes)
	}
}
