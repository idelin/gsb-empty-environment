package netsec

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync/atomic"
	"testing"
)

// TestRedirectRebindBlocksAtDial ensures the dial-time re-resolution also
// protects redirect targets, not just the initial URL.
func TestRedirectRebindBlocksAtDial(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/go", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+rebindHost+"/crl.der", http.StatusFound)
	})
	var downstreamHits int32
	mux.HandleFunc("/crl.der", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&downstreamHits, 1)
	})
	env := newTestEnv(t, mux)

	// rebindHost answers with a public and a link-local address together.
	_, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/go"))
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("redirect rebind err = %v, want ErrInvalidAddress", err)
	}
	if atomic.LoadInt32(&downstreamHits) != 0 {
		t.Fatal("redirect target must never be reached")
	}
}

// TestPreflightThenFlap models the classic TOCTOU: the preflight lookup
// returns a public address while the dial-time lookup returns the metadata
// address. The connection must still be blocked.
func TestPreflightThenFlap(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/crl.der", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	env := newTestEnv(t, mux)

	var lookups atomic.Int32
	env.fetcher.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == rebindHost {
			n := lookups.Add(1)
			if n == 1 {
				// preflight: benign public answer
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			// dial time: attacker flips to the metadata address
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
		return env.resolves[host], nil
	}

	_, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(rebindHost, "/crl.der"))
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("TOCTOU flap err = %v, want ErrInvalidAddress", err)
	}
	if atomic.LoadInt32(&hits) != 0 || len(env.dialed) != 0 {
		t.Fatalf("flapped address must never be dialed/served, dialed=%v", env.dialed)
	}
}

// TestValidatedAddressIsDialed proves the dialer uses a validated resolved
// address (8.8.8.8) rather than the hostname or an unvalidated result.
func TestValidatedAddressIsDialed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/crl.der", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	env := newTestEnv(t, mux)

	body, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/crl.der"))
	if err != nil {
		t.Fatalf("FetchCRL: %v", err)
	}
	if string(body) != "OK" {
		t.Fatalf("body = %q", body)
	}
	if len(env.dialed) != 1 {
		t.Fatalf("dialed %d times, want 1", len(env.dialed))
	}
	gotHost, _, _ := net.SplitHostPort(env.dialed[0])
	if gotHost != "8.8.8.8" && gotHost != "1.1.1.1" {
		t.Fatalf("dialed host %q is not a validated public address", gotHost)
	}
}
