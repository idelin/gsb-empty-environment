package netsec_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"time"

	netsec "example.org/netsec/q-021"
)

func ExampleVerify() {
	rootPublic, rootPrivate, _ := ed25519.GenerateKey(rand.Reader)
	serverPublic, serverPrivate, _ := ed25519.GenerateKey(rand.Reader)
	clientPublic, _, _ := ed25519.GenerateKey(rand.Reader)

	notBefore := time.Now().Add(-time.Minute)
	notAfter := time.Now().Add(time.Hour)

	parent := netsec.NewCertificate(
		serverPublic, "root", 1,
		[]string{"read", "write"}, []string{"/srv"}, 2,
		notBefore, notAfter,
	)
	_ = parent.Sign(rootPrivate)

	leaf := netsec.NewCertificate(
		clientPublic, parent.SubjectID(), 2,
		[]string{"read"}, []string{"/srv/app"}, 0,
		notBefore, notAfter,
	)
	_ = leaf.Sign(serverPrivate)

	_, _ = netsec.Verify(netsec.Request{
		Operation:        "read",
		Resource:         "/srv/app/report",
		At:               time.Now(),
		SubjectPublicKey: clientPublic,
	}, []netsec.Certificate{*parent, *leaf},
		[]netsec.TrustAnchor{{ID: "root", PublicKey: rootPublic}})
}
