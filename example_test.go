package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0467"
)

// Example shows a symbol-verification host feeding observed protocol
// events into the engine, registering a target secret, and reading its
// earliest knowability after a late key leakage.
func Example() {
	engine := netsec.NewEngine()

	target, err := engine.SetTarget(mustParse("n_secret"))
	if err != nil {
		panic(err)
	}

	// Session traffic: only the ciphertext is observed initially.
	if err := engine.AddFactString("enc(n_secret,k_session)", 1); err != nil {
		panic(err)
	}
	if at, known := engine.Earliest(target); !known {
		fmt.Println("event 1: secret unknown, ciphertext-only")
	} else {
		fmt.Println("event 1: secret leaked at", at)
	}

	// The session key is disclosed only after the session ends.
	if err := engine.AddFactString("k_session", 4); err != nil {
		panic(err)
	}
	at, known := engine.Earliest(target)
	fmt.Printf("event 4: secret known=%v at=%d\n", known, at)

	edge, _ := engine.Derivation(target)
	fmt.Println("rule:", edge.Rule)

	report := engine.Report()
	fmt.Println("consistent:", report.Consistent)

	// Output:
	// event 1: secret unknown, ciphertext-only
	// event 4: secret known=true at=4
	// rule: dec
	// consistent: true
}

func mustParse(s string) netsec.Term {
	term, err := netsec.ParseTerm(s)
	if err != nil {
		panic(err)
	}
	return term
}
