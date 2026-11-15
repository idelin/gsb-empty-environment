// Package netsec builds a fixed-order reverse proxy middleware chain:
// cleanup, authentication, identity injection, and forwarding.
//
// Cleanup is deliberately outermost. It removes hop-by-hop headers and any
// client-supplied identity headers before authentication. A request whose
// Connection header names an internal identity header is rejected with 400.
// Custom middleware can only be placed after identity injection.
package netsec

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

const (
	// HeaderAuthUser is the internal header used to pass the authenticated user.
	HeaderAuthUser = "X-Auth-User"
	// HeaderAuthGroups is the internal header used to pass authenticated groups.
	HeaderAuthGroups = "X-Auth-Groups"
)

var (
	// ErrNilAuthenticator is returned when a chain is created without an
	// authenticator.
	ErrNilAuthenticator = errors.New("netsec: authenticator is nil")
	// ErrNilNextHandler is returned when a chain is created without a handler to
	// forward to.
	ErrNilNextHandler = errors.New("netsec: next handler is nil")
	// ErrNilMiddleware is returned when a custom middleware is nil.
	ErrNilMiddleware = errors.New("netsec: middleware is nil")
	// ErrInvalidMiddlewarePosition is returned for any custom middleware position
	// other than PositionAfterInjection.
	ErrInvalidMiddlewarePosition = errors.New("netsec: custom middleware must be placed after identity injection")
)

// Middleware is the standard net/http middleware shape.
type Middleware func(http.Handler) http.Handler

// Identity is the authenticated identity injected into the forwarded request.
type Identity struct {
	User   string
	Groups []string
}

// Authenticator authenticates a cleaned request. A non-nil error rejects the
// request. The returned identity is only injected when authentication succeeds.
type Authenticator func(*http.Request) (Identity, error)

// Position identifies a point in the fixed middleware chain.
type Position int

const (
	// PositionBeforeCleanup is intentionally invalid for custom middleware.
	PositionBeforeCleanup Position = iota
	// PositionAfterCleanup is intentionally invalid for custom middleware.
	PositionAfterCleanup
	// PositionAfterAuthentication is intentionally invalid for custom middleware.
	PositionAfterAuthentication
	// PositionAfterInjection is the only valid position for custom middleware.
	PositionAfterInjection
)

func (p Position) String() string {
	switch p {
	case PositionBeforeCleanup:
		return "before cleanup"
	case PositionAfterCleanup:
		return "after cleanup"
	case PositionAfterAuthentication:
		return "after authentication"
	case PositionAfterInjection:
		return "after identity injection"
	default:
		return fmt.Sprintf("unknown position %d", int(p))
	}
}

// MiddlewareSpec places caller-provided middleware in the chain. NewChain
// rejects every position except PositionAfterInjection.
type MiddlewareSpec struct {
	Position   Position
	Middleware Middleware
}

// Chain is an immutable middleware chain. Its stage order cannot be changed
// after construction.
type Chain struct {
	handler http.Handler
}

// AuthError lets an authenticator choose a rejection status. Statuses outside
// the 4xx/5xx range fall back to 401 Unauthorized.
type AuthError struct {
	StatusCode int
	Err        error
}

func (e *AuthError) Error() string {
	if e == nil {
		return "authentication failed"
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return http.StatusText(e.StatusCode)
}

// Unwrap returns the underlying authentication error.
func (e *AuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// New creates a chain with custom middleware appended after identity injection.
// Custom middleware are invoked in the order supplied.
func New(authenticate Authenticator, next http.Handler, middleware ...Middleware) (*Chain, error) {
	specs := make([]MiddlewareSpec, 0, len(middleware))
	for _, mw := range middleware {
		specs = append(specs, MiddlewareSpec{
			Position:   PositionAfterInjection,
			Middleware: mw,
		})
	}
	return NewChain(authenticate, next, specs...)
}

// NewChain creates an immutable cleanup -> authenticate -> inject -> forward
// chain. Every custom middleware must declare PositionAfterInjection; any other
// position is rejected before the chain is constructed.
func NewChain(authenticate Authenticator, next http.Handler, middleware ...MiddlewareSpec) (*Chain, error) {
	if authenticate == nil {
		return nil, ErrNilAuthenticator
	}
	if next == nil {
		return nil, ErrNilNextHandler
	}
	for i, spec := range middleware {
		if spec.Position != PositionAfterInjection {
			return nil, fmt.Errorf("%w: middleware %d is %s", ErrInvalidMiddlewarePosition, i, spec.Position)
		}
		if spec.Middleware == nil {
			return nil, fmt.Errorf("%w: middleware %d", ErrNilMiddleware, i)
		}
	}

	// Build from the inside out. Custom middleware only ever wraps the forward
	// handler, so it cannot run before authentication or injection.
	handler := next
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i].Middleware(handler)
	}
	handler = injectIdentity(handler)
	handler = authenticateRequest(authenticate, handler)
	handler = cleanRequest(handler)

	return &Chain{handler: handler}, nil
}

// ServeHTTP implements http.Handler.
func (c *Chain) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.handler.ServeHTTP(w, r)
}

// Handler returns the immutable terminal handler.
func (c *Chain) Handler() http.Handler {
	if c == nil {
		return nil
	}
	return c.handler
}

type identityContextKey struct{}

// IdentityFromContext returns the authenticated identity for a request that has
// reached or passed the injection stage.
func IdentityFromContext(ctx context.Context) (Identity, bool) {
	if ctx == nil {
		return Identity{}, false
	}
	identity, ok := ctx.Value(identityContextKey{}).(Identity)
	if !ok {
		return Identity{}, false
	}
	return cloneIdentity(identity), true
}

func cloneIdentity(identity Identity) Identity {
	identity.Groups = append([]string(nil), identity.Groups...)
	return identity
}

func authenticateRequest(authenticate Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, err := authenticate(r)
		if err != nil {
			writeAuthFailure(w, err)
			return
		}

		ctx := context.WithValue(r.Context(), identityContextKey{}, cloneIdentity(identity))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func writeAuthFailure(w http.ResponseWriter, err error) {
	status := http.StatusUnauthorized
	var authErr *AuthError
	if errors.As(err, &authErr) && authErr.StatusCode >= 400 && authErr.StatusCode <= 599 {
		status = authErr.StatusCode
	}

	clearIdentityHeaders(w.Header())
	http.Error(w, http.StatusText(status), status)
}

func injectIdentity(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity, ok := IdentityFromContext(r.Context())
		if !ok {
			clearIdentityHeaders(w.Header())
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}

		request := r.Clone(r.Context())
		request.Header = r.Header.Clone()
		if request.Header == nil {
			request.Header = make(http.Header)
		}

		// Cleanup already removed client values. Delete again immediately before
		// setting so injection can never coexist with a duplicate field value.
		deleteHeader(request.Header, HeaderAuthUser)
		deleteHeader(request.Header, HeaderAuthGroups)
		request.Header.Set(HeaderAuthUser, identity.User)
		if len(identity.Groups) == 0 {
			deleteHeader(request.Header, HeaderAuthGroups)
		} else {
			request.Header.Set(HeaderAuthGroups, strings.Join(identity.Groups, ","))
		}

		next.ServeHTTP(w, request)
	})
}

func clearIdentityHeaders(header http.Header) {
	deleteHeader(header, HeaderAuthUser)
	deleteHeader(header, HeaderAuthGroups)
}
