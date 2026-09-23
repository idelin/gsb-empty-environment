package featureneg

import (
	"reflect"
	"testing"
)

func feat(name string, min, max uint32, requires []Dep, conflicts []string) Feature {
	return Feature{Name: name, Versions: Range{min, max}, Requires: requires, Conflicts: conflicts}
}

func selectedNames(out Outcome) []string {
	var names []string
	for _, s := range out.Selected {
		names = append(names, s.Name)
	}
	return names
}

func TestProtocolRangeEmptyIntersectionFails(t *testing.T) {
	sess := &Session{
		Local:  &Offer{Party: "a", Protocol: Range{5, 9}, Features: map[string]Feature{}},
		Remote: &Offer{Party: "b", Protocol: Range{1, 3}, Features: map[string]Feature{}},
	}
	out := sess.Negotiate(0)
	if out.Status != Conflict {
		t.Fatalf("status = %v, want conflict", out.Status)
	}
	if len(out.Reasons) == 0 {
		t.Fatal("expected a rejection reason")
	}
}

func TestRequiredFeatureUnsupportedFails(t *testing.T) {
	local := &Offer{
		Party: "a", Protocol: Range{1, 2},
		Required: []string{"auth"},
		Features: map[string]Feature{"auth": feat("auth", 1, 2, nil, nil)},
	}
	remote := &Offer{
		Party: "b", Protocol: Range{1, 2},
		Features: map[string]Feature{}, // does not support "auth"
	}
	out := (&Session{Local: local, Remote: remote}).Negotiate(0)
	if out.Status != Conflict {
		t.Fatalf("status = %v, want conflict", out.Status)
	}
	if !reflect.DeepEqual(out.ConflictCore, []string{"auth"}) {
		t.Fatalf("core = %v, want [auth]", out.ConflictCore)
	}
}

func TestRequiredFeatureEmptyVersionIntersectionFails(t *testing.T) {
	local := &Offer{
		Party: "a", Protocol: Range{1, 2},
		Required: []string{"codec"},
		Features: map[string]Feature{"codec": feat("codec", 1, 2, nil, nil)},
	}
	remote := &Offer{
		Party: "b", Protocol: Range{1, 2},
		Required: []string{"codec"},
		Features: map[string]Feature{"codec": feat("codec", 5, 7, nil, nil)},
	}
	out := (&Session{Local: local, Remote: remote}).Negotiate(0)
	if out.Status != Conflict {
		t.Fatalf("status = %v, want conflict", out.Status)
	}
	if !reflect.DeepEqual(out.ConflictCore, []string{"codec"}) {
		t.Fatalf("core = %v, want [codec]", out.ConflictCore)
	}
}

// A per-feature greedy pass would lock in the top-preference feature X
// and then fail; the global optimum drops X and keeps Y+Z.
func TestGlobalOptimumBeatsGreedy(t *testing.T) {
	features := func() map[string]Feature {
		return map[string]Feature{
			"x": feat("x", 1, 2, nil, []string{"z"}),
			"y": feat("y", 1, 2, []Dep{{Name: "z", MinVersion: 1}}, nil),
			"z": feat("z", 1, 2, nil, nil),
		}
	}
	local := &Offer{
		Party: "a", Protocol: Range{1, 3},
		Optional:   []string{"x", "y", "z"},
		Preference: []string{"x", "y", "z"},
		Features:   features(),
	}
	remote := &Offer{
		Party: "b", Protocol: Range{2, 4},
		Optional:   []string{"x", "y", "z"},
		Preference: []string{"x", "y", "z"},
		Features:   features(),
	}
	out := (&Session{Local: local, Remote: remote}).Negotiate(0)
	if out.Status != OK {
		t.Fatalf("status = %v, reasons %v", out.Status, out.Reasons)
	}
	if got := selectedNames(out); !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Fatalf("selected = %v, want [y z]", got)
	}
	if out.ProtocolVersion != 3 {
		t.Fatalf("protocol = %d, want 3 (highest common)", out.ProtocolVersion)
	}
	for _, s := range out.Selected {
		if s.Version != 2 {
			t.Fatalf("feature %s version = %d, want 2 (highest common)", s.Name, s.Version)
		}
	}
}

func TestVersionConditionUnsatisfiable(t *testing.T) {
	mk := func(bmax uint32) map[string]Feature {
		return map[string]Feature{
			"a": feat("a", 1, 1, []Dep{{Name: "b", MinVersion: 3}}, nil),
			"b": feat("b", 1, bmax, nil, nil),
		}
	}
	local := &Offer{
		Party: "a", Protocol: Range{1, 1},
		Required: []string{"a"},
		Features: mk(2),
	}
	remote := &Offer{
		Party: "b", Protocol: Range{1, 1},
		Required: []string{"a"},
		Features: mk(2),
	}
	out := (&Session{Local: local, Remote: remote}).Negotiate(0)
	if out.Status != Conflict {
		t.Fatalf("status = %v, want conflict", out.Status)
	}
	if !reflect.DeepEqual(out.ConflictCore, []string{"a"}) {
		t.Fatalf("core = %v, want [a]", out.ConflictCore)
	}
}

func TestMinimalConflictCore(t *testing.T) {
	mk := func() map[string]Feature {
		return map[string]Feature{
			"a": feat("a", 1, 1, nil, []string{"b"}),
			"b": feat("b", 1, 1, nil, nil),
			"c": feat("c", 1, 1, nil, nil), // unrelated, must not appear in the core
		}
	}
	local := &Offer{
		Party: "a", Protocol: Range{1, 1},
		Required: []string{"a", "b", "c"},
		Features: mk(),
	}
	remote := &Offer{
		Party: "b", Protocol: Range{1, 1},
		Required: []string{"a", "b", "c"},
		Features: mk(),
	}
	out := (&Session{Local: local, Remote: remote}).Negotiate(0)
	if out.Status != Conflict {
		t.Fatalf("status = %v, want conflict", out.Status)
	}
	if !reflect.DeepEqual(out.ConflictCore, []string{"a", "b"}) {
		t.Fatalf("core = %v, want [a b]", out.ConflictCore)
	}
}

func TestBudgetExhaustedIsUndecided(t *testing.T) {
	mk := func() *Offer {
		feats := map[string]Feature{}
		var opt []string
		for i := 0; i < 25; i++ {
			name := string(rune('a'+i%26)) + string(rune('A'+i/26))
			feats[name] = feat(name, 1, 1, nil, nil)
			opt = append(opt, name)
		}
		return &Offer{Party: "p", Protocol: Range{1, 1}, Optional: opt, Features: feats}
	}
	out := (&Session{Local: mk(), Remote: mk()}).Negotiate(8)
	if out.Status != Undecided {
		t.Fatalf("status = %v, want undecided", out.Status)
	}
	if len(out.Reasons) == 0 {
		t.Fatal("undecided outcome must carry an explanation")
	}
}

func TestCertificateIssuedOnSuccess(t *testing.T) {
	mk := func() *Offer {
		return &Offer{
			Party: "p", Protocol: Range{1, 4},
			Required: []string{"base"},
			Features: map[string]Feature{"base": feat("base", 2, 3, nil, nil)},
		}
	}
	sess := &Session{Local: mk(), Remote: mk()}
	out := sess.Negotiate(0)
	if out.Status != OK || out.Certificate == nil {
		t.Fatalf("status = %v, cert = %v", out.Status, out.Certificate)
	}
	if err := sess.VerifyCertificate(out.Certificate); err != nil {
		t.Fatalf("fresh certificate rejected: %v", err)
	}
	if got := out.Selected[0]; got.Name != "base" || got.Version != 3 {
		t.Fatalf("selection = %+v, want base@3", got)
	}
}
