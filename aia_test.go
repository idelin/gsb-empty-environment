package netsec

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"testing"
	"time"
)

var serial int64

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// issue creates a certificate. parent may be a synthetic certificate
// carrying only the issuer subject, which allows cross-signed loops.
func issue(t *testing.T, cn string, isCA bool, aia []string, parent *x509.Certificate, parentKey, key *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	serial++
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(serial),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IssuingCertificateURL: aia,
		BasicConstraintsValid: true,
		IsCA:                  isCA,
	}
	if isCA {
		tmpl.KeyUsage = x509.KeyUsageCertSign
	} else {
		tmpl.KeyUsage = x509.KeyUsageDigitalSignature
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, &key.PublicKey, parentKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func selfSignedRoot(t *testing.T, cn string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key := newKey(t)
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		BasicConstraintsValid: true,
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

// roundTripFunc serves AIA responses from memory; the sandbox forbids
// listening sockets, so tests never touch a real network.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// aiaClient returns an http.Client that serves DER certificates from
// files, keyed by request URL, and fails any other URL.
func aiaClient(t *testing.T, files map[string][]byte) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		der, ok := files[r.URL.String()]
		if !ok {
			return nil, errors.New("unexpected AIA fetch: " + r.URL.String())
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(der)),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	})}
}

// TestBuildPathOneHTTPSHop: the leaf's issuer is one https fetch away,
// so the path completes and verification continues.
func TestBuildPathOneHTTPSHop(t *testing.T) {
	root, rootKey := selfSignedRoot(t, "root")
	interKey := newKey(t)
	inter := issue(t, "inter", true, nil, root, rootKey, interKey)

	const interURL = "https://aia.example/inter.der"

	leafKey := newKey(t)
	leaf := issue(t, "leaf", false, []string{interURL}, inter, interKey, leafKey)

	roots := x509.NewCertPool()
	roots.AddCert(root)
	b := &Builder{Roots: roots, MaxHops: 4, HTTPClient: aiaClient(t, map[string][]byte{interURL: inter.Raw})}

	chains, err := b.BuildPath(context.Background(), leaf)
	if err != nil {
		t.Fatalf("BuildPath: %v", err)
	}
	if len(chains) != 1 || len(chains[0]) != 3 {
		t.Fatalf("unexpected chains: %v", chains)
	}
	if got := chains[0][2].Subject.CommonName; got != "root" {
		t.Fatalf("chain root = %q, want root", got)
	}
}

// TestBuildPathRejectsFileScheme: a file:// issuer address must never be
// fetched; the build fails instead of reading local files.
func TestBuildPathRejectsFileScheme(t *testing.T) {
	_, rootKey := selfSignedRoot(t, "root")
	caKey := newKey(t)
	ca := issue(t, "ca", true, nil, &x509.Certificate{Subject: pkix.Name{CommonName: "root"}}, rootKey, caKey)
	leafKey := newKey(t)
	leaf := issue(t, "leaf", false, []string{"file:///etc/ssl/ca.der"}, ca, caKey, leafKey)

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Fatalf("file scheme must not be fetched, got %s", r.URL)
		return nil, nil
	})}
	b := &Builder{Roots: x509.NewCertPool(), MaxHops: 4, HTTPClient: client}
	_, err := b.BuildPath(context.Background(), leaf)
	if !errors.Is(err, ErrSchemeNotAllowed) {
		t.Fatalf("err = %v, want ErrSchemeNotAllowed", err)
	}
}

// loopCerts builds leaf -> ca1 -> ca2 -> ca3 -> ca1, a cross-signed
// three-hop AIA loop, plus the client serving the CA certificates.
func loopCerts(t *testing.T) (leaf *x509.Certificate, client *http.Client) {
	t.Helper()
	k1, k2, k3 := newKey(t), newKey(t), newKey(t)
	name1 := &x509.Certificate{Subject: pkix.Name{CommonName: "ca1"}}
	name2 := &x509.Certificate{Subject: pkix.Name{CommonName: "ca2"}}
	name3 := &x509.Certificate{Subject: pkix.Name{CommonName: "ca3"}}

	const (
		url1 = "http://aia.example/ca1.der"
		url2 = "http://aia.example/ca2.der"
		url3 = "http://aia.example/ca3.der"
	)

	ca1 := issue(t, "ca1", true, []string{url2}, name2, k2, k1)
	ca2 := issue(t, "ca2", true, []string{url3}, name3, k3, k2)
	ca3 := issue(t, "ca3", true, []string{url1}, name1, k1, k3)

	leafKey := newKey(t)
	leaf = issue(t, "leaf", false, []string{url1}, ca1, k1, leafKey)

	client = aiaClient(t, map[string][]byte{url1: ca1.Raw, url2: ca2.Raw, url3: ca3.Raw})
	return leaf, client
}

// TestBuildPathDetectsThreeHopLoop: the chase revisits /ca1.der after
// three fetches and must fail instead of looping forever.
func TestBuildPathDetectsThreeHopLoop(t *testing.T) {
	leaf, client := loopCerts(t)
	b := &Builder{Roots: x509.NewCertPool(), MaxHops: 8, HTTPClient: client}
	_, err := b.BuildPath(context.Background(), leaf)
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("err = %v, want ErrLoopDetected", err)
	}
}

// TestBuildPathMaxHops: with a tighter caller limit the same loop fails
// on the hop budget before any URL is revisited.
func TestBuildPathMaxHops(t *testing.T) {
	leaf, client := loopCerts(t)
	b := &Builder{Roots: x509.NewCertPool(), MaxHops: 2, HTTPClient: client}
	_, err := b.BuildPath(context.Background(), leaf)
	if !errors.Is(err, ErrMaxHops) {
		t.Fatalf("err = %v, want ErrMaxHops", err)
	}
}

// TestBuildPathZeroHops: fetching is caller-opt-in; zero hops never
// touches the network.
func TestBuildPathZeroHops(t *testing.T) {
	leaf, _ := loopCerts(t)
	b := &Builder{Roots: x509.NewCertPool(), MaxHops: 0}
	_, err := b.BuildPath(context.Background(), leaf)
	if !errors.Is(err, ErrMaxHops) {
		t.Fatalf("err = %v, want ErrMaxHops", err)
	}
}
