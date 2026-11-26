package sshcert_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"

	"formal.local/w27-ede4350407/sshcert"
)

// ExampleVerify shows the host-side flow: parse a certificate, verify it
// against a trusted CA with policy options, and consult a parsed KRL.
func ExampleVerify() {
	// In a real host these keys and blobs arrive from provisioned CA material
	// and from the SSH certificate authentication message.
	caPub, _, _ := ed25519.GenerateKey(rand.Reader)
	leafPub, _, _ := ed25519.GenerateKey(rand.Reader)

	caBlob := ed25519Blob(caPub)
	_ = leafPub

	// A KRL parsed once at startup and reused across authentications.
	krl, err := sshcert.ParseKRL(emptyKRL())
	if err != nil {
		panic(err)
	}

	// certBytes would be the raw ssh-ed25519-cert-v01 certificate from the
	// peer; here it is omitted to keep the example self-contained.
	var certBytes []byte
	cert, err := sshcert.ParseCertificate(certBytes)
	if err != nil {
		// Reject authentication.
		fmt.Println("parse failed")
		return
	}

	result, err := sshcert.Verify(cert, sshcert.VerifyOptions{
		ExpectedType:    sshcert.CertTypeUser,
		CAPublicKeyBlob: caBlob,
		Principal:       "alice",
		RemoteIP:        net.ParseIP("10.0.0.5"),
		Now:             1700000000,
		KRL:             krl,
	})
	if err != nil {
		fmt.Println("verify failed")
		return
	}
	if result.ForceCommandPresent {
		// Execute result.ForceCommand and ignore any client-supplied command.
		fmt.Printf("forced: %s\n", result.ForceCommand)
	}

	// Output:
	// parse failed
}

// ed25519Blob encodes an Ed25519 key as an SSH public-key blob.
func ed25519Blob(key ed25519.PublicKey) []byte {
	algo := []byte(sshcert.KeyAlgoEd25519)
	buf := make([]byte, 4+len(algo)+4+len(key))
	putBE32(buf, uint32(len(algo)))
	copy(buf[4:], algo)
	putBE32(buf[4+len(algo):], uint32(len(key)))
	copy(buf[8+len(algo):], key)
	return buf
}

func putBE32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

// emptyKRL returns a minimal valid SSHKRL with no revocations.
func emptyKRL() []byte {
	var b []byte
	b = append(b, []byte("SSHKRL")...)
	b = append(b, 0, 0, 0, 1)         // version 1
	b = append(b, make([]byte, 8)...) // generation date
	b = append(b, 0, 0, 0, 0)         // empty comment
	b = append(b, 0, 0, 0, 0, 0)      // NULL terminator (type + zero length)
	return b
}
