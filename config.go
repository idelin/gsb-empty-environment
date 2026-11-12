package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Key associates a key identifier with a Go crypto key. The Value may be:
//
//   - *rsa.PublicKey or *rsa.PrivateKey for RS*/PS* verification
//   - *ecdsa.PublicKey or *ecdsa.PrivateKey for ES* verification
//   - ed25519.PublicKey or ed25519.PrivateKey for EdDSA verification
//   - []byte for HMAC verification or JWE dir/A*KW unwrapping
//   - *rsa.PrivateKey for RSA-OAEP JWE decryption
//
// Raw key values (without Key wrappers) are also accepted when a key id is not
// needed.
type Key struct {
	ID    string
	Value any
}

// Client is the authorization server's registration data needed while
// processing a request object. Algorithms must contain the JWS algorithms the
// client is allowed to use. The parser deliberately does not accept an empty
// allow-list as "all algorithms".
type Client struct {
	ClientID                       string
	RequestObjectSigningAlgorithms []string
	RequestObjectSigningKeys       []any
}

// ClientLookup retrieves a client registration. Returning ErrUnknownClient is
// appropriate for an unregistered client.
type ClientLookup interface {
	LookupClient(ctx context.Context, clientID string) (*Client, error)
}

// ClientLookupFunc adapts a function to ClientLookup.
type ClientLookupFunc func(ctx context.Context, clientID string) (*Client, error)

func (f ClientLookupFunc) LookupClient(ctx context.Context, clientID string) (*Client, error) {
	return f(ctx, clientID)
}

// StaticClients is a small in-memory ClientLookup useful for tests and simple
// deployments.
type StaticClients map[string]*Client

func (s StaticClients) LookupClient(_ context.Context, clientID string) (*Client, error) {
	client, ok := s[clientID]
	if !ok || client == nil {
		return nil, errorf(ErrUnknownClient, clientID)
	}
	return client, nil
}

// RequestURIFetcher resolves a request_uri to its compact JOSE serialization.
// Implementations must enforce their own network policy, timeouts, and size
// limits.
type RequestURIFetcher interface {
	FetchRequestURI(ctx context.Context, uri string) ([]byte, error)
}

// RequestURIFetcherFunc adapts a function to RequestURIFetcher.
type RequestURIFetcherFunc func(ctx context.Context, uri string) ([]byte, error)

func (f RequestURIFetcherFunc) FetchRequestURI(ctx context.Context, uri string) ([]byte, error) {
	return f(ctx, uri)
}

// HTTPRequestURIFetcher is a conservative HTTPS-only fetcher. It disables
// redirects and caps the response body.
type HTTPRequestURIFetcher struct {
	Client          *http.Client
	MaxResponseSize int64
}

func (f *HTTPRequestURIFetcher) FetchRequestURI(ctx context.Context, uri string) ([]byte, error) {
	if !strings.HasPrefix(uri, "https://") {
		return nil, errorf(ErrRequestURIFetch, "request_uri must use https")
	}
	baseClient := f.Client
	if baseClient == nil {
		baseClient = &http.Client{
			Timeout: 5 * time.Second,
		}
	}
	client := &http.Client{
		Transport: baseClient.Transport,
		Jar:       baseClient.Jar,
		Timeout:   baseClient.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return nil, errorf(ErrRequestURIFetch, err.Error())
	}
	req.Header.Set("Accept", "application/jose, application/jwt")
	resp, err := client.Do(req)
	if err != nil {
		return nil, errorf(ErrRequestURIFetch, err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errorf(ErrRequestURIFetch, resp.Status)
	}
	limit := f.MaxResponseSize
	if limit <= 0 {
		limit = 1 << 20
	}
	body, err := readLimited(resp.Body, limit)
	if err != nil {
		return nil, errorf(ErrRequestURIFetch, err.Error())
	}
	return body, nil
}

// Config controls request object verification and decryption.
type Config struct {
	// Issuer is the authorization server's issuer identifier. Every request
	// object must have an aud claim containing this exact value.
	Issuer string

	// Clients resolves the client_id supplied in the outer authorization
	// request.
	Clients ClientLookup

	// DecryptionKeys are the authorization server's private keys used for JWE
	// request objects. They may be raw keys or Key values.
	DecryptionKeys []any

	// DecryptionAlgorithms restricts JWE "alg" values. If empty, a safe default
	// set is used. "none" and RSA1_5 are never supported.
	DecryptionAlgorithms []string

	// ContentEncryptionAlgorithms restricts JWE "enc" values. If empty, AES-GCM
	// and AES-CBC-HMAC defaults are used.
	ContentEncryptionAlgorithms []string

	// RequestURIFetcher resolves request_uri values. If nil, request_uri
	// requests are rejected.
	RequestURIFetcher RequestURIFetcher

	// Clock supplies the current time for optional exp/nbf checks. Defaults to
	// time.Now.
	Clock func() time.Time

	// Leeway is applied to exp and nbf checks.
	Leeway time.Duration
}

func (c *Config) validate() error {
	if strings.TrimSpace(c.Issuer) == "" {
		return errors.New("netsec: Config.Issuer is required")
	}
	if c.Clients == nil {
		return errors.New("netsec: Config.Clients is required")
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.Leeway < 0 {
		return errors.New("netsec: Config.Leeway must not be negative")
	}
	return nil
}

var supportedJWEKeyAlgorithms = map[string]bool{
	"RSA-OAEP":     true,
	"RSA-OAEP-256": true,
	"dir":          true,
	"A128KW":       true,
	"A192KW":       true,
	"A256KW":       true,
}

var supportedContentEncryptionAlgorithms = map[string]bool{
	"A128GCM":       true,
	"A192GCM":       true,
	"A256GCM":       true,
	"A128CBC-HS256": true,
	"A256CBC-HS512": true,
}

var supportedJWSAlgorithms = map[string]bool{
	"HS256": true,
	"HS384": true,
	"HS512": true,
	"RS256": true,
	"RS384": true,
	"RS512": true,
	"PS256": true,
	"PS384": true,
	"PS512": true,
	"ES256": true,
	"ES384": true,
	"ES512": true,
	"EdDSA": true,
}

func normalizeAlgorithmSet(values []string, supported map[string]bool, what string) (map[string]bool, error) {
	if len(values) == 0 {
		set := make(map[string]bool, len(supported))
		for alg := range supported {
			set[alg] = true
		}
		return set, nil
	}
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if !supported[value] {
			return nil, fmt.Errorf("netsec: unsupported %s %q", what, value)
		}
		set[value] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("netsec: %s allow-list is empty", what)
	}
	return set, nil
}
