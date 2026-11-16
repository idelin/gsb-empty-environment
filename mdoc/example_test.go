package mdoc_test

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"example.org/netsec/q0720/mdoc"
)

// This example shows how a verifier wires its own hashing and device
// signature callbacks. In a real deployment the signature callback also
// resolves the MSO deviceKey and validates the COSE_Sign1 headers.
func ExampleVerifier() {
	verifier := mdoc.Verifier{
		Hash: func(algorithm string, data []byte) ([]byte, error) {
			if algorithm != "SHA-256" {
				return nil, errors.New("unsupported algorithm " + algorithm)
			}
			sum := sha256.Sum256(data)
			return sum[:], nil
		},
		VerifyDeviceSignature: func(authBytes []byte, sig mdoc.DeviceSignature) error {
			// Reconstruct and verify the COSE_Sign1 Signature1 structure
			// against the MSO deviceKey here.
			return errors.New("wire in real COSE verification")
		},
		Now: time.Now,
	}

	requested := mdoc.NewRequestedItems(
		[2]string{"org.iso.18013-5.2.1", "family_name"},
	)

	var response *mdoc.Response
	var ownSessionTranscript []byte
	err := verifier.Verify(response, ownSessionTranscript, requested)
	fmt.Println(err != nil)
	// Output: true
}
