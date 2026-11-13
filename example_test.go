package netsec

import "fmt"

// ExampleMachine shows a 0-RTT peer that sends one byte beyond the ticket's
// max_early_data. The in-limit prefix reaches the application, the excess byte
// is withheld, and the connection fails.
func ExampleMachine() {
	m := New()
	_, _ = m.Handle(EventStart{MaxEarlyData: 5})
	_, _ = m.Handle(EventBeginEarlyData{})

	// Six early-data bytes against a five-byte ceiling.
	actions, err := m.Handle(EventEarlyData{Payload: []byte("ABCDEF")})

	delivered := 0
	for _, a := range actions {
		if a.Kind == ActionDeliver {
			delivered += len(a.Payload)
		}
	}

	fmt.Printf("delivered=%d state=%s failed=%v withheld=%d\n",
		delivered, m.State(), err != nil, m.EarlyWithheld())
	// Output: delivered=5 state=Failed failed=true withheld=1
}
