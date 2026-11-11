package shortcircuit_test

import (
	"context"
	"crypto/ed25519"
	"fmt"

	"example.org/netsec/q0119/shortcircuit"
)

func Example() {
	// The host owns a long-lived signing key; the auditor only gets the public key.
	pub, priv, _ := ed25519.GenerateKey(nil)
	engine := shortcircuit.NewEngine(shortcircuit.WithSigningKey(priv))
	auditor := shortcircuit.NewValidator(shortcircuit.WithVerificationKey(pub))

	ipAllow := &shortcircuit.CheckNode{
		ID:   "ip-allow",
		Rule: shortcircuit.RuleVersion{Name: "ipacl", Version: "v3"},
		Fn: func(context.Context) (bool, shortcircuit.Evidence, error) {
			return true, shortcircuit.Evidence{Summary: "ip 10.0.0.5 in allowlist"}, nil
		},
	}
	tokenCheck := &shortcircuit.CheckNode{
		ID:   "token",
		Rule: shortcircuit.RuleVersion{Name: "jwt", Version: "v12"},
		Fn: func(context.Context) (bool, shortcircuit.Evidence, error) {
			return false, shortcircuit.Evidence{Summary: "token expired"}, nil
		},
	}

	// allow := ipAllow AND tokenCheck
	root := &shortcircuit.AndNode{ID: "allow", Children: []shortcircuit.Node{ipAllow, tokenCheck}}

	decision, trace, err := engine.Evaluate(context.Background(), "decision-42", root)
	fmt.Printf("decision=%v err=%v entries=%d\n", decision, err, len(trace.Entries))

	// The auditor replays the trace offline; no check function is ever invoked.
	if err := auditor.Validate(root, trace); err != nil {
		fmt.Println("rejected:", err)
		return
	}
	for _, e := range trace.Entries {
		fmt.Printf("seq=%d node=%s status=%s rule=%s@%s\n",
			e.Seq, e.NodeID, e.Status, e.RuleName, e.RuleVersion)
	}
	// Output:
	// decision=false err=<nil> entries=3
	// seq=1 node=ip-allow status=true rule=ipacl@v3
	// seq=2 node=token status=false rule=jwt@v12
	// seq=3 node=allow status=completed rule=@
}
