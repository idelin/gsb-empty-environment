package netsec

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestPerCertDistributionPointLimit(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	env := newTestEnv(t, mux)

	points := make([]asn1DistributionPoint, 0, 6)
	for i := 0; i < 6; i++ {
		points = append(points, *certWithURIs(env.httpURL(publicHost, fmt.Sprintf("/dp%d", i))))
	}
	cert := certWithCDP(buildCDPExtension(points...))

	chain := NewChainFetcher(env.fetcher)
	if _, err := chain.FetchCRLForCert(context.Background(), cert); err != nil {
		t.Fatalf("FetchCRLForCert: %v", err)
	}
	if chain.Fetched() != MaxDistributionPointsPerCert {
		t.Fatalf("fetched %d, want %d", chain.Fetched(), MaxDistributionPointsPerCert)
	}
	if got := atomic.LoadInt32(&hits); got != MaxDistributionPointsPerCert {
		t.Fatalf("endpoint hits %d, want %d", got, MaxDistributionPointsPerCert)
	}
}

func TestChainFetchLimit(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	env := newTestEnv(t, mux)
	chain := NewChainFetcher(env.fetcher)

	// Each cert has 4 distribution points, all within the per-cert cap.
	buildCert := func(suffix string) *x509.Certificate {
		points := make([]asn1DistributionPoint, 0, 4)
		for i := 0; i < 4; i++ {
			points = append(points, *certWithURIs(
				env.httpURL(publicHost, fmt.Sprintf("/%s/dp%d", suffix, i))))
		}
		return certWithCDP(buildCDPExtension(points...))
	}

	first := buildCert("a")
	bodies, err := chain.FetchCRLForCert(context.Background(), first)
	if err != nil {
		t.Fatalf("first cert: %v", err)
	}
	if len(bodies) != 4 || chain.Fetched() != 4 {
		t.Fatalf("after first cert: bodies=%d fetched=%d", len(bodies), chain.Fetched())
	}

	second := buildCert("b")
	var secondBodies [][]byte
	secondBodies, err = chain.FetchCRLForCert(context.Background(), second)
	if err != nil {
		t.Fatalf("second cert fills the budget exactly: %v", err)
	}
	// The second certificate contributes four more retrievals, filling the
	// chain to eight retrievals (still under the 12 cap).
	if len(secondBodies) != 4 {
		t.Fatalf("second-cert bodies = %d, want 4", len(secondBodies))
	}
	if chain.Fetched() != 8 {
		t.Fatalf("fetched %d, want 8", chain.Fetched())
	}

	// The third certificate fills the remaining four slots of the 12 cap.
	thirdBodies, err := chain.FetchCRLForCert(context.Background(), buildCert("c"))
	if err != nil {
		t.Fatalf("third cert fills the budget exactly: %v", err)
	}
	if len(thirdBodies) != 4 || chain.Fetched() != MaxFetchesPerChain {
		t.Fatalf("after third cert: bodies=%d fetched=%d", len(thirdBodies), chain.Fetched())
	}

	// A fourth certificate must be refused before any connection is made.
	if _, err = chain.FetchCRLForCert(context.Background(), buildCert("d")); !errors.Is(err, ErrFetchLimit) {
		t.Fatalf("fourth cert err = %v, want ErrFetchLimit", err)
	}
	if chain.Fetched() != MaxFetchesPerChain {
		t.Fatalf("refused retrieval must not be charged, fetched %d", chain.Fetched())
	}
	if got := atomic.LoadInt32(&hits); got != MaxFetchesPerChain {
		t.Fatalf("endpoint hits %d, want %d", got, MaxFetchesPerChain)
	}

	// A fresh chain resets the budget.
	fresh := NewChainFetcher(env.fetcher)
	if fresh.Fetched() != 0 {
		t.Fatal("new chain must start with zero fetches")
	}
}

func TestIndirectCRLFieldsSkipped(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	env := newTestEnv(t, mux)

	withReasons := asn1DistributionPoint{
		DistributionPoint: fullName(env.httpURL(publicHost, "/with-reasons")),
		Reasons:           reasonBitString(),
	}
	withIssuer := asn1DistributionPoint{
		DistributionPoint: fullName(env.httpURL(publicHost, "/with-issuer")),
		CRLIssuer:         crlIssuer(),
	}
	direct := asn1DistributionPoint{
		DistributionPoint: fullName(env.httpURL(publicHost, "/direct")),
	}
	cert := certWithCDP(buildCDPExtension(withReasons, withIssuer, direct))

	bodies, err := NewChainFetcher(env.fetcher).FetchCRLForCert(context.Background(), cert)
	if err != nil {
		t.Fatalf("FetchCRLForCert: %v", err)
	}
	if len(bodies) != 1 {
		t.Fatalf("bodies = %d, want only the direct CRL", len(bodies))
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("endpoint hits %d, want 1", got)
	}
}

func TestCRLDPRoundTrip(t *testing.T) {
	uris := []string{"http://a.example.test/a.crl", "http://b.example.test/b.crl"}
	cert := certWithCDP(buildCDPExtension(
		asn1DistributionPoint{DistributionPoint: fullName(uris...)},
		asn1DistributionPoint{
			DistributionPoint: fullName("http://c.example.test/c.crl"),
			Reasons:           reasonBitString(),
			CRLIssuer:         crlIssuer(),
		},
	))

	entries, err := parseCRLDistributionPoints(cert.Raw)
	if err != nil {
		t.Fatalf("parseCRLDistributionPoints: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if len(entries[0].uris) != 2 || entries[0].uris[0] != uris[0] || entries[0].uris[1] != uris[1] {
		t.Fatalf("entry[0] uris = %v", entries[0].uris)
	}
	if entries[0].reasons || entries[0].crlIssuer {
		t.Fatal("entry[0] must be a direct CRL")
	}
	if !entries[1].reasons || !entries[1].crlIssuer {
		t.Fatalf("entry[1] reasons=%v crlIssuer=%v, want both true", entries[1].reasons, entries[1].crlIssuer)
	}
	if len(entries[1].uris) != 1 || entries[1].uris[0] != "http://c.example.test/c.crl" {
		t.Fatalf("entry[1] uris = %v", entries[1].uris)
	}
}

func TestNoCRLDPExtension(t *testing.T) {
	cert := certWithCDP()
	entries, err := parseCRLDistributionPoints(cert.Raw)
	if err != nil {
		t.Fatalf("parseCRLDistributionPoints: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(entries))
	}
	bodies, err := NewChainFetcher(NewFetcher()).FetchCRLForCert(context.Background(), cert)
	if err != nil {
		t.Fatalf("FetchCRLForCert: %v", err)
	}
	if len(bodies) != 0 {
		t.Fatalf("bodies = %d, want 0", len(bodies))
	}
}
