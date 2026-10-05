package packer

import (
	"reflect"
	"testing"
)

func TestParseRuleNormalization(t *testing.T) {
	cases := []struct {
		in       string
		segs     []string
		anchored bool
		dirOnly  bool
	}{
		{"a", []string{"a"}, false, false},
		{"*.txt", []string{"*.txt"}, false, false},
		{"**", []string{"**"}, false, false},
		{"/a", []string{"a"}, true, false},
		{"a/b", []string{"a", "b"}, true, false},
		{"a/", []string{"a"}, false, true},
		{"/a/b/", []string{"a", "b"}, true, true},
		{"./a//b/./c", []string{"a", "b", "c"}, true, false},
		{"a//b", []string{"a", "b"}, true, false},
		{"a/./b", []string{"a", "b"}, true, false},
		{"a/../b", []string{"b"}, true, false},
		{"**/tmp/**", []string{"**", "tmp", "**"}, true, false},
		{"/a/**", []string{"a", "**"}, true, false},
	}
	for _, c := range cases {
		r, err := ParseRule(Include, c.in)
		if err != nil {
			t.Errorf("ParseRule(%q): %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(r.Segs, c.segs) || r.Anchored != c.anchored || r.DirOnly != c.dirOnly {
			t.Errorf("ParseRule(%q) = %+v, want segs=%v anchored=%v dirOnly=%v",
				c.in, r, c.segs, c.anchored, c.dirOnly)
		}
	}
}

func TestParseRuleInvalid(t *testing.T) {
	for _, in := range []string{"", "/", "//", "/./"} {
		if _, err := ParseRule(Include, in); err == nil {
			t.Errorf("ParseRule(%q): expected error", in)
		}
	}
}

func TestSameNamePatternsNormalizeEqual(t *testing.T) {
	a, err := ParseRule(Exclude, "a/b")
	if err != nil {
		t.Fatal(err)
	}
	for _, alias := range []string{"./a/b", "a//b", "a/./b", "/a/b", "x/../a/b"} {
		b, err := ParseRule(Exclude, alias)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(a.Segs, b.Segs) {
			t.Errorf("%q and %q normalize differently: %v vs %v", a.Raw, b.Raw, a.Segs, b.Segs)
		}
	}
}

func mustRuleSet(t *testing.T, defaultInclude bool, lines ...string) *RuleSet {
	t.Helper()
	text := ""
	for _, l := range lines {
		text += l + "\n"
	}
	rules, err := ParseRules(text)
	if err != nil {
		t.Fatalf("ParseRules: %v", err)
	}
	rs := &RuleSet{Rules: rules, DefaultInclude: defaultInclude}
	rs.Build()
	return rs
}

func TestDecideLaterOverridesEarlier(t *testing.T) {
	rs := mustRuleSet(t, true, "- *.txt", "+ keep.txt")
	if d := rs.Decide([]string{"drop.txt"}, false); d.Included || d.Rule != 0 {
		t.Errorf("drop.txt: got %+v, want excluded by rule 0", d)
	}
	if d := rs.Decide([]string{"keep.txt"}, false); !d.Included || d.Rule != 1 {
		t.Errorf("keep.txt: got %+v, want included by rule 1", d)
	}
	if d := rs.Decide([]string{"other.log"}, false); !d.Included || d.Rule != -1 {
		t.Errorf("other.log: got %+v, want included by default", d)
	}
}

func TestDecideExclusionPropagatesAndReincludes(t *testing.T) {
	rs := mustRuleSet(t, true, "- /a", "+ /a/b/c.txt")
	if d := rs.Decide([]string{"a"}, true); d.Included || d.Rule != 0 {
		t.Errorf("a: got %+v", d)
	}
	if d := rs.Decide([]string{"a", "b", "other.log"}, false); d.Included || d.Rule != 0 {
		t.Errorf("a/b/other.log: got %+v, want excluded via ancestor", d)
	}
	if d := rs.Decide([]string{"a", "b", "c.txt"}, false); !d.Included || d.Rule != 1 {
		t.Errorf("a/b/c.txt: got %+v, want re-included by rule 1", d)
	}
}

func TestDecideDirOnly(t *testing.T) {
	rs := mustRuleSet(t, true, "- /a/")
	if d := rs.Decide([]string{"a"}, true); d.Included {
		t.Errorf("dir a: got %+v, want excluded", d)
	}
	if d := rs.Decide([]string{"a"}, false); !d.Included {
		t.Errorf("file a: got %+v, want included (dir-only rule must not match)", d)
	}
	if d := rs.Decide([]string{"a", "b"}, false); d.Included {
		t.Errorf("a/b: got %+v, want excluded via dir ancestor", d)
	}
}

func TestDecideDefaultExclude(t *testing.T) {
	rs := mustRuleSet(t, false, "+ **/*.txt")
	if d := rs.Decide([]string{"d", "a.txt"}, false); !d.Included || d.Rule != 0 {
		t.Errorf("d/a.txt: got %+v", d)
	}
	if d := rs.Decide([]string{"d", "b.log"}, false); d.Included || d.Rule != -1 {
		t.Errorf("d/b.log: got %+v, want excluded by default", d)
	}
}

func TestFeasible(t *testing.T) {
	cases := []struct {
		pat  []string
		dir  []string
		want bool
	}{
		{[]string{"a", "b"}, []string{"a"}, true},
		{[]string{"a", "b"}, []string{"a", "b"}, true},
		{[]string{"a", "b"}, []string{"a", "c"}, false},
		{[]string{"a", "b"}, []string{"x"}, false},
		{[]string{"a"}, []string{"a", "b"}, false}, // exact match above the dir only
		{[]string{"**"}, []string{"a", "b"}, true},
		{[]string{"**", "c"}, []string{"a", "b"}, true},
		{[]string{"a", "**", "d"}, []string{"a", "b"}, true},
		{[]string{"a", "**", "d"}, []string{"x"}, false},
		{[]string{"*.txt"}, []string{"a"}, false},
		{[]string{"a*"}, []string{"ab"}, true},
		{[]string{"a", "b"}, []string{}, true},
	}
	for _, c := range cases {
		if got := feasible(c.pat, c.dir); got != c.want {
			t.Errorf("feasible(%v, %v) = %v, want %v", c.pat, c.dir, got, c.want)
		}
	}
}

func TestCanReinclude(t *testing.T) {
	rs := mustRuleSet(t, true, "- /a", "+ /a/b/c.txt")
	if !rs.canReinclude(0, []string{"a"}) {
		t.Error("expected re-include to be feasible under a")
	}
	if rs.canReinclude(0, []string{"x"}) {
		t.Error("no include rule can match under x")
	}
	rs2 := mustRuleSet(t, true, "- /a", "+ *.txt")
	if !rs2.canReinclude(0, []string{"anything"}) {
		t.Error("basename include pattern is feasible anywhere")
	}
}
