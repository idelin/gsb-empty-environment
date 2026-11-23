// Package webhook implements the abuse-protection handshake of the
// CloudEvents HTTP Webhook specification.
//
// Before an event producer starts delivering events to a subscriber
// callback URL, it must send an HTTP OPTIONS request and confirm that the
// target is willing to receive the traffic. The validation implemented here
// prevents an attacker from registering the address of an unrelated victim
// service and turning the event platform into a traffic-amplification proxy.
package webhook

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CloudEvents HTTP Webhook handshake headers. Header names are
// case-insensitive on the wire; these are the canonical spellings.
const (
	HeaderRequestOrigin = "WebHook-Request-Origin"
	HeaderRequestRate   = "WebHook-Request-Rate"
	HeaderAllowedOrigin = "WebHook-Allowed-Origin"
	HeaderAllowedRate   = "WebHook-Allowed-Rate"
)

// Wildcard indicates that a subscriber accepts any origin or imposes no
// numeric delivery-rate ceiling.
const Wildcard = "*"

// DefaultTTL is how long a successful validation remains acceptable. A
// subscription that is not revalidated within this window is paused.
const DefaultTTL = 24 * time.Hour

var (
	// ErrInvalidConfig means the validator options are unusable.
	ErrInvalidConfig = errors.New("webhook: invalid validator configuration")
	// ErrInvalidCallback means the callback URL is malformed or not HTTPS.
	ErrInvalidCallback = errors.New("webhook: callback must be an https URL")
	// ErrBlockedAddress means the callback host resolves to a loopback,
	// private or otherwise non-public address.
	ErrBlockedAddress = errors.New("webhook: callback host resolves to a blocked address")
	// ErrUnexpectedStatus means the validation response status was not 2xx.
	ErrUnexpectedStatus = errors.New("webhook: validation returned a non-success status")
	// ErrHostMismatch means the response came from a different host than the
	// registered callback address.
	ErrHostMismatch = errors.New("webhook: validation response host does not match the registered host")
	// ErrMissingAllowedOrigin means WebHook-Allowed-Origin was absent.
	ErrMissingAllowedOrigin = errors.New("webhook: missing WebHook-Allowed-Origin header")
	// ErrOriginNotAllowed means the allowed origin does not match the sender.
	ErrOriginNotAllowed = errors.New("webhook: WebHook-Allowed-Origin does not permit the request origin")
	// ErrMissingAllowedRate means WebHook-Allowed-Rate was absent.
	ErrMissingAllowedRate = errors.New("webhook: missing WebHook-Allowed-Rate header")
	// ErrInvalidRate means the rate header is neither "*" nor a positive int.
	ErrInvalidRate = errors.New("webhook: WebHook-Allowed-Rate must be a positive integer or *")
	// ErrExpired means the subscription has not been revalidated in time.
	ErrExpired = errors.New("webhook: subscription validation has expired")
	// ErrUnknownSubscription means no validated subscription exists for the URL.
	ErrUnknownSubscription = errors.New("webhook: unknown subscription")
)

// Options configures a Validator.
type Options struct {
	// Origin is the reverse-DNS name of this event producer, sent in
	// WebHook-Request-Origin and matched against WebHook-Allowed-Origin.
	Origin string
	// RequestRate is the rate (deliveries per second) this producer wants to
	// use, sent in WebHook-Request-Rate. Must be positive.
	RequestRate int
	// TTL overrides DefaultTTL; zero keeps the 24-hour default.
	TTL time.Duration
	// HTTPClient optionally supplies a client. Redirects are always disabled
	// by overwriting CheckRedirect, and a timeout is applied when unset.
	HTTPClient *http.Client
	// LookupIP overrides DNS resolution, primarily for tests. When nil the
	// system resolver is used.
	LookupIP func(ctx context.Context, host string) ([]net.IP, error)
	// Now overrides the clock, primarily for tests.
	Now func() time.Time
}

// Subscription records the outcome of a successful validation.
type Subscription struct {
	CallbackURL string
	// AllowedRate is the effective delivery rate: the smaller of the
	// producer request rate and the subscriber-allowed rate. A zero value
	// means the subscriber returned "*" and only the producer rate applies.
	AllowedRate int
	// VerifiedAt is when the last successful validation completed.
	VerifiedAt time.Time
	// ExpiresAt is VerifiedAt plus the configured TTL.
	ExpiresAt time.Time
}

// Fresh reports whether the subscription may still receive events at time now.
func (s *Subscription) Fresh(now time.Time) bool {
	return now.Before(s.ExpiresAt)
}

// Validator performs webhook handshakes and tracks validated subscriptions.
type Validator struct {
	origin      string
	requestRate int
	ttl         time.Duration
	client      *http.Client
	lookupIP    func(ctx context.Context, host string) ([]net.IP, error)
	now         func() time.Time

	mu            sync.RWMutex
	subscriptions map[string]*Subscription
}

// New constructs a Validator.
func New(opts Options) (*Validator, error) {
	if strings.TrimSpace(opts.Origin) == "" {
		return nil, fmt.Errorf("%w: origin is required", ErrInvalidConfig)
	}
	if opts.RequestRate <= 0 {
		return nil, fmt.Errorf("%w: request rate must be positive", ErrInvalidConfig)
	}
	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy:               nil,
				DialContext:         (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
				TLSHandshakeTimeout: 5 * time.Second,
			},
		}
	}
	// Never follow redirects: a 3xx response is itself a validation failure.
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	lookup := opts.LookupIP
	if lookup == nil {
		resolver := &net.Resolver{}
		lookup = func(ctx context.Context, host string) ([]net.IP, error) {
			return resolver.LookupIP(ctx, "ip", host)
		}
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Validator{
		origin:        opts.Origin,
		requestRate:   opts.RequestRate,
		ttl:           ttl,
		client:        client,
		lookupIP:      lookup,
		now:           now,
		subscriptions: make(map[string]*Subscription),
	}, nil
}

// Register validates callbackURL with an OPTIONS handshake and, on success,
// stores the resulting subscription.
func (v *Validator) Register(ctx context.Context, callbackURL string) (*Subscription, error) {
	sub, err := v.validate(ctx, callbackURL)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	v.subscriptions[callbackURL] = sub
	v.mu.Unlock()
	return sub, nil
}

// Revalidate repeats the handshake for an already-registered URL and refreshes
// its expiry. Re-registering a new URL through Register also works.
func (v *Validator) Revalidate(ctx context.Context, callbackURL string) (*Subscription, error) {
	v.mu.RLock()
	_, known := v.subscriptions[callbackURL]
	v.mu.RUnlock()
	if !known {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSubscription, callbackURL)
	}
	return v.Register(ctx, callbackURL)
}

// AllowDelivery reports whether events may currently be delivered to
// callbackURL. Delivery is paused once the validation TTL elapses until the
// subscription is revalidated.
func (v *Validator) AllowDelivery(callbackURL string) (*Subscription, error) {
	v.mu.RLock()
	sub, ok := v.subscriptions[callbackURL]
	v.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownSubscription, callbackURL)
	}
	if !sub.Fresh(v.now()) {
		return nil, fmt.Errorf("%w: %s", ErrExpired, callbackURL)
	}
	return sub, nil
}

func (v *Validator) validate(ctx context.Context, callbackURL string) (*Subscription, error) {
	u, host, err := v.preflight(ctx, callbackURL)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodOptions, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("webhook: build validation request: %w", err)
	}
	req.Header.Set(HeaderRequestOrigin, v.origin)
	req.Header.Set(HeaderRequestRate, strconv.Itoa(v.requestRate))

	resp, err := v.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webhook: send validation request: %w", err)
	}
	defer resp.Body.Close()

	// Defensive check: with redirects disabled the response host must be the
	// registered host. A transport that somehow reached another host fails.
	if err := verifyResponseHost(req.URL, resp.Request.URL, host); err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: %d", ErrUnexpectedStatus, resp.StatusCode)
	}

	if err := verifyAllowedOrigin(resp.Header, v.origin); err != nil {
		return nil, err
	}
	rate, err := verifyAllowedRate(resp.Header, v.requestRate)
	if err != nil {
		return nil, err
	}

	now := v.now()
	return &Subscription{
		CallbackURL: callbackURL,
		AllowedRate: rate,
		VerifiedAt:  now,
		ExpiresAt:   now.Add(v.ttl),
	}, nil
}

// preflight enforces the URL scheme and rejects any callback whose host
// resolves to a non-public address before any network request is sent.
func (v *Validator) preflight(ctx context.Context, callbackURL string) (*url.URL, string, error) {
	u, err := url.Parse(callbackURL)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrInvalidCallback, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return nil, "", fmt.Errorf("%w: %s", ErrInvalidCallback, callbackURL)
	}
	host := u.Hostname()
	if host == "" {
		return nil, "", fmt.Errorf("%w: missing host", ErrInvalidCallback)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		ips, err = v.lookupIP(ctx, host)
		if err != nil {
			return nil, "", fmt.Errorf("webhook: resolve callback host: %w", err)
		}
		if len(ips) == 0 {
			return nil, "", fmt.Errorf("webhook: resolve callback host: no addresses for %s", host)
		}
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return nil, "", fmt.Errorf("%w: %s -> %s", ErrBlockedAddress, host, ip)
		}
	}
	return u, host, nil
}

// verifyResponseHost rejects any response whose final host differs from the
// host of the registered callback URL.
func verifyResponseHost(registered, actual *url.URL, registeredHost string) error {
	if actual == nil {
		return fmt.Errorf("%w: no response request URL", ErrHostMismatch)
	}
	if !strings.EqualFold(actual.Hostname(), registeredHost) {
		return fmt.Errorf("%w: registered %q, response from %q", ErrHostMismatch,
			registered.Hostname(), actual.Hostname())
	}
	return nil
}

func verifyAllowedOrigin(h http.Header, origin string) error {
	allowed := h.Get(HeaderAllowedOrigin)
	if allowed == "" {
		return ErrMissingAllowedOrigin
	}
	if allowed != Wildcard && !strings.EqualFold(allowed, origin) {
		return fmt.Errorf("%w: got %q", ErrOriginNotAllowed, allowed)
	}
	return nil
}

// verifyAllowedRate validates WebHook-Allowed-Rate and returns the effective
// rate: zero when the subscriber allows any rate, otherwise the smaller of
// the two rates.
func verifyAllowedRate(h http.Header, requestRate int) (int, error) {
	raw := h.Get(HeaderAllowedRate)
	if raw == "" {
		return 0, ErrMissingAllowedRate
	}
	raw = strings.TrimSpace(raw)
	if raw == Wildcard {
		return 0, nil
	}
	rate, err := strconv.Atoi(raw)
	if err != nil || rate <= 0 {
		return 0, fmt.Errorf("%w: got %q", ErrInvalidRate, raw)
	}
	return min(requestRate, rate), nil
}

// isBlockedIP reports addresses that must never be reached by a publicly
// registered callback: loopback, link-local, private, multicast, unspecified
// and shared/CGNAT address space.
func isBlockedIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// 100.64.0.0/10 carrier-grade NAT (not covered by net.IP.IsPrivate).
	if v4 := ip.To4(); v4 != nil && len(v4) == 4 && v4[0] == 100 && v4[1]&0xc0 == 64 {
		return true
	}
	return false
}
