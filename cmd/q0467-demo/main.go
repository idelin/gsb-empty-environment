// Command q0467-demo drives the netsec knowledge closure through the
// late-key-leakage scenario and prints its JSON report. It is a small
// host integration example rather than part of the library API.
package main

import (
	"fmt"
	"os"

	netsec "example.org/netsec/q0467"
)

func main() {
	engine := netsec.NewEngine()

	secret, err := netsec.ParseTerm("n_secret")
	if err != nil {
		fail(err)
	}
	if _, err := engine.SetTarget(secret); err != nil {
		fail(err)
	}

	events := []struct {
		term string
		at   int
	}{
		{"enc(n_secret,k_session)", 1},
		{"a", 2},
		{"pair(a,alice)", 3},
		{"k_session", 4},
	}
	for _, event := range events {
		if err := engine.AddFactString(event.term, event.at); err != nil {
			fail(err)
		}
	}

	raw, err := engine.JSON()
	if err != nil {
		fail(err)
	}
	fmt.Println(string(raw))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
