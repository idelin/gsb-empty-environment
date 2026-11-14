package depverify

import (
	"errors"
	"testing"
)

func TestParseGoModAllDirectives(t *testing.T) {
	in := `
module example.com/app

go 1.24.0

require (
	example.com/a v1.2.3
	example.com/b v2.0.0 // indirect
	"example.com/quoted" v0.1.0
)

require example.com/c v1.0.0

replace (
	example.com/a v1.2.3 => example.com/fork-a v1.2.4
	example.com/b => ./local/b
	example.com/d v1.0.0 => ../vendor/d
)

exclude example.com/a v1.2.2

retract (
	[v1.0.0, v1.0.1]
)
`
	mod, err := ParseGoMod(in)
	if err != nil {
		t.Fatalf("ParseGoMod: %v", err)
	}
	if len(mod.Requires) != 4 {
		t.Fatalf("requires = %d, want 4: %+v", len(mod.Requires), mod.Requires)
	}
	if !mod.Requires[1].Indirect {
		t.Fatalf("example.com/b should be indirect: %+v", mod.Requires[1])
	}
	if mod.Requires[2].Path != "example.com/quoted" {
		t.Fatalf("quoted path not parsed: %+v", mod.Requires[2])
	}

	reps := map[string]Replacement{}
	for _, r := range mod.Replacements {
		reps[r.OldPath] = r
	}
	if r := reps["example.com/a"]; r.Local || r.NewPath != "example.com/fork-a" ||
		r.NewVersion != "v1.2.4" || r.Wildcard || r.OldVersion != "v1.2.3" {
		t.Fatalf("versioned remote replace wrong: %+v", r)
	}
	if r := reps["example.com/b"]; !r.Local || !r.Wildcard || r.NewPath != "./local/b" || r.NewVersion != "" {
		t.Fatalf("local wildcard replace wrong: %+v", r)
	}
	if r := reps["example.com/d"]; !r.Local || r.NewPath != "../vendor/d" {
		t.Fatalf("local versioned replace wrong: %+v", r)
	}

	if len(mod.Excludes) != 1 || mod.Excludes[0].Path != "example.com/a" ||
		mod.Excludes[0].Version != "v1.2.2" {
		t.Fatalf("exclude wrong: %+v", mod.Excludes)
	}
}

func TestParseGoModErrors(t *testing.T) {
	cases := map[string]string{
		"unterminated string":   "require \"abc v1.0.0\n",
		"unterminated block":    "require (\n\texample.com/a v1.0.0\n",
		"missing arrow":         "replace example.com/a v1.0.0 -> example.com/b v1.0.0\n",
		"missing replace rhs":   "replace example.com/a v1.0.0 =>\n",
		"missing require value": "require example.com/a\n",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseGoMod(in); err == nil {
				t.Fatalf("expected ParseError for %q", name)
			} else {
				var pe *ParseError
				if !errors.As(err, &pe) {
					t.Fatalf("error %v is not ParseError", err)
				}
			}
		})
	}
}

func TestParseGoSum(t *testing.T) {
	in := "example.com/a v1.0.0 h1:aaa\n" +
		"example.com/a v1.0.0/go.mod h1:bbb\n" +
		"// a comment\n\n" +
		"example.com/b v2.0.0 h1:ccc\n"
	sum, err := ParseGoSum(in)
	if err != nil {
		t.Fatalf("ParseGoSum: %v", err)
	}
	if h, ok := sum.Lookup("example.com/a", "v1.0.0", false); !ok || h != "h1:aaa" {
		t.Fatalf("module lookup = %q,%v", h, ok)
	}
	if h, ok := sum.Lookup("example.com/a", "v1.0.0", true); !ok || h != "h1:bbb" {
		t.Fatalf("go.mod lookup = %q,%v", h, ok)
	}
	if _, ok := sum.Lookup("example.com/missing", "v0.0.1", false); ok {
		t.Fatal("missing key unexpectedly present")
	}

	if _, err := ParseGoSum("example.com/a v1.0.0 sha256:xxx\n"); err == nil {
		t.Fatal("expected error for non-h1 hash")
	}
	if _, err := ParseGoSum("example.com/a h1:hash\n"); err == nil {
		t.Fatal("expected error for malformed line")
	}
}
