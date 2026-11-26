package capmig

import (
	"encoding/json"
	"testing"
)

func TestJSONRoundTrip(t *testing.T) {
	in := &Input{
		BarrierEpoch: 1,
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []ActionSplit{{OldAction: "manage", NewActions: []string{"read"}}},
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "P0", Action: "manage", Epoch: 1}},
		Tickets:      []Ticket{{ID: "t", Subject: "alice", NewObject: "P1", NewActions: []string{"read"}, AncestorTokens: []string{"T"}}},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Result
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.TicketReports[0].Decision != DecisionMigrate {
		t.Fatalf("round-trip decision = %s", back.TicketReports[0].Decision)
	}
}

func TestNoAmbiguitySingleHistory(t *testing.T) {
	in := &Input{
		ResourceMaps: []ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []ActionSplit{{OldAction: "manage", NewActions: []string{"read", "write"}}},
		Grants:       []Grant{{Token: "T", Subject: "alice", Object: "P0", Action: "manage", Epoch: 1}},
		Handoffs:     []Handoff{{ID: "h", Token: "T", From: "alice", To: "bob", Epoch: 2}},
		Tickets: []Ticket{
			{ID: "ok", Subject: "bob", NewObject: "P1", NewActions: []string{"read", "write"}, AncestorTokens: []string{"T"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.TotalHistories != 1 || res.AdmissibleHistories != 1 {
		t.Fatalf("histories = %d/%d", res.TotalHistories, res.AdmissibleHistories)
	}
	if res.TicketReports[0].Decision != DecisionMigrate {
		t.Fatalf("decision = %s", res.TicketReports[0].Decision)
	}
	if len(res.MinimumReauthSubjects) != 0 {
		t.Fatalf("unexpected reauth set %v", res.MinimumReauthSubjects)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]*Input{
		"conflicting alias": {
			AliasBindings: []AliasBinding{
				{Alias: "a", PhysicalID: "x"},
				{Alias: "a", PhysicalID: "y"},
			},
		},
		"action cycle": {
			ActionSplits: []ActionSplit{
				{OldAction: "a", NewActions: []string{"b"}},
				{OldAction: "b", NewActions: []string{"a"}},
			},
		},
		"unknown ticket object": {
			Tickets: []Ticket{{ID: "t", Subject: "s", NewObject: "ghost",
				NewActions: []string{"read"}, AncestorTokens: []string{"T"}}},
		},
	}
	for name, in := range cases {
		if _, err := Solve(in); err == nil {
			t.Fatalf("%s: expected validation error", name)
		}
	}
}

func TestTooManyAmbiguousHandoffs(t *testing.T) {
	in := &Input{}
	for i := 0; i <= MaxAmbiguousHandoffs; i++ {
		tok := "T" + itoa(i)
		in.Grants = append(in.Grants, Grant{Token: tok, Subject: "a", Object: "P", Action: "read", Epoch: 1})
		in.Handoffs = append(in.Handoffs, Handoff{
			ID: "h" + itoa(i), Token: tok, From: "a", To: "b",
			Epoch: uint64(2 + i), LostConfirmation: true,
		})
	}
	if _, err := Solve(in); err == nil {
		t.Fatal("expected error for >18 ambiguous handoffs")
	}
}

func TestManyToManyMappingAndPartialSplit(t *testing.T) {
	in := &Input{
		ResourceMaps: []ResourceMapEdge{
			{OldObject: "old1", NewObject: "merged"},
			{OldObject: "old2", NewObject: "merged"},
		},
		ActionSplits: []ActionSplit{
			{OldAction: "manage", NewActions: []string{"read", "write"}},
		},
		Grants: []Grant{
			{Token: "T1", Subject: "alice", Object: "old1", Action: "manage", Epoch: 1},
			{Token: "T2", Subject: "alice", Object: "old2", Action: "read", Epoch: 1},
		},
		Tickets: []Ticket{
			// Full manage split requires both read and write: T2 (read only)
			// cannot satisfy it, but T1 covers the whole set.
			{ID: "full", Subject: "alice", NewObject: "merged",
				NewActions: []string{"read", "write"}, AncestorTokens: []string{"T1", "T2"}},
			{ID: "readonly", Subject: "alice", NewObject: "merged",
				NewActions: []string{"read"}, AncestorTokens: []string{"T2"}},
		},
	}
	res, err := Solve(in)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if findReport(t, res, "full").Decision != DecisionMigrate {
		t.Fatal("full split must migrate via the manage token")
	}
	if findReport(t, res, "readonly").Decision != DecisionMigrate {
		t.Fatal("read-only token covers the read requirement")
	}
}
