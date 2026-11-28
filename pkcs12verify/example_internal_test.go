package pkcs12verify

import (
	"errors"
	"fmt"
)

// ExampleVerify shows the host-side import gateway flow: validate the raw
// uploaded PFX bytes before any key or certificate handling, and classify the
// rejection for audit logging.
func ExampleVerify() {
	b := newBuilder().addCert().addShroudedKey(false)
	der, _ := b.Build()

	report, err := Verify(der, b.password, Options{
		MinIterations: DefaultMinIterations,
		MaxIterations: DefaultMaxIterations,
		MinSaltLength: DefaultMinSalt,
	})
	if err != nil {
		switch {
		case errors.Is(err, ErrIntegrity):
			fmt.Println("reject: tampered or password mismatch")
		case errors.Is(err, ErrWeakCipher):
			fmt.Println("reject: weak legacy cipher")
		case errors.Is(err, ErrIterations):
			fmt.Println("reject: iteration count out of policy")
		default:
			fmt.Println("reject:", err)
		}
		return
	}

	fmt.Printf("accept: mode=%s certs=%d shroudedKeys=%d cipher=%s\n",
		report.MACMode, report.CertBagCount, report.ShroudedKeyCount,
		report.Ciphers[0].Algorithm)
	// Output: accept: mode=pkcs12-mac certs=1 shroudedKeys=1 cipher=AES-256-CBC
}

// ExampleVerify_rejectWeakCipher demonstrates the default-deny classification
// for a PKCS#12 legacy RC2 PBE algorithm.
func ExampleVerify_rejectWeakCipher() {
	b := newBuilder().addCert().addWeakShroudedKey(oidPbeWithSHAAnd40BitRC2)
	der, _ := b.Build()

	_, err := Verify(der, b.password, Options{})
	switch {
	case errors.Is(err, ErrWeakCipher):
		fmt.Println("reject: weak legacy cipher")
	default:
		fmt.Println("unexpected:", err)
	}
	// Output: reject: weak legacy cipher
}
