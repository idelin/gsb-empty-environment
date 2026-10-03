package witness_test

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"leasefed/witness"
)

var errListen = errors.New("listen not permitted")

// TestHTTPAPI exercises the real HTTP round-trip. Skipped in sandboxes that
// forbid listening on local ports.
func TestHTTPAPI(t *testing.T) {
	srv, err := func() (s *httptest.Server, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = errListen
			}
		}()
		return httptest.NewServer(witness.New().Handler()), nil
	}()
	if err != nil {
		t.Skipf("cannot listen on a local port in this environment: %v", err)
	}
	defer srv.Close()

	c := witness.NewHTTPClient(srv.URL)
	l, ok, err := c.Acquire("dc1", 200*time.Millisecond)
	if err != nil || !ok || l.Epoch != 1 {
		t.Fatalf("acquire over HTTP failed: %+v ok=%v err=%v", l, ok, err)
	}
	if _, ok, _ := c.Acquire("dc2", 200*time.Millisecond); ok {
		t.Fatal("dc2 must be refused while dc1 holds the lease")
	}
	if _, ok, _ := c.Renew("dc1", 1, 200*time.Millisecond); !ok {
		t.Fatal("renew over HTTP failed")
	}
	time.Sleep(400 * time.Millisecond)
	l2, ok, _ := c.Acquire("dc2", 200*time.Millisecond)
	if !ok || l2.Epoch != 2 {
		t.Fatalf("takeover over HTTP failed: %+v ok=%v", l2, ok)
	}
	if _, ok, _ := c.Renew("dc1", 1, 200*time.Millisecond); ok {
		t.Fatal("stale renew must be rejected over HTTP")
	}
}
