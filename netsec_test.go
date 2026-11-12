package netsec

import (
	"strings"
	"testing"
)

const sampleMetadata = `<?xml version="1.0" encoding="UTF-8"?>
<verification-metadata xmlns="https://schema.gradle.org/dependency-verification">
    <configuration>
        <also-trust>
            <sha1/>
            <md5/>
        </also-trust>
        <trusted-artifacts>
            <trust group="^com.(evil|attacker)$" regex="true"/>
            <trust group="^com\.safe$" name=".*" regex="true"/>
            <trust group="^com\.example$" name="^safe-lib$" version="^1\.0$" regex="true"/>
            <trust group="com.example.dyn" name="dyn-lib" version="1.+"/>
            <trust group="com.example.range" name="range-lib" version="[1.0,2.0)"/>
            <trust group="com.missing" name="missing-lib" version="2.+"/>
            <trust group="literal.group" name="literal-name" version=".*"/>
        </trusted-artifacts>
    </configuration>
    <components>
        <component group="com.example" name="safe-lib" version="1.0">
            <artifact name="safe-lib-1.0.jar">
                <sha256 value="abc123"/>
                <sha512 value="def456"/>
                <also-trust>
                    <sha1 value="aaa111"/>
                </also-trust>
            </artifact>
        </component>
        <component group="com.example" name="loose-lib" version="2.0">
            <artifact name="loose-lib-2.0.jar">
                <sha256 value="loosehash"/>
                <also-trust>
                    <md5 value="md5hash"/>
                </also-trust>
            </artifact>
        </component>
        <component group="org.other" name="no-trust" version="1.0">
            <artifact name="no-trust-1.0.jar">
                <sha256 value="onlyhash"/>
            </artifact>
        </component>
    </components>
</verification-metadata>`

const sampleLockfile = `# This is a Gradle generated file
org.other:no-trust:1.0=debugCompileClasspath,runtimeClasspath
com.example:safe-lib:1.0=runtimeClasspath
com.example.dyn:dyn-lib:1.2:sources:jar=debugCompileClasspath
com.example.range:range-lib:1.5=runtimeClasspath
`

func contains(haystack []string, needle string) bool {
	for _, item := range haystack {
		if item == needle {
			return true
		}
	}
	return false
}

func findingIDs(findings []Finding) map[string]int {
	ids := make(map[string]int)
	for _, finding := range findings {
		ids[finding.ID]++
	}
	return ids
}

func TestParseMetadata(t *testing.T) {
	meta, err := ParseMetadataBytes([]byte(sampleMetadata))
	if err != nil {
		t.Fatalf("ParseMetadata returned error: %v", err)
	}
	if len(meta.Components) != 3 {
		t.Fatalf("expected 3 components, got %d", len(meta.Components))
	}
	if len(meta.TrustRules) != 7 {
		t.Fatalf("expected 7 trust rules, got %d", len(meta.TrustRules))
	}

	first := meta.Components[0]
	if first.Group != "com.example" || first.Name != "safe-lib" || first.Version != "1.0" {
		t.Fatalf("unexpected first component: %+v", first)
	}
	checksums := first.Artifacts[0].Checksums
	if len(checksums) != 3 {
		t.Fatalf("expected 3 checksums (incl. also-trust), got %d", len(checksums))
	}
	if checksums[0].Algorithm != "sha256" || checksums[0].AlsoTrust {
		t.Fatalf("expected primary sha256 checksum, got %+v", checksums[0])
	}
	if checksums[2].Algorithm != "sha1" || !checksums[2].AlsoTrust || checksums[2].Value != "aaa111" {
		t.Fatalf("expected also-trust sha1 checksum, got %+v", checksums[2])
	}

	loose := meta.Components[1].Artifacts[0].Checksums
	if len(loose) != 2 || !loose[1].AlsoTrust || loose[1].Algorithm != "md5" {
		t.Fatalf("expected also-trust md5 entry, got %+v", loose)
	}

	if !contains(meta.AlsoTrust, "sha1") || !contains(meta.AlsoTrust, "md5") {
		t.Fatalf("also-trust algorithms not recorded: %v", meta.AlsoTrust)
	}

	dynRule := meta.TrustRules[3]
	if dynRule.Regex || dynRule.Version == nil || *dynRule.Version != "1.+" {
		t.Fatalf("dynamic literal rule parsed incorrectly: %+v", dynRule)
	}
}

func TestOverbroadRegex(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))
	findings := meta.Analyze(nil)

	var overbroad []Finding
	for _, finding := range findings {
		if finding.ID == FindingOverbroadRegex {
			overbroad = append(overbroad, finding)
		}
	}
	if len(overbroad) != 1 {
		t.Fatalf("expected exactly one overbroad-regex finding, got %d: %+v", len(overbroad), findings)
	}
	if overbroad[0].Rule == nil || overbroad[0].Rule.Name == nil || *overbroad[0].Rule.Name != ".*" {
		t.Fatalf("overbroad finding points at wrong rule: %+v", overbroad[0])
	}

	// The name=".*" rule accepts any artifact name under the pinned group.
	catchAll := meta.TrustRules[1]
	matched, err := catchAll.Matches("com.safe", "anything", "9.9", "evil.jar")
	if err != nil || !matched {
		t.Fatalf("name catch-all rule failed to match arbitrary name: matched=%v err=%v", matched, err)
	}
	matched, _ = catchAll.Matches("com.unsafe", "anything", "9.9", "evil.jar")
	if matched {
		t.Fatal("anchored group must still constrain the catch-all name rule")
	}

	for _, pattern := range []string{"^.*$", "(.*)", ".*|.*", "^(.+)$"} {
		if !IsMatchAllRegex(pattern) {
			t.Errorf("IsMatchAllRegex(%q) = false, want true", pattern)
		}
	}
	if IsMatchAllRegex(`^com\.example$`) {
		t.Errorf("anchored group regex incorrectly classified as match-all")
	}
}

func TestUnescapedDot(t *testing.T) {
	if !HasUnescapedDot("^com.(evil|attacker)$") {
		t.Fatal("expected unescaped dot to be detected")
	}
	if HasUnescapedDot(`^1\.0$`) {
		t.Fatal("escaped dot must not be flagged")
	}
	if !HasUnescapedDot("(.*)") {
		t.Fatal("dot inside a wrapping group is still an unescaped metacharacter")
	}
	if HasUnescapedDot(`[.]`) {
		t.Fatal("dot inside a character class must not be flagged")
	}
	if HasUnescapedDot(`[a-z.]+`) {
		t.Fatal("dot inside a character class must not be flagged")
	}

	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))
	findings := meta.Analyze(nil)
	var dotFindings []Finding
	for _, finding := range findings {
		if finding.ID == FindingUnescapedDot {
			dotFindings = append(dotFindings, finding)
		}
	}
	if len(dotFindings) != 2 {
		t.Fatalf("expected two unescaped-dot findings, got %d: %+v", len(dotFindings), findings)
	}

	// The unescaped dot accepts any character, but whole-string anchoring
	// still rejects additional suffixes.
	risky := meta.TrustRules[0]
	cases := []struct {
		group string
		want  bool
	}{
		{"com.evil", true},
		{"comXevil", true},
		{"com_evil", true},
		{"com.evil.extra", false},
	}
	for _, tc := range cases {
		got, err := risky.Matches(tc.group, "ignored", "ignored", "")
		if err != nil {
			t.Fatalf("unexpected regex error: %v", err)
		}
		if got != tc.want {
			t.Errorf("risky rule.Matches(%q) = %v, want %v", tc.group, got, tc.want)
		}
	}

	safe := meta.TrustRules[2]
	matched, _ := safe.Matches("comXexample", "safe-lib", "1.0", "")
	if matched {
		t.Fatal("escaped group dot must not accept a substituted character")
	}
	matched, _ = safe.Matches("com.example", "safeXlib", "1.0", "")
	if matched {
		t.Fatal("regex name pattern must still anchor and reject substitution mismatch")
	}
}

func TestFullStringLiteralVsRegex(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))
	literal := meta.TrustRules[6] // version=".*" without regex
	if literal.Regex {
		t.Fatal("test setup: expected a literal (non-regex) rule")
	}
	matched, err := literal.Matches("literal.group", "literal-name", "1.5", "")
	if err != nil || matched {
		t.Fatalf("literal .* pattern must not behave like a regex: matched=%v err=%v", matched, err)
	}
	matched, _ = literal.Matches("literal.group", "literal-name", ".*", "")
	if !matched {
		t.Fatal("literal pattern should match its own exact string")
	}

	// Regex rules are anchored: "^safe-lib$" must not match a prefix.
	anchored := meta.TrustRules[2]
	matched, _ = anchored.Matches("com.example", "safe-lib-extra", "1.0", "")
	if matched {
		t.Fatal("whole-string regex incorrectly matched a prefixed name")
	}
	matched, _ = anchored.Matches("com.example", "safe-lib", "1.0", "")
	if !matched {
		t.Fatal("anchored regex should match the exact coordinate")
	}
}

func TestDynamicVersionMatching(t *testing.T) {
	cases := []struct {
		selector string
		version  string
		want     bool
	}{
		{"1.+", "1.0", true},
		{"1.+", "1.2.3", true},
		{"1.+", "1", true},
		{"1.+", "10", false},
		{"1.+", "2.0", false},
		{"1.2.+", "1.2", true},
		{"1.2.+", "1.3.0", false},
		{"+", "anything", true},
		{"latest.release", "4.2", true},
		{"[1.0,2.0)", "1.0", true},
		{"[1.0,2.0)", "1.9", true},
		{"[1.0,2.0)", "2.0", false},
		{"[1.0,2.0)", "0.9", false},
		{"(1.0,2.0]", "1.0", false},
		{"(1.0,2.0]", "2.0", true},
		{"[1.5]", "1.5", true},
		{"[1.5]", "1.6", false},
		{"1.0", "1.0", true},
		{"1.0", "1.1", false},
	}
	for _, tc := range cases {
		got := DynamicVersionMatches(tc.selector, tc.version)
		if got != tc.want {
			t.Errorf("DynamicVersionMatches(%q, %q) = %v, want %v", tc.selector, tc.version, got, tc.want)
		}
		if tc.selector == "1.0" {
			if IsDynamicVersion(tc.selector) {
				t.Errorf("fixed version %q must not be classified dynamic", tc.selector)
			}
		} else if !IsDynamicVersion(tc.selector) {
			t.Errorf("selector %q must be classified dynamic", tc.selector)
		}
	}
}

func TestLockfileCoverage(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))
	lock, err := ParseLockfileBytes([]byte(sampleLockfile))
	if err != nil {
		t.Fatalf("ParseLockfile error: %v", err)
	}
	if len(lock.Entries) != 4 {
		t.Fatalf("expected 4 lock entries, got %d", len(lock.Entries))
	}
	classified := lock.Entries[2]
	if classified.Classifier != "sources" || classified.Extension != "jar" {
		t.Fatalf("classifier/extension not parsed: %+v", classified)
	}

	findings := meta.Analyze(lock)
	ids := findingIDs(findings)

	// Only the 2.+ rule for com.missing is uncovered; 1.+ and the range rule
	// both have matching lockfile entries.
	if ids[FindingUncoveredDynamicVersion] != 1 {
		t.Fatalf("expected one uncovered-dynamic-version finding, got %+v", findings)
	}
	for _, finding := range findings {
		if finding.ID == FindingUncoveredDynamicVersion {
			if finding.Rule == nil || *finding.Rule.Version != "2.+" {
				t.Fatalf("wrong rule flagged as uncovered: %+v", finding)
			}
		}
	}

	// Only loose-lib:2.0 sits outside the lockfile.
	if ids[FindingVersionNotInLockfile] != 1 {
		t.Fatalf("expected one version-not-in-lockfile finding, got %+v", findings)
	}
	for _, finding := range findings {
		if finding.ID == FindingVersionNotInLockfile && !strings.Contains(finding.Message, "loose-lib") {
			t.Fatalf("expected loose-lib to be flagged, message: %s", finding.Message)
		}
	}

	if !lock.Contains("com.example", "safe-lib", "1.0") {
		t.Fatal("locked coordinate reported as missing")
	}
	if lock.Contains("com.example", "safe-lib", "9.9") {
		t.Fatal("unpinned version reported as locked")
	}
}

func TestVerifyPrimaryChecksums(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))

	decision := meta.Verify(ArtifactRef{
		Group: "com.example", Name: "safe-lib", Version: "1.0", File: "safe-lib-1.0.jar",
	}, map[string]string{"sha256": "abc123"})
	if !decision.Allowed || decision.Reason != ReasonChecksumMatched {
		t.Fatalf("matching sha256 should be allowed: %+v", decision)
	}

	// Hex comparison is case-insensitive and tolerates surrounding whitespace.
	decision = meta.Verify(ArtifactRef{
		Group: "com.example", Name: "safe-lib", Version: "1.0", File: "safe-lib-1.0.jar",
	}, map[string]string{"sha512": " DEF456 "})
	if !decision.Allowed {
		t.Fatalf("case-insensitive sha512 match should be allowed: %+v", decision)
	}

	// A mismatched digest for a component with no trust rule is denied.
	decision = meta.Verify(ArtifactRef{
		Group: "org.other", Name: "no-trust", Version: "1.0", File: "no-trust-1.0.jar",
	}, map[string]string{"sha256": "tampered"})
	if decision.Allowed || decision.Reason != ReasonChecksumMismatch {
		t.Fatalf("tampered artifact must be denied: %+v", decision)
	}

	// No observed digest means there is nothing to verify.
	decision = meta.Verify(ArtifactRef{
		Group: "org.other", Name: "no-trust", Version: "1.0", File: "no-trust-1.0.jar",
	}, nil)
	if decision.Allowed || decision.Reason != ReasonNoUsableChecksum {
		t.Fatalf("missing digest must not pass: %+v", decision)
	}
}

func TestVerifyAlsoTrustOnlyForListedAlgorithms(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))
	ref := ArtifactRef{
		Group: "com.example", Name: "safe-lib", Version: "1.0", File: "safe-lib-1.0.jar",
	}

	// sha1 is listed by an also-trust entry, so a matching sha1 may rescue
	// the artifact even when sha256 cannot be computed by the gateway.
	decision := meta.Verify(ref, map[string]string{"sha1": "aaa111"})
	if !decision.Allowed || decision.Reason != ReasonChecksumMatched {
		t.Fatalf("listed also-trust sha1 should be accepted: %+v", decision)
	}

	// md5 is listed in the metadata, but this component declares no md5
	// checksum, so the observed md5 cannot verify it; the decision falls
	// through to the trusted-artifact rule for safe-lib:1.0.
	decision = meta.Verify(ref, map[string]string{"md5": "whatever"})
	if decision.Reason != ReasonTrustedArtifact {
		t.Fatalf("unrelated md5 digest must not verify, expected trust-rule fallback: %+v", decision)
	}

	// loose-lib declares an also-trust md5, and md5 is listed in metadata.
	looseRef := ArtifactRef{
		Group: "com.example", Name: "loose-lib", Version: "2.0", File: "loose-lib-2.0.jar",
	}
	decision = meta.Verify(looseRef, map[string]string{"md5": "md5hash"})
	if !decision.Allowed {
		t.Fatalf("listed also-trust md5 should be accepted for loose-lib: %+v", decision)
	}

	// A wrong also-trust digest still fails closed.
	decision = meta.Verify(looseRef, map[string]string{"md5": "forged"})
	if decision.Allowed {
		t.Fatalf("forged also-trust digest must be denied: %+v", decision)
	}
}

func TestVerifyAlsoTrustUnlistedAlgorithmRejected(t *testing.T) {
	const metadataWithUnlisted = `<?xml version="1.0" encoding="UTF-8"?>
<verification-metadata>
    <components>
        <component group="com.example" name="old-lib" version="1.0">
            <artifact name="old-lib-1.0.jar">
                <also-trust>
                    <md5 value="oldmd5"/>
                </also-trust>
            </artifact>
        </component>
    </components>
</verification-metadata>`
	meta, err := ParseMetadataBytes([]byte(metadataWithUnlisted))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	// No trust-level also-trust exists, so no fallback algorithm is enabled.
	if len(meta.AlsoTrust) != 0 {
		t.Fatalf("artifact-level also-trust without enabling entry must not register algorithms: %v", meta.AlsoTrust)
	}

	decision := meta.Verify(ArtifactRef{
		Group: "com.example", Name: "old-lib", Version: "1.0", File: "old-lib-1.0.jar",
	}, map[string]string{"md5": "oldmd5"})
	if decision.Allowed || decision.Reason != ReasonUnsupportedAlgorithm {
		t.Fatalf("unlisted also-trust algorithm must be rejected without a trust rule: %+v", decision)
	}
}

func TestVerifyTrustRuleFallback(t *testing.T) {
	meta, _ := ParseMetadataBytes([]byte(sampleMetadata))

	// An attacker coordinate swallowed by the unescaped-dot group rule; the
	// decision is allowed yet carries the unescaped-dot risk finding.
	decision := meta.Verify(ArtifactRef{
		Group: "comXattacker", Name: "rogue", Version: "9.9", File: "rogue-9.9.jar",
	}, nil)
	if !decision.Allowed || decision.Reason != ReasonTrustedArtifact {
		t.Fatalf("risky trust rule should allow the substituted group: %+v", decision)
	}
	ids := findingIDs(decision.Findings)
	if ids[FindingTrustedArtifactRule] == 0 {
		t.Fatalf("trusted-artifact finding missing: %+v", decision.Findings)
	}
	if ids[FindingUnescapedDot] == 0 {
		t.Fatalf("unescaped-dot risk must travel with the decision: %+v", decision.Findings)
	}

	// Dynamic literal rule accepts 1.9 even though no component checksum exists.
	decision = meta.Verify(ArtifactRef{
		Group: "com.example.dyn", Name: "dyn-lib", Version: "1.9", File: "dyn-lib-1.9.jar",
	}, nil)
	if !decision.Allowed || decision.Reason != ReasonTrustedArtifact {
		t.Fatalf("dynamic trust rule should accept 1.9: %+v", decision)
	}

	// Coordinate with neither checksums nor a matching rule is denied.
	decision = meta.Verify(ArtifactRef{
		Group: "net.unknown", Name: "mystery", Version: "0.1", File: "mystery-0.1.jar",
	}, nil)
	if decision.Allowed {
		t.Fatalf("untrusted unknown coordinate must be denied: %+v", decision)
	}
}

func TestInvalidRegexReported(t *testing.T) {
	const metadataBadRegex = `<?xml version="1.0" encoding="UTF-8"?>
<verification-metadata>
    <configuration>
        <trusted-artifacts>
            <trust group="^(unclosed" regex="true"/>
        </trusted-artifacts>
    </configuration>
</verification-metadata>`
	meta, err := ParseMetadataBytes([]byte(metadataBadRegex))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	findings := meta.Analyze(nil)
	if findingIDs(findings)[FindingInvalidRegex] != 1 {
		t.Fatalf("expected invalid-regex finding, got %+v", findings)
	}

	// A broken rule must never match anything.
	matched, err := meta.TrustRules[0].Matches("unclosed", "a", "1", "a.jar")
	if err == nil || matched {
		t.Fatalf("invalid regex must return an error and never match: matched=%v err=%v", matched, err)
	}

	decision := meta.Verify(ArtifactRef{
		Group: "unclosed", Name: "a", Version: "1", File: "a.jar",
	}, nil)
	if decision.Allowed {
		t.Fatalf("artifact accepted through a broken regex: %+v", decision)
	}
}

func TestMalformedXMLAndLockfile(t *testing.T) {
	if _, err := ParseMetadataBytes([]byte("<verification-metadata>")); err == nil {
		t.Fatal("malformed XML must return an error")
	}
	if _, err := ParseLockfileBytes([]byte("org.other:no-trust=runtime")); err == nil {
		t.Fatal("malformed lockfile coordinate must return an error")
	}
}

func TestUnknownChecksumAlgorithmAndReaderAPIs(t *testing.T) {
	const metadataWeird = `<?xml version="1.0" encoding="UTF-8"?>
<verification-metadata>
    <components>
        <component group="com.weird" name="weird-lib" version="1.0">
            <artifact name="weird-lib-1.0.jar">
                <sha3-256 value="weird-digest"/>
            </artifact>
        </component>
    </components>
</verification-metadata>`
	meta, err := ParseMetadata(strings.NewReader(metadataWeird))
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if len(meta.ParseErrors) == 0 {
		t.Fatal("unknown checksum element should be recorded as a parse error")
	}
	findings := meta.Analyze(nil)
	if findingIDs(findings)[FindingUnknownChecksumAlgo] != 1 {
		t.Fatalf("expected unknown-algorithm finding, got %+v", findings)
	}

	// The unknown algorithm cannot verify the artifact, and no trust rule exists.
	decision := meta.Verify(ArtifactRef{
		Group: "com.weird", Name: "weird-lib", Version: "1.0", File: "weird-lib-1.0.jar",
	}, map[string]string{"sha3-256": "weird-digest"})
	if decision.Allowed || decision.Reason != ReasonUnsupportedAlgorithm {
		t.Fatalf("unknown algorithm must not verify the artifact: %+v", decision)
	}

	lock, err := ParseLockfile(strings.NewReader("com.weird:weird-lib:1.0=runtime\n"))
	if err != nil {
		t.Fatalf("lockfile parse error: %v", err)
	}
	if !lock.Contains("com.weird", "weird-lib", "1.0") {
		t.Fatal("reader-based lockfile parse failed")
	}

	var nilMeta *Metadata
	if nilMeta.Verify(ArtifactRef{}, nil).Allowed {
		t.Fatal("nil metadata must deny every artifact")
	}
}
