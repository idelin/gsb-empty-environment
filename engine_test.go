package main

import "testing"

func TestRuleOrderIsExplicit(t *testing.T) {
	in := LabelSet{"a": "1", "b": "2"}
	// rename a->b then delete b: nothing of a remains
	r1 := []Rule{
		RenameLabel{RuleID: "x", From: "a", To: "b"},
		DeleteLabel{RuleID: "y", Label: "b"},
	}
	// delete b then rename a->b: b survives with a's value
	r2 := []Rule{
		DeleteLabel{RuleID: "y", Label: "b"},
		RenameLabel{RuleID: "x", From: "a", To: "b"},
	}
	out1, out2 := in, in
	for _, r := range r1 {
		out1 = r.Apply(out1)
	}
	for _, r := range r2 {
		out2 = r.Apply(out2)
	}
	if out1.Equal(out2) {
		t.Fatalf("rule order must matter, both gave %v", out1)
	}
	if !out1.Equal(LabelSet{}) {
		t.Fatalf("rename-then-delete: got %v", out1)
	}
	if !out2.Equal(LabelSet{"b": "1"}) {
		t.Fatalf("delete-then-rename: got %v", out2)
	}
}

func TestRenameDeleteSynth(t *testing.T) {
	e := NewEngine(false, []string{"__name__"}, nil)
	if err := e.UpdateRules([]Rule{
		RenameLabel{RuleID: "r1", From: "job", To: "service"},
		DeleteLabel{RuleID: "r2", Label: "debug"},
		SynthLabel{RuleID: "r3", Label: "site", Template: "{dc}-{env}"},
	}); err != nil {
		t.Fatal(err)
	}
	err := e.AddSeries(Series{Name: "m", Value: 5, Labels: LabelSet{
		"job": "api", "debug": "1", "dc": "eu", "env": "prod",
	}})
	if err != nil {
		t.Fatal(err)
	}
	outs := e.Outputs()
	if len(outs) != 1 {
		t.Fatalf("want 1 output, got %d", len(outs))
	}
	for _, o := range outs {
		want := LabelSet{"service": "api", "dc": "eu", "env": "prod", "site": "eu-prod"}
		if !o.Labels.Equal(want) {
			t.Fatalf("got %v want %v", o.Labels, want)
		}
		if o.Value() != 5 {
			t.Fatalf("value = %g", o.Value())
		}
	}
}

func TestReservedLabelCannotBeRewritten(t *testing.T) {
	e := NewEngine(false, []string{"tenant"}, nil)
	cases := []Rule{
		DeleteLabel{RuleID: "d", Label: "tenant"},
		RenameLabel{RuleID: "r", From: "tenant", To: "t"},
		RenameLabel{RuleID: "r2", From: "t", To: "tenant"},
		SynthLabel{RuleID: "s", Label: "tenant", Template: "x"},
	}
	for _, r := range cases {
		if err := e.UpdateRules([]Rule{r}); err == nil {
			t.Fatalf("rule %s must be rejected", r.Describe())
		}
	}
}

func TestDeleteDistinguishingLabelConflictsWithoutOverwrite(t *testing.T) {
	e := NewEngine(false, nil, nil)
	if err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r1", Label: "instance"}}); err != nil {
		t.Fatal(err)
	}
	first := Series{Name: "m", Labels: LabelSet{"job": "a", "instance": "i1"}, Value: 10}
	second := Series{Name: "m", Labels: LabelSet{"job": "a", "instance": "i2"}, Value: 20}
	if err := e.AddSeries(first); err != nil {
		t.Fatal(err)
	}
	err := e.AddSeries(second)
	cerr, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if cerr.Pair[0] >= cerr.Pair[1] {
		t.Fatalf("pair not sorted: %v", cerr.Pair)
	}
	outs := e.Outputs()
	if len(outs) != 1 {
		t.Fatalf("want 1 output, got %d", len(outs))
	}
	for _, o := range outs {
		if o.Value() != 10 || len(o.Contributions) != 1 {
			t.Fatalf("existing output overwritten: %v", o)
		}
	}
}

func TestAggregationMergeAndRetractRestores(t *testing.T) {
	e := NewEngine(true, nil, nil)
	if err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r1", Label: "instance"}}); err != nil {
		t.Fatal(err)
	}
	a := Series{Name: "m", Labels: LabelSet{"job": "a", "instance": "i1"}, Value: 10}
	b := Series{Name: "m", Labels: LabelSet{"job": "a", "instance": "i2"}, Value: 20}
	if err := e.AddSeries(a); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(b); err != nil {
		t.Fatal(err)
	}
	outs := e.Outputs()
	if len(outs) != 1 {
		t.Fatalf("want merged single output, got %d", len(outs))
	}
	for _, o := range outs {
		if o.Value() != 30 || len(o.Contributions) != 2 {
			t.Fatalf("merged output = %v", o)
		}
	}
	// Retract the delete rule: series split back with original contributions.
	if err := e.RetractRule("r1"); err != nil {
		t.Fatal(err)
	}
	outs = e.Outputs()
	if len(outs) != 2 {
		t.Fatalf("want 2 outputs after retract, got %d", len(outs))
	}
	seen := map[string]float64{}
	for _, o := range outs {
		seen[o.Labels["instance"]] = o.Value()
	}
	if seen["i1"] != 10 || seen["i2"] != 20 {
		t.Fatalf("contributions not restored: %v", seen)
	}
}

func TestAtomicBatchUpdateAndMinimalPair(t *testing.T) {
	e := NewEngine(false, nil, nil)
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"pod": "p1", "app": "x"}, Value: 1}); err != nil {
		t.Fatal(err)
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"pod": "p2", "app": "x"}, Value: 2}); err != nil {
		t.Fatal(err)
	}
	v0 := e.Version()
	err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r9", Label: "pod"}})
	cerr, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("want ConflictError, got %v", err)
	}
	if e.Version() != v0 {
		t.Fatalf("failed batch must not bump version: %d -> %d", v0, e.Version())
	}
	if len(e.Rules()) != 0 {
		t.Fatalf("failed batch must not publish rules")
	}
	want0 := Series{Name: "m", Labels: LabelSet{"pod": "p1", "app": "x"}}.Key()
	if cerr.Pair[0] != want0 {
		t.Fatalf("minimal pair = %v, want first %q", cerr.Pair, want0)
	}
	// A harmless batch publishes atomically and bumps the version.
	if err := e.UpdateRules([]Rule{RenameLabel{RuleID: "r1", From: "app", To: "service"}}); err != nil {
		t.Fatal(err)
	}
	if e.Version() != v0+1 {
		t.Fatalf("version = %d", e.Version())
	}
}

func TestCacheIsolatedByRuleVersion(t *testing.T) {
	e := NewEngine(false, nil, nil)
	s := Series{Name: "m", Labels: LabelSet{"a": "1"}, Value: 1}
	if err := e.AddSeries(s); err != nil {
		t.Fatal(err)
	}
	if e.CacheSize(0) != 1 {
		t.Fatalf("v0 cache size = %d", e.CacheSize(0))
	}
	if err := e.UpdateRules([]Rule{DeleteLabel{RuleID: "r1", Label: "a"}}); err != nil {
		t.Fatal(err)
	}
	if e.CacheSize(0) != 1 {
		t.Fatalf("old version cache must stay isolated, got %d", e.CacheSize(0))
	}
	if err := e.AddSeries(Series{Name: "m", Labels: LabelSet{"a": "2"}, Value: 2}); err == nil {
		t.Fatal("want conflict after delete")
	}
	if e.CacheSize(1) != 1 {
		t.Fatalf("v1 cache size = %d", e.CacheSize(1))
	}
}
