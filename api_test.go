package netsec_test

import (
	"context"
	"errors"
	"testing"

	netsec "example.org/netsec/q0178"
)

func TestImportablePublicAPI(t *testing.T) {
	if err := netsec.ValidateStatusListURI("https://issuer.example", "https://evil.example/status"); !errors.Is(err, netsec.ErrStatusListHostMismatch) {
		t.Fatalf("cross-host list error = %v", err)
	}
	if !netsec.SameHost("https://issuer.example", "https://issuer.example/status") {
		t.Fatal("same-host list should be trusted")
	}

	v := &netsec.Verifier{Fetcher: netsec.StatusListFetcherFunc(func(context.Context, string) ([]byte, error) {
		return nil, errors.New("unexpected fetch")
	})}
	_, err := v.CheckRevocation(context.Background(), "https://issuer.example", netsec.StatusListReference{
		URI:   "https://evil.example/status",
		Index: 1,
	})
	if !errors.Is(err, netsec.ErrStatusListHostMismatch) {
		t.Fatalf("CheckRevocation error = %v", err)
	}
}
