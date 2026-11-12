package netsec

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestMetadataAddressBlocked(t *testing.T) {
	mux := http.NewServeMux()
	var hits int32
	mux.HandleFunc("/", func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&hits, 1)
	})
	env := newTestEnv(t, mux)

	cert := certWithCDP(buildCDPExtension(
		*certWithURIs(env.httpURL(metadataHost, "/latest/meta-data/")),
	))
	if _, err := NewChainFetcher(env.fetcher).FetchCRLForCert(context.Background(), cert); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("err = %v, want ErrInvalidAddress", err)
	}

	literal := "http://169.254.169.254/latest/meta-data/"
	if _, err := env.fetcher.FetchCRL(context.Background(), literal); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("literal metadata err = %v, want ErrInvalidAddress", err)
	}
	if atomic.LoadInt32(&hits) != 0 {
		t.Fatal("metadata endpoint must never be reached")
	}
	if len(env.dialed) != 0 {
		t.Fatalf("no dial expected, got %v", env.dialed)
	}
}

func TestDNSRebindingPublicThenPrivate(t *testing.T) {
	mux := http.NewServeMux()
	env := newTestEnv(t, mux)

	// Mixed answer: one public plus one link-local address. The whole lookup
	// must be rejected so the private answer can never be dialed.
	cert := certWithCDP(buildCDPExtension(
		*certWithURIs(env.httpURL(rebindHost, "/crl.der")),
	))
	_, err := NewChainFetcher(env.fetcher).FetchCRLForCert(context.Background(), cert)
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("mixed-answer err = %v, want ErrInvalidAddress", err)
	}
	if len(env.dialed) != 0 {
		t.Fatalf("mixed answer must not dial, got %v", env.dialed)
	}

	// Flapping resolver: preflight sees a public address, dial-time
	// re-resolution returns the link-local metadata address.
	var flapped atomic.Bool
	env.fetcher.lookup = func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == rebindHost {
			if flapped.CompareAndSwap(false, true) {
				return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
			}
			return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
		}
		return env.resolves[host], nil
	}
	_, err = env.fetcher.FetchCRL(context.Background(), env.httpURL(rebindHost, "/crl.der"))
	if !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("flapping resolver err = %v, want ErrInvalidAddress", err)
	}
	if len(env.dialed) != 0 {
		t.Fatalf("rebind must never dial, got %v", env.dialed)
	}
}

func TestRedirectsRevalidatedAndCapped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hop1", http.StatusFound)
	})
	mux.HandleFunc("/hop1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/hop2", http.StatusFound)
	})
	mux.HandleFunc("/hop2", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("REDIRECTED-CRL"))
	})
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+metadataHost+"/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "file:///etc/secret", http.StatusFound)
	})
	mux.HandleFunc("/chain4", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chain4-b", http.StatusFound)
	})
	mux.HandleFunc("/chain4-b", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chain4-c", http.StatusFound)
	})
	mux.HandleFunc("/chain4-c", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/chain4-d", http.StatusFound)
	})
	mux.HandleFunc("/chain4-d", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})

	env := newTestEnv(t, mux)

	body, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/start"))
	if err != nil {
		t.Fatalf("three-hop redirect: %v", err)
	}
	if string(body) != "REDIRECTED-CRL" {
		t.Fatalf("body = %q", body)
	}

	if _, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/chain4")); !errors.Is(err, ErrTooManyRedirects) {
		t.Fatalf("four hops err = %v, want ErrTooManyRedirects", err)
	}
	if _, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/to-metadata")); !errors.Is(err, ErrInvalidAddress) {
		t.Fatalf("redirect to metadata err = %v, want ErrInvalidAddress", err)
	}
	if _, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/to-file")); !errors.Is(err, ErrSchemeNotAllowed) {
		t.Fatalf("redirect to file err = %v, want ErrSchemeNotAllowed", err)
	}
}

func TestBodySizeLimit(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/exact", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxBodyBytes))
	})
	mux.HandleFunc("/oversize", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, MaxBodyBytes+1024))
	})
	env := newTestEnv(t, mux)

	body, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/exact"))
	if err != nil {
		t.Fatalf("exact-size body: %v", err)
	}
	if len(body) != MaxBodyBytes {
		t.Fatalf("exact body len = %d, want %d", len(body), MaxBodyBytes)
	}

	if _, err := env.fetcher.FetchCRL(context.Background(), env.httpURL(publicHost, "/oversize")); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("oversize err = %v, want ErrBodyTooLarge", err)
	}
}

func TestPrivateRangesRejected(t *testing.T) {
	bad := []string{
		"127.0.0.1", "10.1.2.3", "172.16.5.4", "192.168.0.9",
		"169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1",
		"255.255.255.255", "240.0.0.1", "192.0.2.7", "::1",
		"fc00::1", "fe80::1", "2001:db8::1",
	}
	for _, s := range bad {
		addr := netip.MustParseAddr(s)
		if isGlobalUnicast(addr) {
			t.Errorf("isGlobalUnicast(%s) = true, want false", s)
		}
	}
	good := []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111", "2001:4860:4860::8888"}
	for _, s := range good {
		addr := netip.MustParseAddr(s)
		if !isGlobalUnicast(addr) {
			t.Errorf("isGlobalUnicast(%s) = false, want true", s)
		}
	}
}

func TestUserInfoRejected(t *testing.T) {
	env := newTestEnv(t, http.NewServeMux())
	raw := strings.Replace(env.httpURL(publicHost, "/crl.der"), "http://", "http://user:pass@", 1)
	if _, err := env.fetcher.FetchCRL(context.Background(), raw); !errors.Is(err, ErrBadURL) {
		t.Fatalf("userinfo URL err = %v, want ErrBadURL", err)
	}
	if len(env.dialed) != 0 {
		t.Fatalf("userinfo URL must not dial, got %v", env.dialed)
	}
}
