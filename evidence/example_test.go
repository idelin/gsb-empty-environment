package evidence_test

import (
	"crypto/ed25519"
	"fmt"

	"example.org/domainreissue/q012/evidence"
)

// Example shows a mail host trusting one access node, consuming its signed
// facts, and obtaining the minimum authenticated fact set for release.
func Example() {
	public, private, _ := ed25519.GenerateKey(nil)
	engine := &evidence.Engine{
		Trusted: evidence.TrustedNodes{"edge-1": public},
	}

	message := []byte("Received: ...\r\n\r\ntransaction alert")
	spf, err := evidence.NewFact("spf.pass", "edge-1", "spf", message, true, private)
	if err != nil {
		panic(err)
	}
	dkim, err := evidence.NewFact("dkim.pass", "edge-1", "dkim", message, true, private)
	if err != nil {
		panic(err)
	}

	rule, err := evidence.ParseRule("spf.pass and dkim.pass and not dmarc.disavowed")
	if err != nil {
		panic(err)
	}

	decision, err := engine.Evaluate(rule, message, []*evidence.Fact{spf, dkim})
	if err != nil {
		panic(err)
	}

	fmt.Printf("value=%s release=%t authenticated=%d minimal=%d\n",
		decision.Value, decision.Release,
		len(decision.Authenticated), len(decision.MinimalSets))
	fmt.Printf("dmarc.disavowed=%s\n", decision.Values["dmarc.disavowed"])

	disavowed, err := evidence.NewFact("dmarc.disavowed", "edge-1", "dmarc", message, false, private)
	if err != nil {
		panic(err)
	}
	decision, err = engine.Evaluate(rule, message, []*evidence.Fact{spf, dkim, disavowed})
	if err != nil {
		panic(err)
	}
	fmt.Printf("value=%s release=%t minimal-set-size=%d\n",
		decision.Value, decision.Release, len(decision.MinimalSets[0]))

	// Output:
	// value=unknown release=false authenticated=2 minimal=0
	// dmarc.disavowed=unknown
	// value=true release=true minimal-set-size=3
}
