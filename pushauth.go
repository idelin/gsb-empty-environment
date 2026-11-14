package netsec

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

var (
	ErrCrossOrigin      = errors.New("pushauth: push target origin differs from request origin")
	ErrCrossTenant      = errors.New("pushauth: push target tenant differs from request tenant")
	ErrPrivateResource  = errors.New("pushauth: resource carries a user-private marker")
	ErrCredentialBound  = errors.New("pushauth: resource representation is bound to a credential")
	ErrCrossContext     = errors.New("pushauth: promise context does not match receiver context")
	ErrCacheKeyMismatch = errors.New("pushauth: promise cache key does not match recomputed key")
)

// negotiatedHeaders are the request headers that shape a representation and
// therefore must be part of the resource key.
var negotiatedHeaders = []string{"Accept", "Accept-Encoding", "Accept-Language"}

// RequestContext binds a push decision to the session and credential context
// of the request that triggered it.
type RequestContext struct {
	Origin     string      // scheme://host[:port] of the triggering request
	Tenant     string      // tenant the request is authenticated against
	Credential string      // fingerprint of the caller credential, "" for anonymous
	Negotiated http.Header // content-negotiation headers of the request
}

// ResourceKey uniquely identifies a pushed representation inside a session
// context. It covers the negotiated headers and the credential context so a
// promise issued for one session can never be confused with another.
type ResourceKey struct {
	Origin         string
	Tenant         string
	Credential     string
	NegotiatedHash string
	Path           string
}

func (k ResourceKey) String() string {
	return strings.Join([]string{k.Origin, k.Tenant, k.Credential, k.NegotiatedHash, k.Path}, "|")
}

func hashNegotiated(h http.Header) string {
	var parts []string
	for _, name := range negotiatedHeaders {
		vals := append([]string(nil), h.Values(name)...)
		sort.Strings(vals)
		parts = append(parts, http.CanonicalHeaderKey(name)+"="+strings.Join(vals, ","))
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:16])
}

// Key derives the resource key for path within this context.
func (c RequestContext) Key(path string) ResourceKey {
	return ResourceKey{
		Origin:         c.Origin,
		Tenant:         c.Tenant,
		Credential:     c.Credential,
		NegotiatedHash: hashNegotiated(c.Negotiated),
		Path:           path,
	}
}

// PushItem is a candidate resource the gateway considers pushing.
type PushItem struct {
	URL             string // absolute URL or same-origin path
	Tenant          string
	Private         bool // user-private marker set by the origin
	CredentialBound bool // representation varies per credential
	Size            int64
	Header          http.Header
}

// PushPromise is an authorized push, keyed to the issuing request context.
type PushPromise struct {
	Key    ResourceKey
	Path   string
	Header http.Header
	Size   int64
}

// Authorizer decides which candidates may be pushed for a request. The zero
// value applies conservative defaults.
type Authorizer struct {
	MaxPushes    int   // maximum promises per request
	MaxPushBytes int64 // maximum cumulative pushed bytes per request
}

const (
	defaultMaxPushes    = 8
	defaultMaxPushBytes = 1 << 20
)

func (a Authorizer) limits() (int, int64) {
	maxPushes := a.MaxPushes
	if maxPushes <= 0 {
		maxPushes = defaultMaxPushes
	}
	maxBytes := a.MaxPushBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxPushBytes
	}
	return maxPushes, maxBytes
}

// Authorize filters candidates down to promises that share the request's
// origin, tenant and credential context, and that fit within the push budget.
// Rejected candidates are reported through errs; the main response is never
// affected by rejections.
func (a Authorizer) Authorize(ctx RequestContext, items []PushItem) ([]PushPromise, []error) {
	maxPushes, maxBytes := a.limits()
	var promises []PushPromise
	var errs []error
	var spent int64

	for _, item := range items {
		path, err := a.check(ctx, item)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if len(promises) >= maxPushes || spent+item.Size > maxBytes {
			errs = append(errs, fmt.Errorf("pushauth: %q skipped: push budget exhausted", item.URL))
			continue
		}
		spent += item.Size
		promises = append(promises, PushPromise{
			Key:    ctx.Key(path),
			Path:   path,
			Header: item.Header,
			Size:   item.Size,
		})
	}
	return promises, errs
}

func (a Authorizer) check(ctx RequestContext, item PushItem) (string, error) {
	u, err := url.Parse(item.URL)
	if err != nil {
		return "", fmt.Errorf("pushauth: %q: %w", item.URL, err)
	}
	if u.IsAbs() {
		origin := u.Scheme + "://" + u.Host
		if !strings.EqualFold(origin, ctx.Origin) {
			return "", fmt.Errorf("pushauth: %q: %w", item.URL, ErrCrossOrigin)
		}
	}
	if item.Tenant != ctx.Tenant {
		return "", fmt.Errorf("pushauth: %q: %w", item.URL, ErrCrossTenant)
	}
	if item.Private {
		return "", fmt.Errorf("pushauth: %q: %w", item.URL, ErrPrivateResource)
	}
	if item.CredentialBound {
		return "", fmt.Errorf("pushauth: %q: %w", item.URL, ErrCredentialBound)
	}
	path := u.EscapedPath()
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path, nil
}

// Push emits promises through w's http.Pusher. Any push failure is swallowed
// so the main response is never disturbed.
func Push(w http.ResponseWriter, promises []PushPromise) {
	pusher, ok := w.(http.Pusher)
	if !ok {
		return
	}
	for _, promise := range promises {
		_ = pusher.Push(promise.Path, &http.PushOptions{Header: promise.Header})
	}
}

// Receiver validates incoming promises on the receiving side of a connection.
// Promises issued for a different origin, tenant, credential or negotiation
// context are rejected; promises whose key does not match the recomputed key
// are discarded instead of being cached.
type Receiver struct {
	ctx RequestContext
}

func NewReceiver(ctx RequestContext) *Receiver {
	return &Receiver{ctx: ctx}
}

// Admit reports whether an incoming promise belongs to this receiver's
// session context.
func (r *Receiver) Admit(promise PushPromise) error {
	expected := r.ctx.Key(promise.Path)
	if promise.Key.Origin != expected.Origin ||
		promise.Key.Tenant != expected.Tenant ||
		promise.Key.Credential != expected.Credential ||
		promise.Key.NegotiatedHash != expected.NegotiatedHash {
		return fmt.Errorf("pushauth: %q: %w", promise.Path, ErrCrossContext)
	}
	return nil
}

// Accept admits a promise and verifies its cache key against the key
// recomputed from the receiver's own context. A promise that passes Admit but
// carries a stale or foreign cache key is discarded.
func (r *Receiver) Accept(promise PushPromise) (ResourceKey, error) {
	if err := r.Admit(promise); err != nil {
		return ResourceKey{}, err
	}
	expected := r.ctx.Key(promise.Path)
	if promise.Key != expected {
		return ResourceKey{}, fmt.Errorf("pushauth: %q: %w", promise.Path, ErrCacheKeyMismatch)
	}
	return expected, nil
}
