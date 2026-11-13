package netsec

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func mustParse(t *testing.T, document string, build Build) *Config {
	t.Helper()
	cfg, err := ParseConfig(strings.NewReader(document), build)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

func mustPolicy(t *testing.T, cfg *Config, host string) Policy {
	t.Helper()
	p, err := cfg.PolicyFor(host)
	if err != nil {
		t.Fatalf("PolicyFor(%q): %v", host, err)
	}
	return p
}

func assertPolicy(t *testing.T, got Policy, cleartext, system, user bool, domain string) {
	t.Helper()
	if got.CleartextTrafficPermitted != cleartext {
		t.Errorf("cleartext = %v, want %v", got.CleartextTrafficPermitted, cleartext)
	}
	if got.TrustAnchors.System != system || got.TrustAnchors.User != user {
		t.Errorf("trust anchors = %+v, want system=%v user=%v", got.TrustAnchors, system, user)
	}
	if got.MatchedDomain != domain {
		t.Errorf("matched domain = %q, want %q", got.MatchedDomain, domain)
	}
}

func TestNestedInheritanceAndMostSpecificDomain(t *testing.T) {
	const document = `
<network-security-config>
  <base-config cleartextTrafficPermitted="false">
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
    <pin-set expiration="2030-01-01">
      <pin digest="SHA-256">cGV0ZXJzLWNvbS1waW4=</pin>
    </pin-set>
  </base-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="true">example.com</domain>
    <domain-config>
      <domain includeSubdomains="true">api.example.com</domain>
      <domain-config cleartextTrafficPermitted="false">
        <domain>admin.api.example.com</domain>
        <trust-anchors>
          <certificates src="user"/>
        </trust-anchors>
      </domain-config>
    </domain-config>
  </domain-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	base := mustPolicy(t, cfg, "unrelated.example.org")
	assertPolicy(t, base, false, true, false, "")
	if !base.PinsEnforcedAt(at) {
		t.Fatal("base policy should inherit and enforce the pin set")
	}

	parent := mustPolicy(t, cfg, "www.example.com")
	assertPolicy(t, parent, true, true, false, "example.com")
	if !parent.PinsEnforcedAt(at) {
		t.Fatal("parent domain should inherit the base pin set")
	}

	child := mustPolicy(t, cfg, "api.example.com")
	assertPolicy(t, child, true, true, false, "api.example.com")
	if !child.PinsEnforcedAt(at) {
		t.Fatal("nested child should inherit the base pin set")
	}

	grandchild := mustPolicy(t, cfg, "admin.api.example.com")
	assertPolicy(t, grandchild, false, false, true, "admin.api.example.com")
	if len(grandchild.Pins.Pins) != 1 {
		t.Fatalf("grandchild pins = %d, want inherited 1", len(grandchild.Pins.Pins))
	}
	if !grandchild.PinsEnforcedAt(at) {
		t.Fatal("grandchild should inherit the base pin set")
	}

	// A suffix match must not cross label boundaries.
	notSubdomain := mustPolicy(t, cfg, "badexample.com")
	assertPolicy(t, notSubdomain, false, true, false, "")
}

func TestSiblingSpecificity(t *testing.T) {
	const document = `
<network-security-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="true">example.com</domain>
  </domain-config>
  <domain-config cleartextTrafficPermitted="false">
    <domain includeSubdomains="true">api.example.com</domain>
  </domain-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	if p := mustPolicy(t, cfg, "api.example.com"); p.CleartextTrafficPermitted {
		t.Fatalf("most specific sibling should win: got cleartext=%v, want false", p.CleartextTrafficPermitted)
	}
	if p := mustPolicy(t, cfg, "www.example.com"); !p.CleartextTrafficPermitted {
		t.Fatal("less specific sibling should allow cleartext")
	}
}

func TestCleartextAndUserCADefaultsByTargetSDK(t *testing.T) {
	const emptyDocument = `<network-security-config/>`

	tests := []struct {
		targetSDK int
		cleartext bool
		user      bool
	}{
		{targetSDK: 23, cleartext: true, user: true},
		{targetSDK: 24, cleartext: true, user: false},
		{targetSDK: 27, cleartext: true, user: false},
		{targetSDK: 28, cleartext: false, user: false},
		{targetSDK: 34, cleartext: false, user: false},
	}

	for _, tc := range tests {
		t.Run(strconv.Itoa(tc.targetSDK), func(t *testing.T) {
			cfg := mustParse(t, emptyDocument, Build{TargetSDK: tc.targetSDK})
			p := mustPolicy(t, cfg, "anything.example.com")
			if p.CleartextTrafficPermitted != tc.cleartext {
				t.Errorf("targetSdk %d cleartext = %v, want %v", tc.targetSDK, p.CleartextTrafficPermitted, tc.cleartext)
			}
			if !p.TrustAnchors.System {
				t.Errorf("targetSdk %d must trust system CAs", tc.targetSDK)
			}
			if p.TrustAnchors.User != tc.user {
				t.Errorf("targetSdk %d user CAs = %v, want %v", tc.targetSDK, p.TrustAnchors.User, tc.user)
			}
		})
	}
}

func TestExplicitBaseConfigOverridesDefaults(t *testing.T) {
	const document = `
<network-security-config>
  <base-config cleartextTrafficPermitted="true">
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
  </base-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 23})
	p := mustPolicy(t, cfg, "example.com")
	if !p.CleartextTrafficPermitted {
		t.Fatal("explicit cleartext=true should override the targetSdk default")
	}
	if p.TrustAnchors.User {
		t.Fatal("explicit system-only trust anchors should override the targetSdk<24 default")
	}

	cfg = mustParse(t, document, Build{TargetSDK: 34})
	if p := mustPolicy(t, cfg, "example.com"); !p.CleartextTrafficPermitted {
		t.Fatal("explicit cleartext=true should override the targetSdk>=28 default")
	}
}

func TestPinSetExpiration(t *testing.T) {
	const document = `
<network-security-config>
  <base-config>
    <pin-set expiration="2025-06-01">
      <pin digest="SHA-256">ZXhwaXJpbmctcGlu</pin>
    </pin-set>
  </base-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	p := mustPolicy(t, cfg, "example.com")

	before := time.Date(2025, 5, 31, 23, 59, 59, 0, time.UTC)
	if !p.PinsEnforcedAt(before) {
		t.Fatal("pin set should be enforced before its expiration")
	}
	if pins := p.EffectivePins(before); len(pins) != 1 || pins[0].Digest != "SHA-256" {
		t.Fatalf("effective pins before expiration = %+v", pins)
	}

	atExpiration := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	if p.PinsEnforcedAt(atExpiration) {
		t.Fatal("pin set must not be enforced once its expiration is reached")
	}
	if pins := p.EffectivePins(atExpiration); pins != nil {
		t.Fatalf("effective pins at expiration = %+v, want nil", pins)
	}

	after := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	if p.PinsEnforcedAt(after) {
		t.Fatal("expired pin set must not be enforced")
	}
	if len(p.Pins.Pins) != 1 {
		t.Fatal("expired pins should remain available for diagnostics")
	}
}

func TestPinSetWithoutExpirationIsPermanent(t *testing.T) {
	const document = `
<network-security-config>
  <base-config>
    <pin-set>
      <pin digest="sha256">cGVybWFuZW50LXBpbg==</pin>
    </pin-set>
  </base-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	p := mustPolicy(t, cfg, "example.com")
	if p.Pins.Expiration.IsZero() == false {
		t.Fatal("pin set without expiration must have a zero expiration")
	}
	if !p.PinsEnforcedAt(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("permanent pin set must remain enforced")
	}
	if p.Pins.Pins[0].Digest != "SHA-256" {
		t.Fatalf("digest = %q, want normalized SHA-256", p.Pins.Pins[0].Digest)
	}
}

func TestExpiredInheritedPinSetIsNotEnforced(t *testing.T) {
	const document = `
<network-security-config>
  <domain-config>
    <domain includeSubdomains="true">example.com</domain>
    <pin-set expiration="2020-01-01">
      <pin digest="SHA-256">b2xkLXBpbg==</pin>
    </pin-set>
    <domain-config>
      <domain>api.example.com</domain>
    </domain-config>
  </domain-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	p := mustPolicy(t, cfg, "api.example.com")
	if p.PinsEnforcedAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("nested config inherited an expired pin set but it is still enforced")
	}
}

func TestDebugOverridesOnlyApplyToDebugBuilds(t *testing.T) {
	const document = `
<network-security-config>
  <base-config>
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
  </base-config>
  <domain-config>
    <domain includeSubdomains="true">api.example.com</domain>
    <trust-anchors>
      <certificates src="system"/>
    </trust-anchors>
  </domain-config>
  <debug-overrides>
    <trust-anchors>
      <certificates src="user"/>
    </trust-anchors>
  </debug-overrides>
</network-security-config>`

	release := mustParse(t, document, Build{TargetSDK: 34, Debug: false})
	if p := mustPolicy(t, release, "api.example.com"); p.TrustAnchors.User {
		t.Fatal("debug-overrides must be ignored for release builds")
	}
	if p := release.BasePolicy(); p.TrustAnchors.User {
		t.Fatal("debug-overrides must be ignored for the base policy in release builds")
	}

	debug := mustParse(t, document, Build{TargetSDK: 34, Debug: true})
	if p := mustPolicy(t, debug, "api.example.com"); !p.TrustAnchors.User {
		t.Fatal("debug-overrides should add user CAs for debug builds")
	}
	if p := debug.BasePolicy(); !p.TrustAnchors.User {
		t.Fatal("debug-overrides should add user CAs to the base policy for debug builds")
	}
	if p := mustPolicy(t, debug, "api.example.com"); !p.TrustAnchors.System {
		t.Fatal("debug-overrides must add to, not replace, existing trust anchors")
	}
}

func TestDomainMatchingRules(t *testing.T) {
	const document = `
<network-security-config>
  <base-config cleartextTrafficPermitted="false"/>
  <domain-config cleartextTrafficPermitted="true">
    <domain includeSubdomains="false">exact.example.com</domain>
  </domain-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain>*.wild.example.com</domain>
  </domain-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain>suffix.example.com</domain>
  </domain-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	tests := []struct {
		host      string
		cleartext bool
		domain    string
	}{
		{"exact.example.com", true, "exact.example.com"},
		{"sub.exact.example.com", false, ""},
		{"wild.example.com", false, ""},
		{"a.wild.example.com", true, "wild.example.com"},
		{"deep.a.wild.example.com", true, "wild.example.com"},
		{"suffix.example.com", true, "suffix.example.com"},
		{"a.suffix.example.com", true, "suffix.example.com"},
		{"notsuffix.example.com", false, ""},
	}

	for _, tc := range tests {
		p := mustPolicy(t, cfg, tc.host)
		if p.CleartextTrafficPermitted != tc.cleartext {
			t.Errorf("%s cleartext = %v, want %v", tc.host, p.CleartextTrafficPermitted, tc.cleartext)
		}
		if p.MatchedDomain != tc.domain {
			t.Errorf("%s matched domain = %q, want %q", tc.host, p.MatchedDomain, tc.domain)
		}
	}
}

func TestPolicyForURLAndEmptyHost(t *testing.T) {
	const document = `
<network-security-config>
  <domain-config cleartextTrafficPermitted="true">
    <domain>example.com</domain>
  </domain-config>
</network-security-config>`

	cfg := mustParse(t, document, Build{TargetSDK: 34})
	p, err := cfg.PolicyForURL("https://api.example.com:8443/path")
	if err != nil {
		t.Fatalf("PolicyForURL: %v", err)
	}
	if !p.CleartextTrafficPermitted {
		t.Fatal("URL evaluation should match the domain")
	}

	p = mustPolicy(t, cfg, "")
	if p.CleartextTrafficPermitted {
		t.Fatal("empty host should evaluate the base policy")
	}
	if p.MatchedDomain != "" {
		t.Fatalf("empty host matched %q", p.MatchedDomain)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "wrong root",
			document: `<configuration/>`,
			want:     "root element",
		},
		{
			name:     "unknown element",
			document: `<network-security-config><other/></network-security-config>`,
			want:     "unexpected element",
		},
		{
			name:     "bad boolean",
			document: `<network-security-config><base-config cleartextTrafficPermitted="yes"/></network-security-config>`,
			want:     "invalid cleartextTrafficPermitted",
		},
		{
			name:     "empty pin set",
			document: `<network-security-config><base-config><pin-set/></base-config></network-security-config>`,
			want:     "contains no <pin>",
		},
		{
			name:     "bad expiration",
			document: `<network-security-config><base-config><pin-set expiration="next-year"><pin digest="SHA-256">cGlu</pin></pin-set></base-config></network-security-config>`,
			want:     "invalid pin-set expiration",
		},
		{
			name:     "unsupported digest",
			document: `<network-security-config><base-config><pin-set><pin digest="MD5">cGlu</pin></pin-set></base-config></network-security-config>`,
			want:     "unsupported pin digest",
		},
		{
			name:     "wildcard with includeSubdomains false",
			document: `<network-security-config><domain-config><domain includeSubdomains="false">*.example.com</domain></domain-config></network-security-config>`,
			want:     "wildcard domain",
		},
		{
			name:     "domain config without domain",
			document: `<network-security-config><domain-config><trust-anchors><certificates src="system"/></trust-anchors></domain-config></network-security-config>`,
			want:     "contains no <domain>",
		},
		{
			name:     "unsupported certificate source",
			document: `<network-security-config><base-config><trust-anchors><certificates src="remote"/></trust-anchors></base-config></network-security-config>`,
			want:     "unsupported certificate source",
		},
		{
			name:     "content after root",
			document: `<network-security-config/><network-security-config/>`,
			want:     "after root element",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(strings.NewReader(tc.document), Build{TargetSDK: 34})
			if err == nil {
				t.Fatal("ParseConfig succeeded, want error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want substring %q", err, tc.want)
			}
		})
	}

	for _, targetSDK := range []int{-1, 0} {
		if _, err := ParseConfig(strings.NewReader(`<network-security-config/>`), Build{TargetSDK: targetSDK}); err == nil {
			t.Fatalf("targetSdk %d should be rejected", targetSDK)
		}
	}
}
