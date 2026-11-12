package modselect

import (
	"errors"
	"testing"
)

func mustRetraction(t *testing.T, spec, rationale string) Retraction {
	t.Helper()
	r, err := ParseRetraction(spec, rationale)
	if err != nil {
		t.Fatalf("ParseRetraction(%q): %v", spec, err)
	}
	return r
}

func mustSelector(t *testing.T, versions []string, retractions []Retraction, excludes []string) *Selector {
	t.Helper()
	s, err := NewSelector(versions, retractions, excludes)
	if err != nil {
		t.Fatalf("NewSelector: %v", err)
	}
	return s
}

func TestComparePrereleaseChain(t *testing.T) {
	// semver.org precedence chain, plus the release itself.
	chain := []string{
		"v1.0.0-alpha",
		"v1.0.0-alpha.1",
		"v1.0.0-alpha.beta",
		"v1.0.0-beta",
		"v1.0.0-beta.2",
		"v1.0.0-beta.11",
		"v1.0.0-rc.1",
		"v1.0.0",
	}
	for i := 0; i < len(chain); i++ {
		for j := 0; j < len(chain); j++ {
			a, _ := Parse(chain[i])
			b, _ := Parse(chain[j])
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := Compare(a, b); got != want {
				t.Errorf("Compare(%s, %s) = %d, want %d", chain[i], chain[j], got, want)
			}
		}
	}
}

func TestCompareIncompatibleSuffix(t *testing.T) {
	pre, _ := Parse("v2.0.0-rc.1")
	inc, _ := Parse("v2.0.0+incompatible")
	plain, _ := Parse("v2.0.0")
	if got := Compare(pre, inc); got >= 0 {
		t.Errorf("Compare(v2.0.0-rc.1, v2.0.0+incompatible) = %d, want < 0", got)
	}
	if got := Compare(inc, plain); got != 0 {
		t.Errorf("Compare(v2.0.0+incompatible, v2.0.0) = %d, want 0", got)
	}
	next, _ := Parse("v2.0.1")
	if got := Compare(inc, next); got >= 0 {
		t.Errorf("Compare(v2.0.0+incompatible, v2.0.1) = %d, want < 0", got)
	}
}

func TestRetractIntervalBoundaries(t *testing.T) {
	r := mustRetraction(t, "[v1.0.0, v1.0.5]", "bad series")
	cases := []struct {
		version string
		want    bool
	}{
		{"v0.9.9", false},
		{"v1.0.0", true}, // low boundary is inclusive
		{"v1.0.3", true},
		{"v1.0.5", true}, // high boundary is inclusive
		{"v1.0.6", false},
		{"v1.0.5+incompatible", true}, // build metadata ignored
		{"v1.0.6-rc.1", false},
	}
	for _, c := range cases {
		if got := r.Contains(c.version); got != c.want {
			t.Errorf("Contains(%s) = %v, want %v", c.version, got, c.want)
		}
	}
}

func TestRetractIntervalSkipsLatest(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.0.0", "v1.0.3", "v1.0.5", "v1.0.6"},
		[]Retraction{mustRetraction(t, "[v1.0.0, v1.0.5]", "bad series")},
		nil,
	)
	sel, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if sel.Version != "v1.0.6" || sel.Retracted {
		t.Errorf("Latest = %+v, want v1.0.6 unretracted", sel)
	}
}

func TestLatestPrefersReleaseOverHigherPrerelease(t *testing.T) {
	s := mustSelector(t, []string{"v1.0.0", "v1.1.0-rc.1"}, nil, nil)
	sel, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if sel.Version != "v1.0.0" {
		t.Errorf("Latest = %q, want v1.0.0 (release beats higher prerelease)", sel.Version)
	}
}

func TestLatestFallsBackToPrerelease(t *testing.T) {
	s := mustSelector(t, []string{"v1.0.0-rc.1", "v1.0.0-rc.2"}, nil, nil)
	sel, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if sel.Version != "v1.0.0-rc.2" || sel.Retracted {
		t.Errorf("Latest = %+v, want v1.0.0-rc.2 unretracted", sel)
	}
}

func TestLatestFallsBackToRetracted(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.0.0", "v1.0.1"},
		[]Retraction{mustRetraction(t, "[v1.0.0, v1.0.1]", "everything broken")},
		nil,
	)
	sel, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if sel.Version != "v1.0.1" || !sel.Retracted || sel.Rationale != "everything broken" {
		t.Errorf("Latest = %+v, want retracted v1.0.1 with rationale", sel)
	}
}

func TestLatestNoVersion(t *testing.T) {
	s := mustSelector(t, nil, nil, nil)
	if _, err := s.Latest(); !errors.Is(err, ErrNoVersion) {
		t.Errorf("Latest err = %v, want ErrNoVersion", err)
	}
}

func TestExplicitRetractedVersionReturnsRationale(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.2.2", "v1.2.3", "v1.2.4"},
		[]Retraction{mustRetraction(t, "v1.2.3", "CVE-2026-0001: remote code execution")},
		nil,
	)
	sel, err := s.Select("v1.2.3")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Version != "v1.2.3" || !sel.Retracted || sel.Rationale == "" {
		t.Errorf("Select(v1.2.3) = %+v, want retracted v1.2.3 with rationale", sel)
	}
	// The same retraction keeps v1.2.3 out of @latest.
	latest, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Version != "v1.2.4" {
		t.Errorf("Latest = %q, want v1.2.4", latest.Version)
	}
}

func TestExcludeChainBumpsToNextVersion(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.2.3", "v1.2.4", "v1.2.5", "v1.2.6"},
		nil,
		[]string{"v1.2.3", "v1.2.4", "v1.2.5"},
	)
	sel, err := s.Select("v1.2.3")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Version != "v1.2.6" {
		t.Errorf("Select(v1.2.3) = %q, want bump past exclude chain to v1.2.6", sel.Version)
	}
	latest, err := s.Latest()
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if latest.Version != "v1.2.6" {
		t.Errorf("Latest = %q, want v1.2.6", latest.Version)
	}
}

func TestExcludeExhaustsVersions(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.2.3"},
		nil,
		[]string{"v1.2.3"},
	)
	if _, err := s.Select("v1.2.3"); !errors.Is(err, ErrNoVersion) {
		t.Errorf("Select err = %v, want ErrNoVersion", err)
	}
	if _, err := s.Latest(); !errors.Is(err, ErrNoVersion) {
		t.Errorf("Latest err = %v, want ErrNoVersion", err)
	}
}

func TestExcludeDoesNotHideExplicitUnexcluded(t *testing.T) {
	s := mustSelector(t,
		[]string{"v1.0.0", "v1.0.1"},
		nil,
		[]string{"v1.0.1"},
	)
	sel, err := s.Select("v1.0.0")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Version != "v1.0.0" || sel.Retracted {
		t.Errorf("Select(v1.0.0) = %+v, want clean v1.0.0", sel)
	}
}

func TestUnknownVersion(t *testing.T) {
	s := mustSelector(t, []string{"v1.0.0"}, nil, nil)
	if _, err := s.Select("v9.9.9"); err == nil {
		t.Error("Select(v9.9.9) succeeded, want unknown version error")
	}
}

func TestParseRetractionRejectsInvertedInterval(t *testing.T) {
	if _, err := ParseRetraction("[v1.0.5, v1.0.0]", ""); err == nil {
		t.Error("ParseRetraction accepted inverted interval")
	}
	if _, err := ParseRetraction("[v1.0.0, v1.0.5", ""); err == nil {
		t.Error("ParseRetraction accepted unclosed interval")
	}
}
