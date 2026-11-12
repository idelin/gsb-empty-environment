package netsec

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

const (
	defaultTimeout          = 5 * time.Second
	defaultMaxResponseBytes = int64(1 << 20)
)

type selectorConfig struct {
	logger           *slog.Logger
	dialer           Dialer
	timeout          time.Duration
	maxResponseBytes int64
	now              func() time.Time
	onDegrade        func(Degradation)
}

// Option configures a Selector.
type Option func(*selectorConfig)

// WithLogger sets the logger used for degradation records. The default is
// slog.Default().
func WithLogger(logger *slog.Logger) Option {
	return func(config *selectorConfig) {
		config.logger = logger
	}
}

// WithDialer injects the dialer used for TCP, TLS, and UDP connections.
func WithDialer(dialer Dialer) Option {
	return func(config *selectorConfig) {
		config.dialer = dialer
	}
}

// WithTimeout sets the default per-upstream timeout.
func WithTimeout(timeout time.Duration) Option {
	return func(config *selectorConfig) {
		config.timeout = timeout
	}
}

// WithMaxResponseBytes limits the size of an accepted DNS response.
func WithMaxResponseBytes(limit int64) Option {
	return func(config *selectorConfig) {
		config.maxResponseBytes = limit
	}
}

// WithClock injects the clock used for degradation timestamps.
func WithClock(now func() time.Time) Option {
	return func(config *selectorConfig) {
		config.now = now
	}
}

// WithDegradationHook registers a callback invoked for every opportunistic
// plaintext fallback.
func WithDegradationHook(hook func(Degradation)) Option {
	return func(config *selectorConfig) {
		config.onDegrade = hook
	}
}

// Selector dispatches DNS queries according to a strict or opportunistic
// policy.
type Selector struct {
	mode             Mode
	upstreams        []preparedUpstream
	clients          []*http.Client
	logger           *slog.Logger
	dialer           Dialer
	now              func() time.Time
	onDegrade        func(Degradation)
	maxResponseBytes int64
}

// Resolver is an alias for Selector.
type Resolver = Selector

// NewSelector validates upstream configuration and builds a query dispatcher.
func NewSelector(mode Mode, upstreams []Upstream, options ...Option) (*Selector, error) {
	if mode != Strict && mode != Opportunistic {
		return nil, fmt.Errorf("netsec: invalid mode %d", int(mode))
	}
	if len(upstreams) == 0 {
		return nil, ErrNoUpstreams
	}
	config := selectorConfig{
		logger:           slog.Default(),
		dialer:           &net.Dialer{},
		timeout:          defaultTimeout,
		maxResponseBytes: defaultMaxResponseBytes,
		now:              time.Now,
	}
	for _, option := range options {
		option(&config)
	}
	if config.logger == nil {
		config.logger = slog.Default()
	}
	if config.dialer == nil {
		config.dialer = &net.Dialer{}
	}
	if config.timeout <= 0 {
		config.timeout = defaultTimeout
	}
	if config.maxResponseBytes <= 0 {
		config.maxResponseBytes = defaultMaxResponseBytes
	}
	if config.now == nil {
		config.now = time.Now
	}

	selector := &Selector{
		mode:             mode,
		upstreams:        make([]preparedUpstream, len(upstreams)),
		clients:          make([]*http.Client, len(upstreams)),
		logger:           config.logger,
		dialer:           config.dialer,
		now:              config.now,
		onDegrade:        config.onDegrade,
		maxResponseBytes: config.maxResponseBytes,
	}
	for i, upstream := range upstreams {
		prepared, err := prepareUpstream(mode, upstream, config.timeout)
		if err != nil {
			return nil, err
		}
		prepared.index = i
		selector.upstreams[i] = prepared
	}
	for i := range selector.upstreams {
		if selector.upstreams[i].upstream.Protocol == DoH {
			selector.clients[i] = newDoHClient(&selector.upstreams[i], selector.dialer)
		}
	}
	return selector, nil
}

// NewResolver is an alias for NewSelector.
func NewResolver(mode Mode, upstreams []Upstream, options ...Option) (*Selector, error) {
	return NewSelector(mode, upstreams, options...)
}

// Mode returns the selector's policy.
func (s *Selector) Mode() Mode {
	return s.mode
}

// Upstreams returns a copy of the configured upstreams.
func (s *Selector) Upstreams() []Upstream {
	upstreams := make([]Upstream, len(s.upstreams))
	for i := range s.upstreams {
		upstreams[i] = s.upstreams[i].upstream
		upstreams[i].Bootstrap = append([]string(nil), upstreams[i].Bootstrap...)
		upstreams[i].SPKIPins = append([]string(nil), upstreams[i].SPKIPins...)
	}
	return upstreams
}

// Exchange returns the DNS response message for query.
func (s *Selector) Exchange(ctx context.Context, query []byte) ([]byte, error) {
	result, err := s.Resolve(ctx, query)
	if err != nil {
		return nil, err
	}
	return result.Message, nil
}

// Query is an alias for Exchange.
func (s *Selector) Query(ctx context.Context, query []byte) ([]byte, error) {
	return s.Exchange(ctx, query)
}

// ResolveQuestion packages a question and dispatches it.
func (s *Selector) ResolveQuestion(ctx context.Context, question Question) (*Result, error) {
	query, err := PackQuery(question)
	if err != nil {
		return nil, err
	}
	return s.Resolve(ctx, query)
}

// Resolve dispatches query and returns the response plus fallback metadata.
func (s *Selector) Resolve(ctx context.Context, query []byte) (*Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := parseMessage(query); err != nil {
		return nil, fmt.Errorf("netsec: invalid query: %w", err)
	}

	attempts := make([]Attempt, 0, len(s.upstreams))
	var secureFailures []error
	secureAvailable := false
	for i := range s.upstreams {
		upstream := &s.upstreams[i]
		if !upstream.upstream.Protocol.secure() {
			continue
		}
		secureAvailable = true
		started := time.Now()
		message, err := s.exchangeOne(ctx, upstream, query)
		attempt := Attempt{
			Upstream: upstream.upstream.Name,
			Protocol: upstream.upstream.Protocol,
			Duration: time.Since(started),
			Err:      err,
		}
		attempts = append(attempts, attempt)
		if err == nil {
			return &Result{
				Message:  message,
				Upstream: upstream.upstream,
				Protocol: upstream.upstream.Protocol,
				Attempts: attempts,
			}, nil
		}
		secureFailures = append(secureFailures, err)
		if ctx.Err() != nil {
			return nil, &ExchangeError{Mode: s.mode, Attempts: attempts}
		}
	}

	if s.mode == Strict {
		if !secureAvailable {
			return nil, fmt.Errorf("%w: %w", ErrNoSecureUpstream, ErrStrictNoFallback)
		}
		return nil, &ExchangeError{Mode: Strict, Attempts: attempts}
	}

	plain := s.plainUpstreams()
	if len(plain) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrNoPlainFallback, &ExchangeError{
			Mode:     Opportunistic,
			Attempts: attempts,
		})
	}
	reason := errors.Join(secureFailures...)
	for _, upstream := range plain {
		started := time.Now()
		message, err := s.exchangeOne(ctx, upstream, query)
		attempts = append(attempts, Attempt{
			Upstream: upstream.upstream.Name,
			Protocol: upstream.upstream.Protocol,
			Duration: time.Since(started),
			Err:      err,
		})
		if err != nil {
			reason = errors.Join(reason, err)
			if ctx.Err() != nil {
				return nil, &ExchangeError{Mode: Opportunistic, Attempts: attempts}
			}
			continue
		}
		degradation := Degradation{
			From:     lastSecureName(attempts),
			To:       upstream.upstream.Name,
			Protocol: PlainDNS,
			Reason:   reason,
			At:       s.now(),
			Attempts: append([]Attempt(nil), attempts...),
		}
		s.recordDegradation(ctx, degradation)
		return &Result{
			Message:     message,
			Upstream:    upstream.upstream,
			Protocol:    upstream.upstream.Protocol,
			Degraded:    true,
			Degradation: &degradation,
			Attempts:    attempts,
		}, nil
	}
	return nil, &ExchangeError{Mode: Opportunistic, Attempts: attempts}
}

func (s *Selector) exchangeOne(ctx context.Context, upstream *preparedUpstream, query []byte) ([]byte, error) {
	exchangeCtx, cancel := context.WithTimeout(ctx, upstream.timeout)
	defer cancel()
	var message []byte
	var err error
	switch upstream.upstream.Protocol {
	case DoH:
		message, err = s.exchangeDoH(exchangeCtx, upstream, query)
	case DoT:
		message, err = s.exchangeDoT(exchangeCtx, upstream, query)
	case PlainDNS:
		message, err = s.exchangePlain(exchangeCtx, upstream, query)
	default:
		err = fmt.Errorf("netsec: unsupported protocol %q", upstream.upstream.Protocol)
	}
	if err != nil {
		return nil, fmt.Errorf("upstream %q: %w", upstream.upstream.Name, err)
	}
	return message, nil
}

func (s *Selector) plainUpstreams() []*preparedUpstream {
	upstreams := make([]*preparedUpstream, 0, len(s.upstreams))
	for i := range s.upstreams {
		if s.upstreams[i].upstream.Protocol == PlainDNS {
			upstreams = append(upstreams, &s.upstreams[i])
		}
	}
	return upstreams
}

func (s *Selector) recordDegradation(ctx context.Context, degradation Degradation) {
	s.logger.WarnContext(ctx, "DNS resolver degraded to plaintext",
		"from", degradation.From,
		"to", degradation.To,
		"reason", degradation.Reason,
	)
	if s.onDegrade != nil {
		s.onDegrade(degradation)
	}
}

func lastSecureName(attempts []Attempt) string {
	for i := len(attempts) - 1; i >= 0; i-- {
		if attempts[i].Protocol.secure() {
			return attempts[i].Upstream
		}
	}
	return ""
}

// CloseIdleConnections closes idle DoH connections.
func (s *Selector) CloseIdleConnections() {
	for _, client := range s.clients {
		if client == nil {
			continue
		}
		if transport, ok := client.Transport.(*http.Transport); ok {
			transport.CloseIdleConnections()
		}
	}
}
