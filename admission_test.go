package netsec

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
	"time"
)

func mustStandardSuite(t *testing.T, number uint8) Suite {
	t.Helper()
	suite, ok := StandardSuite(number)
	if !ok {
		t.Fatalf("standard suite %d not found", number)
	}
	return suite
}

func newTestAdmission(t *testing.T, config Config) (*Admission, *ManualClock) {
	t.Helper()
	clock := NewManualClock(time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC))
	if config.Clock == nil {
		config.Clock = clock
	}
	if config.AllowedSuites == nil {
		config.AllowedSuites = []uint8{3, 17}
	}
	if config.Throttle == nil {
		config.Throttle = &ThrottleConfig{Window: 60 * time.Second, MaxFailures: 10}
	}
	admission, err := NewAdmissionWithConfig(config)
	if err != nil {
		t.Fatalf("NewAdmissionWithConfig: %v", err)
	}
	return admission, clock
}

func TestCipherSuiteAdmission(t *testing.T) {
	admission, err := NewAdmission(3, 17)
	if err != nil {
		t.Fatalf("NewAdmission: %v", err)
	}

	for _, number := range []uint8{3, 17} {
		suite, err := admission.AdmitSuite(number)
		if err != nil {
			t.Fatalf("suite %d should be admitted: %v", number, err)
		}
		if suite.Number != number {
			t.Fatalf("suite number = %d, want %d", suite.Number, number)
		}
	}

	_, err = admission.AdmitSuite(0)
	if err == nil {
		t.Fatal("cipher suite 0 was admitted")
	}
	if !errors.Is(err, ErrSuiteRejected) {
		t.Fatalf("suite 0 error = %v, want ErrSuiteRejected", err)
	}
	if !errors.Is(err, ErrAuthAlgorithmNone) {
		t.Fatalf("suite 0 error = %v, want ErrAuthAlgorithmNone", err)
	}
	if !errors.Is(err, ErrConfidentialityNone) {
		t.Fatalf("suite 0 error = %v, want ErrConfidentialityNone", err)
	}
	if !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("suite 0 error = %v, want ErrSuiteNotRegistered", err)
	}

	// Suite 2 is a valid standard suite but was not registered by the caller.
	_, err = admission.AdmitSuite(2)
	if !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("suite 2 error = %v, want ErrSuiteNotRegistered", err)
	}

	// Registration is not enough to bypass the algorithm policy.
	if err := admission.RegisterSuite(Suite{
		Number:          100,
		Auth:            AuthRAKPHMACSHA1,
		Integrity:       IntegrityHMACSHA196,
		Confidentiality: ConfidentialityNone,
	}); err != nil {
		t.Fatalf("RegisterSuite: %v", err)
	}
	if _, err := admission.AdmitSuite(100); !errors.Is(err, ErrConfidentialityNone) {
		t.Fatalf("confidentiality-none suite error = %v, want ErrConfidentialityNone", err)
	}

	if err := admission.RegisterSuite(Suite{
		Number:          101,
		Auth:            AuthNone,
		Integrity:       IntegrityHMACSHA196,
		Confidentiality: ConfidentialityAESCBC128,
	}); err != nil {
		t.Fatalf("RegisterSuite: %v", err)
	}
	if _, err := admission.AdmitSuite(101); !errors.Is(err, ErrAuthAlgorithmNone) {
		t.Fatalf("authentication-none suite error = %v, want ErrAuthAlgorithmNone", err)
	}

	if err := admission.RegisterSuite(Suite{
		Number:          102,
		Auth:            AuthRAKPHMACSHA1,
		Integrity:       IntegrityHMACSHA196,
		Confidentiality: ConfidentialityAESCBC128,
	}); err != nil {
		t.Fatalf("RegisterSuite: %v", err)
	}
	if _, err := admission.AdmitSuite(102); err != nil {
		t.Fatalf("custom valid suite should be admitted: %v", err)
	}

	// Standard suite 6 has confidentiality none, so allowing its number must
	// still leave it rejected.
	if err := admission.RegisterSuite(mustStandardSuite(t, 6)); err != nil {
		t.Fatalf("RegisterSuite: %v", err)
	}
	if _, err := admission.AdmitSuite(6); !errors.Is(err, ErrConfidentialityNone) {
		t.Fatalf("suite 6 error = %v, want ErrConfidentialityNone", err)
	}

	if _, err := admission.OpenSession("192.0.2.1:1234", 0); !errors.Is(err, ErrSuiteRejected) {
		t.Fatalf("OpenSession(suite 0) error = %v, want ErrSuiteRejected", err)
	}
	if _, err := admission.OpenSession("192.0.2.1:1234", 2); !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("OpenSession(suite 2) error = %v, want ErrSuiteNotRegistered", err)
	}
	if _, err := admission.OpenSession("192.0.2.1:1234", 3); err != nil {
		t.Fatalf("OpenSession(suite 3): %v", err)
	}

	// A registered unknown suite number is a configuration error.
	if _, err := NewAdmission(99); !errors.Is(err, ErrSuiteNotRegistered) {
		t.Fatalf("NewAdmission(99) error = %v, want ErrSuiteNotRegistered", err)
	}

	// Cipher suite 0 can be named during construction, but it is never
	// admitted.
	zeroAdmission, err := NewAdmission(0)
	if err != nil {
		t.Fatalf("NewAdmission(0): %v", err)
	}
	if _, err := zeroAdmission.OpenSession("192.0.2.1", 0); !errors.Is(err, ErrAuthAlgorithmNone) {
		t.Fatalf("OpenSession(suite 0) error = %v, want ErrAuthAlgorithmNone", err)
	}
}

type rakpObservation struct {
	response       *RAKP2Response
	closeErr       error
	responseLen    int
	authCodeLen    int
	rakp2Elapsed   time.Duration
	totalElapsed   time.Duration
	closeErrorText string
}

func observeLogin(
	t *testing.T,
	admission *Admission,
	clock *ManualClock,
	source string,
	suite uint8,
	username string,
	password []byte,
) rakpObservation {
	t.Helper()
	clock.ResetSleeps()
	start := clock.Now()

	session, err := admission.OpenSession(source, suite)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := session.RAKP1(RAKP1Request{
		Username:     username,
		ClientRandom: bytes.Repeat([]byte{0x42}, 16),
	}); err != nil {
		t.Fatalf("RAKP1: %v", err)
	}
	response, err := session.RAKP2()
	if err != nil {
		t.Fatalf("RAKP2: %v", err)
	}
	afterRAKP2 := clock.Now()

	closeErr := session.RAKP3(session.ClientAuthCode(password))
	afterRAKP3 := clock.Now()
	text := ""
	if closeErr != nil {
		text = closeErr.Error()
	}
	return rakpObservation{
		response:       response,
		closeErr:       closeErr,
		responseLen:    response.Len(),
		authCodeLen:    len(response.AuthCode),
		rakp2Elapsed:   afterRAKP2.Sub(start),
		totalElapsed:   afterRAKP3.Sub(start),
		closeErrorText: text,
	}
}

func TestUnknownUserAndBadPasswordAreIndistinguishable(t *testing.T) {
	admission, clock := newTestAdmission(t, Config{
		Users:  map[string][]byte{"admin": []byte("correct horse battery staple")},
		Timing: &TimingProfile{Bucket: 100 * time.Millisecond},
	})

	for _, suite := range []uint8{3, 17} {
		t.Run(fmt.Sprintf("suite-%d", suite), func(t *testing.T) {
			unknown := observeLogin(t, admission, clock, "198.51.100.10:1000", suite, "ghost", []byte("wrong password"))
			badPassword := observeLogin(t, admission, clock, "198.51.100.10:1001", suite, "admin", []byte("wrong password"))

			if unknown.response == nil || badPassword.response == nil {
				t.Fatal("RAKP2 returned a nil response")
			}
			if unknown.responseLen != badPassword.responseLen {
				t.Fatalf("RAKP2 length differs: unknown=%d bad-password=%d", unknown.responseLen, badPassword.responseLen)
			}
			if unknown.authCodeLen != badPassword.authCodeLen {
				t.Fatalf("RAKP2 auth-code length differs: unknown=%d bad-password=%d", unknown.authCodeLen, badPassword.authCodeLen)
			}
			if unknown.rakp2Elapsed != 100*time.Millisecond {
				t.Fatalf("unknown-user RAKP2 elapsed = %s, want 100ms", unknown.rakp2Elapsed)
			}
			if badPassword.rakp2Elapsed != 100*time.Millisecond {
				t.Fatalf("bad-password RAKP2 elapsed = %s, want 100ms", badPassword.rakp2Elapsed)
			}
			if unknown.rakp2Elapsed != badPassword.rakp2Elapsed {
				t.Fatalf("RAKP2 timing differs: unknown=%s bad-password=%s", unknown.rakp2Elapsed, badPassword.rakp2Elapsed)
			}
			if unknown.totalElapsed != badPassword.totalElapsed {
				t.Fatalf("RAKP3 timing differs: unknown=%s bad-password=%s", unknown.totalElapsed, badPassword.totalElapsed)
			}
			if !errors.Is(unknown.closeErr, ErrAuthenticationFailed) {
				t.Fatalf("unknown-user RAKP3 error = %v, want ErrAuthenticationFailed", unknown.closeErr)
			}
			if !errors.Is(badPassword.closeErr, ErrAuthenticationFailed) {
				t.Fatalf("bad-password RAKP3 error = %v, want ErrAuthenticationFailed", badPassword.closeErr)
			}
			if unknown.closeErrorText != badPassword.closeErrorText {
				t.Fatalf("error text differs: unknown=%q bad-password=%q", unknown.closeErrorText, badPassword.closeErrorText)
			}
			if len(unknown.response.ServerRandom) != 16 || len(unknown.response.BMCGUID) != 16 {
				t.Fatalf("RAKP2 fixed fields have wrong length: random=%d guid=%d",
					len(unknown.response.ServerRandom), len(unknown.response.BMCGUID))
			}
		})
	}
}

func TestUnknownUserCannotAuthenticateWithDummySecret(t *testing.T) {
	admission, _ := newTestAdmission(t, Config{
		Timing: &TimingProfile{},
	})
	session, err := admission.OpenSession("203.0.113.5", 3)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := session.RAKP1(RAKP1Request{Username: "nobody"}); err != nil {
		t.Fatalf("RAKP1: %v", err)
	}
	if _, err := session.RAKP2(); err != nil {
		t.Fatalf("RAKP2: %v", err)
	}

	// Even a code computed with the internal dummy secret must not turn an
	// unknown username into an authenticated session.
	code := session.ClientAuthCode(admission.dummySecret)
	if err := session.RAKP3(code); !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("RAKP3 with dummy secret error = %v, want ErrAuthenticationFailed", err)
	}
}

func TestAnonymousLoginRejected(t *testing.T) {
	admission, _ := newTestAdmission(t, Config{
		Users:  map[string][]byte{"admin": []byte("secret")},
		Timing: &TimingProfile{},
	})

	for _, password := range [][]byte{nil, {}} {
		_, err := admission.Authenticate(AuthRequest{
			Source:   "192.0.2.20",
			Suite:    3,
			Username: "",
			Password: password,
		})
		if !errors.Is(err, ErrAnonymous) {
			t.Fatalf("Authenticate anonymous error = %v, want ErrAnonymous", err)
		}
	}

	// An empty username is never a valid RAKP identity, even if a password is
	// supplied.
	_, err := admission.Authenticate(AuthRequest{
		Source:   "192.0.2.20",
		Suite:    3,
		Username: "",
		Password: []byte("not-empty"),
	})
	if !errors.Is(err, ErrAnonymous) {
		t.Fatalf("Authenticate empty username error = %v, want ErrAnonymous", err)
	}

	session, err := admission.OpenSession("192.0.2.20", 3)
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if err := session.RAKP1(RAKP1Request{Username: ""}); !errors.Is(err, ErrAnonymous) {
		t.Fatalf("RAKP1 empty username error = %v, want ErrAnonymous", err)
	}
}

func failRAKP(t *testing.T, admission *Admission, source string) error {
	t.Helper()
	session, err := admission.OpenSession(source, 3)
	if err != nil {
		return err
	}
	if err := session.RAKP1(RAKP1Request{Username: "admin"}); err != nil {
		t.Fatalf("RAKP1: %v", err)
	}
	if _, err := session.RAKP2(); err != nil {
		t.Fatalf("RAKP2: %v", err)
	}
	return session.RAKP3(session.ClientAuthCode([]byte("wrong")))
}

func TestFailureThrottle(t *testing.T) {
	admission, clock := newTestAdmission(t, Config{
		Users:    map[string][]byte{"admin": []byte("correct")},
		Timing:   &TimingProfile{Bucket: 10 * time.Millisecond},
		Throttle: &ThrottleConfig{Window: 60 * time.Second, MaxFailures: 10},
	})

	// Different ephemeral ports must collapse to the same source-address key.
	for i := 0; i < 11; i++ {
		source := fmt.Sprintf("198.51.100.7:%d", 2000+i)
		err := failRAKP(t, admission, source)
		if !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("failure %d error = %v, want ErrAuthenticationFailed", i+1, err)
		}
	}
	if got := admission.FailureCount("198.51.100.7"); got != 11 {
		t.Fatalf("failure count = %d, want 11", got)
	}
	if !admission.SourceBlocked("198.51.100.7:9999") {
		t.Fatal("source should be blocked after more than 10 failures")
	}

	// Throttled sources are discarded before suite or RAKP processing.
	if _, err := admission.OpenSession("198.51.100.7:9999", 3); !errors.Is(err, ErrThrottled) {
		t.Fatalf("OpenSession from throttled source error = %v, want ErrThrottled", err)
	}
	if _, err := admission.Authenticate(AuthRequest{
		Source:   "198.51.100.7:9999",
		Suite:    3,
		Username: "admin",
		Password: []byte("correct"),
	}); !errors.Is(err, ErrThrottled) {
		t.Fatalf("Authenticate from throttled source error = %v, want ErrThrottled", err)
	}

	// A different source is unaffected.
	if _, err := admission.OpenSession("198.51.100.8", 3); err != nil {
		t.Fatalf("OpenSession from other source: %v", err)
	}

	// Once every failure has aged out of the 60-second window, the source is
	// allowed again.
	clock.Advance(60 * time.Second)
	if admission.SourceBlocked("198.51.100.7") {
		t.Fatal("source should not be blocked after the window expires")
	}
	if _, err := admission.OpenSession("198.51.100.7:9999", 3); err != nil {
		t.Fatalf("OpenSession after window expiry: %v", err)
	}
}

func TestFailureThrottleSlidingWindow(t *testing.T) {
	admission, clock := newTestAdmission(t, Config{
		Users:    map[string][]byte{"admin": []byte("correct")},
		Timing:   &TimingProfile{},
		Throttle: &ThrottleConfig{Window: 60 * time.Second, MaxFailures: 10},
	})

	for i := 0; i < 6; i++ {
		if err := failRAKP(t, admission, "203.0.113.9"); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("failure %d error = %v", i+1, err)
		}
	}
	clock.Advance(61 * time.Second)
	for i := 0; i < 5; i++ {
		if err := failRAKP(t, admission, "203.0.113.9"); !errors.Is(err, ErrAuthenticationFailed) {
			t.Fatalf("failure %d after window error = %v", i+1, err)
		}
	}
	if admission.SourceBlocked("203.0.113.9") {
		t.Fatal("old failures should not count toward the current window")
	}
}

func TestSuccessfulLogin(t *testing.T) {
	admission, clock := newTestAdmission(t, Config{
		Users:  map[string][]byte{"admin": []byte("correct")},
		Timing: &TimingProfile{Bucket: 25 * time.Millisecond},
	})

	result, err := admission.Login("192.0.2.30", 17, "admin", []byte("correct"))
	if err != nil {
		t.Fatalf("Login with correct password: %v", err)
	}
	if result == nil || result.Session == nil || result.Challenge == nil {
		t.Fatal("Login returned an incomplete result")
	}
	if len(result.Challenge.AuthCode) != 32 {
		t.Fatalf("suite 17 auth code length = %d, want 32", len(result.Challenge.AuthCode))
	}
	if got := admission.FailureCount("192.0.2.30"); got != 0 {
		t.Fatalf("successful login recorded %d failures", got)
	}

	result, err = admission.Login("192.0.2.30", 3, "admin", []byte("incorrect"))
	if !errors.Is(err, ErrAuthenticationFailed) {
		t.Fatalf("Login with bad password error = %v, want ErrAuthenticationFailed", err)
	}
	if result == nil || result.Challenge == nil {
		t.Fatal("failed Login should still return the RAKP2 challenge")
	}

	// The failed Login consumed one 25ms RAKP2 bucket and one 25ms RAKP3
	// bucket; the successful Login did the same.
	if len(clock.Sleeps()) == 0 {
		t.Fatal("expected the caller clock to simulate response latency")
	}
}
