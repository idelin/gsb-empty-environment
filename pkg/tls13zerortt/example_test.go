package tls13zerortt_test

import (
	"fmt"
	"time"

	"formal.local/w28-742f7bec6e/pkg/tls13zerortt"
)

// ExampleValidator shows the host-facing admission flow for one ClientHello.
func ExampleValidator() {
	now := time.Unix(1_700_000_000, 0)
	psk := make([]byte, 32) // In production this comes from the ticket store.
	for i := range psk {
		psk[i] = byte(i)
	}

	const measuredAgeMs = 10_000
	ticket := &tls13zerortt.SessionTicket{
		PSK:              psk,
		Mode:             tls13zerortt.ResumptionPSK,
		TicketAgeAdd:     0x01020304,
		Lifetime:         time.Hour,
		IssuedAt:         now.Add(-measuredAgeMs * time.Millisecond),
		CipherSuite:      0x1301,
		ALPN:             []byte("h2"),
		MaxEarlyDataSize: 1024,
	}

	replay, err := tls13zerortt.NewBoundedReplayFilter(time.Minute, 4096)
	if err != nil {
		panic(err)
	}
	v, err := tls13zerortt.NewValidator(tls13zerortt.Config{
		Policy: tls13zerortt.TicketPolicy{Skew: 5 * time.Second},
		Replay: replay,
		IsIdempotent: func(route string) bool {
			return route == "GET /balance"
		},
	})
	if err != nil {
		panic(err)
	}

	obfAge := uint32(measuredAgeMs) + ticket.TicketAgeAdd
	msg := wireBuilder{}.buildHello(psk, []byte("ticket-1"), obfAge, 0x1301, true)
	parsed, err := tls13zerortt.ParseClientHello(msg)
	if err != nil {
		panic(err)
	}

	admit := func(route string) tls13zerortt.Decision {
		return v.Admit(tls13zerortt.AdmissionRequest{
			Parsed:        parsed,
			IdentityIndex: 0,
			Ticket:        ticket,
			Now:           now,
			OfferedALPN:   []byte("h2"),
			RouteKey:      route,
		}).Decision
	}

	// First attempt on an idempotent route is admitted for 0-RTT.
	fmt.Println(admit("GET /balance"))
	// The same single-use ticket replayed on a non-idempotent route is
	// downgraded to a 1-RTT resumption.
	fmt.Println(admit("POST /pay"))

	// Output:
	// accept-early-data
	// resume-1-rtt
}
