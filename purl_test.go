package netsec

import (
	"strings"
	"testing"
)

func TestIdentityUnifiesCaseAndEncoding(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
	}{
		{
			name: "npm scope and name case",
			a:    "pkg:npm/%40Angular/Core@1.0.0",
			b:    "pkg:npm/@angular/core@1.0.0",
		},
		{
			name: "npm encoded scope",
			a:    "pkg:NPM/%40angular%2Fcore@1.0.0",
			b:    "pkg:npm/@angular/core@1.0.0",
		},
		{
			name: "pypi PEP 503",
			a:    "pkg:pypi/Django_Rest.Framework@3.14.0",
			b:    "pkg:pypi/django-rest-framework@3.14.0",
		},
		{
			name: "nuget case",
			a:    "pkg:nuget/Newtonsoft.Json@13.0.1",
			b:    "pkg:nuget/newtonsoft.json@13.0.1",
		},
		{
			name: "version v prefix",
			a:    "pkg:npm/lodash@v4.17.21",
			b:    "pkg:npm/lodash@4.17.21",
		},
		{
			name: "partial version",
			a:    "pkg:npm/lodash@4.17",
			b:    "pkg:npm/lodash@4.17.0",
		},
		{
			name: "qualifier order and key case",
			a:    "pkg:deb/Debian/Curl@7.50.3?ARCH=amd64&distro=Debian_11",
			b:    "pkg:deb/debian/curl@7.50.3?distro=Debian_11&arch=amd64",
		},
		{
			name: "subpath does not split identity",
			a:    "pkg:golang/github.com/gorilla/mux@v1.8.0#mux",
			b:    "pkg:golang/github.com/gorilla/mux@v1.8.0",
		},
		{
			name: "type alias",
			a:    "pkg:go/github.com/gorilla/mux@v1.8.0",
			b:    "pkg:golang/github.com/gorilla/mux@v1.8.0",
		},
		{
			name: "pip alias",
			a:    "pkg:pip/Requests@2.31.0",
			b:    "pkg:pypi/requests@2.31.0",
		},
		{
			name: "go module proxy escape",
			a:    "pkg:golang/github.com/!masterminds/semver@v3.2.1",
			b:    "pkg:golang/github.com/Masterminds/semver@v3.2.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := MustParse(tc.a)
			b := MustParse(tc.b)
			if a.Identity() != b.Identity() {
				t.Fatalf("identities differ:\n  %s\n  %s\n  %s\n  %s", tc.a, a.Identity(), tc.b, b.Identity())
			}
		})
	}
}

func TestIdentityKeepsMeaningfulCase(t *testing.T) {
	mavenA := MustParse("pkg:maven/org.apache/Commons-Lang@3.12.0")
	mavenB := MustParse("pkg:maven/org.apache/commons-lang@3.12.0")
	if mavenA.Identity() == mavenB.Identity() {
		t.Fatalf("Maven coordinates are case-sensitive")
	}

	cranA := MustParse("pkg:cran/Foo@1.0.0")
	cranB := MustParse("pkg:cran/foo@1.0.0")
	if cranA.Identity() == cranB.Identity() {
		t.Fatalf("CRAN coordinates are case-sensitive")
	}
}

func TestVersionRangeIdentity(t *testing.T) {
	forms := []string{
		"pkg:npm/lodash@>=1.0.0 <2.0.0",
		"pkg:npm/lodash@>=1.0.0,<2.0.0",
		"pkg:npm/lodash@[1.0.0,2.0.0)",
		"pkg:npm/lodash@^1.0.0",
		"pkg:npm/lodash@1.x",
		"pkg:npm/lodash?vers=vers:npm/%3E%3D1.0.0%7C%3C2.0.0",
	}
	var want string
	for i, form := range forms {
		p := MustParse(form)
		if !p.IsRange {
			t.Fatalf("%s was not parsed as a range", form)
		}
		if i == 0 {
			want = p.Identity()
			continue
		}
		if p.Identity() != want {
			t.Fatalf("range identity mismatch for %s:\n  %s\n  %s", form, want, p.Identity())
		}
	}
}

func TestVersionRangeMatching(t *testing.T) {
	r := MustParse("pkg:npm/lodash@^4.17.0")
	for _, version := range []string{"4.17.0", "4.17.21", "v4.18.0-beta.1"} {
		if !r.Satisfies(version) {
			t.Errorf("expected %s to satisfy %s", version, r.Version)
		}
	}
	for _, version := range []string{"4.16.9", "5.0.0"} {
		if r.Satisfies(version) {
			t.Errorf("expected %s not to satisfy %s", version, r.Version)
		}
	}

	exact := MustParse("pkg:npm/lodash@4.17.21")
	if exact.Satisfies("4.17.21+build.1") != true {
		t.Errorf("build metadata should not affect exact matching")
	}
	if exact.Satisfies("4.17.22") {
		t.Errorf("different version matched")
	}
}

func TestVersionRangeExclusions(t *testing.T) {
	r, err := ParseVersionRange(">=1.0.0|<2.0.0|!=1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Contains("1.4.0") || r.Contains("1.5.0") || r.Contains("2.0.0") {
		t.Fatalf("unexpected exclusion behavior for %s", r)
	}
	only, err := ParseVersionRange("!=1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if !only.Contains("1.4.0") || only.Contains("1.5.0") {
		t.Fatalf("exclusion-only range is wrong: %s", only)
	}
}

func TestPackageAliasRegistry(t *testing.T) {
	ResetAliases()
	defer ResetAliases()
	if err := RegisterPackageAlias("pkg:npm/legacy-name", "pkg:npm/current-name"); err != nil {
		t.Fatal(err)
	}
	a := MustParse("pkg:npm/legacy-name@2.1.0")
	b := MustParse("pkg:npm/current-name@2.1.0")
	if a.Identity() != b.Identity() {
		t.Fatalf("registered package alias did not map to the same identity:\n  %s\n  %s", a.Identity(), b.Identity())
	}
	if !a.Aliased || a.AliasOf == "" {
		t.Fatalf("alias metadata missing")
	}
}

func TestNPMDependencyAlias(t *testing.T) {
	ResetAliases()
	defer ResetAliases()
	aliased := MustParse("pkg:npm/my-alias@npm:real-package%402.0.0")
	real := MustParse("pkg:npm/real-package@2.0.0")
	if aliased.Identity() != real.Identity() {
		t.Fatalf("npm alias identity mismatch:\n  %s\n  %s", aliased.Identity(), real.Identity())
	}
}

func TestUnknownTypePreservedAndMarked(t *testing.T) {
	p := MustParse("pkg:MyCustomType/Name_Space/Name@1.0.0?arch=AMD64")
	if !p.Unknown || !p.UnknownType || p.IsKnown() {
		t.Fatalf("unknown type was not marked: %+v", p)
	}
	if p.Type != "MyCustomType" {
		t.Fatalf("unknown type spelling was changed: %q", p.Type)
	}
	if p.Namespace != "Name_Space" || p.Name != "Name" {
		t.Fatalf("unknown component spelling was changed: %q %q", p.Namespace, p.Name)
	}
	if p.Version != "1.0.0" {
		t.Fatalf("unknown version spelling was changed: %q", p.Version)
	}
}

func TestDifferentComponentsHaveDifferentIdentities(t *testing.T) {
	purls := []string{
		"pkg:npm/lodash@4.17.21",
		"pkg:npm/lodash@4.17.20",
		"pkg:npm/react@18.2.0",
		"pkg:npm/@angular/core@17.0.0",
		"pkg:pypi/requests@2.31.0",
		"pkg:maven/org.apache/commons-lang3@3.12.0",
		"pkg:nuget/Newtonsoft.Json@13.0.1",
	}
	seen := make(map[string]string)
	for _, raw := range purls {
		p := MustParse(raw)
		if previous, exists := seen[p.Identity()]; exists {
			t.Fatalf("%s and %s collided", previous, raw)
		}
		seen[p.Identity()] = raw
	}
}

func TestCanonicalString(t *testing.T) {
	p := MustParse("pkg:NPM/%40angular/core@1.0.0?Z=last&a=first#src/main.ts")
	want := "pkg:npm/%40angular/core@1.0.0?a=first&z=last#src/main.ts"
	if got := p.String(); got != want {
		t.Fatalf("canonical purl:\n got %s\nwant %s", got, want)
	}
}

func TestInvalidPURLs(t *testing.T) {
	for _, raw := range []string{
		"",
		"npm/lodash@1.0.0",
		"pkg:",
		"pkg:npm",
		"pkg:npm/lodash@",
		"pkg:npm/lodash@1.0.0?arch=x&arch=y",
		"pkg:npm/lodash@1.0.0?bad key=x",
		"pkg:pypi/group/requests@1.0.0",
		"pkg:npm/lodash@1.0.0#../escape",
		"pkg:npm/lodash@%zz",
	} {
		if _, err := Parse(raw); err == nil {
			t.Errorf("expected parse error for %q", raw)
		}
	}
}

func TestUnknownTypeEncodingUnifiesWithoutCaseFolding(t *testing.T) {
	a := MustParse("pkg:Custom/Foo%5FBar@1.0")
	b := MustParse("pkg:Custom/Foo_Bar@1.0")
	if a.Identity() != b.Identity() {
		t.Fatalf("encoding variants should unify for unknown types")
	}
	c := MustParse("pkg:Custom/foo_bar@1.0")
	if a.Identity() == c.Identity() {
		t.Fatalf("unknown types must not be case-folded")
	}
}

func TestParseVersionRangeErrors(t *testing.T) {
	for _, expr := range []string{"", ">=", "^", "1.2.x-rc", "[2.0,1.0)"} {
		if _, err := ParseVersionRange(expr); err == nil {
			t.Errorf("expected range parse error for %q", expr)
		}
	}
}

func TestQualifierVersAndRangeIdentity(t *testing.T) {
	a := MustParse("pkg:npm/lodash?vers=vers:npm/%3E%3D1.0.0%7C%3C2.0.0")
	b := MustParse("pkg:npm/lodash@>=1.0.0 <2.0.0")
	if a.Identity() != b.Identity() {
		t.Fatalf("vers qualifier and version range should share identity")
	}
	if !a.Range.Contains("1.5.0") {
		t.Fatalf("vers qualifier range did not parse")
	}
}

func TestSemverPrecedence(t *testing.T) {
	ordered := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
		"1.0.1",
		"1.1.0",
		"2.0.0",
	}
	for i := 1; i < len(ordered); i++ {
		if c := CompareVersions(ordered[i-1], ordered[i]); c >= 0 {
			t.Fatalf("expected %s < %s, got %d", ordered[i-1], ordered[i], c)
		}
	}
}

func TestCanonicalizeHelpers(t *testing.T) {
	got, err := Canonicalize("pkg:PyPI/Django_Rest_Framework@v3.14")
	if err != nil {
		t.Fatal(err)
	}
	if got != "pkg:pypi/django-rest-framework@3.14.0" {
		t.Fatalf("unexpected canonical purl %q", got)
	}
	if !strings.HasPrefix(got, "pkg:pypi/") {
		t.Fatalf("unexpected scheme")
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"pkg:npm/%40angular/core@1.0.0",
		"pkg:pypi/Django_Rest_Framework@3.14",
		"pkg:unknown/Name@>=1.0.0 <2.0.0",
		"pkg:npm/foo@^1.2.3",
		"pkg:deb/debian/curl@7.50.3-1?arch=amd64",
		"pkg:generic/foo#a/b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if p, err := Parse(raw); err == nil {
			if p.String() == "" || p.Identity() == "" {
				t.Fatalf("empty canonical output for %q", raw)
			}
			_, _ = Parse(p.String())
		}
		_, _ = ParseVersionRange(raw)
		_ = CompareVersions(raw, raw)
	})
}
