package timeverify_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"time"

	"example.org/netsec/q0077/timeverify"
)

// ExampleVerifier shows how a verification service checks the signed
// timestamp of a Cosign bundle against a trusted transparency log and the
// leaf certificate's validity window.
func ExampleVerifier() {
	logKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	trusted, err := timeverify.NewLogKey(&logKey.PublicKey)
	if err != nil {
		panic(err)
	}

	now := time.Now().UTC()
	leaf := selfSignedLeaf(now.Add(-time.Hour), now.Add(time.Hour))

	verifier, err := timeverify.NewVerifier(
		[]*timeverify.LogKey{trusted},
		timeverify.WithSkew(30*time.Second),
	)
	if err != nil {
		panic(err)
	}

	payload := timeverify.Payload{
		Body:           timeverify.EncodeBody([]byte(`{"kind":"hashedrekord"}`)),
		IntegratedTime: now.Unix(),
		LogIndex:       128,
		LogID:          trusted.LogID,
	}
	set, err := timeverify.SignSET(logKey, payload)
	if err != nil {
		panic(err)
	}

	bundle := &timeverify.Bundle{
		Leaf:    leaf,
		Payload: payload,
		SET:     set,
	}

	if err := verifier.Verify(bundle); err != nil {
		switch {
		case errors.Is(err, timeverify.ErrInvalidLogID):
			fmt.Println("rejected: untrusted or swapped logID")
		case errors.Is(err, timeverify.ErrSETVerification):
			fmt.Println("rejected: tampered SET payload")
		case errors.Is(err, timeverify.ErrIntegratedTimeAfterCert):
			fmt.Println("rejected: signed time is after certificate expiry")
		default:
			fmt.Println("rejected:", err)
		}
		return
	}
	fmt.Println("bundle timestamp verified")
	// Output: bundle timestamp verified
}

func selfSignedLeaf(notBefore, notAfter time.Time) *x509.Certificate {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		panic(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		panic(err)
	}
	return cert
}
