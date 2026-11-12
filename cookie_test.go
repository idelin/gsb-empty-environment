package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"
)

var testKey = bytes.Repeat([]byte{0x5a}, 32)

func TestServerCookieLayoutAndMAC(t *testing.T) {
	clientCookie := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	clientIP := net.ParseIP("192.0.2.10")
	now := time.Unix(1_700_000_000, 0)

	cookie, err := GenerateServerCookie(testKey, clientCookie, clientIP, now)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	if len(cookie) != ServerCookieLen {
		t.Fatalf("server cookie length = %d, want %d", len(cookie), ServerCookieLen)
	}
	if cookie[0] != ServerCookieVersion {
		t.Fatalf("version = %d, want %d", cookie[0], ServerCookieVersion)
	}
	if !bytes.Equal(cookie[1:4], []byte{0, 0, 0}) {
		t.Fatalf("reserved bytes = %v, want zero", cookie[1:4])
	}
	if got := binary.BigEndian.Uint32(cookie[4:8]); got != uint32(now.Unix()) {
		t.Fatalf("timestamp = %d, want %d", got, now.Unix())
	}

	mac := hmac.New(sha256.New, testKey)
	_, _ = mac.Write(clientCookie)
	_, _ = mac.Write(cookie[:8])
	_, _ = mac.Write(clientIP.To4())
	wantMAC := mac.Sum(nil)[:8]
	if !bytes.Equal(cookie[8:16], wantMAC) {
		t.Fatalf("MAC = %x, want %x", cookie[8:16], wantMAC)
	}

	if err := ValidateServerCookie(testKey, clientCookie, cookie, clientIP, now); err != nil {
		t.Fatalf("ValidateServerCookie: %v", err)
	}
}

func TestServerCookieBoundToClientIP(t *testing.T) {
	clientCookie := []byte("12345678")
	victimIP := net.ParseIP("192.0.2.10")
	forgedIP := net.ParseIP("198.51.100.20")
	now := time.Unix(1_700_000_000, 0)

	cookie, err := GenerateServerCookie(testKey, clientCookie, victimIP, now)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	err = ValidateServerCookie(testKey, clientCookie, cookie, forgedIP, now)
	if !errors.Is(err, ErrServerCookieMAC) {
		t.Fatalf("forged-IP validation error = %v, want %v", err, ErrServerCookieMAC)
	}
}

func TestServerCookieTimestampWindow(t *testing.T) {
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("2001:db8::1")
	base := time.Unix(1_700_000_000, 0)

	cookie, err := GenerateServerCookie(testKey, clientCookie, clientIP, base)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}

	tests := []struct {
		name    string
		now     time.Time
		wantErr error
	}{
		{name: "five minutes future is accepted", now: base.Add(-MaxCookieFuture), wantErr: nil},
		{name: "too far future is rejected", now: base.Add(-MaxCookieFuture - time.Second), wantErr: ErrServerCookieFuture},
		{name: "one hour old is accepted", now: base.Add(MaxCookieAge), wantErr: nil},
		{name: "older than one hour is rejected", now: base.Add(MaxCookieAge + time.Second), wantErr: ErrServerCookieExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateServerCookie(testKey, clientCookie, cookie, clientIP, test.now)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateServerCookie error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestTimestampSerialWraparound(t *testing.T) {
	clientCookie := []byte("abcdefgh")
	clientIP := net.ParseIP("192.0.2.99")
	maxUnix := int64(^uint32(0))

	tests := []struct {
		name       string
		cookieAt   time.Time
		validateAt time.Time
		wantErr    error
	}{
		{
			name:       "short age across wrap is accepted",
			cookieAt:   time.Unix(maxUnix-100, 0),
			validateAt: time.Unix(maxUnix+150, 0),
			wantErr:    nil,
		},
		{
			name:       "expired across wrap is rejected",
			cookieAt:   time.Unix(maxUnix-3700, 0),
			validateAt: time.Unix(maxUnix+100, 0),
			wantErr:    ErrServerCookieExpired,
		},
		{
			name:       "short future across wrap is accepted",
			cookieAt:   time.Unix(100, 0),
			validateAt: time.Unix(maxUnix-149, 0),
			wantErr:    nil,
		},
		{
			name:       "distant future across wrap is rejected",
			cookieAt:   time.Unix(100, 0),
			validateAt: time.Unix(maxUnix-399, 0),
			wantErr:    ErrServerCookieFuture,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cookie, err := GenerateServerCookie(testKey, clientCookie, clientIP, test.cookieAt)
			if err != nil {
				t.Fatalf("GenerateServerCookie: %v", err)
			}
			err = ValidateServerCookie(testKey, clientCookie, cookie, clientIP, test.validateAt)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidateServerCookie error = %v, want %v", err, test.wantErr)
			}
		})
	}

	if got := SerialDelta(100, uint32(maxUnix-10)); got != 111 {
		t.Fatalf("SerialDelta(100, Max-10) = %d, want 111", got)
	}
	if got := SerialDelta(uint32(maxUnix-10), 100); got != -111 {
		t.Fatalf("SerialDelta(Max-10, 100) = %d, want -111", got)
	}
}

func TestCookieVersionAndReservedBytes(t *testing.T) {
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("192.0.2.1")
	now := time.Unix(1_700_000_000, 0)
	cookie, err := GenerateServerCookie(testKey, clientCookie, clientIP, now)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}

	cookie[0] = 2
	if err := ValidateServerCookie(testKey, clientCookie, cookie, clientIP, now); !errors.Is(err, ErrServerCookieVersion) {
		t.Fatalf("version error = %v, want %v", err, ErrServerCookieVersion)
	}

	cookie[0] = ServerCookieVersion
	cookie[2] = 1
	if err := ValidateServerCookie(testKey, clientCookie, cookie, clientIP, now); !errors.Is(err, ErrServerCookieReserved) {
		t.Fatalf("reserved-byte error = %v, want %v", err, ErrServerCookieReserved)
	}
}

func TestGateMissingServerCookieReturnsBoundedTC(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	query := buildQuery(t, []byte("12345678"), nil)

	decision, response, err := gate.Check(query, net.ParseIP("192.0.2.10"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieMissingServer {
		t.Fatalf("status = %v, want CookieMissingServer", decision.Status)
	}
	if len(response) > len(query) {
		t.Fatalf("TC response is %d bytes, request is %d bytes", len(response), len(query))
	}
	if len(response) < dnsHeaderLen {
		t.Fatalf("TC response too short: %d bytes", len(response))
	}
	flags := binary.BigEndian.Uint16(response[2:4])
	if flags&flagQR == 0 || flags&flagTC == 0 {
		t.Fatalf("response flags = %#x, want QR and TC set", flags)
	}
	if got := binary.BigEndian.Uint16(response[6:8]); got != 0 {
		t.Fatalf("TC answer count = %d, want 0", got)
	}
	if got := binary.BigEndian.Uint16(response[10:12]); got != 0 {
		t.Fatalf("TC additional count = %d, want 0", got)
	}
}

func TestGateInvalidCookieReturnsBadCookie(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("192.0.2.10")

	validServer, err := gate.GenerateServerCookie(clientCookie, clientIP)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	forgedServer := append([]byte(nil), validServer...)
	forgedServer[8] ^= 0xff
	query := buildQuery(t, clientCookie, forgedServer)

	decision, response, err := gate.Check(query, clientIP)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieInvalid {
		t.Fatalf("status = %v, want CookieInvalid", decision.Status)
	}
	if !errors.Is(decision.Reason, ErrServerCookieMAC) {
		t.Fatalf("reason = %v, want %v", decision.Reason, ErrServerCookieMAC)
	}
	if got := extendedRCode(t, response); got != BadCookieRCode {
		t.Fatalf("extended RCODE = %d, want %d", got, BadCookieRCode)
	}

	parsed, err := parseMessage(response)
	if err != nil {
		t.Fatalf("parse BADCOOKIE response: %v", err)
	}
	if !bytes.Equal(parsed.client, clientCookie) {
		t.Fatalf("response client cookie = %x, want %x", parsed.client, clientCookie)
	}
	if len(parsed.server) != ServerCookieLen {
		t.Fatalf("response server cookie length = %d, want %d", len(parsed.server), ServerCookieLen)
	}
	if err := gate.ValidateServerCookie(clientCookie, parsed.server, clientIP); err != nil {
		t.Fatalf("fresh response cookie does not validate: %v", err)
	}
}

func TestGateForgedSourceIPIsRejected(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	clientCookie := []byte("12345678")
	victimIP := net.ParseIP("192.0.2.10")
	forgedIP := net.ParseIP("198.51.100.20")

	serverCookie, err := gate.GenerateServerCookie(clientCookie, victimIP)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	query := buildQuery(t, clientCookie, serverCookie)

	decision, response, err := gate.Check(query, forgedIP)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieInvalid {
		t.Fatalf("status = %v, want CookieInvalid", decision.Status)
	}
	if !errors.Is(decision.Reason, ErrServerCookieMAC) {
		t.Fatalf("reason = %v, want %v", decision.Reason, ErrServerCookieMAC)
	}
	if got := extendedRCode(t, response); got != BadCookieRCode {
		t.Fatalf("extended RCODE = %d, want BADCOOKIE", got)
	}

	parsed, err := parseMessage(response)
	if err != nil {
		t.Fatalf("parse BADCOOKIE response: %v", err)
	}
	if len(parsed.server) != ServerCookieLen {
		t.Fatalf("fresh server cookie length = %d, want %d", len(parsed.server), ServerCookieLen)
	}
	if err := gate.ValidateServerCookie(clientCookie, parsed.server, forgedIP); err != nil {
		t.Fatalf("fresh cookie is not bound to the actual source IP: %v", err)
	}
	if err := gate.ValidateServerCookie(clientCookie, parsed.server, victimIP); !errors.Is(err, ErrServerCookieMAC) {
		t.Fatalf("fresh cookie unexpectedly validates for victim IP: %v", err)
	}
}

func TestGateExpiredCookieReturnsBadCookie(t *testing.T) {
	issuedAt := time.Unix(1_700_000_000, 0)
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("192.0.2.10")
	oldGate := newTestGate(t, issuedAt)
	serverCookie, err := oldGate.GenerateServerCookie(clientCookie, clientIP)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	query := buildQuery(t, clientCookie, serverCookie)

	gate := newTestGate(t, issuedAt.Add(MaxCookieAge+time.Second))
	decision, response, err := gate.Check(query, clientIP)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieInvalid || !errors.Is(decision.Reason, ErrServerCookieExpired) {
		t.Fatalf("decision = %+v, want expired CookieInvalid", decision)
	}
	if got := extendedRCode(t, response); got != BadCookieRCode {
		t.Fatalf("extended RCODE = %d, want BADCOOKIE", got)
	}
}

func TestGateRejectsMalformedClientCookie(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	query := buildQuery(t, []byte("1234567"), nil)

	decision, response, err := gate.Check(query, net.ParseIP("192.0.2.10"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieInvalid || !errors.Is(decision.Reason, ErrClientCookieLength) {
		t.Fatalf("decision = %+v, want invalid client length", decision)
	}
	if got := extendedRCode(t, response); got != BadCookieRCode {
		t.Fatalf("extended RCODE = %d, want BADCOOKIE", got)
	}
}

func TestGateRejectsMissingClientCookie(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	query := buildQuery(t, nil, nil)

	decision, response, err := gate.Check(query, net.ParseIP("192.0.2.10"))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieInvalid || !errors.Is(decision.Reason, ErrClientCookieLength) {
		t.Fatalf("decision = %+v, want invalid client length", decision)
	}
	if got := extendedRCode(t, response); got != BadCookieRCode {
		t.Fatalf("extended RCODE = %d, want BADCOOKIE", got)
	}
	parsed, err := parseMessage(response)
	if err != nil {
		t.Fatalf("parse BADCOOKIE response: %v", err)
	}
	if parsed.hasCookie {
		t.Fatalf("BADCOOKIE response has a Cookie option without a Client Cookie")
	}
}

func TestGateAcceptsValidCookie(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("192.0.2.10")
	serverCookie, err := gate.GenerateServerCookie(clientCookie, clientIP)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	query := buildQuery(t, clientCookie, serverCookie)

	decision, response, err := gate.Check(query, clientIP)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieValid {
		t.Fatalf("status = %v, want CookieValid", decision.Status)
	}
	if response != nil {
		t.Fatalf("valid cookie produced a challenge response: %x", response)
	}
}

func TestGateAcceptsCookieAcrossTimestampWrap(t *testing.T) {
	maxUnix := int64(^uint32(0))
	clientCookie := []byte("12345678")
	clientIP := net.ParseIP("192.0.2.10")

	oldGate := newTestGate(t, time.Unix(maxUnix-100, 0))
	serverCookie, err := oldGate.GenerateServerCookie(clientCookie, clientIP)
	if err != nil {
		t.Fatalf("GenerateServerCookie: %v", err)
	}
	query := buildQuery(t, clientCookie, serverCookie)

	gate := newTestGate(t, time.Unix(maxUnix+50, 0))
	decision, response, err := gate.Check(query, clientIP)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if decision.Status != CookieValid || response != nil {
		t.Fatalf("wrapped cookie decision = %+v, response = %x", decision, response)
	}
}

func TestGateRejectsMalformedMessage(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	gate := newTestGate(t, now)
	query := buildQuery(t, []byte("12345678"), nil)
	query = query[:len(query)-1]

	decision, response, err := gate.Check(query, net.ParseIP("192.0.2.10"))
	if !errors.Is(err, ErrMalformedDNS) {
		t.Fatalf("Check error = %v, want %v", err, ErrMalformedDNS)
	}
	if decision.Status == CookieValid {
		t.Fatalf("malformed message produced CookieValid")
	}
	if response != nil {
		t.Fatalf("malformed message produced a response: %x", response)
	}
}

func newTestGate(t *testing.T, now time.Time) *CookieGate {
	t.Helper()
	gate, err := NewCookieGate(testKey, WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatalf("NewCookieGate: %v", err)
	}
	return gate
}

func buildQuery(t *testing.T, clientCookie, serverCookie []byte) []byte {
	t.Helper()
	message := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(message[0:2], 0x1234)
	binary.BigEndian.PutUint16(message[2:4], flagRD)
	binary.BigEndian.PutUint16(message[4:6], 1)
	if clientCookie != nil {
		binary.BigEndian.PutUint16(message[10:12], 1)
	}

	questionName := []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0}
	message = append(message, questionName...)
	message = append(message, 0, 1, 0, 1)

	if clientCookie == nil {
		return message
	}
	cookie := append([]byte(nil), clientCookie...)
	cookie = append(cookie, serverCookie...)
	optionLen := 4 + len(cookie)

	message = append(message, 0)
	message = append(message, byte(optType>>8), byte(optType))
	message = append(message, 4, 0xd0)
	message = append(message, 0, 0, 0, 0)
	message = append(message, byte(optionLen>>8), byte(optionLen))
	message = append(message, 0, CookieOptionCode)
	message = append(message, byte(len(cookie)>>8), byte(len(cookie)))
	message = append(message, cookie...)
	return message
}

func extendedRCode(t *testing.T, response []byte) int {
	t.Helper()
	if len(response) < dnsHeaderLen {
		t.Fatalf("response too short: %d bytes", len(response))
	}
	parsed, err := parseMessage(response)
	if err != nil {
		t.Fatalf("parse response: %v", err)
	}
	return int(parsed.extRCode)<<4 | int(parsed.flags&0x0f)
}
