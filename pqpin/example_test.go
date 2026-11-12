package pqpin_test

import (
	"crypto/tls"
	"fmt"
	"time"

	"example.org/netsec/q0214/pqpin"
)

// ExampleDialer shows the recommended connection-establishment pattern. It is
// a documentation-only example because it would open a real network socket.
func ExampleDialer_usage() {
	// Pins are valid for a week; the clock is injectable for testing.
	pinner := pqpin.New(pqpin.WithTTL(7 * 24 * time.Hour))

	dialer := &pqpin.Dialer{
		Pinner: pinner,
		TLSConfig: &tls.Config{
			// Verify certificates normally; pinning is an additional control.
			MinVersion: tls.VersionTLS13,
		},
	}

	conn, err := dialer.Dial("tcp", "example.com:443")
	if err != nil {
		if de, ok := pqpin.AsDowngrade(err); ok {
			// A pinned server negotiated a classic group, a classic HRR, or
			// resumed a classic-origin session. Refuse the connection.
			fmt.Printf("downgrade blocked: %s\n", de.Reason)
			return
		}
		fmt.Printf("dial failed: %v\n", err)
		return
	}
	defer conn.Close()

	// A successful hybrid handshake has already refreshed the pin after the
	// peer's Finished was verified. Unpinned hosts are learned on first use.
	fmt.Println("connected with hybrid key exchange")
}

func ExamplePinner_manualEnforcement() {
	pinner := pqpin.New()

	// Manual enforcement when integrating with an existing dial path. Only
	// call Commit once the handshake completed and Finished was verified.
	obs := pqpin.Observation{
		ServerID:        "example.com:443",
		NegotiatedGroup: uint16(tls.X25519MLKEM768),
	}
	if err := pinner.Evaluate(obs); err != nil {
		fmt.Println(err)
		return
	}
	if err := pinner.Commit(obs); err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("pin committed")
	// Output: pin committed
}
