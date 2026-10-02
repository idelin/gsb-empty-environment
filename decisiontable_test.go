package decisiontable

import (
	"math"
	"strings"
	"testing"
)

var (
	vipDomain     = []Value{true, false}
	channelDomain = []Value{"online", "offline"}
)

func sampleDims() []Dim {
	return []Dim{
		{Name: "VIP", Domain: vipDomain},
		{Name: "Amount", Numeric: true},
		{Name: "Channel", Domain: channelDomain},
	}
}

func vip(v bool) Cond       { return NewSetCond(vipDomain, v) }
func channel(v string) Cond { return NewSetCond(channelDomain, v) }

// sampleRules mirrors the 10 business-line rules used by cmd/demo.
func sampleRules() []Rule {
	rule := func(id int, conds []Cond, action string) Rule {
		return Rule{ID: id, Conds: conds, Action: action}
	}
	return []Rule{
		rule(1, []Cond{vip(false), NewRangeCond(0, 100, false, true), AnyCond{}}, "1.0"),
		rule(2, []Cond{vip(false), NewRangeCond(100, 500, false, true), AnyCond{}}, "0.95"),
		rule(3, []Cond{vip(true), NewRangeCond(500, math.Inf(1), false, true), AnyCond{}}, "0.8"),
		rule(4, []Cond{vip(true), NewRangeCond(100, 500, false, true), AnyCond{}}, "0.9"),
		rule(5, []Cond{vip(true), NewRangeCond(0, 100, false, true), AnyCond{}}, "0.95"),
		rule(6, []Cond{vip(false), NewRangeCond(500, math.Inf(1), false, true), channel("online")}, "0.9"),
		rule(7, []Cond{vip(true), NewRangeCond(500, math.Inf(1), false, true), channel("offline")}, "0.7"),
		rule(8, []Cond{vip(false), NewRangeCond(500, math.Inf(1), false, true), channel("offline")}, "0.9"),
		rule(9, []Cond{vip(true), NewRangeCond(0, 100, false, true), channel("offline")}, "0.95"),
		rule(10, []Cond{vip(false), NewRangeCond(500, 1000, false, true), AnyCond{}}, "0.9"),
	}
}

func fixedRules() []Rule {
	rules := sampleRules()
	for i := range rules {
		if rules[i].ID == 7 {
			rules[i].Action = "0.8"
		}
	}
	return rules
}

func TestDetectConflictsFindsRule3VsRule7(t *testing.T) {
	conflicts := DetectConflicts(sampleRules())
	if len(conflicts) != 1 {
		t.Fatalf("expected exactly 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	c := conflicts[0]
	if c.A.ID != 3 || c.B.ID != 7 {
		t.Errorf("expected conflict between rule 3 and rule 7, got R%d vs R%d", c.A.ID, c.B.ID)
	}
	if c.A.Action != "0.8" || c.B.Action != "0.7" {
		t.Errorf("expected actions 0.8 vs 0.7, got %s vs %s", c.A.Action, c.B.Action)
	}
	region := FormatRegion(sampleDims(), c.Region)
	for _, want := range []string{"VIP=true", "Amount=(500, +∞)", "Channel=offline"} {
		if !strings.Contains(region, want) {
			t.Errorf("region %q missing %q", region, want)
		}
	}
}

func TestNoFalsePositives(t *testing.T) {
	// Rules 6 and 8 share VIP/Amount but are disjoint on Channel.
	r6 := sampleRules()[5]
	r8 := sampleRules()[7]
	if _, ok := intersectRules(r6, r8); ok {
		t.Error("rules 6 and 8 must not overlap (online vs offline)")
	}
	// Same-action overlaps are not conflicts.
	for _, c := range DetectConflicts(sampleRules()) {
		if c.A.Action == c.B.Action {
			t.Errorf("same-action pair R%d/R%d reported as conflict", c.A.ID, c.B.ID)
		}
	}
}

func TestNoConflictsAfterFix(t *testing.T) {
	if got := DetectConflicts(fixedRules()); len(got) != 0 {
		t.Fatalf("expected no conflicts after fix, got %+v", got)
	}
}

func TestCompressMergesToWildcardRules(t *testing.T) {
	compressed := Compress(fixedRules())
	if len(compressed) != 6 {
		t.Fatalf("expected 6 compressed rules, got %d:\n%s", len(compressed),
			Table{Dims: sampleDims(), Rules: compressed})
	}
	// Channel must be fully absorbed into wildcards.
	for _, r := range compressed {
		if !r.Conds[2].IsWildcard() {
			t.Errorf("rule %d still has non-wildcard channel %s", r.ID, r.Conds[2])
		}
	}
	type key struct {
		vip, amount, action string
	}
	want := map[key]bool{
		{"false", "(0, 100]", "1.0"}:    true,
		{"false", "(100, 500]", "0.95"}: true,
		{"false", "(500, +∞)", "0.9"}:   true,
		{"true", "(0, 100]", "0.95"}:    true,
		{"true", "(100, 500]", "0.9"}:   true,
		{"true", "(500, +∞)", "0.8"}:    true,
	}
	if len(want) != len(compressed) {
		t.Fatal("test expectation table malformed")
	}
	for _, r := range compressed {
		k := key{r.Conds[0].String(), r.Conds[1].String(), r.Action}
		if !want[k] {
			t.Errorf("unexpected compressed rule: VIP=%s Amount=%s -> %s", k.vip, k.amount, k.action)
		}
		delete(want, k)
	}
	if len(want) != 0 {
		t.Errorf("missing expected rules: %+v", want)
	}
}

func TestDecisionTreeMinimal(t *testing.T) {
	tree := BuildTree(Compress(fixedRules()), sampleDims())

	leaves := countLeaves(tree)
	if leaves != 6 {
		t.Errorf("expected 6 leaf nodes, got %d\ntree:\n%s", leaves, tree)
	}
	out := tree.String()
	for _, want := range []string{
		"if Amount in (0, 100]:",
		"if Amount in (100, 500]:",
		"if Amount > 500:",
		"if VIP = false → 1.0",
		"if VIP = true → 0.95",
		"if VIP = false → 0.95",
		"if VIP = true → 0.9",
		"if VIP = false → 0.9",
		"if VIP = true → 0.8",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("tree output missing %q\ntree:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Channel") {
		t.Errorf("tree should not branch on the eliminated Channel dimension\ntree:\n%s", out)
	}
}

func countLeaves(n *Node) int {
	if n == nil {
		return 0
	}
	if n.Leaf {
		return 1
	}
	total := 0
	for _, br := range n.Branches {
		total += countLeaves(br.Child)
	}
	return total
}

func TestRangeCondOps(t *testing.T) {
	a := NewRangeCond(0, 100, false, true)   // (0, 100]
	b := NewRangeCond(100, 500, false, true) // (100, 500]
	if a.Overlaps(b) {
		t.Error("(0,100] and (100,500] must not overlap")
	}
	u, ok := a.Union(b)
	if !ok {
		t.Fatal("adjacent ranges (0,100] and (100,500] must union")
	}
	if !u.Matches(100.0) || !u.Matches(500.0) || u.Matches(0.0) {
		t.Errorf("union (0,500] misbehaves: %s", u)
	}
	if got := a.Intersect(b); got != nil {
		t.Errorf("disjoint ranges must intersect to nil, got %s", got)
	}
	c := NewRangeCond(50, 150, true, true)
	if !a.Overlaps(c) {
		t.Error("(0,100] and [50,150] must overlap")
	}
	if got := a.Intersect(c); got == nil || got.String() != "[50, 100]" {
		t.Errorf("intersection should be [50, 100], got %v", got)
	}
	if wild := NewRangeCond(math.Inf(-1), math.Inf(1), false, false); wild != (AnyCond{}) {
		t.Error("open-infinite range must normalize to wildcard")
	}
}

func TestSetCondOps(t *testing.T) {
	on := NewSetCond(channelDomain, "online")
	off := NewSetCond(channelDomain, "offline")
	if on.Overlaps(off) {
		t.Error("online and offline must not overlap")
	}
	u, ok := on.Union(off)
	if !ok || !u.IsWildcard() {
		t.Errorf("online ∪ offline over full domain must be wildcard, got %v", u)
	}
	if got := on.Intersect(off); got != nil {
		t.Errorf("disjoint sets must intersect to nil, got %v", got)
	}
	if got := (AnyCond{}).Intersect(on); got == nil || got.String() != "online" {
		t.Errorf("wildcard ∩ online should be online, got %v", got)
	}
	both := NewSetCond(vipDomain, true, false)
	if !both.IsWildcard() {
		t.Error("set covering the whole VIP domain must be wildcard")
	}
}

func TestCompressIsIdempotent(t *testing.T) {
	once := Compress(fixedRules())
	twice := Compress(once)
	if len(once) != len(twice) {
		t.Fatalf("compress not idempotent: %d vs %d rules", len(once), len(twice))
	}
	for i := range once {
		for k := range once[i].Conds {
			if !once[i].Conds[k].Equal(twice[i].Conds[k]) {
				t.Fatalf("rule %d changed on recompress", once[i].ID)
			}
		}
	}
}
