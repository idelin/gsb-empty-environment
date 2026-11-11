package netsec

import (
	"strings"
	"testing"
)

func secretSet(t Tag) TagSet { return TagSet(0).With(t) }

func TestTagSetOps(t *testing.T) {
	s := TagSet(0).With(0).With(3)
	if !s.Contains(0) || !s.Contains(3) || s.Contains(1) {
		t.Fatalf("unexpected membership: %s", s)
	}
	if s.Count() != 2 || len(s.List()) != 2 {
		t.Fatalf("unexpected count: %d", s.Count())
	}
	s = s.Without(3)
	if s.Contains(3) {
		t.Fatalf("delete failed: %s", s)
	}
	got := (TagSet(0).With(0).With(1)).Intersect(TagSet(0).With(1).With(2))
	if want := TagSet(0).With(1); got != want {
		t.Fatalf("intersect failed: %s", got)
	}
}

func TestPrefixRangeContainment(t *testing.T) {
	r := PrefixRange{Network: MustParsePrefix("10.0.0.0/8"), MinLen: 10, MaxLen: 24}
	cases := []struct {
		prefix string
		want   bool
	}{
		{"10.1.0.0/16", true},
		{"10.0.0.0/8", false},
		{"10.1.2.0/25", false},
		{"11.0.0.0/16", false},
	}
	for _, tc := range cases {
		got := r.Match(Route{Prefix: MustParsePrefix(tc.prefix)})
		if got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.prefix, got, tc.want)
		}
	}
}

func TestConditions(t *testing.T) {
	rt := Route{PathLen: 5, Tags: TagSet(0).With(1).With(2)}
	if !(PathLenRange{Min: 1, Max: 10}).Match(rt) {
		t.Fatal("path length in range should match")
	}
	if (PathLenRange{Min: 6}).Match(rt) {
		t.Fatal("path length below min should not match")
	}
	if !(TagCondition{Op: TagAll, Tags: TagSet(0).With(1)}).Match(rt) {
		t.Fatal("TagAll should match")
	}
	if (TagCondition{Op: TagAll, Tags: TagSet(0).With(4)}).Match(rt) {
		t.Fatal("TagAll with missing tag must fail")
	}
	if !(TagCondition{Op: TagAny, Tags: TagSet(0).With(4).With(2)}).Match(rt) {
		t.Fatal("TagAny should match on overlap")
	}
	if (TagCondition{Op: TagNone, Tags: TagSet(0).With(1)}).Match(rt) {
		t.Fatal("TagNone must fail when present")
	}
	if !(NotConds{Inner: TagCondition{Op: TagNone, Tags: TagSet(0).With(1)}}).Match(rt) {
		t.Fatal("NOT should invert")
	}
}

func TestEvalTraceAndEarlyReturn(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"main": {
			Name: "main",
			Rules: []Rule{
				{Name: "reject-secret", When: TagCondition{Op: TagAll, Tags: secretSet(0)}, Do: Reject{}},
				{Name: "add-egress", Do: AddTags{Tags: secretSet(1)}},
				{Name: "accept-all", Do: Accept{}},
			},
		},
	}}
	if err := ps.Validate(); err != nil {
		t.Fatal(err)
	}

	v, out, trace, err := ps.Eval("main", Route{
		Prefix:  MustParsePrefix("10.0.0.0/16"),
		PathLen: 3,
		Tags:    secretSet(0),
	})
	if err != nil || v != VerdictReject {
		t.Fatalf("got %v %v err=%v", v, out, err)
	}
	rendered := RenderTrace(trace)
	if !strings.Contains(rendered, "rule reject-secret MATCH") ||
		!strings.Contains(rendered, "rule add-egress not-run") {
		t.Fatalf("trace missing early-return evidence:\n%s", rendered)
	}

	v, out, _, err = ps.Eval("main", Route{Prefix: MustParsePrefix("8.8.8.0/24"), Tags: secretSet(2)})
	if err != nil || v != VerdictAccept || !out.Tags.Contains(1) {
		t.Fatalf("got %v %v err=%v", v, out, err)
	}
}

func TestSubPolicyPropagation(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"main": {
			Name: "main",
			Rules: []Rule{
				{Name: "scrub", Do: DelTags{Tags: secretSet(0)}},
				{Name: "delegate", Do: CallPolicy{Name: "guard"}},
				{Name: "accept", Do: Accept{}},
			},
		},
		"guard": {
			Name: "guard",
			Rules: []Rule{
				{Name: "long-path-reject", When: PathLenRange{Min: 10}, Do: Reject{}},
			},
		},
	}}
	if err := ps.Validate(); err != nil {
		t.Fatal(err)
	}

	v, out, trace, err := ps.Eval("main", Route{PathLen: 1, Tags: secretSet(0)})
	if err != nil || v != VerdictAccept || out.Tags.Contains(0) {
		t.Fatalf("got %v %v err=%v", v, out, err)
	}
	if !strings.Contains(RenderTrace(trace), "enter policy guard") {
		t.Fatalf("nested trace missing:\n%s", RenderTrace(trace))
	}

	v, _, trace, err = ps.Eval("main", Route{PathLen: 12, Tags: secretSet(0)})
	if err != nil || v != VerdictReject {
		t.Fatalf("got %v err=%v", v, err)
	}
	if strings.Contains(RenderTrace(trace), "rule accept MATCH") {
		t.Fatalf("accept must not run after propagated reject:\n%s", RenderTrace(trace))
	}
}

func TestRejectRecursionAndDangling(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"a": {Name: "a", Rules: []Rule{{Name: "call-b", Do: CallPolicy{Name: "b"}}}},
		"b": {Name: "b", Rules: []Rule{{Name: "call-a", Do: CallPolicy{Name: "a"}}}},
	}}
	if err := ps.Validate(); err == nil || !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("expected recursion error, got %v", err)
	}

	bad := &PolicySet{Policies: map[string]*Policy{
		"a": {Name: "a", Rules: []Rule{{Name: "missing", Do: CallPolicy{Name: "ghost"}}}},
	}}
	if err := bad.Validate(); err == nil {
		t.Fatal("expected dangling reference error")
	}
}

func TestUnreachableRuleDetected(t *testing.T) {
	ps := &PolicySet{Policies: map[string]*Policy{
		"p": {
			Name: "p",
			Rules: []Rule{
				{Name: "stop", Do: Reject{}},
				{Name: "dead", Do: Accept{}},
			},
		},
	}}
	err := ps.Validate()
	if err == nil || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("expected unreachable error, got %v", err)
	}
}
