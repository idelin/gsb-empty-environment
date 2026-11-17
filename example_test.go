package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0286"
)

// A review host diffs two policy versions over a finite domain and
// reports the operations that the update newly opens.
func Example() {
	dom := netsec.Domain{
		{Name: "subject", Values: []string{"alice", "bob"}},
		{Name: "action", Values: []string{"read", "write"}},
		{Name: "resource", Values: []string{"doc1", "doc2"}},
	}
	readers := netsec.Rect{Values: map[string]map[string]bool{
		"action": {"read": true},
	}}
	aliceWritesDoc2 := netsec.Rect{Values: map[string]map[string]bool{
		"subject":  {"alice": true},
		"action":   {"write": true},
		"resource": {"doc2": true},
	}}
	oldPol := &netsec.Policy{Rules: []netsec.Rule{
		{Name: "readers", Effect: netsec.Allow, When: readers},
	}}
	newPol := &netsec.Policy{Rules: []netsec.Rule{
		{Name: "readers", Effect: netsec.Allow, When: readers},
		{Name: "alice-writes-doc2", Effect: netsec.Allow, When: aliceWritesDoc2},
	}}
	res := netsec.Diff(newPol, oldPol, dom, 0)
	if err := netsec.Verify(res, newPol, oldPol, dom); err != nil {
		panic(err)
	}
	for _, cr := range res.Covered {
		fmt.Printf("newly opened: %v granted by %v\n", cr.Rect.Values, cr.GrantedBy)
	}
	// Output:
	// newly opened: map[action:map[write:true] resource:map[doc2:true] subject:map[alice:true]] granted by [alice-writes-doc2]
}
