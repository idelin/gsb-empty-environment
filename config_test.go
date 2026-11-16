package netsec

import (
	"strings"
	"testing"
	"time"
)

const nestedInheritanceXML = `<?xml version="1.0" encoding="utf-8"?>
<network-security-config>
  <base-config cleartextTrafficPermitted="false">
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
    <pin-set expiration="2030-01-01">
      <pin digest="SHA-256">base-pin</pin>
    </pin-set>
  </base-config>

  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="true">example.com</domain>
    <trust-anchors>
      <certificates src="system"/>
      <certificates src="user"/>
    </trust-anchors>

    <domain-config>
      <domain includeSubdomains="true">api.example.com</domain>
      <pin-set expiration="2031-01-01">
        <pin digest="SHA-256">api-pin</pin>
      </pin-set>

      <domain-config>
        <domain>v2.api.example.com</domain>
      </domain-config>
    </domain-config>
  </domain-config>
</network-security-config>`

func TestNestedInheritanceAndMostSpecificDomain(t *testing.T) {
	cfg, err := Parse([]byte(nestedInheritanceXML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opts := Options{
		TargetSDK: 28,
		Now:       time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC),
	}

	tests := []struct {
		name          string
		host          string
		cleartext     bool
		system        bool
		user          bool
		matchedDomain string
		pin           string
	}{
		{
			name:      "unmatched host uses base config",
			host:      "other.test",
			cleartext: false,
			system:    true,
			pin:       "base-pin",
		},
		{
			name:          "domain config and its inherited values",
			host:          "www.example.com",
			cleartext:     true,
			system:        true,
			user:          true,
			matchedDomain: "example.com",
			pin:           "base-pin",
		},
		{
			name:          "nested config overrides pin set",
			host:          "api.example.com",
			cleartext:     true,
			system:        true,
			user:          true,
			matchedDomain: "api.example.com",
			pin:           "api-pin",
		},
		{
			name:          "deepest exact domain inherits nested config",
			host:          "v2.api.example.com",
			cleartext:     true,
			system:        true,
			user:          true,
			matchedDomain: "v2.api.example.com",
			pin:           "api-pin",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, err := cfg.Evaluate(test.host, opts)
			if err != nil {
				t.Fatalf("Evaluate(%q): %v", test.host, err)
			}
			if policy.CleartextTrafficPermitted != test.cleartext {
				t.Errorf("cleartext = %v, want %v", policy.CleartextTrafficPermitted, test.cleartext)
			}
			if policy.TrustAnchors.System != test.system {
				t.Errorf("system trust = %v, want %v", policy.TrustAnchors.System, test.system)
			}
			if policy.TrustAnchors.User != test.user {
				t.Errorf("user trust = %v, want %v", policy.TrustAnchors.User, test.user)
			}
			if policy.MatchedDomain != test.matchedDomain {
				t.Errorf("matched domain = %q, want %q", policy.MatchedDomain, test.matchedDomain)
			}
			if !policy.Pinning || len(policy.Pins) != 1 {
				t.Fatalf("pinning = %v, pins = %#v, want one enforced pin", policy.Pinning, policy.Pins)
			}
			if policy.Pins[0].Value != test.pin {
				t.Errorf("pin = %q, want %q", policy.Pins[0].Value, test.pin)
			}
		})
	}
}

func TestDomainSpecificityAndIncludeSubdomains(t *testing.T) {
	const input = `<network-security-config>
  <base-config cleartextTrafficPermitted="false"/>
  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="true">example.com</domain>
    <domain-config cleartextTrafficPermitted="false">
      <domain>exact.example.com</domain>
    </domain-config>
  </domain-config>
</network-security-config>`

	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	opts := Options{TargetSDK: 28, Now: time.Now()}

	for _, test := range []struct {
		host      string
		cleartext bool
	}{
		{host: "exact.example.com", cleartext: false},
		{host: "EXACT.EXAMPLE.COM.", cleartext: false},
		{host: "https://exact.example.com:8443/path", cleartext: false},
		{host: "sub.exact.example.com", cleartext: true},
		{host: "example.com", cleartext: true},
		{host: "notexample.com", cleartext: false},
	} {
		policy, err := cfg.Evaluate(test.host, opts)
		if err != nil {
			t.Fatalf("Evaluate(%q): %v", test.host, err)
		}
		if policy.CleartextTrafficPermitted != test.cleartext {
			t.Errorf("Evaluate(%q) cleartext = %v, want %v", test.host, policy.CleartextTrafficPermitted, test.cleartext)
		}
	}
}

func TestPinSetExpiration(t *testing.T) {
	const input = `<network-security-config>
  <base-config>
    <pin-set expiration="2025-01-01">
      <pin digest="SHA-256">old-pin</pin>
    </pin-set>
  </base-config>
</network-security-config>`

	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	before := time.Date(2024, time.December, 31, 23, 59, 59, 0, time.UTC)
	policy, err := cfg.Evaluate("example.com", Options{TargetSDK: 28, Now: before})
	if err != nil {
		t.Fatalf("Evaluate before expiration: %v", err)
	}
	if !policy.Pinning || len(policy.Pins) != 1 {
		t.Fatalf("before expiration: pinning = %v, pins = %#v", policy.Pinning, policy.Pins)
	}

	after := time.Date(2025, time.January, 1, 0, 0, 1, 0, time.UTC)
	policy, err = cfg.Evaluate("example.com", Options{TargetSDK: 28, Now: after})
	if err != nil {
		t.Fatalf("Evaluate after expiration: %v", err)
	}
	if policy.Pinning {
		t.Error("pinning is still enforced after expiration")
	}
	if !policy.PinSetExpired {
		t.Error("PinSetExpired = false, want true")
	}
	if policy.PinSet == nil || len(policy.PinSet.Pins) != 1 {
		t.Errorf("configured pin set was not retained: %#v", policy.PinSet)
	}
	if len(policy.Pins) != 0 {
		t.Errorf("enforced pins after expiration = %#v, want none", policy.Pins)
	}
}

func TestExpiredNestedPinSetDoesNotFallBackToParent(t *testing.T) {
	const input = `<network-security-config>
  <base-config>
    <pin-set expiration="2035-01-01">
      <pin digest="SHA-256">parent-pin</pin>
    </pin-set>
  </base-config>
  <domain-config>
    <domain>api.example.com</domain>
    <pin-set expiration="2020-01-01">
      <pin digest="SHA-256">child-pin</pin>
    </pin-set>
  </domain-config>
</network-security-config>`

	policy, err := Evaluate([]byte(input), "api.example.com", Options{
		TargetSDK: 28,
		Now:       time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if policy.Pinning {
		t.Error("expired child pin set should disable pinning instead of inheriting the parent pin set")
	}
	if policy.PinSet == nil || len(policy.PinSet.Pins) != 1 || policy.PinSet.Pins[0].Value != "child-pin" {
		t.Errorf("effective configured pin set = %#v, want child-pin", policy.PinSet)
	}
}

func TestDebugOverridesOnlyInDebugBuilds(t *testing.T) {
	const input = `<network-security-config>
  <base-config>
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
  </base-config>
  <debug-overrides>
    <trust-anchors>
      <certificates src="user"/>
    </trust-anchors>
  </debug-overrides>
</network-security-config>`

	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	now := time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC)

	release, err := cfg.Evaluate("example.com", Options{TargetSDK: 28, Now: now})
	if err != nil {
		t.Fatalf("Evaluate release: %v", err)
	}
	if !release.TrustAnchors.System || release.TrustAnchors.User {
		t.Errorf("release trust anchors = %#v, want system only", release.TrustAnchors)
	}
	if release.DebugOverridesApplied {
		t.Error("debug overrides applied to release build")
	}

	debug, err := cfg.Evaluate("example.com", Options{TargetSDK: 28, Debug: true, Now: now})
	if err != nil {
		t.Fatalf("Evaluate debug: %v", err)
	}
	if !debug.TrustAnchors.System || !debug.TrustAnchors.User {
		t.Errorf("debug trust anchors = %#v, want system and user", debug.TrustAnchors)
	}
	if !debug.DebugOverridesApplied {
		t.Error("debug overrides were not applied to debug build")
	}
}

func TestPlatformDefaults(t *testing.T) {
	const input = `<network-security-config/>`
	cfg, err := Parse([]byte(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	now := time.Date(2026, time.November, 16, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		targetSDK int
		cleartext bool
		userCA    bool
	}{
		{targetSDK: 23, cleartext: true, userCA: true},
		{targetSDK: 24, cleartext: true, userCA: false},
		{targetSDK: 27, cleartext: true, userCA: false},
		{targetSDK: 28, cleartext: false, userCA: false},
		{targetSDK: 35, cleartext: false, userCA: false},
	}
	for _, test := range tests {
		policy, err := cfg.Evaluate("example.com", Options{TargetSDK: test.targetSDK, Now: now})
		if err != nil {
			t.Fatalf("Evaluate targetSdk %d: %v", test.targetSDK, err)
		}
		if policy.CleartextTrafficPermitted != test.cleartext {
			t.Errorf("targetSdk %d cleartext = %v, want %v", test.targetSDK, policy.CleartextTrafficPermitted, test.cleartext)
		}
		if policy.TrustAnchors.User != test.userCA {
			t.Errorf("targetSdk %d user CA = %v, want %v", test.targetSDK, policy.TrustAnchors.User, test.userCA)
		}
		if !policy.TrustAnchors.System {
			t.Errorf("targetSdk %d does not trust system CAs by default", test.targetSDK)
		}
	}
}

func TestExplicitTrustAnchorsOverridePlatformDefaults(t *testing.T) {
	const input = `<network-security-config>
  <base-config>
    <trust-anchors>
      <certificates src="user"/>
    </trust-anchors>
  </base-config>
</network-security-config>`
	policy, err := Evaluate([]byte(input), "example.com", Options{TargetSDK: 28})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if policy.TrustAnchors.System || !policy.TrustAnchors.User {
		t.Errorf("trust anchors = %#v, want explicit user-only set", policy.TrustAnchors)
	}
}

func TestParseRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "wrong root",
			input: `<network-security><base-config/></network-security>`,
			want:  "network-security-config",
		},
		{
			name: "invalid boolean",
			input: `<network-security-config>
  <base-config cleartextTrafficPermitted="sometimes"/>
</network-security-config>`,
			want: "cleartextTrafficPermitted",
		},
		{
			name: "invalid expiration",
			input: `<network-security-config>
  <base-config>
    <pin-set expiration="next year"><pin digest="SHA-256">x</pin></pin-set>
  </base-config>
</network-security-config>`,
			want: "expiration",
		},
		{
			name: "duplicate base config",
			input: `<network-security-config>
  <base-config/>
  <base-config/>
</network-security-config>`,
			want: "multiple base-config",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse([]byte(test.input))
			if err == nil {
				t.Fatal("Parse returned nil error")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("error = %q, want substring %q", err, test.want)
			}
		})
	}
}
