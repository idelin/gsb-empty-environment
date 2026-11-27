package dnscookie

import (
	"bytes"
	"encoding/hex"
	"errors"
	"net/netip"
	"testing"
	"time"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex: %v", err)
	}
	return b
}

func testKeyRing(now time.Time) *KeyRing {
	raw, _ := hex.DecodeString("00112233445566778899aabbccddeeff")
	var secret [16]byte
	copy(secret[:], raw)
	kr := NewKeyRing(secret)
	kr.Now = func() time.Time { return now }
	return kr
}

// TestServerCookieRFC9018Vector pins a worked example of the RFC 9018
// Section 5.2.2 construction:
//
//	Server Cookie = 0x01 || 0x000000 || timestamp(BE) ||
//	    SipHash-2-4(secret, clientCookie || 0x01 || 0x000000 ||
//	                timestamp(BE) || clientIP)[LE]
//
// with client cookie 2464c4abcf10c957 (the example client cookie used
// throughout RFC 7873/9018), client IP 192.0.2.1, timestamp 1700000000
// and server secret 00112233445566778899aabbccddeeff.
func TestServerCookieRFC9018Vector(t *testing.T) {
	now := time.Unix(1700000000, 0)
	kr := testKeyRing(now)
	clientCookie := mustHex(t, "2464c4abcf10c957")
	ip := netip.MustParseAddr("192.0.2.1")

	cookie, err := kr.NewServerCookie(clientCookie, ip)
	if err != nil {
		t.Fatalf("NewServerCookie: %v", err)
	}
	want := mustHex(t, "010000006553f100ba8afc3ba7ceaa8f")
	if !bytes.Equal(cookie, want) {
		t.Fatalf("server cookie = %x, want %x", cookie, want)
	}
	if err := kr.VerifyServerCookie(cookie, clientCookie, ip); err != nil {
		t.Fatalf("VerifyServerCookie: %v", err)
	}
}

func TestKeyRotationWindow(t *testing.T) {
	now := time.Unix(1700000000, 0)
	kr := testKeyRing(now)
	cc := mustHex(t, "2464c4abcf10c957")
	ip := netip.MustParseAddr("192.0.2.1")

	cookie, err := kr.NewServerCookie(cc, ip)
	if err != nil {
		t.Fatal(err)
	}

	// After one rotation the old cookie must still verify (previous
	// secret window).
	kr.Rotate([16]byte(mustHex(t, "ffeeddccbbaa00998877665544332211")))
	if err := kr.VerifyServerCookie(cookie, cc, ip); err != nil {
		t.Fatalf("cookie minted with previous secret rejected: %v", err)
	}

	// After a second rotation the original secret is gone.
	kr.Rotate([16]byte(mustHex(t, "0f1e2d3c4b5a69788796a5b4c3d2e1f0")))
	if err := kr.VerifyServerCookie(cookie, cc, ip); !errors.Is(err, ErrCookieHash) {
		t.Fatalf("expected ErrCookieHash after secret aged out, got %v", err)
	}
}

func TestCrossIPTheftRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	kr := testKeyRing(now)
	cc := mustHex(t, "2464c4abcf10c957")
	victim := netip.MustParseAddr("192.0.2.1")
	attacker := netip.MustParseAddr("203.0.113.66")

	cookie, err := kr.NewServerCookie(cc, victim)
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.VerifyServerCookie(cookie, cc, attacker); !errors.Is(err, ErrCookieHash) {
		t.Fatalf("stolen cookie accepted for other IPv4: %v", err)
	}

	// IPv6 binding must hold as well.
	v6a := netip.MustParseAddr("2001:db8::1")
	v6b := netip.MustParseAddr("2001:db8::2")
	cookie6, err := kr.NewServerCookie(cc, v6a)
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.VerifyServerCookie(cookie6, cc, v6a); err != nil {
		t.Fatalf("v6 cookie rejected for issuing IP: %v", err)
	}
	if err := kr.VerifyServerCookie(cookie6, cc, v6b); !errors.Is(err, ErrCookieHash) {
		t.Fatalf("stolen v6 cookie accepted for other IP: %v", err)
	}
}

func TestTimestampAntiReplayWindow(t *testing.T) {
	issued := time.Unix(1700000000, 0)
	kr := testKeyRing(issued)
	cc := mustHex(t, "2464c4abcf10c957")
	ip := netip.MustParseAddr("192.0.2.1")
	cookie, err := kr.NewServerCookie(cc, ip)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name    string
		now     time.Time
		wantErr error
	}{
		{"now", issued, nil},
		{"clock 5m behind (boundary)", issued.Add(-5 * time.Minute), nil},
		{"clock 5m1s behind (too far future)", issued.Add(-5*time.Minute - time.Second), ErrCookieFuture},
		{"clock 1h ahead (boundary)", issued.Add(time.Hour), nil},
		{"clock 1h1s ahead (expired)", issued.Add(time.Hour + time.Second), ErrCookieExpired},
	}
	for _, tc := range cases {
		kr.Now = func() time.Time { return tc.now }
		if err := kr.VerifyServerCookie(cookie, cc, ip); !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestMalformedServerCookiesRejected(t *testing.T) {
	now := time.Unix(1700000000, 0)
	kr := testKeyRing(now)
	cc := mustHex(t, "2464c4abcf10c957")
	ip := netip.MustParseAddr("192.0.2.1")
	good, err := kr.NewServerCookie(cc, ip)
	if err != nil {
		t.Fatal(err)
	}

	tamper := func(mut func([]byte)) []byte {
		c := append([]byte(nil), good...)
		mut(c)
		return c
	}

	if err := kr.VerifyServerCookie(good[:15], cc, ip); !errors.Is(err, ErrCookieTooShort) {
		t.Errorf("short cookie: got %v", err)
	}
	if err := kr.VerifyServerCookie(tamper(func(c []byte) { c[0] = 0x02 }), cc, ip); !errors.Is(err, ErrCookieVersion) {
		t.Errorf("bad version: got %v", err)
	}
	if err := kr.VerifyServerCookie(tamper(func(c []byte) { c[2] = 0x01 }), cc, ip); !errors.Is(err, ErrCookieReserved) {
		t.Errorf("non-zero reserved: got %v", err)
	}
	if err := kr.VerifyServerCookie(tamper(func(c []byte) { c[15] ^= 0xff }), cc, ip); !errors.Is(err, ErrCookieHash) {
		t.Errorf("bad hash: got %v", err)
	}
	otherCC := mustHex(t, "0011223344556677")
	if err := kr.VerifyServerCookie(good, otherCC, ip); !errors.Is(err, ErrCookieHash) {
		t.Errorf("foreign client cookie: got %v", err)
	}
}
