package bundle

import "testing"

func TestSegmentPrefixTable(t *testing.T) {
	cases := []struct {
		candidate string
		prefix    string
		want      bool
	}{
		{"a/b/c", "a/b", true},
		{"a/b", "a/b", true},
		{"a/bc", "a/b", false},
		{"a/bc/c", "a/b", false},
		{"a/b", "a/b/c", false},
		{"a/c/b", "a/b", false},
		{"team/a", "team", true},
		{"team-ab", "team", false},
		{"", "a", false},
		{"a", "", true},
	}
	for _, tc := range cases {
		got := segmentPrefix(splitSegments(tc.candidate), splitSegments(tc.prefix))
		if got != tc.want {
			t.Errorf("segmentPrefix(%q,%q) = %v, want %v", tc.candidate, tc.prefix, got, tc.want)
		}
	}
}

func TestRootsOverlapTable(t *testing.T) {
	cases := []struct {
		a, b []string
		want bool
	}{
		{[]string{"a/b"}, []string{"a/b/c"}, true},
		{[]string{"a/b"}, []string{"a/bc"}, false},
		{[]string{"a/b", "x"}, []string{"a/bc", "y"}, false},
		{[]string{""}, []string{"a/b"}, true},
		{[]string{""}, []string{""}, true},
		{[]string{"a"}, []string{"b"}, false},
	}
	for _, tc := range cases {
		if got := rootsOverlap(tc.a, tc.b); got != tc.want {
			t.Errorf("rootsOverlap(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
