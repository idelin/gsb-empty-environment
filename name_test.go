package netsec

import (
	"testing"
)

func TestParseNameCanonicalAndEscapes(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"ExAmPle.COM.", "example.com."},
		{"example.com", "example.com."},
		{".", "."},
		{"a\\.b.example.", "a\\.b.example."},
		{"a\\065.example.", "aa.example."},
		{"*.*.example.", "*.*.example."},
	}
	for _, tt := range tests {
		n, err := ParseName(tt.in)
		if err != nil {
			t.Fatalf("ParseName(%q): %v", tt.in, err)
		}
		if got := n.String(); got != tt.want {
			t.Fatalf("ParseName(%q).String() = %q, want %q", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "a..b.", ".a.", "a\\", "a\\256."} {
		if _, err := ParseName(bad); err == nil {
			t.Fatalf("ParseName(%q) unexpectedly succeeded", bad)
		}
	}
}

func TestNameCompareDNSSECOrder(t *testing.T) {
	ordered := []string{
		".",
		"example.com.",
		"a.example.com.",
		"a.a.example.com.",
		"b.a.example.com.",
		"b.example.com.",
		"b.b.example.com.",
	}
	names := make([]Name, len(ordered))
	for i := range ordered {
		names[i] = MustParseName(ordered[i])
	}
	for i := 0; i < len(names); i++ {
		for j := 0; j < len(names); j++ {
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got := names[i].Compare(names[j]); got != want {
				t.Fatalf("%s.Compare(%s) = %d, want %d", ordered[i], ordered[j], got, want)
			}
		}
	}

	before := []string{
		"a.example.",
		"z.a.example.",
		"aa.example.",
		"b.example.",
		"a.b.example.",
	}
	for i := 0; i+1 < len(before); i++ {
		left := MustParseName(before[i])
		right := MustParseName(before[i+1])
		if left.Compare(right) >= 0 {
			t.Fatalf("%s should sort before %s", before[i], before[i+1])
		}
	}
}

func TestNameRelationships(t *testing.T) {
	zone := MustParseName("z.")
	a := MustParseName("a.z.")
	ab := MustParseName("b.a.z.")
	other := MustParseName("a.other.")

	if !a.IsSubdomainOf(zone) || !ab.IsSubdomainOf(zone) || !zone.IsSubdomainOf(zone) {
		t.Fatal("expected names to be at or below zone")
	}
	if other.IsSubdomainOf(zone) {
		t.Fatal("unexpected out-of-zone acceptance")
	}
	if !zone.IsAncestorOf(a) || !a.IsAncestorOf(ab) || a.IsAncestorOf(a) {
		t.Fatal("strict ancestor relationship is wrong")
	}
	parent, ok := ab.Parent()
	if !ok || parent.Compare(a) != 0 {
		t.Fatalf("Parent() = %s, %v; want %s, true", parent, ok, a)
	}
}
