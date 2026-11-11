package netsec

import "testing"

func TestParseValid(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"0.0.0", "0.0.0"},
		{"1.2.3", "1.2.3"},
		{"10.20.30", "10.20.30"},
		{"01.002.0003", "1.2.3"}, // core segments normalize to integers
		{"1.2.3-alpha", "1.2.3-alpha"},
		{"1.2.3-alpha.1", "1.2.3-alpha.1"},
		{"1.2.3-0.2.10", "1.2.3-0.2.10"},
		{"1.2.3-00", "1.2.3-0"},            // numeric identifiers normalize
		{"1.2.3-a-1.x-y", "1.2.3-a-1.x-y"}, // hyphen allowed inside identifiers
	}
	for _, c := range cases {
		v, err := Parse(c.in)
		if err != nil {
			t.Fatalf("Parse(%q) error: %v", c.in, err)
		}
		if got := v.String(); got != c.want {
			t.Errorf("Parse(%q).String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	for _, in := range []string{
		"", "1.2", "1.2.3.4", "a.2.3", "1.2.x", "1..3",
		"1.2.3-", "1.2.3-a..1", "1.2.3-a_1", "1.2.3-α",
		"18446744073709551616.0.0", "-1.2.3",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) succeeded, want error", in)
		}
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0},
		{"1.2.3", "1.2.4", -1},
		{"1.2.4", "1.2.3", 1},
		{"1.10.0", "1.9.0", 1},
		{"2.0.0", "10.0.0", -1},
		{"1.0.0", "1.0.0-0", 1}, // release > pre-release
		{"1.0.0-a", "1.0.0", -1},
		{"1.0.0-a", "1.0.0-b", -1}, // byte order
		{"1.0.0-b", "1.0.0-a", 1},
		{"1.0.0-2", "1.0.0-10", -1}, // numeric identifiers as integers
		{"1.0.0-1", "1.0.0-a", -1},  // numeric before alphanumeric
		{"1.0.0-a", "1.0.0-1", 1},
		{"1.0.0-a", "1.0.0-a.0", -1}, // shorter list first
		{"1.0.0-a.1", "1.0.0-a.b", -1},
		{"1.0.0-00", "1.0.0-0", 0}, // leading zeros normalize
		{"0.0.0", "0.0.1-0", -1},
		{"1.0.0-9", "1.0.0-10", -1},
	}
	for _, c := range cases {
		a, b := MustParse(c.a), MustParse(c.b)
		if got := Compare(a, b); got != c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(b, a); got != -c.want {
			t.Errorf("Compare(%s, %s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}

func TestAdjacent(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.0.0", "1.0.1-0", true},     // patch boundary, release to lowest pre-release
		{"1.0.0", "1.0.1", false},      // 1.0.1-0 lies between
		{"1.0.0-a", "1.0.0-a.0", true}, // pre-release list extension by "0"
		{"1.0.0-a", "1.0.0-b", false},  // 1.0.0-a.0 lies between
		{"1.0.0-a", "1.0.0", false},    // 1.0.0-a.0 lies between
		{"1.0.0-0", "1.0.0-1", false},  // 1.0.0-0.0 lies between
		{"1.0.0", "1.0.2-0", false},    // 1.0.1 lies between
		{"1.0.0", "1.1.0-0", false},    // 1.0.1 lies between
		{"1.0.0-a", "1.0.1-0", false},  // 1.0.0-a.0 lies between
	}
	for _, c := range cases {
		a, b := MustParse(c.a), MustParse(c.b)
		if got := adjacent(a, b); got != c.want {
			t.Errorf("adjacent(%s, %s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
