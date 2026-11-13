package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0235"
)

func ExampleStateMachine() {
	machine := netsec.NewStateMachine()

	for _, event := range []netsec.Event{
		netsec.CertificateRequest,
		netsec.Certificate,
		netsec.CertificateVerify,
		netsec.Finished,
	} {
		if err := machine.Step(event); err != nil {
			panic(err)
		}
	}

	fmt.Println(machine.State())
	// Output: Finished
}
