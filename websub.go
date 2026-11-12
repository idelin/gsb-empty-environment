// Package netsec provides WebSub subscription verification and content
// distribution helpers for a hub.
package netsec

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxLeaseSeconds is the longest lease a hub will grant.
	MaxLeaseSeconds = 864000
	// MaxSecretBytes is the largest accepted hub.secret, in bytes. The
	// WebSub requirement is that the secret be strictly less than 200 bytes.
	MaxSecretBytes = 199
	// MaxUnverifiedPerHour is the number of subscription attempts allowed
	// from one callback in a rolling hour.
	MaxUnverifiedPerHour = 20
	// ChallengeBytes is the number of random bytes in a subscription
	// challenge. The challenge sent on the wire is its hexadecimal encoding.
	ChallengeBytes = 16

	// MaxLease is an alias for MaxLeaseSeconds.
	MaxLease = MaxLeaseSeconds
	// LeaseCapSeconds is an alias for MaxLeaseSeconds.
	LeaseCapSeconds = MaxLeaseSeconds
	// MaxSecretLength is an alias for MaxSecretBytes.
	MaxSecretLength = MaxSecretBytes
	// SecretLimitBytes is the strict exclusive upper bound for hub.secret.
	SecretLimitBytes = 200
	// MaxUnverifiedRequestsPerHour is an alias for MaxUnverifiedPerHour.
	MaxUnverifiedRequestsPerHour = MaxUnverifiedPerHour
	// MinChallengeBytes is an alias for ChallengeBytes.
	MinChallengeBytes = ChallengeBytes
)

var (
	// ErrInvalidMode is returned when hub.mode is not "subscribe".
	ErrInvalidMode = errors.New("websub: invalid hub mode")
	// ErrInvalidCallback is returned for a missing or malformed callback URL.
	ErrInvalidCallback = errors.New("websub: invalid callback URL")
	// ErrInsecureCallback is returned when the callback is not HTTPS.
	ErrInsecureCallback = errors.New("websub: callback must use HTTPS")
	// ErrPrivateCallback is returned when the callback resolves to a private
	// or otherwise non-public address.
	ErrPrivateCallback = errors.New("websub: callback resolves to a private address")
	// ErrInvalidTopic is returned for a missing or malformed topic URL.
	ErrInvalidTopic = errors.New("websub: invalid topic URL")
	// ErrInvalidLease is returned for a malformed or non-positive lease.
	ErrInvalidLease = errors.New("websub: invalid lease_seconds")
	// ErrSecretTooLong is returned when hub.secret is 200 bytes or longer.
	ErrSecretTooLong = errors.New("websub: hub.secret must be less than 200 bytes")
	// ErrRateLimited is returned when a callback has exceeded its hourly
	// allowance of unverified subscription requests.
	ErrRateLimited = errors.New("websub: too many unverified subscription requests")
	// ErrVerificationFailed indicates that the callback did not prove
	// control of the callback URL.
	ErrVerificationFailed = errors.New("websub: callback verification failed")
	// ErrChallengeMismatch indicates that a callback returned a body that was
	// not byte-for-byte equal to the challenge.
	ErrChallengeMismatch = errors.New("websub: callback challenge mismatch")
)

// IPResolver is the small part of net.Resolver needed to vet callback
// hostnames. It is injectable for tests and private DNS setups.
type IPResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// Option configures a Manager.
type Option func(*Manager)

// Hub is an alias for Manager.
type Hub = Manager

// SubscriptionManager is an alias for Manager.
type SubscriptionManager = Manager

// SubscriptionRequest is the data needed to verify a subscription.
type SubscriptionRequest struct {
	Callback     string
	Topic        string
	Secret       string
	LeaseSeconds int
}

// Subscription is a verified callback registration.
type Subscription struct {
	Callback     string    `json:"callback"`
	Topic        string    `json:"topic"`
	Secret       string    `json:"secret,omitempty"`
	LeaseSeconds int       `json:"lease_seconds"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// DeliveryResult describes one attempted content distribution.
type DeliveryResult struct {
	Callback   string
	Topic      string
	StatusCode int
	Err        error
}

type subscriptionKey struct {
	callback string
	topic    string
}

type pendingSubscription struct {
	callback  *url.URL
	key       subscriptionKey
	secret    string
	lease     int
	challenge string
}

// Manager verifies and stores WebSub subscriptions. Its zero value is not
// useful; construct one with New, NewManager, NewHub, or NewWithConfig.
type Manager struct {
	mu sync.Mutex

	client   *http.Client
	resolver IPResolver
	now      func() time.Time
	random   io.Reader

	allowPrivate         bool
	verificationTimeout  time.Duration
	maxLeaseSeconds      int
	maxUnverifiedPerHour int

	subscriptions map[subscriptionKey]Subscription
	attempts      map[string][]time.Time
	pending       map[string]pendingSubscription
	wg            sync.WaitGroup
}

// Config is an alternative, value-oriented way to construct a Manager.
// Zero fields use the same secure defaults as New.
type Config struct {
	HTTPClient *http.Client
	Resolver   IPResolver
	Clock      func() time.Time
	Rand       io.Reader

	AllowPrivateCallbacks bool
	VerificationTimeout   time.Duration
	MaxLeaseSeconds       int
	MaxUnverifiedPerHour  int
}

// New returns a subscription manager with secure defaults.
func New(opts ...Option) *Manager {
	m := &Manager{
		now:                  time.Now,
		random:               rand.Reader,
		resolver:             net.DefaultResolver,
		verificationTimeout:  10 * time.Second,
		maxLeaseSeconds:      MaxLeaseSeconds,
		maxUnverifiedPerHour: MaxUnverifiedPerHour,
		subscriptions:        make(map[subscriptionKey]Subscription),
		attempts:             make(map[string][]time.Time),
		pending:              make(map[string]pendingSubscription),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(m)
		}
	}
	m.finishConstruction()
	return m
}

// NewManager is an alias for New.
func NewManager(opts ...Option) *Manager {
	return New(opts...)
}

// NewSubscriptionManager is an alias for New.
func NewSubscriptionManager(opts ...Option) *Manager {
	return New(opts...)
}

// NewHub is an alias for New.
func NewHub(opts ...Option) *Manager {
	return New(opts...)
}

// Handler returns an http.Handler that accepts subscription requests.
func (m *Manager) Handler() http.Handler {
	return m
}

// NewWithConfig constructs a Manager from a Config.
func NewWithConfig(cfg Config) *Manager {
	opts := make([]Option, 0, 8)
	if cfg.HTTPClient != nil {
		opts = append(opts, WithHTTPClient(cfg.HTTPClient))
	}
	if cfg.Resolver != nil {
		opts = append(opts, WithResolver(cfg.Resolver))
	}
	if cfg.Clock != nil {
		opts = append(opts, WithClock(cfg.Clock))
	}
	if cfg.Rand != nil {
		opts = append(opts, WithRand(cfg.Rand))
	}
	if cfg.AllowPrivateCallbacks {
		opts = append(opts, WithAllowPrivateCallbacks())
	}
	if cfg.VerificationTimeout > 0 {
		opts = append(opts, WithVerificationTimeout(cfg.VerificationTimeout))
	}
	if cfg.MaxLeaseSeconds > 0 {
		opts = append(opts, WithMaxLeaseSeconds(cfg.MaxLeaseSeconds))
	}
	if cfg.MaxUnverifiedPerHour > 0 {
		opts = append(opts, WithMaxUnverifiedPerHour(cfg.MaxUnverifiedPerHour))
	}
	return New(opts...)
}

// WithHTTPClient sets the client used for verification and distribution.
// The supplied client is shallow-copied so redirect safety can be enforced
// without mutating the caller's client.
func WithHTTPClient(client *http.Client) Option {
	return func(m *Manager) {
		m.client = client
	}
}

// WithResolver sets the DNS resolver used for callback SSRF checks.
func WithResolver(resolver IPResolver) Option {
	return func(m *Manager) {
		if resolver != nil {
			m.resolver = resolver
		}
	}
}

// WithClock sets the clock used for leases and rate limiting.
func WithClock(now func() time.Time) Option {
	return func(m *Manager) {
		if now != nil {
			m.now = now
		}
	}
}

// WithRand sets the source of challenge randomness.
func WithRand(random io.Reader) Option {
	return func(m *Manager) {
		if random != nil {
			m.random = random
		}
	}
}

// WithAllowPrivateCallbacks disables the private-address check. It is
// intended for tests and for deployments that deliberately use an internal
// callback network; production hubs should not enable it.
func WithAllowPrivateCallbacks() Option {
	return func(m *Manager) {
		m.allowPrivate = true
	}
}

// WithVerificationTimeout sets the deadline for one callback verification.
func WithVerificationTimeout(timeout time.Duration) Option {
	return func(m *Manager) {
		if timeout > 0 {
			m.verificationTimeout = timeout
		}
	}
}

// WithMaxLeaseSeconds lowers the lease cap. Values above the WebSub limit
// are ignored, so this option can never raise MaxLeaseSeconds.
func WithMaxLeaseSeconds(seconds int) Option {
	return func(m *Manager) {
		if seconds > 0 && seconds <= MaxLeaseSeconds {
			m.maxLeaseSeconds = seconds
		}
	}
}

// WithMaxUnverifiedPerHour lowers the per-callback hourly attempt limit.
func WithMaxUnverifiedPerHour(limit int) Option {
	return func(m *Manager) {
		if limit > 0 && limit < MaxUnverifiedPerHour {
			m.maxUnverifiedPerHour = limit
		}
	}
}

func (m *Manager) finishConstruction() {
	if m.now == nil {
		m.now = time.Now
	}
	if m.random == nil {
		m.random = rand.Reader
	}
	if m.resolver == nil {
		m.resolver = net.DefaultResolver
	}
	if m.verificationTimeout <= 0 {
		m.verificationTimeout = 10 * time.Second
	}
	if m.maxLeaseSeconds <= 0 || m.maxLeaseSeconds > MaxLeaseSeconds {
		m.maxLeaseSeconds = MaxLeaseSeconds
	}
	if m.maxUnverifiedPerHour <= 0 || m.maxUnverifiedPerHour > MaxUnverifiedPerHour {
		m.maxUnverifiedPerHour = MaxUnverifiedPerHour
	}
	if m.subscriptions == nil {
		m.subscriptions = make(map[subscriptionKey]Subscription)
	}
	if m.attempts == nil {
		m.attempts = make(map[string][]time.Time)
	}
	if m.pending == nil {
		m.pending = make(map[string]pendingSubscription)
	}
	if m.client == nil {
		m.client = m.defaultHTTPClient()
	} else {
		clientCopy := *m.client
		clientCopy.CheckRedirect = m.checkRedirect
		m.client = &clientCopy
	}
}

func (m *Manager) defaultHTTPClient() *http.Client {
	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if m.allowPrivate {
			return dialer.DialContext(ctx, network, address)
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := m.lookupIPs(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if isBlockedIP(ip) {
				return nil, ErrPrivateCallback
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
	}
	return &http.Client{
		Transport:     transport,
		Timeout:       m.verificationTimeout,
		CheckRedirect: m.checkRedirect,
	}
}

func (m *Manager) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("websub: too many callback redirects")
	}
	if err := m.validateCallbackURL(req.Context(), req.URL); err != nil {
		return err
	}
	return nil
}

// ServeHTTP accepts WebSub subscription requests. It validates the request
// synchronously, returns 202, and only then starts the callback verification
// in the background.
func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.HandleSubscription(w, r)
}

// HandleSubscription is the explicit form of ServeHTTP.
func (m *Manager) HandleSubscription(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeHTTPError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	const maxFormBytes = 1 << 20
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	}

	req, mode, err := parseSubscriptionForm(r)
	if err != nil {
		writeHTTPError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.EqualFold(mode, "subscribe") {
		writeHTTPError(w, http.StatusBadRequest, ErrInvalidMode.Error())
		return
	}

	pending, err := m.prepareSubscription(r.Context(), req)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, ErrRateLimited) {
			status = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "3600")
		}
		if errors.Is(err, ErrChallengeMismatch) || errors.Is(err, ErrVerificationFailed) {
			status = http.StatusInternalServerError
		}
		writeHTTPError(w, status, err.Error())
		return
	}

	if !m.addPending(pending) {
		writeHTTPError(w, http.StatusInternalServerError, "could not allocate challenge")
		return
	}

	w.WriteHeader(http.StatusAccepted)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.removePending(pending.challenge)

		ctx, cancel := context.WithTimeout(context.Background(), m.verificationTimeout)
		defer cancel()
		if err := m.verifyCallback(ctx, pending); err != nil {
			return
		}
		m.activate(pending)
	}()
}

func parseSubscriptionForm(r *http.Request) (SubscriptionRequest, string, error) {
	if err := r.ParseForm(); err != nil {
		return SubscriptionRequest{}, "", fmt.Errorf("websub: invalid form: %w", err)
	}

	mode := strings.TrimSpace(r.Form.Get("hub.mode"))
	callback := strings.TrimSpace(r.Form.Get("hub.callback"))
	topic := strings.TrimSpace(r.Form.Get("hub.topic"))
	secret := r.Form.Get("hub.secret")
	if callback == "" {
		return SubscriptionRequest{}, mode, ErrInvalidCallback
	}
	if topic == "" {
		return SubscriptionRequest{}, mode, ErrInvalidTopic
	}

	lease := 0
	if rawLease := strings.TrimSpace(r.Form.Get("hub.lease_seconds")); rawLease != "" {
		parsed, err := strconv.Atoi(rawLease)
		if err != nil {
			return SubscriptionRequest{}, mode, ErrInvalidLease
		}
		lease = parsed
	}

	return SubscriptionRequest{
		Callback:     callback,
		Topic:        topic,
		Secret:       secret,
		LeaseSeconds: lease,
	}, mode, nil
}

func (m *Manager) prepareSubscription(ctx context.Context, req SubscriptionRequest) (pendingSubscription, error) {
	callbackURL, err := parseCallbackURL(req.Callback)
	if err != nil {
		return pendingSubscription{}, err
	}
	if err := m.validateCallbackURL(ctx, callbackURL); err != nil {
		return pendingSubscription{}, err
	}

	topic, err := parseTopicURL(req.Topic)
	if err != nil {
		return pendingSubscription{}, err
	}

	lease, err := m.effectiveLease(req.LeaseSeconds)
	if err != nil {
		return pendingSubscription{}, err
	}
	if len([]byte(req.Secret)) >= 200 {
		return pendingSubscription{}, ErrSecretTooLong
	}

	key := subscriptionKey{
		callback: canonicalCallback(callbackURL),
		topic:    topic,
	}
	if err := m.reserveAttempt(key.callback); err != nil {
		return pendingSubscription{}, err
	}

	challenge, err := m.newChallenge()
	if err != nil {
		return pendingSubscription{}, fmt.Errorf("websub: cannot generate challenge: %w", err)
	}

	return pendingSubscription{
		callback:  callbackURL,
		key:       key,
		secret:    req.Secret,
		lease:     lease,
		challenge: challenge,
	}, nil
}

func (m *Manager) effectiveLease(requested int) (int, error) {
	if requested < 0 {
		return 0, ErrInvalidLease
	}
	if requested == 0 {
		return m.maxLeaseSeconds, nil
	}
	if requested > m.maxLeaseSeconds {
		return m.maxLeaseSeconds, nil
	}
	return requested, nil
}

func (m *Manager) newChallenge() (string, error) {
	return buildChallenge(m.random)
}

func buildChallenge(random io.Reader) (string, error) {
	randomBytes := make([]byte, ChallengeBytes)
	if _, err := io.ReadFull(random, randomBytes); err != nil {
		return "", err
	}
	return hexEncode(randomBytes), nil
}

// GenerateChallenge returns a fresh challenge as the hexadecimal encoding of
// ChallengeBytes random bytes. Callers that need an injectable random source
// can use Manager.Challenge instead.
func GenerateChallenge() (string, error) {
	return buildChallenge(rand.Reader)
}

// Challenge returns a fresh challenge using the manager's random source.
func (m *Manager) Challenge() (string, error) {
	return m.newChallenge()
}

func (m *Manager) addPending(pending pendingSubscription) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.pending[pending.challenge]; exists {
		return false
	}
	m.pending[pending.challenge] = pending
	return true
}

func (m *Manager) removePending(challenge string) {
	m.mu.Lock()
	delete(m.pending, challenge)
	m.mu.Unlock()
}

// Wait blocks until all verifications started by the manager have finished.
// It is primarily useful for orderly shutdown and tests.
func (m *Manager) Wait() {
	m.wg.Wait()
}

// PendingCount reports the number of verification callbacks in flight.
func (m *Manager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// Subscribe verifies a subscription synchronously and stores it on success.
// HTTP handlers should normally use ServeHTTP, which acknowledges before
// calling the callback as WebSub requires.
func (m *Manager) Subscribe(ctx context.Context, req SubscriptionRequest) (Subscription, error) {
	pending, err := m.prepareSubscription(ctx, req)
	if err != nil {
		return Subscription{}, err
	}
	if !m.addPending(pending) {
		return Subscription{}, errors.New("websub: could not allocate challenge")
	}
	defer m.removePending(pending.challenge)

	if err := m.verifyCallback(ctx, pending); err != nil {
		return Subscription{}, err
	}
	return m.activate(pending), nil
}

// Verify is an alias for Subscribe.
func (m *Manager) Verify(ctx context.Context, req SubscriptionRequest) (Subscription, error) {
	return m.Subscribe(ctx, req)
}

func (m *Manager) verifyCallback(ctx context.Context, pending pendingSubscription) error {
	verificationURL := callbackVerificationURL(pending.callback, pending.key.topic, pending.challenge, pending.lease)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, verificationURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	req.Header.Set("User-Agent", "netsec-websub/1.0")
	req.Header.Set("Accept", "text/plain")

	resp, err := m.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: callback returned HTTP %d", ErrVerificationFailed, resp.StatusCode)
	}

	const maxVerificationBody = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxVerificationBody+1))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerificationFailed, err)
	}
	if len(body) > maxVerificationBody {
		return fmt.Errorf("%w: response body too large", ErrChallengeMismatch)
	}
	if len(body) != len(pending.challenge) || subtleCompare(body, []byte(pending.challenge)) != 1 {
		return ErrChallengeMismatch
	}
	return nil
}

func callbackVerificationURL(callback *url.URL, topic, challenge string, lease int) string {
	target := *callback
	query := target.Query()
	query.Set("hub.mode", "subscribe")
	query.Set("hub.topic", topic)
	query.Set("hub.challenge", challenge)
	query.Set("hub.lease_seconds", strconv.Itoa(lease))
	target.RawQuery = query.Encode()
	target.Fragment = ""
	return target.String()
}

func (m *Manager) activate(pending pendingSubscription) Subscription {
	now := m.now()
	sub := Subscription{
		Callback:     pending.key.callback,
		Topic:        pending.key.topic,
		Secret:       pending.secret,
		LeaseSeconds: pending.lease,
		CreatedAt:    now,
		UpdatedAt:    now,
		ExpiresAt:    now.Add(time.Duration(pending.lease) * time.Second),
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if old, exists := m.subscriptions[pending.key]; exists {
		sub.CreatedAt = old.CreatedAt
		if sub.Secret == "" {
			sub.Secret = old.Secret
		}
	}
	m.subscriptions[pending.key] = sub
	return sub
}

// Subscription returns the active subscription for a callback/topic pair.
// The callback is normalized in the same way as incoming requests.
func (m *Manager) Subscription(callback, topic string) (Subscription, bool) {
	callbackURL, err := parseCallbackURL(callback)
	if err != nil {
		return Subscription{}, false
	}
	parsedTopic, err := parseTopicURL(topic)
	if err != nil {
		return Subscription{}, false
	}
	key := subscriptionKey{
		callback: canonicalCallback(callbackURL),
		topic:    parsedTopic,
	}
	return m.getSubscription(key)
}

// GetSubscription is an alias for Subscription.
func (m *Manager) GetSubscription(callback, topic string) (Subscription, bool) {
	return m.Subscription(callback, topic)
}

func (m *Manager) getSubscription(key subscriptionKey) (Subscription, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sub, exists := m.subscriptions[key]
	if !exists {
		return Subscription{}, false
	}
	if !sub.ExpiresAt.After(m.now()) {
		delete(m.subscriptions, key)
		return Subscription{}, false
	}
	return sub, true
}

// Subscriptions returns all active subscriptions in deterministic order.
func (m *Manager) Subscriptions() []Subscription {
	now := m.now()
	m.mu.Lock()
	out := make([]Subscription, 0, len(m.subscriptions))
	for key, sub := range m.subscriptions {
		if !sub.ExpiresAt.After(now) {
			delete(m.subscriptions, key)
			continue
		}
		out = append(out, sub)
	}
	m.mu.Unlock()

	sort.Slice(out, func(i, j int) bool {
		if out[i].Callback == out[j].Callback {
			return out[i].Topic < out[j].Topic
		}
		return out[i].Callback < out[j].Callback
	})
	return out
}

// SubscriptionsForTopic returns active subscriptions for a topic.
func (m *Manager) SubscriptionsForTopic(topic string) []Subscription {
	all := m.Subscriptions()
	out := make([]Subscription, 0, len(all))
	for _, sub := range all {
		if sub.Topic == topic {
			out = append(out, sub)
		}
	}
	return out
}

// SubscriptionCount returns the number of active subscriptions.
func (m *Manager) SubscriptionCount() int {
	return len(m.Subscriptions())
}

// PurgeExpired removes expired subscriptions and attempt records older than
// one hour. It returns the number of expired subscriptions removed.
func (m *Manager) PurgeExpired() int {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()

	removed := 0
	for key, sub := range m.subscriptions {
		if !sub.ExpiresAt.After(now) {
			delete(m.subscriptions, key)
			removed++
		}
	}
	m.pruneAttemptsLocked(now)
	return removed
}

func (m *Manager) reserveAttempt(callback string) error {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()

	cutoff := now.Add(-time.Hour)
	times := m.attempts[callback]
	firstCurrent := 0
	for firstCurrent < len(times) && !times[firstCurrent].After(cutoff) {
		firstCurrent++
	}
	if firstCurrent > 0 {
		times = append([]time.Time(nil), times[firstCurrent:]...)
	}
	if len(times) >= m.maxUnverifiedPerHour {
		m.attempts[callback] = times
		return ErrRateLimited
	}
	m.attempts[callback] = append(times, now)
	return nil
}

func (m *Manager) pruneAttemptsLocked(now time.Time) {
	cutoff := now.Add(-time.Hour)
	for callback, times := range m.attempts {
		firstCurrent := 0
		for firstCurrent < len(times) && !times[firstCurrent].After(cutoff) {
			firstCurrent++
		}
		if firstCurrent == len(times) {
			delete(m.attempts, callback)
			continue
		}
		if firstCurrent > 0 {
			m.attempts[callback] = append([]time.Time(nil), times[firstCurrent:]...)
		}
	}
}

func parseCallbackURL(raw string) (*url.URL, error) {
	callbackURL, err := url.Parse(raw)
	if err != nil || !callbackURL.IsAbs() || callbackURL.Host == "" {
		return nil, ErrInvalidCallback
	}
	if !strings.EqualFold(callbackURL.Scheme, "https") {
		return nil, ErrInsecureCallback
	}
	if callbackURL.User != nil {
		return nil, ErrInvalidCallback
	}
	return callbackURL, nil
}

func validateTopic(raw string) (string, error) {
	topic, err := url.Parse(raw)
	if err != nil || !topic.IsAbs() || topic.Host == "" {
		return "", ErrInvalidTopic
	}
	if !strings.EqualFold(topic.Scheme, "http") && !strings.EqualFold(topic.Scheme, "https") {
		return "", ErrInvalidTopic
	}
	return topic.String(), nil
}

func parseTopicURL(raw string) (string, error) {
	return validateTopic(raw)
}

func canonicalCallback(callbackURL *url.URL) string {
	canonical := *callbackURL
	canonical.Scheme = strings.ToLower(canonical.Scheme)
	canonical.Host = strings.ToLower(canonical.Host)
	canonical.Fragment = ""
	if (canonical.Scheme == "https" && canonical.Port() == "443") ||
		(canonical.Scheme == "http" && canonical.Port() == "80") {
		hostname := canonical.Hostname()
		if strings.Contains(hostname, ":") {
			hostname = "[" + hostname + "]"
		}
		canonical.Host = hostname
	}
	return canonical.String()
}

// ValidateCallback checks that a callback is an HTTPS URL resolving only to
// public addresses. A Manager configured with WithAllowPrivateCallbacks skips
// the address check.
func (m *Manager) ValidateCallback(ctx context.Context, raw string) error {
	callbackURL, err := parseCallbackURL(raw)
	if err != nil {
		return err
	}
	return m.validateCallbackURL(ctx, callbackURL)
}

func (m *Manager) validateCallbackURL(ctx context.Context, callbackURL *url.URL) error {
	if callbackURL == nil || !strings.EqualFold(callbackURL.Scheme, "https") || callbackURL.Host == "" {
		return ErrInsecureCallback
	}
	if callbackURL.User != nil {
		return ErrInvalidCallback
	}
	if m.allowPrivate {
		return nil
	}
	ips, err := m.lookupIPs(ctx, callbackURL.Hostname())
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPrivateCallback, err)
	}
	if len(ips) == 0 {
		return ErrPrivateCallback
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return ErrPrivateCallback
		}
	}
	return nil
}

func (m *Manager) lookupIPs(ctx context.Context, host string) ([]net.IP, error) {
	if parsed := net.ParseIP(host); parsed != nil {
		return []net.IP{parsed}, nil
	}
	addrs, err := m.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, addr := range addrs {
		if addr.IP != nil {
			ips = append(ips, addr.IP)
		}
	}
	return ips, nil
}

func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || !ip.IsGlobalUnicast() {
		return true
	}
	if ip4 := ip.To4(); ip4 != nil {
		// RFC 6598 shared address space, plus non-routable documentation
		// and benchmarking ranges.
		if ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0 {
			return true
		}
		if ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2 {
			return true
		}
		if ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19) {
			return true
		}
		if ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100 {
			return true
		}
		if ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113 {
			return true
		}
	}
	return false
}

func writeHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, message+"\n")
}
