package packer

import "testing"

func TestSegMatch(t *testing.T) {
	cases := []struct {
		pat, name string
		want      bool
	}{
		{"a", "a", true},
		{"a", "b", false},
		{"*", "anything", true},
		{"*", "", true},
		{"*.txt", "x.txt", true},
		{"*.txt", "x.log", false},
		{"a*c", "abc", true},
		{"a*c", "ac", true},
		{"a*c", "ab", false},
		{"?", "x", true},
		{"?", "xy", false},
		{"?.log", "y.log", true},
		{"[abc]", "b", true},
		{"[abc]", "d", false},
		{"[!abc]", "d", true},
		{"[!abc]", "a", false},
		{"[a-z].txt", "g.txt", true},
		{"[a-z].txt", "G.txt", false},
		{"[]a]", "]", true},
		{"\\*", "*", true},
		{"\\*", "x", false},
		{"[", "[", true},
		{"[", "x", false},
	}
	for _, c := range cases {
		if got := segMatch(c.pat, c.name); got != c.want {
			t.Errorf("segMatch(%q, %q) = %v, want %v", c.pat, c.name, got, c.want)
		}
	}
}

func TestMatchSegments(t *testing.T) {
	cases := []struct {
		pat  []string
		segs []string
		want bool
	}{
		{[]string{"a"}, []string{"a"}, true},
		{[]string{"a"}, []string{"a", "b"}, false},
		{[]string{"**"}, []string{"a", "b", "c"}, true},
		{[]string{"**"}, []string{}, true},
		{[]string{"a", "**"}, []string{"a"}, true},
		{[]string{"a", "**"}, []string{"a", "b", "c"}, true},
		{[]string{"**", "b"}, []string{"b"}, true},
		{[]string{"**", "b"}, []string{"a", "a", "b"}, true},
		{[]string{"**", "b"}, []string{"a", "a", "c"}, false},
		{[]string{"**", "*.txt"}, []string{"x.txt"}, true},
		{[]string{"**", "*.txt"}, []string{"a", "b", "x.txt"}, true},
		{[]string{"a", "**", "c"}, []string{"a", "c"}, true},
		{[]string{"a", "**", "c"}, []string{"a", "b", "b", "c"}, true},
		{[]string{"a", "**", "c"}, []string{"a", "b", "d"}, false},
	}
	for _, c := range cases {
		if got := matchSegments(c.pat, c.segs); got != c.want {
			t.Errorf("matchSegments(%v, %v) = %v, want %v", c.pat, c.segs, got, c.want)
		}
	}
}
