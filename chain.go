package netsec

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// Middleware is a standard HTTP middleware constructor.
type Middleware func(http.Handler) http.Handler

// Identity is the authenticated identity that is injected into the upstream
// request. It must only come from an Authenticator, never from an inbound
// request's X-Auth-* headers.
type Identity struct {
	User   string
	Groups []string
}

func (id Identity) clone() Identity {
	id.Groups = append([]string(nil), id.Groups...)
	return id
}

// Authenticator authenticates a sanitized inbound request. A non-nil error
// rejects the request with the configured unauthorized handler.
type Authenticator func(*http.Request) (Identity, error)

// AuthenticateFunc is an alias for Authenticator.
type AuthenticateFunc = Authenticator

// Config configures a Chain. Middleware is appended after the identity
// injection stage; it cannot be used to run code before sanitization or
// authentication.
type Config struct {
	Authenticator       Authenticator
	Forward             http.Handler
	Middleware          []Middleware
	UnauthorizedHandler http.Handler
}

// Chain is an immutable, fixed-order middleware chain.
type Chain struct {
	handler http.Handler
}

// Option configures a Chain while it is being constructed.
type Option interface {
	apply(*chainOptions) error
}

type optionFunc func(*chainOptions) error

func (f optionFunc) apply(options *chainOptions) error {
	return f(options)
}

type chainOptions struct {
	middleware          []Middleware
	unauthorizedHandler http.Handler
}

// WithMiddleware appends custom middleware after identity injection. The
// middleware wrappers are applied in the order supplied.
func WithMiddleware(middleware ...Middleware) Option {
	return optionFunc(func(options *chainOptions) error {
		for _, item := range middleware {
			if item == nil {
				return errors.New("netsec: nil middleware")
			}
			options.middleware = append(options.middleware, item)
		}
		return nil
	})
}

// WithMiddlewares is an alias for WithMiddleware.
func WithMiddlewares(middleware ...Middleware) Option {
	return WithMiddleware(middleware...)
}

// WithUnauthorizedHandler replaces the default 401 response used when
// authentication fails.
func WithUnauthorizedHandler(handler http.Handler) Option {
	return optionFunc(func(options *chainOptions) error {
		if handler == nil {
			return errors.New("netsec: nil unauthorized handler")
		}
		options.unauthorizedHandler = handler
		return nil
	})
}

// WithUnauthorized is an alias for WithUnauthorizedHandler.
func WithUnauthorized(handler http.Handler) Option {
	return WithUnauthorizedHandler(handler)
}

// NewChain builds a chain with the fixed order
// sanitize -> authenticate -> inject -> custom middleware -> forward.
//
// The chain cannot be reordered or extended after construction.
func NewChain(authenticator Authenticator, forward http.Handler, options ...Option) (*Chain, error) {
	return newChain(authenticator, forward, chainOptions{}, options)
}

// NewChainFromConfig is the Config-based constructor.
func NewChainFromConfig(config Config) (*Chain, error) {
	options := chainOptions{
		middleware:          append([]Middleware(nil), config.Middleware...),
		unauthorizedHandler: config.UnauthorizedHandler,
	}
	return newChain(config.Authenticator, config.Forward, options, nil)
}

func newChain(authenticator Authenticator, forward http.Handler, base chainOptions, options []Option) (*Chain, error) {
	if authenticator == nil {
		return nil, errors.New("netsec: nil authenticator")
	}
	if forward == nil {
		return nil, errors.New("netsec: nil forward handler")
	}

	config := chainOptions{
		middleware:          append([]Middleware(nil), base.middleware...),
		unauthorizedHandler: base.unauthorizedHandler,
	}
	for _, option := range options {
		if option == nil {
			return nil, errors.New("netsec: nil option")
		}
		if err := option.apply(&config); err != nil {
			return nil, err
		}
	}
	for _, item := range config.middleware {
		if item == nil {
			return nil, errors.New("netsec: nil middleware")
		}
	}
	if config.unauthorizedHandler == nil {
		config.unauthorizedHandler = http.HandlerFunc(defaultUnauthorized)
	}

	chain := &Chain{}
	chain.handler = chain.build(authenticator, forward, config)
	return chain, nil
}

// New is an alias for NewChainFromConfig.
func New(config Config) (*Chain, error) {
	return NewChainFromConfig(config)
}

// MustNewChain is like NewChain but panics on construction errors.
func MustNewChain(authenticator Authenticator, forward http.Handler, options ...Option) *Chain {
	chain, err := NewChain(authenticator, forward, options...)
	if err != nil {
		panic(err)
	}
	return chain
}

// ServeHTTP implements http.Handler.
func (chain *Chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if chain == nil || chain.handler == nil {
		http.Error(w, "netsec: uninitialized chain", http.StatusInternalServerError)
		return
	}
	chain.handler.ServeHTTP(w, r)
}

// Handler returns the chain as an http.Handler.
func (chain *Chain) Handler() http.Handler {
	return chain
}

func (chain *Chain) build(authenticator Authenticator, forward http.Handler, options chainOptions) http.Handler {
	postInjection := forward
	for i := len(options.middleware) - 1; i >= 0; i-- {
		postInjection = options.middleware[i](postInjection)
	}

	inject := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := identityFromContext(r.Context())
		if !ok {
			http.Error(w, "netsec: missing authenticated identity", http.StatusInternalServerError)
			return
		}
		if !validIdentity(identity) {
			http.Error(w, "netsec: invalid authenticated identity", http.StatusInternalServerError)
			return
		}
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		injectIdentity(r.Header, identity)
		postInjection.ServeHTTP(w, r)
	})

	authenticate := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := authenticator(r)
		if err != nil {
			deleteIdentityHeaders(w.Header())
			options.unauthorizedHandler.ServeHTTP(identityScrubbingWriter{ResponseWriter: w}, r)
			deleteIdentityHeaders(w.Header())
			return
		}
		identity = identity.clone()
		ctx := context.WithValue(r.Context(), identityContextKey{}, identity)
		inject.ServeHTTP(w, r.WithContext(ctx))
	})

	return sanitizeMiddleware(authenticate)
}

func defaultUnauthorized(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// identityScrubbingWriter prevents an unauthorized handler from emitting
// response identity headers on an authentication failure.
type identityScrubbingWriter struct {
	http.ResponseWriter
}

func (writer identityScrubbingWriter) WriteHeader(statusCode int) {
	deleteIdentityHeaders(writer.Header())
	writer.ResponseWriter.WriteHeader(statusCode)
}

func (writer identityScrubbingWriter) Write(body []byte) (int, error) {
	deleteIdentityHeaders(writer.Header())
	return writer.ResponseWriter.Write(body)
}

// Unwrap supports http.ResponseController.
func (writer identityScrubbingWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

type identityContextKey struct{}

// WithIdentity adds an authenticated identity to a context. It is exported so
// custom middleware placed after injection can read the same trusted value
// without parsing headers.
func WithIdentity(ctx context.Context, identity Identity) context.Context {
	return context.WithValue(ctx, identityContextKey{}, identity.clone())
}

// IdentityFromContext returns the authenticated identity stored in ctx.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	return identityFromContext(ctx)
}

func identityFromContext(ctx context.Context) (Identity, bool) {
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	return identity.clone(), true
}

func validIdentity(identity Identity) bool {
	if !validHeaderValue(identity.User) {
		return false
	}
	for _, group := range identity.Groups {
		if !validHeaderValue(group) {
			return false
		}
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b == '\t' {
			continue
		}
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}

func injectIdentity(header http.Header, identity Identity) {
	deleteIdentityHeaders(header)
	header.Set(HeaderAuthUser, identity.User)
	header.Set(HeaderAuthGroups, strings.Join(identity.Groups, ","))
}

func deleteIdentityHeaders(header http.Header) {
	for name := range header {
		if isIdentityHeader(name) {
			delete(header, name)
		}
	}
}

func isIdentityHeader(name string) bool {
	name = strings.TrimSpace(name)
	return strings.EqualFold(name, HeaderAuthUser) || strings.EqualFold(name, HeaderAuthGroups)
}
