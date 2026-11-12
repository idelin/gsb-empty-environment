package netsec

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// DefaultMaxUsernameLength bounds the USERNAME value accepted by a
	// Validator. STUN attributes are at most 65535 bytes, but normal TURN
	// credentials are far smaller.
	DefaultMaxUsernameLength = 1024

	// maxExpiry is 9999-12-31T23:59:59Z. Rejecting later values keeps expiry
	// arithmetic well inside time.Time's supported range.
	maxExpiry int64 = 253402300799
)

// PasswordAlgorithm selects the HMAC password derivation used by the TURN
// REST-style credential scheme. HMACSHA1Base64 is the interoperable default
// used by coturn and other TURN REST implementations:
//
//	password = base64(HMAC-SHA1(shared_secret, username))
type PasswordAlgorithm string

const (
	HMACSHA1Base64   PasswordAlgorithm = "hmac-sha1-base64"
	HMACSHA256Base64 PasswordAlgorithm = "hmac-sha256-base64"
	HMACSHA1Hex      PasswordAlgorithm = "hmac-sha1-hex"
	HMACSHA256Hex    PasswordAlgorithm = "hmac-sha256-hex"
)

// Config configures a Validator.
type Config struct {
	// SharedSecret is the long-term TURN shared secret. It must be non-empty
	// and is copied by NewValidator.
	SharedSecret []byte

	// Realm is the TURN realm included in MD5(username:realm:password).
	Realm string

	// Now supplies the current time and is primarily useful in tests. A nil
	// field means time.Now.
	Now func() time.Time

	// ClockSkew extends or shortens the expiry deadline. Positive values make
	// credentials live longer; negative values expire them earlier.
	ClockSkew time.Duration

	// AllowedTenants is an optional exact-match tenant allowlist. An empty
	// slice allows any well-formed tenant.
	AllowedTenants []string

	// TenantAuthorizer is an optional additional tenant policy. When set, the
	// tenant must both be in AllowedTenants (if that list is non-empty) and be
	// accepted by this function.
	TenantAuthorizer func(tenant string) bool

	// PasswordAlgorithm selects the HMAC derivation. The empty value means
	// HMACSHA1Base64.
	PasswordAlgorithm PasswordAlgorithm

	// MaxUsernameLength overrides DefaultMaxUsernameLength when positive.
	MaxUsernameLength int

	// Peers is the peer-address policy applied by AuthenticateSTUN and
	// CheckPeer. The zero value rejects loopback and private addresses.
	Peers PeerPolicy
}

// Credentials is a username plus the secret-derived values used by TURN.
type Credentials struct {
	// Username is the exact username string presented by the client.
	Username string

	// Expiry is the Unix timestamp (seconds) at which the credential expires.
	Expiry int64

	// ExpiresAt is Expiry converted to a UTC time.
	ExpiresAt time.Time

	// Tenant is the middle segment of the username.
	Tenant string

	// User is the final segment of the username. It never contains a colon.
	User string

	// Realm is the realm used to derive IntegrityKey.
	Realm string

	// Password is the HMAC-derived TURN password.
	Password string

	// IntegrityKey is MD5(username:realm:password), the key used by
	// MESSAGE-INTEGRITY on the wire.
	IntegrityKey []byte
}

// Validator validates TURN usernames and STUN requests using long-term
// MESSAGE-INTEGRITY credentials.
type Validator struct {
	secret            []byte
	realm             string
	now               func() time.Time
	clockSkew         time.Duration
	tenants           map[string]struct{}
	tenantAuthorizer  func(string) bool
	passwordAlgorithm PasswordAlgorithm
	maxUsernameLength int
	peers             PeerPolicy
}

// NewValidator validates cfg and returns a credential validator.
func NewValidator(cfg Config) (*Validator, error) {
	if len(cfg.SharedSecret) == 0 {
		return nil, fmt.Errorf("%w: shared secret is empty", ErrConfig)
	}
	if cfg.Realm == "" {
		return nil, fmt.Errorf("%w: realm is empty", ErrConfig)
	}
	if !utf8.ValidString(cfg.Realm) || hasControlRune(cfg.Realm) {
		return nil, fmt.Errorf("%w: realm contains invalid characters", ErrConfig)
	}

	algorithm := cfg.PasswordAlgorithm
	if algorithm == "" {
		algorithm = HMACSHA1Base64
	}
	switch algorithm {
	case HMACSHA1Base64, HMACSHA256Base64, HMACSHA1Hex, HMACSHA256Hex:
	default:
		return nil, fmt.Errorf("%w: unknown password algorithm %q", ErrConfig, algorithm)
	}

	maxUsernameLength := cfg.MaxUsernameLength
	if maxUsernameLength <= 0 {
		maxUsernameLength = DefaultMaxUsernameLength
	}

	tenants := make(map[string]struct{}, len(cfg.AllowedTenants))
	for _, tenant := range cfg.AllowedTenants {
		if tenant == "" {
			return nil, fmt.Errorf("%w: empty allowed tenant", ErrConfig)
		}
		tenants[tenant] = struct{}{}
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Validator{
		secret:            append([]byte(nil), cfg.SharedSecret...),
		realm:             cfg.Realm,
		now:               now,
		clockSkew:         cfg.ClockSkew,
		tenants:           tenants,
		tenantAuthorizer:  cfg.TenantAuthorizer,
		passwordAlgorithm: algorithm,
		maxUsernameLength: maxUsernameLength,
		peers:             cfg.Peers,
	}, nil
}

// New is a convenience constructor for the common configuration.
func New(sharedSecret []byte, realm string) (*Validator, error) {
	return NewValidator(Config{SharedSecret: sharedSecret, Realm: realm})
}

// ParseUsername parses expiry:tenant:user. It checks structure, expiry range,
// and text well-formedness, but it does not check expiration or tenant policy
// because it has no clock or configuration.
func ParseUsername(username string) (*Credentials, error) {
	if len(username) > DefaultMaxUsernameLength {
		return nil, fmt.Errorf("%w: username exceeds %d bytes", ErrUsernameFormat, DefaultMaxUsernameLength)
	}

	parts := strings.Split(username, ":")
	if len(parts) != 3 {
		return nil, fmt.Errorf("%w: expected expiry:tenant:user", ErrUsernameFormat)
	}
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return nil, fmt.Errorf("%w: empty username segment", ErrUsernameFormat)
	}
	if !validUsernameSegment(parts[1]) || !validUsernameSegment(parts[2]) {
		return nil, fmt.Errorf("%w: invalid tenant or user segment", ErrUsernameFormat)
	}

	expiry, err := parseExpiry(parts[0])
	if err != nil {
		return nil, err
	}

	return &Credentials{
		Username:  username,
		Expiry:    expiry,
		ExpiresAt: time.Unix(expiry, 0).UTC(),
		Tenant:    parts[1],
		User:      parts[2],
	}, nil
}

func parseExpiry(s string) (int64, error) {
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("%w: expiry must be decimal Unix seconds", ErrUsernameFormat)
		}
	}

	expiry, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: expiry is out of range", ErrUsernameFormat)
	}
	if expiry > maxExpiry {
		return 0, fmt.Errorf("%w: expiry is later than year 9999", ErrUsernameFormat)
	}
	return expiry, nil
}

func validUsernameSegment(s string) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	// Reject leading/trailing whitespace and control characters to avoid
	// tenant or user canonicalisation surprises.
	if strings.TrimSpace(s) != s || hasControlRune(s) {
		return false
	}
	return true
}

func hasControlRune(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// Validate parses and authorizes username using the configured clock, expiry,
// and tenant policy. It does not authenticate a STUN message; callers that
// accept wire input must use AuthenticateSTUN or AuthenticateMessage so that
// MESSAGE-INTEGRITY proves the expiry and tenant were not altered.
func (v *Validator) Validate(username string) (*Credentials, error) {
	credentials, err := v.parse(username)
	if err != nil {
		return nil, err
	}
	if err := v.authorize(credentials); err != nil {
		return nil, err
	}
	return credentials, nil
}

// Authenticate is an alias for Validate.
func (v *Validator) Authenticate(username string) (*Credentials, error) {
	return v.Validate(username)
}

func (v *Validator) parse(username string) (*Credentials, error) {
	if len(username) > v.maxUsernameLength {
		return nil, fmt.Errorf("%w: username exceeds %d bytes", ErrUsernameFormat, v.maxUsernameLength)
	}

	credentials, err := ParseUsername(username)
	if err != nil {
		return nil, err
	}

	password, err := DerivePasswordWith(v.secret, username, v.passwordAlgorithm)
	if err != nil {
		return nil, err
	}
	credentials.Password = password
	credentials.Realm = v.realm
	credentials.IntegrityKey = MessageIntegrityKey(username, v.realm, password)
	return credentials, nil
}

func (v *Validator) authorize(credentials *Credentials) error {
	deadline := credentials.ExpiresAt.Add(v.clockSkew)
	if v.now().After(deadline) {
		return fmt.Errorf("%w: expiry %d", ErrUsernameExpired, credentials.Expiry)
	}

	if len(v.tenants) > 0 {
		if _, ok := v.tenants[credentials.Tenant]; !ok {
			return fmt.Errorf("%w: %q", ErrTenantNotAllowed, credentials.Tenant)
		}
	}
	if v.tenantAuthorizer != nil && !v.tenantAuthorizer(credentials.Tenant) {
		return fmt.Errorf("%w: %q", ErrTenantNotAllowed, credentials.Tenant)
	}
	return nil
}

// DerivePassword derives the interoperable TURN REST password:
// base64(HMAC-SHA1(sharedSecret, username)).
func DerivePassword(sharedSecret []byte, username string) string {
	password, _ := DerivePasswordWith(sharedSecret, username, HMACSHA1Base64)
	return password
}

// DerivePasswordWith derives a TURN password with the selected algorithm.
func DerivePasswordWith(sharedSecret []byte, username string, algorithm PasswordAlgorithm) (string, error) {
	if algorithm == "" {
		algorithm = HMACSHA1Base64
	}

	switch algorithm {
	case HMACSHA1Base64:
		return base64.StdEncoding.EncodeToString(hmacDigest(sharedSecret, username, sha1.New)), nil
	case HMACSHA256Base64:
		return base64.StdEncoding.EncodeToString(hmacDigest(sharedSecret, username, sha256.New)), nil
	case HMACSHA1Hex:
		return hex.EncodeToString(hmacDigest(sharedSecret, username, sha1.New)), nil
	case HMACSHA256Hex:
		return hex.EncodeToString(hmacDigest(sharedSecret, username, sha256.New)), nil
	default:
		return "", fmt.Errorf("%w: unknown password algorithm %q", ErrConfig, algorithm)
	}
}

func hmacDigest(key []byte, data string, newHash func() hash.Hash) []byte {
	mac := hmac.New(newHash, key)
	_, _ = mac.Write([]byte(data))
	return mac.Sum(nil)
}

// MessageIntegrityKey returns MD5(username:realm:password), the key used to
// authenticate STUN MESSAGE-INTEGRITY attributes for long-term credentials.
func MessageIntegrityKey(username, realm, password string) []byte {
	sum := md5.Sum([]byte(username + ":" + realm + ":" + password))
	return sum[:]
}

// CheckPeer applies the validator's peer policy to ip.
func (v *Validator) CheckPeer(ip net.IP) error {
	return v.peers.Check(ip)
}

// Realm returns the configured TURN realm.
func (v *Validator) Realm() string {
	return v.realm
}

// PasswordAlgorithm returns the configured password derivation.
func (v *Validator) PasswordAlgorithm() PasswordAlgorithm {
	return v.passwordAlgorithm
}

// ValidateCredentials is a convenience wrapper using the default password
// algorithm and strict peer policy.
func ValidateCredentials(username string, sharedSecret []byte, realm string) (*Credentials, error) {
	validator, err := New(sharedSecret, realm)
	if err != nil {
		return nil, err
	}
	return validator.Validate(username)
}

// AuthenticateSTUN verifies a wire-format STUN message, returning the
// authenticated credentials. The configured realm is used in the integrity
// key. A REALM attribute, if present, must match that configuration.
func AuthenticateSTUN(raw []byte, sharedSecret []byte, realm string) (*Credentials, error) {
	validator, err := New(sharedSecret, realm)
	if err != nil {
		return nil, err
	}
	return validator.AuthenticateSTUN(raw)
}

// ValidateSTUN is an alias for AuthenticateSTUN.
func ValidateSTUN(raw []byte, sharedSecret []byte, realm string) (*Credentials, error) {
	return AuthenticateSTUN(raw, sharedSecret, realm)
}
