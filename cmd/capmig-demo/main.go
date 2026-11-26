// Command capmig-demo runs the three canonical attack scenarios through the
// capability migration solver and prints the auditable text report.
package main

import (
	"fmt"

	"formal.local/w3b-a9f48525a0/capmig"
)

func main() {
	scenarios := []struct {
		name string
		in   *capmig.Input
	}{
		{"multi-alias impersonation", aliasScenario()},
		{"chained lost confirmations", chainedScenario()},
		{"late revocation receipt", revocationScenario()},
	}
	for _, sc := range scenarios {
		res, err := capmig.Solve(sc.in)
		if err != nil {
			panic(err)
		}
		fmt.Printf("==== %s ====\n%s\n", sc.name, capmig.RenderText(res))
	}
}

func aliasScenario() *capmig.Input {
	return &capmig.Input{
		BarrierEpoch: 1,
		AliasBindings: []capmig.AliasBinding{
			{Alias: "decoy", PhysicalID: "phys-B"},
			{Alias: "target", PhysicalID: "phys-C"},
		},
		ResourceMaps: []capmig.ResourceMapEdge{{OldObject: "phys-B", NewObject: "phys-C"}},
		ActionSplits: []capmig.ActionSplit{
			{OldAction: "manage", NewActions: []string{"read", "admin"}},
		},
		Grants: []capmig.Grant{
			{Token: "TB", Subject: "mallory", Object: "phys-B", Action: "manage", Epoch: 1},
		},
		Handoffs: []capmig.Handoff{
			{ID: "lost", Token: "TB", From: "mallory", To: "alice", Epoch: 6, LostConfirmation: true},
		},
		Tickets: []capmig.Ticket{
			{ID: "spoof", Subject: "alice", NewObject: "target",
				NewActions: []string{"admin"}, AncestorTokens: []string{"TB"}},
		},
	}
}

func chainedScenario() *capmig.Input {
	return &capmig.Input{
		BarrierEpoch: 1,
		AliasBindings: []capmig.AliasBinding{
			{Alias: "old", PhysicalID: "P0"},
			{Alias: "new", PhysicalID: "P1"},
		},
		ResourceMaps: []capmig.ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []capmig.ActionSplit{
			{OldAction: "manage", NewActions: []string{"read", "write"}},
		},
		Grants: []capmig.Grant{{Token: "T", Subject: "alice", Object: "old", Action: "manage", Epoch: 1}},
		Handoffs: []capmig.Handoff{
			{ID: "h1", Token: "T", From: "alice", To: "bob", Epoch: 2, LostConfirmation: true},
			{ID: "h2", Token: "T", From: "bob", To: "carol", Epoch: 3, LostConfirmation: true},
		},
		Tickets: []capmig.Ticket{
			{ID: "carol", Subject: "carol", NewObject: "new",
				NewActions: []string{"read", "write"}, AncestorTokens: []string{"T"}},
		},
	}
}

func revocationScenario() *capmig.Input {
	return &capmig.Input{
		BarrierEpoch: 8,
		AliasBindings: []capmig.AliasBinding{
			{Alias: "old", PhysicalID: "P0"},
			{Alias: "new", PhysicalID: "P1"},
		},
		ResourceMaps: []capmig.ResourceMapEdge{{OldObject: "P0", NewObject: "P1"}},
		ActionSplits: []capmig.ActionSplit{
			{OldAction: "manage", NewActions: []string{"read"}},
		},
		Grants:      []capmig.Grant{{Token: "T", Subject: "alice", Object: "old", Action: "manage", Epoch: 1}},
		Revocations: []capmig.Revocation{{Token: "T", Epoch: 5}},
		Handoffs: []capmig.Handoff{
			{ID: "late", Token: "T", From: "alice", To: "bob", Epoch: 6, ReceiptEpoch: 4},
		},
		Tickets: []capmig.Ticket{
			{ID: "bob", Subject: "bob", NewObject: "new",
				NewActions: []string{"read"}, AncestorTokens: []string{"T"}},
		},
	}
}
