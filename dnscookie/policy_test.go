package dnscookie

import (
	"errors"
	"net/netip"
	"testing"
	"time"
)

var (
	testServerIP = netip.MustParseAddr("192.0.2.53")
	testClientIP = netip.MustParseAddr("203.0.113.7")
	testServer   = netip.MustParseAddrPort("192.0.2.53:53")
)

func newTestPolicy() *ServerPolicy {
	kr := testKeyRing(time.Unix(1700000000, 0))
	return NewServerPolicy(kr)
}

func queryWithCookies(t *testing.T, cc, sc []byte) []byte {
	t.Helper()
	q, err := BuildQuery(0x1234, "www.example.com.", 1, cc, sc, 1232)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestServerMissingServerCookieGetsBadCookie(t *testing.T) {
	p := newTestPolicy()
	cc := mustHex(t, "2464c4abcf10c957")

	dec := p.HandleUDP(queryWithCookies(t, cc, nil), testClientIP, 4000)
	if dec.Action != ActionBadCookie {
		t.Fatalf("action = %v, want BADCOOKIE", dec.Action)
	}
	resp, err := ParseMessage(dec.Response)
	if err != nil {
		t.Fatal(err)
	}
	if resp.RCODE != RCodeBadCookie {
		t.Fatalf("RCODE = %d, want 23 (BADCOOKIE)", resp.RCODE)
	}
	if resp.QName != "www.example.com." || resp.QType != 1 {
		t.Fatalf("question not echoed: %q type %d", resp.QName, resp.QType)
	}
	if len(resp.ServerCookie) != ServerCookieLen {
		t.Fatalf("no fresh server cookie in BADCOOKIE reply")
	}
	// The minted cookie must verify for this client IP.
	if err := p.Keys.VerifyServerCookie(resp.ServerCookie, cc, testClientIP); err != nil {
		t.Fatalf("minted cookie does not verify: %v", err)
	}
}

func TestServerInvalidServerCookieGetsBadCookie(t *testing.T) {
	p := newTestPolicy()
	cc := mustHex(t, "2464c4abcf10c957")
	// Cookie minted for a different client IP (stolen).
	stolen, err := p.Keys.NewServerCookie(cc, netip.MustParseAddr("198.51.100.9"))
	if err != nil {
		t.Fatal(err)
	}
	dec := p.HandleUDP(queryWithCookies(t, cc, stolen), testClientIP, 100)
	if dec.Action != ActionBadCookie {
		t.Fatalf("action = %v, want BADCOOKIE for stolen cookie", dec.Action)
	}
}

func TestServerValidCookieAnswers(t *testing.T) {
	p := newTestPolicy()
	cc := mustHex(t, "2464c4abcf10c957")
	sc, err := p.Keys.NewServerCookie(cc, testClientIP)
	if err != nil {
		t.Fatal(err)
	}
	dec := p.HandleUDP(queryWithCookies(t, cc, sc), testClientIP, 800)
	if dec.Action != ActionAnswer {
		t.Fatalf("action = %v (%s), want ANSWER", dec.Action, dec.Reason)
	}
}

func TestServerAmplificationGuardTruncates(t *testing.T) {
	p := newTestPolicy()
	cc := mustHex(t, "2464c4abcf10c957")

	// No COOKIE option at all + large answer => TC=1, no amplification.
	plain, err := BuildQuery(0x99, "big.example.com.", 255, nil, nil, 512)
	if err != nil {
		t.Fatal(err)
	}
	// Strip the cookie option: BuildQuery with nil cookies still emits
	// an (empty) COOKIE option; craft a truly cookie-less query instead.
	plain = plain[:len(plain)-4] // drop empty cookie option data+header
	// Fix OPT rdlength to 0.
	plain[len(plain)-2] = 0
	plain[len(plain)-1] = 0

	dec := p.HandleUDP(plain, testClientIP, 4000)
	if dec.Action != ActionTruncated {
		t.Fatalf("action = %v, want TRUNCATED for uncookied large answer", dec.Action)
	}
	resp, err := ParseMessage(dec.Response)
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Truncated {
		t.Fatal("TC bit not set")
	}
	if resp.AnswerCount != 0 {
		t.Fatal("truncated response must not carry answers")
	}

	// Small answers to legacy queries are still allowed.
	dec = p.HandleUDP(plain, testClientIP, 200)
	if dec.Action != ActionAnswer {
		t.Fatalf("small legacy answer blocked: %v (%s)", dec.Action, dec.Reason)
	}

	// Even a valid cookie does not permit oversized UDP answers.
	sc, err := p.Keys.NewServerCookie(cc, testClientIP)
	if err != nil {
		t.Fatal(err)
	}
	dec = p.HandleUDP(queryWithCookies(t, cc, sc), testClientIP, 4096)
	if dec.Action != ActionTruncated {
		t.Fatalf("oversized answer with valid cookie: %v, want TRUNCATED", dec.Action)
	}
}

func TestClientAcceptsLegitResponse(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	kr := testKeyRing(time.Unix(1700000000, 0))
	sc, err := kr.NewServerCookie(c.Cookie, testClientIP)
	if err != nil {
		t.Fatal(err)
	}

	_, id, err := c.Query(testServer, "www.example.com.", 1)
	if err != nil {
		t.Fatal(err)
	}
	realQuery, err := BuildQuery(id, "www.example.com.", 1, c.Cookie, nil, 1232)
	if err != nil {
		t.Fatal(err)
	}
	pq, err := ParseMessage(realQuery)
	if err != nil {
		t.Fatal(err)
	}
	resp := pq.NewResponse(ResponseOptions{ServerCookie: sc})

	res, err := c.ProcessResponse(resp, testServer)
	if err != nil {
		t.Fatalf("legit response rejected: %v", err)
	}
	if res.Status != StatusAccepted {
		t.Fatalf("status = %v, want Accepted", res.Status)
	}
	if _, ok := c.ServerCookieFor(testServerIP); !ok {
		t.Fatal("server cookie not cached per server IP")
	}
}

func mustBuildRawQuery(t *testing.T, id uint16, name string, qtype uint16, cc, sc []byte) []byte {
	t.Helper()
	w, err := BuildQuery(id, name, qtype, cc, sc, 1232)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// forgeResponse builds a response to the client's pending query with
// the given mutations applied, simulating an off-path attacker.
func forgeResponse(t *testing.T, c *Client, id uint16, mutate func(q []byte) []byte, sc []byte) []byte {
	t.Helper()
	realQuery := mustBuildRawQuery(t, id, "www.example.com.", 1, c.Cookie, nil)
	if mutate != nil {
		realQuery = mutate(realQuery)
	}
	pq, err := ParseMessage(realQuery)
	if err != nil {
		t.Fatal(err)
	}
	return pq.NewResponse(ResponseOptions{ServerCookie: sc})
}

func TestClientRejectsOffPathPoisoning(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	kr := testKeyRing(time.Unix(1700000000, 0))
	sc, err := kr.NewServerCookie(c.Cookie, testClientIP)
	if err != nil {
		t.Fatal(err)
	}

	setup := func(t *testing.T) uint16 {
		_, id, err := c.Query(testServer, "www.example.com.", 1)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	t.Run("guessed TXID only", func(t *testing.T) {
		setup(t)
		// Attacker does not see the query; guesses a TXID that is not pending.
		forged := forgeResponse(t, c, 0xbeef, nil, sc)
		if _, err := c.ProcessResponse(forged, testServer); !errors.Is(err, ErrUnknownTXID) {
			t.Fatalf("got %v, want ErrUnknownTXID", err)
		}
	})

	t.Run("wrong source port", func(t *testing.T) {
		id := setup(t)
		forged := forgeResponse(t, c, id, nil, sc)
		badSrc := netip.MustParseAddrPort("192.0.2.53:5353")
		if _, err := c.ProcessResponse(forged, badSrc); !errors.Is(err, ErrUnexpectedSource) {
			t.Fatalf("got %v, want ErrUnexpectedSource", err)
		}
	})

	t.Run("wrong source IP", func(t *testing.T) {
		id := setup(t)
		forged := forgeResponse(t, c, id, nil, sc)
		badSrc := netip.MustParseAddrPort("198.51.100.1:53")
		if _, err := c.ProcessResponse(forged, badSrc); !errors.Is(err, ErrUnexpectedSource) {
			t.Fatalf("got %v, want ErrUnexpectedSource", err)
		}
	})

	t.Run("wrong QNAME", func(t *testing.T) {
		id := setup(t)
		forged := forgeResponse(t, c, id, func(q []byte) []byte {
			w, err := BuildQuery(id, "evil.example.com.", 1, c.Cookie, nil, 1232)
			if err != nil {
				t.Fatal(err)
			}
			return w
		}, sc)
		if _, err := c.ProcessResponse(forged, testServer); !errors.Is(err, ErrQuestionMismatch) {
			t.Fatalf("got %v, want ErrQuestionMismatch", err)
		}
	})

	t.Run("wrong QTYPE", func(t *testing.T) {
		id := setup(t)
		forged := forgeResponse(t, c, id, func(q []byte) []byte {
			w, err := BuildQuery(id, "www.example.com.", 2, c.Cookie, nil, 1232)
			if err != nil {
				t.Fatal(err)
			}
			return w
		}, sc)
		if _, err := c.ProcessResponse(forged, testServer); !errors.Is(err, ErrQuestionMismatch) {
			t.Fatalf("got %v, want ErrQuestionMismatch", err)
		}
	})

	t.Run("foreign client cookie", func(t *testing.T) {
		id := setup(t)
		// Attacker echoes a client cookie that is not ours.
		forged := forgeResponse(t, c, id, func(q []byte) []byte {
			w, err := BuildQuery(id, "www.example.com.", 1, mustHex(t, "deadbeefdeadbeef"), nil, 1232)
			if err != nil {
				t.Fatal(err)
			}
			return w
		}, sc)
		if _, err := c.ProcessResponse(forged, testServer); !errors.Is(err, ErrClientCookieMismatch) {
			t.Fatalf("got %v, want ErrClientCookieMismatch", err)
		}
	})

	t.Run("malformed server cookie", func(t *testing.T) {
		id := setup(t)
		bad := make([]byte, ServerCookieLen)
		bad[0] = 0x7f // invalid version
		forged := forgeResponse(t, c, id, nil, bad)
		if _, err := c.ProcessResponse(forged, testServer); !errors.Is(err, ErrMalformedServerCookie) {
			t.Fatalf("got %v, want ErrMalformedServerCookie", err)
		}
	})
}

func TestClientBadCookieRetryFlow(t *testing.T) {
	c, err := NewClient()
	if err != nil {
		t.Fatal(err)
	}
	p := newTestPolicy()

	// 1. First query carries no server cookie: server answers BADCOOKIE.
	wire, id, err := c.Query(testServer, "www.example.com.", 1)
	if err != nil {
		t.Fatal(err)
	}
	dec := p.HandleUDP(wire, testClientIP, 800)
	if dec.Action != ActionBadCookie {
		t.Fatalf("first query: action = %v, want BADCOOKIE", dec.Action)
	}
	res, err := c.ProcessResponse(dec.Response, testServer)
	if err != nil {
		t.Fatalf("BADCOOKIE reply rejected: %v", err)
	}
	if res.Status != StatusBadCookieRetry {
		t.Fatalf("status = %v, want BadCookieRetry", res.Status)
	}

	// 2. Retry with the learned server cookie: server answers.
	retry, err := c.RetryQuery(id)
	if err != nil {
		t.Fatal(err)
	}
	dec = p.HandleUDP(retry, testClientIP, 800)
	if dec.Action != ActionAnswer {
		t.Fatalf("retry: action = %v (%s), want ANSWER", dec.Action, dec.Reason)
	}

	// 3. A forged BADCOOKIE from an off-path source must not corrupt
	// the cached cookie.
	forged := forgeResponse(t, c, id, nil, mustHex(t, "010000006553f100aaaaaaaaaaaaaaaa"))
	badSrc := netip.MustParseAddrPort("203.0.113.99:53")
	if _, err := c.ProcessResponse(forged, badSrc); !errors.Is(err, ErrUnexpectedSource) {
		t.Fatalf("off-path BADCOOKIE: got %v, want ErrUnexpectedSource", err)
	}
	cached, ok := c.ServerCookieFor(testServerIP)
	if !ok || string(cached) != string(res.ServerCookie) {
		t.Fatal("cached server cookie corrupted by off-path packet")
	}
}
