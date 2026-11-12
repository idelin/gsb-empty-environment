package earlygate_test

import (
	"fmt"

	"example.org/netsec/q0165/earlygate"
)

type request struct {
	name    string
	mutates bool
}

func ExampleGate() {
	classifier := earlygate.ClassifierFunc[request](func(op request) (earlygate.Classification, error) {
		return earlygate.Classification{
			ReadOnly:   !op.mutates,
			Replayable: !op.mutates,
			Identity:   earlygate.IdentityNone,
		}, nil
	})

	gate, err := earlygate.New[request](classifier, earlygate.WithDecisionHandler(
		func(decisions []earlygate.Decision[request]) {
			for _, d := range decisions {
				switch d.(type) {
				case earlygate.ExecuteEarly[request]:
					fmt.Println("callback: execute early")
				case earlygate.HoldEarly[request]:
					fmt.Println("callback: hold")
				case earlygate.DispatchNormal[request]:
					fmt.Println("callback: dispatch normal")
				}
			}
		},
	))
	if err != nil {
		panic(err)
	}

	for _, d := range gate.SubmitBatch(1, []request{
		{name: "get-profile", mutates: false},
		{name: "update-billing", mutates: true},
	}) {
		switch d.(type) {
		case earlygate.ExecuteEarly[request]:
			fmt.Println("returned: execute early")
		case earlygate.HoldEarly[request]:
			fmt.Println("returned: hold")
		}
	}

	confirmed, err := gate.Confirm()
	if err != nil {
		panic(err)
	}
	fmt.Printf("confirmed %d held request(s) as normal requests\n", len(confirmed))
	fmt.Printf("early requests never replayed: %v\n", gate.EarlyRequests())

	// Output:
	// callback: execute early
	// callback: hold
	// returned: execute early
	// returned: hold
	// callback: dispatch normal
	// confirmed 1 held request(s) as normal requests
	// early requests never replayed: [1]
}
