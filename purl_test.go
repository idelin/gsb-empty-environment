package netsec

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	p, err := Parse("pkg:GITHUB/Acme/Corp/MyLib@1.2.3?Classifier=Linux&arch=arm#some/path/../x")
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	want := PURL{
		Type:       "GITHUB",
		Namespace:  "Acme/Corp",
		Name:       "MyLib",
		Version:    "1.2.3",
		Qualifiers: map[string]string{"classifier": "Linux", "arch": "arm"},
		Subpath:    "some/path/x",
	}
	if !reflect.DeepEqual(*p, want) {
		t.Fatalf("Parse = %+v, want %+v", *p, want)
	}
}

func TestParseInvalid(t *testing.T) {
	invalid := []string{
		"",
		"npm/acme/lib",
		"pkg:",
		"pkg:/acme/lib",
		"pkg:github/",
		"pkg:github/acme/lib%zz",
		"pkg:github/acme/lib?arch",
	}
	for _, input := range invalid {
		if _, err := Parse(input); err == nil {
			t.Errorf("Parse(%q) expected error, got nil", input)
		}
	}
}

func TestNormalizeCaseRules(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "type always lowercased",
			in:   "pkg:NPM/Acme/MyLib",
			want: "pkg:npm/Acme/MyLib",
		},
		{
			name: "github namespace and name lowercased",
			in:   "pkg:GITHUB/Acme/Corp/MyLib",
			want: "pkg:github/acme/corp/mylib",
		},
		{
			name: "bitbucket namespace and name lowercased",
			in:   "pkg:BitBucket/Acme/MyLib",
			want: "pkg:bitbucket/acme/mylib",
		},
		{
			name: "maven keeps namespace and name case",
			in:   "pkg:Maven/Com.Acme/MyLib@1.0",
			want: "pkg:maven/Com.Acme/MyLib@1.0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeString(tc.in)
			if err != nil {
				t.Fatalf("NormalizeString error: %v", err)
			}
			if got != tc.want {
				t.Errorf("NormalizeString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeEquivalence(t *testing.T) {
	variants := []string{
		"pkg:GITHUB/Acme/Corp/Evil%2fPkg@v1?B=2&A=1#sub",
		"pkg:github/acme/corp/Evil%2FPkg@v1?a=1&b=2#sub",
		"pkg:GitHub/ACME/corp/evil%2fpkg@v1?b=2&a=1#sub",
	}
	want := "pkg:github/acme/corp/evil%2Fpkg@v1?a=1&b=2#sub"
	for _, variant := range variants {
		got, err := NormalizeString(variant)
		if err != nil {
			t.Fatalf("NormalizeString(%q) error: %v", variant, err)
		}
		if got != want {
			t.Errorf("NormalizeString(%q) = %q, want %q", variant, got, want)
		}
	}
}

func TestMavenCaseVariantsStayDistinct(t *testing.T) {
	upper, err := NormalizeString("pkg:maven/com.acme/Foo@1.0")
	if err != nil {
		t.Fatal(err)
	}
	lower, err := NormalizeString("pkg:maven/com.acme/foo@1.0")
	if err != nil {
		t.Fatal(err)
	}
	if upper == lower {
		t.Fatalf("maven case variants must stay distinct: %q vs %q", upper, lower)
	}
	if upper != "pkg:maven/com.acme/Foo@1.0" {
		t.Errorf("upper variant = %q", upper)
	}
}

func TestQualifiersSortedAndKeysLowercased(t *testing.T) {
	got, err := NormalizeString("pkg:maven/com.acme/lib?Z=9&Arch=arm&a=1")
	if err != nil {
		t.Fatal(err)
	}
	want := "pkg:maven/com.acme/lib?a=1&arch=arm&z=9"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	in := "pkg:github/acme/corp/lib@v1?arch=arm#a/b"
	once, err := NormalizeString(in)
	if err != nil {
		t.Fatal(err)
	}
	twice, err := NormalizeString(once)
	if err != nil {
		t.Fatal(err)
	}
	if once != twice {
		t.Errorf("canonical form not stable: %q vs %q", once, twice)
	}
}
