package canon_test

import (
	"fmt"

	"example.org/netsec/q0465/canon"
)

func ExampleCanonicalize() {
	// One protocol state with concrete, run-specific nonce names.
	state := &canon.State{
		Queue: []canon.Message{{
			From: "init:s1",
			To:   "resp:s1",
			Payload: canon.Tuple{Items: []canon.Term{
				canon.Const{Name: "init"},
				canon.Fresh{Session: "s1", UID: "random-uid-77"},
			}},
		}},
		AttackerKnowledge: []canon.Term{
			canon.Fresh{Session: "s1", UID: "random-uid-77"},
			canon.Const{Name: "pub"},
		},
	}

	canonical, certificate := canon.Canonicalize(state)

	atom := canonical.Queue[0].Payload.(canon.Tuple).Items[1].(canon.Fresh)
	fmt.Printf("canonical atom: session=%s uid=%s\n", atom.Session, atom.UID)

	name, ok := certificate.CanonicalName(canon.AtomRef{
		Session: "s1",
		UID:     "random-uid-77",
	})
	fmt.Printf("certificate lookup: ok=%v session=%s index=%d bijection=%v\n",
		ok, name.Session, name.Index, certificate.IsBijection())

	// The public constant is never renamed.
	for _, term := range canonical.AttackerKnowledge {
		if c, ok := term.(canon.Const); ok {
			fmt.Printf("constant preserved: %s\n", c.Name)
		}
	}

	// Output:
	// canonical atom: session=s1 uid=c0
	// certificate lookup: ok=true session=s1 index=0 bijection=true
	// constant preserved: pub
}
