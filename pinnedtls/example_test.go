package pinnedtls_test

import (
	"crypto/tls"
	"fmt"
	"time"

	"example.org/offlinecli/pinnedtls"
)

// This example shows the full rotation workflow: build time-bounded rules with
// primary and backup pins, bind the verifier to a tls.Config, then atomically
// swap the configuration when keys rotate.
func ExampleVerifier_Update() {
	// Pins are base64-encoded SHA-256 SPKI digests, as used by HPKP.
	currentKey, err := pinnedtls.DecodeSPKIPin("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	if err != nil {
		panic(err)
	}
	nextKey, err := pinnedtls.DecodeSPKIPin("BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB=")
	if err != nil {
		panic(err)
	}

	now := time.Now()
	initial, err := pinnedtls.NewConfig([]pinnedtls.Rule{{
		Name:        "service.example",
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(7 * 24 * time.Hour),
		PrimaryPins: []pinnedtls.SPKIPin{currentKey},
		BackupPins:  []pinnedtls.SPKIPin{nextKey},
	}})
	if err != nil {
		panic(err)
	}

	verifier, err := pinnedtls.NewVerifier(initial)
	if err != nil {
		panic(err)
	}

	tlsConfig := &tls.Config{ServerName: "service.example"}
	if _, err := verifier.Bind(tlsConfig); err != nil {
		panic(err)
	}

	// Rotation: retire the old key, promote the backup to sole primary.
	rotated, err := pinnedtls.NewConfig([]pinnedtls.Rule{{
		Name:        "service.example",
		NotBefore:   now.Add(-time.Hour),
		NotAfter:    now.Add(7 * 24 * time.Hour),
		PrimaryPins: []pinnedtls.SPKIPin{nextKey},
	}})
	if err != nil {
		panic(err)
	}
	if err := verifier.Update(rotated); err != nil {
		panic(err)
	}

	// Existing handshakes keep their snapshot; new handshakes use rotated.
	fmt.Println(tlsConfig.VerifyConnection != nil)
	// Output: true
}
