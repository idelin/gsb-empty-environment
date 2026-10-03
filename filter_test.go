package memberlist

import "testing"

func TestParseSelector(t *testing.T) {
	conds, err := ParseSelector("zone=us-east, role=worker , weight>=50")
	if err != nil {
		t.Fatal(err)
	}
	if len(conds) != 3 {
		t.Fatalf("got %d conditions, want 3", len(conds))
	}
	if conds[2] != (Condition{Key: "weight", Op: ">=", Value: "50"}) {
		t.Fatalf("unexpected condition: %+v", conds[2])
	}
	if _, err := ParseSelector("zone"); err == nil {
		t.Fatal("expected parse error for bare key")
	}
	if conds, _ := ParseSelector(""); conds != nil {
		t.Fatal("empty selector should yield nil conditions")
	}
}

func TestMatch(t *testing.T) {
	n := &Node{
		ID:     "n1",
		Labels: map[string]string{"disk": "ssd"},
		Meta:   Meta{Zone: "us-east", Role: "worker", Weight: 80},
	}
	cases := []struct {
		selector string
		want     bool
	}{
		{"zone=us-east", true},
		{"zone=us-west", false},
		{"zone=us-east,role=worker", true},
		{"zone=us-east,role=gateway", false},
		{"zone!=us-west", true},
		{"weight>=50", true},
		{"weight>80", false},
		{"weight<=80", true},
		{"label.disk=ssd", true},
		{"label.disk=hdd", false},
		{"label.gpu=v100", false},
		{"id=n1", true},
		{"", true},
	}
	for _, c := range cases {
		conds, err := ParseSelector(c.selector)
		if err != nil {
			t.Fatalf("parse %q: %v", c.selector, err)
		}
		if got := Match(n, conds); got != c.want {
			t.Errorf("Match(%q) = %v, want %v", c.selector, got, c.want)
		}
	}
}
