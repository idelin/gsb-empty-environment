package hsm

import (
	"errors"
	"testing"
)

func TestClassicWrapAndDecryptShortestAttack(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: false,
		AlwaysSensitive: true, NeverExtractable: true,
	})
	wrapper := aesSpec(t, "legacy-wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true, Decrypt: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{target, wrapper},
		Policy:      InsecurePolicy(),
		Permissions: PermissionsFor(APIWrapKey | APIDecrypt),
		Targets:     []ID{"master"},
		MaxDepth:    4,
		MaxStates:   5000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil {
		t.Fatal("expected a wrap-and-decrypt attack")
	}
	if path.Depth != 2 {
		t.Fatalf("attack depth = %d, want 2\n%s", path.Depth, path.Explain())
	}
	if path.Steps[0].Operation != OpWrapKey || path.Steps[1].Operation != OpDecrypt {
		t.Fatalf("unexpected attack path:\n%s", path.Explain())
	}
	if len(path.Leaked) != 1 || path.Leaked[0] != "master" {
		t.Fatalf("leaked = %v, want [master]", path.Leaked)
	}

	report, err := VerifyHardening(cfg)
	if err != nil {
		t.Fatalf("VerifyHardening: %v", err)
	}
	if report.Insecure.NoLeak {
		t.Fatal("insecure policy unexpectedly has no leak")
	}
	if !report.Hardened.NoLeak || !report.Hardened.Exhausted {
		t.Fatalf("hardened enumeration was not a zero-leak proof: %+v", report.Hardened)
	}
	if !report.Secure {
		t.Fatal("hardening report did not classify the policy as secure")
	}
}

func TestUnwrapTemplateRewriteAttack(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true, AlwaysSensitive: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true, Unwrap: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{target, wrapper},
		Policy:      InsecurePolicy(),
		Permissions: PermissionsFor(APIWrapKey | APIUnwrapKey),
		Targets:     []ID{"master"},
		MaxDepth:    3,
		MaxStates:   5000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil || path.Depth != 2 {
		t.Fatalf("expected a 2-step unwrap-template attack, got %+v", path)
	}
	if path.Steps[0].Operation != OpWrapKey || path.Steps[1].Operation != OpUnwrapKey {
		t.Fatalf("unexpected path:\n%s", path.Explain())
	}
	if path.Steps[1].Template == nil || path.Steps[1].Template.Attributes.Sensitive {
		t.Fatalf("unwrap template did not downgrade sensitivity: %+v", path.Steps[1].Template)
	}

	hardened := cfg
	hardened.Policy = HardenedPolicy()
	report, err := EnumerateAttacks(hardened)
	if err != nil {
		t.Fatalf("EnumerateAttacks: %v", err)
	}
	if !report.NoLeak || !report.Exhausted {
		t.Fatalf("hardened template attack proof failed: %+v", report)
	}
}

func TestSetAttributeRoleEscalationAttack(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true, AlwaysSensitive: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	cfg := AttackConfig{
		Keys:   []KeySpec{target, wrapper},
		Policy: InsecurePolicy(),
		Permissions: PermissionsFor(
			APISetAttributeValue | APIWrapKey | APIDecrypt,
		),
		Targets:   []ID{"master"},
		MaxDepth:  4,
		MaxStates: 20000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil {
		t.Fatal("expected attribute-escalation attack")
	}
	if path.Depth != 3 {
		t.Fatalf("attack depth = %d, want 3\n%s", path.Depth, path.Explain())
	}
	if path.Steps[0].Operation != OpSetAttributeValue ||
		path.Steps[1].Operation != OpWrapKey ||
		path.Steps[2].Operation != OpDecrypt {
		t.Fatalf("unexpected path:\n%s", path.Explain())
	}

	hardened := cfg
	hardened.Policy = HardenedPolicy()
	report, err := EnumerateAttacks(hardened)
	if err != nil {
		t.Fatalf("EnumerateAttacks: %v", err)
	}
	if !report.NoLeak || !report.Exhausted {
		t.Fatalf("hardened role-escalation proof failed: %+v", report)
	}
}

func TestDuplicateMaterialRoleSplitAttack(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true, AlwaysSensitive: true,
	})
	wrapOnly := KeySpec{
		ID: "wrap-only", Class: ClassSecretKey, Type: TypeAES,
		Material:   append([]byte(nil), target.Material...),
		Attributes: Attributes{Sensitive: true, Wrap: true},
	}
	decryptOnly := KeySpec{
		ID: "decrypt-only", Class: ClassSecretKey, Type: TypeAES,
		Material:   append([]byte(nil), target.Material...),
		Attributes: Attributes{Sensitive: true, Decrypt: true},
	}
	// Use a different material for the target so it remains protected.
	target.Material[0] ^= 0xff
	wrapOnly.Material[0] ^= 0xaa
	decryptOnly.Material = append([]byte(nil), wrapOnly.Material...)

	cfg := AttackConfig{
		Keys:        []KeySpec{target, wrapOnly, decryptOnly},
		Policy:      InsecurePolicy(),
		Permissions: PermissionsFor(APIWrapKey | APIDecrypt),
		Targets:     []ID{"master"},
		MaxDepth:    3,
		MaxStates:   5000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil || path.Depth != 2 {
		t.Fatalf("expected duplicate-material split attack, got %+v", path)
	}
	if path.Steps[0].Operation != OpWrapKey || path.Steps[1].Operation != OpDecrypt {
		t.Fatalf("unexpected path:\n%s", path.Explain())
	}

	strictCfg := cfg
	strictCfg.Policy = HardenedPolicy()
	if _, err := FindAttack(strictCfg); !errors.Is(err, ErrPolicy) {
		t.Fatalf("strict duplicate-material error = %v, want ErrPolicy", err)
	}
}

func TestAttackSearchRespectsAPISubset(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true, Decrypt: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{target, wrapper},
		Policy:      InsecurePolicy(),
		Permissions: PermissionsFor(APIDecrypt),
		Targets:     []ID{"master"},
		MaxDepth:    4,
		MaxStates:   1000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path != nil {
		t.Fatalf("search used an API outside the granted subset:\n%s", path.Explain())
	}
}

func TestBrokenDeriveMaterialCopyAttack(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: false, Derive: true,
		AlwaysSensitive: true, NeverExtractable: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{target},
		Policy:      InsecurePolicy(),
		Permissions: PermissionsFor(APIDeriveKey),
		Targets:     []ID{"master"},
		MaxDepth:    2,
		MaxStates:   1000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil || path.Depth != 1 || path.Steps[0].Operation != OpDeriveKey {
		t.Fatalf("expected one-step derive-material leak, got %+v", path)
	}
	hardened := cfg
	hardened.Policy = HardenedPolicy()
	report, err := EnumerateAttacks(hardened)
	if err != nil {
		t.Fatalf("EnumerateAttacks: %v", err)
	}
	if !report.NoLeak || !report.Exhausted {
		t.Fatalf("hardened derive proof failed: %+v", report)
	}
}

func TestSearchDepthBoundAndTruncationReporting(t *testing.T) {
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true,
	})
	wrapper := aesSpec(t, "wrapper", 0x22, Attributes{
		Sensitive: true, Wrap: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{target, wrapper},
		Policy:      HardenedPolicy(),
		Permissions: PermissionsFor(APIWrapKey),
		Targets:     []ID{"master"},
		MaxDepth:    MaxSearchDepth + 1,
	}
	if _, err := FindAttack(cfg); !errors.Is(err, ErrPolicy) {
		t.Fatalf("depth bound error = %v, want ErrPolicy", err)
	}

	cfg.MaxDepth = 3
	cfg.MaxStates = 1
	report, err := EnumerateAttacks(cfg)
	if err != nil {
		t.Fatalf("EnumerateAttacks: %v", err)
	}
	if !report.Truncated || report.Exhausted {
		t.Fatalf("truncation not reported: %+v", report)
	}
}

func TestKnownDerivedWrapperIsDiscovered(t *testing.T) {
	base := aesSpec(t, "derive-base", 0x22, Attributes{
		Sensitive: false, Extractable: true, Derive: true,
	})
	target := aesSpec(t, "master", 0x11, Attributes{
		Sensitive: true, Extractable: true, AlwaysSensitive: true,
	})
	cfg := AttackConfig{
		Keys:        []KeySpec{base, target},
		Policy:      HardenedPolicy(),
		Permissions: PermissionsFor(APIDeriveKey | APIWrapKey),
		Targets:     []ID{"master"},
		MaxDepth:    3,
		MaxStates:   5000,
	}
	path, err := FindAttack(cfg)
	if err != nil {
		t.Fatalf("FindAttack: %v", err)
	}
	if path == nil || path.Depth != 2 {
		t.Fatalf("expected derive-then-wrap attack, got %+v", path)
	}
	if path.Steps[0].Operation != OpDeriveKey || path.Steps[1].Operation != OpWrapKey {
		t.Fatalf("unexpected path:\n%s", path.Explain())
	}
}
