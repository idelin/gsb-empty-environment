package netsec

import "testing"

func TestNormalizePurlVariantsCollapse(t *testing.T) {
	const canonical = "pkg:golang/github.com/acme/log@v1.2.3?arch=arm64&revision=abc%20def"

	variants := []string{
		canonical,
		"pkg:GOLANG/GITHUB.COM/acme/log@v1.2.3?revision=abc%20def&arch=arm64",
		"pkg:Golang/Github.com/acme/log@v1.2.3?ARCH=arm64&Revision=abc%20def",
		"pkg:golang/github.com/acme/log@v1.2.3?arch=arm64&revision=abc%20def",
		"pkg:golang/github.com/acme/log@v1.2.3?arch=arm64&revision=abc%20def#",
	}
	for _, raw := range variants {
		got, err := NormalizePurl(raw)
		if err != nil {
			t.Fatalf("NormalizePurl(%q) unexpected error: %v", raw, err)
		}
		if got != canonical {
			t.Errorf("NormalizePurl(%q) = %q, want %q", raw, got, canonical)
		}
	}
}

func TestNormalizePurlPercentEncoding(t *testing.T) {
	cases := map[string]string{
		"pkg:golang/github.com/acme/a%2fb@v1":          "pkg:golang/github.com/acme/a%2Fb@v1",
		"pkg:golang/github.com/acme/a%2Fb@v1":          "pkg:golang/github.com/acme/a%2Fb@v1",
		"pkg:golang/github.com/acme/a-b_c.d@v1%3a2":    "pkg:golang/github.com/acme/a-b_c.d@v1%3A2",
		"pkg:golang/github.com/acme/log@v1?os=go%2dos": "pkg:golang/github.com/acme/log@v1?os=go-os",
	}
	for raw, want := range cases {
		got, err := NormalizePurl(raw)
		if err != nil {
			t.Fatalf("NormalizePurl(%q) error: %v", raw, err)
		}
		if got != want {
			t.Errorf("NormalizePurl(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestNormalizePurlRejectsMissingVersion(t *testing.T) {
	for _, raw := range []string{
		"pkg:golang/github.com/acme/log",
		"pkg:golang/github.com/acme/log?arch=arm64",
	} {
		if _, err := NormalizePurl(raw); err == nil {
			t.Errorf("NormalizePurl(%q) expected error for missing version", raw)
		}
	}
}

func TestNormalizePurlRejectsBadEscape(t *testing.T) {
	if _, err := NormalizePurl("pkg:golang/github.com/acme/lo%2g@v1"); err == nil {
		t.Error("expected error for invalid percent escape")
	}
}

func TestNormalizePurlDistinctIdentitiesStayDistinct(t *testing.T) {
	a, _ := NormalizePurl("pkg:golang/github.com/acme/log@v1.2.3?arch=arm64")
	b, _ := NormalizePurl("pkg:golang/github.com/acme/log@v1.2.3?arch=x86")
	c, _ := NormalizePurl("pkg:golang/github.com/other/log@v1.2.3")
	if a == b || a == c || b == c {
		t.Errorf("distinct purls must not normalize equal: %q %q %q", a, b, c)
	}

	// Go import paths are case-sensitive: name case is preserved.
	mixed, _ := NormalizePurl("pkg:golang/github.com/acme/Log@v1")
	lower, _ := NormalizePurl("pkg:golang/github.com/acme/log@v1")
	if mixed == lower {
		t.Errorf("go package name case must be preserved: %q vs %q", mixed, lower)
	}
}

func TestNormalizePurlDuplicateQualifierLastWins(t *testing.T) {
	got, err := NormalizePurl("pkg:golang/github.com/acme/log@v1?arch=x86&arch=arm64")
	if err != nil {
		t.Fatal(err)
	}
	if want := "pkg:golang/github.com/acme/log@v1?arch=arm64"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
