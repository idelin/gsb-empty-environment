package netsec

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const (
	testSecret = "shared-secret"
	testRealm  = "turn.example.org"
)

func testNow() time.Time {
	return time.Unix(1_700_000_000, 0).UTC()
}

func testValidator(t *testing.T) *Validator {
	t.Helper()

	validator, err := NewValidator(Config{
		SharedSecret: []byte(testSecret),
		Realm:        testRealm,
		Now:          testNow,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	return validator
}

func validUsername(expiry int64) string {
	return fmt.Sprintf("%d:acme:alice", expiry)
}

func TestDerivePasswordKnownVector(t *testing.T) {
	const username = "1700000000:acme:alice"
	password := DerivePassword([]byte(testSecret), username)
	if password != "Z3T3v1sp/pibMzUTAO8f/Vmnlyo=" {
		t.Fatalf("DerivePassword = %q, want HMAC-SHA1 base64 vector", password)
	}
}

func TestMessageIntegrityKeyKnownVector(t *testing.T) {
	password := DerivePassword([]byte(testSecret), "1700000000:acme:alice")
	key := MessageIntegrityKey("1700000000:acme:alice", testRealm, password)
	if got, want := fmt.Sprintf("%x", key), "6e5c93fbd8c40b7654ecf70877f3ad65"; got != want {
		t.Fatalf("MessageIntegrityKey = %s, want %s", got, want)
	}
}

func TestValidateValidCredential(t *testing.T) {
	validator := testValidator(t)
	expiry := testNow().Unix() + 3600

	credentials, err := validator.Validate(validUsername(expiry))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if credentials.Expiry != expiry {
		t.Errorf("Expiry = %d, want %d", credentials.Expiry, expiry)
	}
	if credentials.ExpiresAt.Unix() != expiry {
		t.Errorf("ExpiresAt = %v", credentials.ExpiresAt)
	}
	if credentials.Tenant != "acme" || credentials.User != "alice" {
		t.Errorf("tenant/user = %q/%q", credentials.Tenant, credentials.User)
	}
	if credentials.Password != DerivePassword([]byte(testSecret), credentials.Username) {
		t.Errorf("unexpected password %q", credentials.Password)
	}
	if string(credentials.IntegrityKey) != string(MessageIntegrityKey(credentials.Username, testRealm, credentials.Password)) {
		t.Errorf("unexpected integrity key")
	}
}

func TestParseUsername(t *testing.T) {
	valid, err := ParseUsername("1700003600:acme:alice")
	if err != nil {
		t.Fatalf("ParseUsername: %v", err)
	}
	if valid.Expiry != 1_700_003_600 || valid.Tenant != "acme" || valid.User != "alice" {
		t.Errorf("unexpected parse result: %+v", valid)
	}
	if valid.Password != "" || valid.IntegrityKey != nil {
		t.Errorf("ParseUsername should not derive secrets")
	}

	tests := []string{
		"",
		"1:tenant",
		"1:tenant:user:extra",
		":tenant:user",
		"1::user",
		"1:tenant:",
		"-1:tenant:user",
		"+1:tenant:user",
		"1.5:tenant:user",
		"abc:tenant:user",
		"99999999999999999999:tenant:user",
		"253402300800:tenant:user",
		"1:tenant:user\n",
		"1: tenant:user",
		"1:tenant:user ",
		"1:tenant:user\x00",
	}
	for _, username := range tests {
		if _, err := ParseUsername(username); err == nil {
			t.Errorf("ParseUsername(%q) succeeded, want error", username)
		} else if !errors.Is(err, ErrUsernameFormat) {
			t.Errorf("ParseUsername(%q) error = %v, want ErrUsernameFormat", username, err)
		}
	}
}

func TestExpiryAndClockSkew(t *testing.T) {
	validator := testValidator(t)
	now := testNow().Unix()

	if _, err := validator.Validate(validUsername(now - 1)); !errors.Is(err, ErrUsernameExpired) {
		t.Fatalf("expired credential error = %v, want ErrUsernameExpired", err)
	}

	// At the exact expiry second the deadline has not been passed.
	if _, err := validator.Validate(validUsername(now)); err != nil {
		t.Fatalf("credential at exact expiry: %v", err)
	}

	skewed, err := NewValidator(Config{
		SharedSecret: []byte(testSecret),
		Realm:        testRealm,
		Now:          testNow,
		ClockSkew:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	if _, err := skewed.Validate(validUsername(now - 10)); err != nil {
		t.Fatalf("clock skew should keep credential valid: %v", err)
	}
}

func TestTenantPolicy(t *testing.T) {
	validator, err := NewValidator(Config{
		SharedSecret:   []byte(testSecret),
		Realm:          testRealm,
		Now:            testNow,
		AllowedTenants: []string{"acme"},
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	expiry := testNow().Unix() + 3600

	if _, err := validator.Validate(validUsername(expiry)); err != nil {
		t.Fatalf("allowed tenant rejected: %v", err)
	}
	if _, err := validator.Validate(fmt.Sprintf("%d:evil:alice", expiry)); !errors.Is(err, ErrTenantNotAllowed) {
		t.Fatalf("unknown tenant error = %v, want ErrTenantNotAllowed", err)
	}

	withCallback, err := NewValidator(Config{
		SharedSecret: []byte(testSecret),
		Realm:        testRealm,
		Now:          testNow,
		TenantAuthorizer: func(tenant string) bool {
			return strings.HasPrefix(tenant, "team-")
		},
	})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	if _, err := withCallback.Validate(fmt.Sprintf("%d:team-a:alice", expiry)); err != nil {
		t.Fatalf("authorized tenant rejected: %v", err)
	}
	if _, err := withCallback.Validate(fmt.Sprintf("%d:other:alice", expiry)); !errors.Is(err, ErrTenantNotAllowed) {
		t.Fatalf("tenant callback error = %v, want ErrTenantNotAllowed", err)
	}
}

func TestConfigValidationAndSecretCopy(t *testing.T) {
	if _, err := NewValidator(Config{Realm: testRealm}); !errors.Is(err, ErrConfig) {
		t.Errorf("empty secret error = %v", err)
	}
	if _, err := NewValidator(Config{SharedSecret: []byte(testSecret)}); !errors.Is(err, ErrConfig) {
		t.Errorf("empty realm error = %v", err)
	}
	if _, err := NewValidator(Config{
		SharedSecret:      []byte(testSecret),
		Realm:             testRealm,
		PasswordAlgorithm: PasswordAlgorithm("unknown"),
	}); !errors.Is(err, ErrConfig) {
		t.Errorf("unknown algorithm error = %v", err)
	}

	secret := []byte(testSecret)
	validator, err := NewValidator(Config{SharedSecret: secret, Realm: testRealm})
	if err != nil {
		t.Fatalf("NewValidator: %v", err)
	}
	secret[0] = 'X'
	credentials, err := validator.Validate(validUsername(time.Now().Unix() + 3600))
	if err != nil {
		t.Fatalf("Validate after mutating caller's secret: %v", err)
	}
	if credentials.Password != DerivePassword([]byte(testSecret), credentials.Username) {
		t.Errorf("validator retained caller's secret slice")
	}
}

func TestPasswordAlgorithms(t *testing.T) {
	username := "1700000000:acme:alice"
	for _, algorithm := range []PasswordAlgorithm{
		HMACSHA1Base64,
		HMACSHA256Base64,
		HMACSHA1Hex,
		HMACSHA256Hex,
	} {
		password, err := DerivePasswordWith([]byte(testSecret), username, algorithm)
		if err != nil {
			t.Fatalf("DerivePasswordWith(%q): %v", algorithm, err)
		}
		if password == "" {
			t.Errorf("DerivePasswordWith(%q) returned empty", algorithm)
		}

		validator, err := NewValidator(Config{
			SharedSecret:      []byte(testSecret),
			Realm:             testRealm,
			Now:               testNow,
			PasswordAlgorithm: algorithm,
		})
		if err != nil {
			t.Fatalf("NewValidator(%q): %v", algorithm, err)
		}
		testUsername := validUsername(testNow().Unix() + 3600)
		expectedPassword, err := DerivePasswordWith([]byte(testSecret), testUsername, algorithm)
		if err != nil {
			t.Fatalf("DerivePasswordWith(%q): %v", algorithm, err)
		}
		credentials, err := validator.Validate(testUsername)
		if err != nil {
			t.Fatalf("Validate(%q): %v", algorithm, err)
		}
		if credentials.Password != expectedPassword {
			t.Errorf("algorithm %q password = %q, want %q", algorithm, credentials.Password, expectedPassword)
		}
	}
}
