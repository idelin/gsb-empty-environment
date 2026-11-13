package netsec

import (
	"errors"
	"testing"
	"time"
)

var (
	testSecret      = []byte("0123456789abcdef0123456789abcdef")
	testClientHello = []byte("client-hello-1")
	testAddr        = "192.0.2.1:4433"
)

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m, err := NewManager(testSecret, 0)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

// No cookie was issued: the second ClientHello carries no cookie and
// the server still continues the handshake.
func TestVerifyEcho_NoCookieIssued_ServerContinues(t *testing.T) {
	if err := VerifyEcho(nil, nil); err != nil {
		t.Fatalf("VerifyEcho(nil, nil) = %v, want nil", err)
	}
}

// The retry carried a cookie and the second ClientHello echoes it
// byte-for-byte: the handshake continues.
func TestVerifyEcho_ExactEcho_Continues(t *testing.T) {
	m := newTestManager(t)
	cookie := m.Issue(testClientHello, testAddr)

	if err := VerifyEcho(cookie, cookie); err != nil {
		t.Fatalf("VerifyEcho(issued, exact echo) = %v, want nil", err)
	}
	if err := m.Validate(cookie, testClientHello, testAddr); err != nil {
		t.Fatalf("Validate(echoed cookie) = %v, want nil", err)
	}
}

// The retry carried a cookie but the second ClientHello omits it:
// the handshake fails.
func TestVerifyEcho_MissingEcho_Fails(t *testing.T) {
	m := newTestManager(t)
	cookie := m.Issue(testClientHello, testAddr)

	if err := VerifyEcho(cookie, nil); !errors.Is(err, ErrCookieMissing) {
		t.Fatalf("VerifyEcho(issued, nil) = %v, want ErrCookieMissing", err)
	}
}

// The retry carried a cookie but the second ClientHello echoes it
// with one byte flipped: the handshake fails.
func TestVerifyEcho_FlippedByte_Fails(t *testing.T) {
	m := newTestManager(t)
	cookie := m.Issue(testClientHello, testAddr)

	for i := range cookie {
		tampered := make([]byte, len(cookie))
		copy(tampered, cookie)
		tampered[i] ^= 0x01

		if err := VerifyEcho(cookie, tampered); !errors.Is(err, ErrCookieMismatch) {
			t.Fatalf("VerifyEcho with byte %d flipped = %v, want ErrCookieMismatch", i, err)
		}
		if err := m.Validate(tampered, testClientHello, testAddr); !errors.Is(err, ErrCookieInvalid) {
			t.Fatalf("Validate with byte %d flipped = %v, want ErrCookieInvalid", i, err)
		}
	}
}

// End-to-end flow as the TLS stack drives it: first ClientHello,
// HelloRetryRequest with cookie, second ClientHello.
func TestHandshakeFlow(t *testing.T) {
	m := newTestManager(t)

	// First ClientHello: server decides to ask for a cookie.
	cookie := m.Issue(testClientHello, testAddr)

	// Second ClientHello echoes the cookie unchanged.
	echo := make([]byte, len(cookie))
	copy(echo, cookie)
	if err := VerifyEcho(cookie, echo); err != nil {
		t.Fatalf("VerifyEcho: %v", err)
	}
	if err := m.Validate(echo, testClientHello, testAddr); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// Same flow with one flipped byte must be rejected.
	echo[len(echo)/2] ^= 0x80
	if err := VerifyEcho(cookie, echo); !errors.Is(err, ErrCookieMismatch) {
		t.Fatalf("VerifyEcho after bit flip = %v, want ErrCookieMismatch", err)
	}
}

func TestValidate_Expiry(t *testing.T) {
	m := newTestManager(t)
	cookie := m.Issue(testClientHello, testAddr)

	m.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := m.Validate(cookie, testClientHello, testAddr); !errors.Is(err, ErrCookieExpired) {
		t.Fatalf("Validate after lifetime = %v, want ErrCookieExpired", err)
	}
}

func TestNewManager_ShortSecret(t *testing.T) {
	if _, err := NewManager([]byte("short"), 0); err == nil {
		t.Fatal("NewManager with short secret: want error, got nil")
	}
}
