package netsec

import "fmt"

// ExampleAnalyze walks through the headline teaching contrast: a long-term key
// compromise opens past traffic, while a destroyed session key keeps its past
// session secret even after a later compromise attempt.
func ExampleModel_Analyze() {
	m := NewModel()

	ltk := m.Name("ltk")
	sessionKey := m.Name("session_key")
	pastSecret := m.Name("past_secret")
	laterSecret := m.Name("later_secret")

	m.Send(m.Enc(sessionKey, pastSecret), 1)
	m.Destroy(sessionKey, 2)
	m.Send(m.Enc(ltk, laterSecret), 3)
	m.Compromise(sessionKey, 4) // rejected: destroyed at t=2
	m.Compromise(ltk, 5)

	for _, r := range m.Analyze(pastSecret, laterSecret) {
		if !r.Leaked {
			fmt.Printf("%s: never leaks\n", r.Secret)
			continue
		}
		fmt.Printf("%s: earliest leak t=%d\n%s\n", r.Secret, r.Earliest, r.Proof)
	}

	// Output:
	// past_secret: never leaks
	// later_secret: earliest leak t=5
	// t=5 decrypt: later_secret
	//     t=3 send: enc(ltk, later_secret) (event #3)
	//     t=5 compromise: ltk (event #5)
}
