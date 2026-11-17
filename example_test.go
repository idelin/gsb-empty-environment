package netsec_test

import (
	"encoding/hex"
	"fmt"

	netsec "example.org/netsec/q0480"
)

// ExampleCheck shows a host authenticating each captured message's raw
// bytes and asking the checker whether the "weak algorithms only" story in
// the log is backed by authenticated evidence.
func ExampleCheck() {
	const (
		alice   = "alice"
		bob     = "bob"
		strong  = "AES256-GCM"
		medium  = "AES128-GCM"
		weak    = "3DES"
		session = "sess-42"
	)

	env := func(msg *netsec.Message, authed bool) netsec.Envelope {
		raw, _ := netsec.EncodeMessage(msg)
		mark := netsec.AuthMark{Authenticated: authed}
		if authed {
			mark.Signer = msg.Sender
		}
		return netsec.Envelope{Raw: raw, Auth: mark}
	}

	envelopes := []netsec.Envelope{
		// Both peers' authenticated raw capability lists include the
		// strong algorithm.
		env(&netsec.Message{Type: netsec.TypeCapabilities, Sender: alice, SessionID: session,
			Capabilities: []string{strong, medium}}, true),
		env(&netsec.Message{Type: netsec.TypeCapabilities, Sender: bob, SessionID: session,
			Capabilities: []string{strong, medium, weak}}, true),
		// The log nevertheless claims the weak algorithm was selected.
		env(&netsec.Message{Type: netsec.TypeSelection, Sender: alice, SessionID: session,
			Selection: weak}, true),
	}

	report, err := netsec.Check(&netsec.Transcript{
		Envelopes:       envelopes,
		PreferenceOrder: []string{strong, medium, weak},
	})
	if err != nil {
		panic(err)
	}

	fmt.Println("secure:", report.Secure)
	fmt.Println("downgrade detected:", report.HasError(netsec.FindingPreferenceViolation))
	fmt.Println("authenticated intersection:", report.Intersection)
	fmt.Println("transcript digest:", report.TranscriptHash == hex.EncodeToString(netsec.TranscriptDigest(envelopes)))

	// Output:
	// secure: false
	// downgrade detected: true
	// authenticated intersection: [AES256-GCM AES128-GCM]
	// transcript digest: true
}
