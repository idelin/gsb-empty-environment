package capsum_test

import (
	"fmt"

	"prescreen.local/ps-2237843e01/capsum"
)

// ExampleAnalyze shows the importable entry points: build a program with
// normalized path grants, run the fixpoint, inspect a program point, and emit
// a hash-bound certificate.
func ExampleAnalyze() {
	p := &capsum.Program{
		Name:       "demo",
		MaxHandles: 4,
		Roots: []capsum.Root{
			{Name: "ROOT", Version: "v1"},
			{Name: "TENANT", AliasOf: "ROOT", Version: "tenant-v1"},
		},
		Domains: []capsum.Domain{
			{Name: "D", Grants: []capsum.Grant{
				{Root: "ROOT", Components: []string{"data"}, Kind: capsum.GrantChild},
			}},
		},
		EntryNode:    "main",
		EntryDomains: []string{"D"},
		Nodes:        []string{"main", "use"},
		Edges: []capsum.Edge{
			{
				ID: "e1", From: "main", To: "use", Kind: capsum.EdgeLocal,
				Action: &capsum.FileAction{
					Name: "read", Root: "ROOT", Path: []string{"data", "file1"},
				},
			},
		},
	}

	res, evidence, err := capsum.AnalyzeWithEvidence(p, 0)
	if err != nil {
		panic(err)
	}
	pr := res.Points["use"]
	fmt.Println(pr.Status)

	guaranteed, possible, status := res.CheckAction("use", &capsum.FileAction{
		Name: "read", Root: "ROOT", Path: []string{"data", "file1"},
	})
	fmt.Println(guaranteed, possible, status)

	c := res.Certificate(p, evidence)
	raw, err := c.Marshal()
	if err != nil {
		panic(err)
	}
	fmt.Println(len(raw) > 0)
	// Output:
	// reachable
	// true true reachable
	// true
}
