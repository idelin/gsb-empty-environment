package netsec

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// HeaderTraceParent is the W3C traceparent header.
	HeaderTraceParent = "traceparent"
	// HeaderTraceState is the W3C tracestate header.
	HeaderTraceState = "tracestate"
	// HeaderTenant carries the authenticated tenant identity bound to the
	// signed trace context.
	HeaderTenant = "X-Trace-Tenant"
	// HeaderOrigin carries the authenticated origin identity bound to the
	// signed trace context.
	HeaderOrigin = "X-Trace-Origin"
	// HeaderTimestamp carries the Unix timestamp (seconds) covered by the
	// signature.
	HeaderTimestamp = "X-Trace-Timestamp"
	// HeaderSignature carries the versioned HMAC over the complete context.
	HeaderSignature = "X-Trace-Signature"

	// Aliases using the TraceParentHeader naming convention.
	TraceParentHeader = HeaderTraceParent
	TraceStateHeader  = HeaderTraceState
	TenantHeader      = HeaderTenant
	OriginHeader      = HeaderOrigin
	TimestampHeader   = HeaderTimestamp
	SignatureHeader   = HeaderSignature
)

const (
	// MaxTraceParentBytes is the maximum accepted traceparent header size.
	// W3C version 00 is exactly 55 bytes; the extra room is for strict
	// rejection without unbounded parsing.
	MaxTraceParentBytes = 128
	// MaxTraceStateBytes is the W3C tracestate size limit.
	MaxTraceStateBytes = 512
	// MaxBindingBytes is the maximum tenant or origin identity length.
	MaxBindingBytes = 128
	// MaxTimestampBytes is the maximum timestamp field length.
	MaxTimestampBytes = 20
	// MaxSignatureBytes is the maximum signature field length.
	MaxSignatureBytes = 128
	// MaxTracingHeaderBytes is the default aggregate size limit for all
	// tracing headers.
	MaxTracingHeaderBytes = 2048
	// MaxTraceStateMembers is the W3C tracestate member limit.
	MaxTraceStateMembers = 32
	// MinSigningKeyBytes is the minimum HMAC key size accepted by New.
	MinSigningKeyBytes = 32
)

const (
	signatureVersion  = "v1"
	defaultKeyID      = "default"
	defaultMaxAge     = 5 * time.Minute
	defaultFutureSkew = 30 * time.Second
)

var keyIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// Binding is the authenticated identity of a request. Both values are
// authoritative local inputs; they are never read from tracing headers.
type Binding struct {
	TenantID string
	Origin   string
}

// Validate checks that the binding is suitable for signing and comparison.
func (b Binding) Validate() error {
	if err := validateBindingValue("tenant", b.TenantID); err != nil {
		return err
	}
	if err := validateBindingValue("origin", b.Origin); err != nil {
		return err
	}
	return nil
}

func validateBindingValue(name, value string) error {
	if value == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidContext, name)
	}
	if len(value) > MaxBindingBytes {
		return fmt.Errorf("%w: %s exceeds %d bytes", ErrHeaderTooLong, name, MaxBindingBytes)
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c <= 0x20 || c >= 0x7f || c == ',' {
			return fmt.Errorf("%w: %s contains a forbidden byte", ErrInvalidContext, name)
		}
	}
	return nil
}

// TraceContext is the trustworthy tracing state attached to a request by the
// middleware. Trusted is true only when the inbound context passed format,
// size, signature, freshness, and tenant/origin binding checks.
type TraceContext struct {
	// TraceID is the 32-character lowercase hex trace identifier.
	TraceID string
	// SpanID is the 16-character lowercase hex span generated locally for
	// this request. It is never copied from an inbound header.
	SpanID string
	// ParentSpanID is the authenticated inbound span. It is link metadata
	// only and is never used as this request's span ID.
	ParentSpanID string
	// TenantID and Origin are locally authenticated identities. For trusted
	// inbound contexts they exactly match the signed values.
	TenantID string
	Origin   string
	// Sampled is decided solely by the local sampler.
	Sampled bool
	// RemoteSampled records the inbound flag for audit only. It never
	// influences Sampled.
	RemoteSampled bool
	// Trusted reports whether the inbound remote parent was authenticated.
	Trusted bool
	// TraceState is validated and signed W3C tracestate data.
	TraceState string
}

// HasRemoteParent reports whether an authenticated remote context was linked
// to this trace.
func (tc TraceContext) HasRemoteParent() bool {
	return tc.ParentSpanID != ""
}

// Validate checks the structural integrity of a trace context.
func (tc TraceContext) Validate() error {
	if !validTraceID(tc.TraceID) {
		return fmt.Errorf("%w: invalid trace ID", ErrInvalidContext)
	}
	if !validSpanID(tc.SpanID) {
		return fmt.Errorf("%w: invalid span ID", ErrInvalidContext)
	}
	if tc.ParentSpanID != "" && !validSpanID(tc.ParentSpanID) {
		return fmt.Errorf("%w: invalid parent span ID", ErrInvalidContext)
	}
	if err := validateBindingValue("tenant", tc.TenantID); err != nil {
		return err
	}
	if err := validateBindingValue("origin", tc.Origin); err != nil {
		return err
	}
	if tc.TraceState != "" {
		if _, err := NormalizeTraceState(tc.TraceState); err != nil {
			return err
		}
	}
	return nil
}

type traceContextKey struct{}

// WithTraceContext attaches a trace context to ctx.
func WithTraceContext(ctx context.Context, tc TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, tc)
}

// FromContext retrieves a trace context previously attached by the middleware
// or WithTraceContext.
func FromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	tc, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return tc, ok
}

// TraceIDFromContext returns the trace ID attached to ctx, if any.
func TraceIDFromContext(ctx context.Context) string {
	tc, _ := FromContext(ctx)
	return tc.TraceID
}

// SpanIDFromContext returns the locally generated span ID attached to ctx.
func SpanIDFromContext(ctx context.Context) string {
	tc, _ := FromContext(ctx)
	return tc.SpanID
}

// RemoteContext is the authenticated inbound context retained for audit and
// links. It is not the parent used for outbound propagation.
type RemoteContext struct {
	TraceID    string
	SpanID     string
	TenantID   string
	Origin     string
	KeyID      string
	TraceState string
	Sampled    bool
	Timestamp  time.Time
}

// Extraction is the result of inspecting an inbound request. Rejection is
// non-nil when the request carried tracing headers that were not trusted. The
// returned Context is always a fresh, locally valid context rooted in this
// service; a rejected inbound context never contributes identifiers.
type Extraction struct {
	Context   TraceContext
	Remote    *RemoteContext
	Trusted   bool
	Rejection error
}

// Config controls trace authentication, binding, and local sampling.
type Config struct {
	// SigningKey is the shared HMAC-SHA256 key for the current trust domain.
	// At least MinSigningKeyBytes bytes are required.
	SigningKey []byte
	// KeyID identifies SigningKey in propagated signatures. Empty means
	// "default".
	KeyID string
	// TenantID and Origin are the static authenticated binding for requests
	// when Bind is nil. They must be set together.
	TenantID string
	Origin   string
	// Bind resolves the authenticated tenant and origin for a request. If
	// supplied, it is authoritative and static TenantID/Origin are ignored.
	Bind func(*http.Request) (Binding, bool)
	// Sampler chooses the local sampling decision. Empty means AlwaysSample.
	Sampler Sampler
	// Now supplies the clock for tests. Empty means time.Now.
	Now func() time.Time
	// MaxAge is the maximum age of a signed inbound context. Zero means five
	// minutes. Negative values are rejected.
	MaxAge time.Duration
	// FutureSkew is the allowed clock skew into the future. Zero means 30
	// seconds, clamped to MaxAge. Negative values are rejected.
	FutureSkew time.Duration
	// MaxHeaderBytes is the aggregate limit across tracing headers. Zero
	// means MaxTracingHeaderBytes.
	MaxHeaderBytes int
	// MaxTraceStateBytes is the accepted tracestate size. Zero means
	// MaxTraceStateBytes. It may only lower the W3C limit.
	MaxTraceStateBytes int
}

// Tracer validates, strips, regenerates, and signs trace contexts. It is safe
// for concurrent use after construction.
type Tracer struct {
	key            []byte
	keyID          string
	tenantID       string
	origin         string
	bind           func(*http.Request) (Binding, bool)
	sampler        Sampler
	now            func() time.Time
	maxAge         time.Duration
	futureSkew     time.Duration
	maxHeaderBytes int
	maxTraceState  int
	newTraceIDFunc func() (string, error)
	newSpanIDFunc  func() (string, error)
}

// New constructs a Tracer. Invalid or weak security configuration is rejected
// rather than silently degraded.
func New(cfg Config) (*Tracer, error) {
	if len(cfg.SigningKey) < MinSigningKeyBytes {
		return nil, fmt.Errorf("netsec: signing key must be at least %d bytes", MinSigningKeyBytes)
	}
	keyID := cfg.KeyID
	if keyID == "" {
		keyID = defaultKeyID
	}
	if !keyIDPattern.MatchString(keyID) {
		return nil, errors.New("netsec: key ID must match [A-Za-z0-9_-]{1,32}")
	}

	if cfg.Bind == nil {
		if (cfg.TenantID == "") != (cfg.Origin == "") {
			return nil, errors.New("netsec: tenant and origin must be configured together")
		}
		if cfg.TenantID != "" {
			if err := (Binding{TenantID: cfg.TenantID, Origin: cfg.Origin}).Validate(); err != nil {
				return nil, err
			}
		}
	}

	maxAge := cfg.MaxAge
	if maxAge == 0 {
		maxAge = defaultMaxAge
	}
	if maxAge < 0 {
		return nil, errors.New("netsec: max age must be positive")
	}
	futureSkew := cfg.FutureSkew
	if futureSkew == 0 {
		futureSkew = defaultFutureSkew
		if futureSkew > maxAge {
			futureSkew = maxAge
		}
	}
	if futureSkew < 0 {
		return nil, errors.New("netsec: future skew must be positive")
	}
	if futureSkew > maxAge {
		return nil, errors.New("netsec: future skew must not exceed max age")
	}

	maxHeaderBytes := cfg.MaxHeaderBytes
	if maxHeaderBytes == 0 {
		maxHeaderBytes = MaxTracingHeaderBytes
	}
	if maxHeaderBytes < 256 || maxHeaderBytes > 16*1024 {
		return nil, errors.New("netsec: max header bytes must be between 256 and 16384")
	}
	maxTraceState := cfg.MaxTraceStateBytes
	if maxTraceState == 0 {
		maxTraceState = MaxTraceStateBytes
	}
	if maxTraceState < 16 || maxTraceState > MaxTraceStateBytes {
		return nil, errors.New("netsec: max tracestate bytes must be between 16 and 512")
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	sampler := cfg.Sampler
	if sampler == nil {
		sampler = AlwaysSample{}
	}

	keyCopy := make([]byte, len(cfg.SigningKey))
	copy(keyCopy, cfg.SigningKey)

	return &Tracer{
		key:            keyCopy,
		keyID:          keyID,
		tenantID:       cfg.TenantID,
		origin:         cfg.Origin,
		bind:           cfg.Bind,
		sampler:        sampler,
		now:            now,
		maxAge:         maxAge,
		futureSkew:     futureSkew,
		maxHeaderBytes: maxHeaderBytes,
		maxTraceState:  maxTraceState,
		newTraceIDFunc: func() (string, error) { return randomID(16) },
		newSpanIDFunc:  func() (string, error) { return randomID(8) },
	}, nil
}

// NewTracer is an alias for New.
func NewTracer(cfg Config) (*Tracer, error) {
	return New(cfg)
}

func randomID(size int) (string, error) {
	b := make([]byte, size)
	for attempt := 0; attempt < 16; attempt++ {
		if _, err := rand.Read(b); err != nil {
			return "", fmt.Errorf("netsec: generate ID: %w", err)
		}
		if !allZero(b) {
			return hex.EncodeToString(b), nil
		}
	}
	return "", errors.New("netsec: failed to generate a non-zero random ID")
}

func allZero(b []byte) bool {
	var zero byte
	for _, v := range b {
		zero |= v
	}
	return zero == 0
}

func (t *Tracer) bindingForRequest(r *http.Request) (Binding, bool, error) {
	if t.bind != nil {
		b, ok := t.bind(r)
		if !ok {
			return Binding{}, false, nil
		}
		if err := b.Validate(); err != nil {
			return Binding{}, false, err
		}
		return b, true, nil
	}
	if t.tenantID == "" {
		return Binding{}, false, nil
	}
	return Binding{TenantID: t.tenantID, Origin: t.origin}, true, nil
}

// BindingForRequest exposes the authoritative binding resolution used by the
// middleware. It never reads tenant or origin from tracing headers.
func (t *Tracer) BindingForRequest(r *http.Request) (Binding, bool) {
	b, ok, _ := t.bindingForRequest(r)
	return b, ok
}

func (t *Tracer) newRoot(binding Binding, hasBinding bool) (TraceContext, error) {
	traceID, err := t.newTraceIDFunc()
	if err != nil {
		return TraceContext{}, err
	}
	spanID, err := t.newSpanIDFunc()
	if err != nil {
		return TraceContext{}, err
	}
	tc := TraceContext{
		TraceID: traceID,
		SpanID:  spanID,
		Sampled: t.sampler.Sample(traceID, binding.TenantID, binding.Origin),
		Trusted: false,
	}
	if hasBinding {
		tc.TenantID = binding.TenantID
		tc.Origin = binding.Origin
	}
	return tc, nil
}

func (t *Tracer) localizeRemote(remote *RemoteContext, binding Binding) (TraceContext, error) {
	spanID, err := t.newSpanIDFunc()
	if err != nil {
		return TraceContext{}, err
	}
	return TraceContext{
		TraceID:       remote.TraceID,
		SpanID:        spanID,
		ParentSpanID:  remote.SpanID,
		TenantID:      binding.TenantID,
		Origin:        binding.Origin,
		Sampled:       t.sampler.Sample(remote.TraceID, binding.TenantID, binding.Origin),
		RemoteSampled: remote.Sampled,
		Trusted:       true,
		TraceState:    remote.TraceState,
	}, nil
}

// StartChild creates a locally generated child span. The parent span ID is the
// current local span, and the sampling decision is made again by local policy.
func (t *Tracer) StartChild(parent TraceContext) (TraceContext, error) {
	if !validTraceID(parent.TraceID) || !validSpanID(parent.SpanID) {
		return TraceContext{}, fmt.Errorf("%w: invalid parent identifiers", ErrInvalidContext)
	}
	if err := (Binding{TenantID: parent.TenantID, Origin: parent.Origin}).Validate(); err != nil {
		return TraceContext{}, err
	}
	spanID, err := t.newSpanIDFunc()
	if err != nil {
		return TraceContext{}, err
	}
	return TraceContext{
		TraceID:      parent.TraceID,
		SpanID:       spanID,
		ParentSpanID: parent.SpanID,
		TenantID:     parent.TenantID,
		Origin:       parent.Origin,
		Sampled:      t.sampler.Sample(parent.TraceID, parent.TenantID, parent.Origin),
		Trusted:      parent.Trusted,
		TraceState:   parent.TraceState,
	}, nil
}

// ChildContext is StartChild plus context attachment for use by application
// code creating an outbound operation.
func (t *Tracer) ChildContext(ctx context.Context) (context.Context, TraceContext, error) {
	parent, ok := FromContext(ctx)
	if !ok {
		return ctx, TraceContext{}, ErrNoTraceContext
	}
	child, err := t.StartChild(parent)
	if err != nil {
		return ctx, TraceContext{}, err
	}
	return WithTraceContext(ctx, child), child, nil
}

// Extract inspects a request and always returns a usable local trace context.
// An error reports why inbound tracing data was rejected; callers should treat
// the returned context as a new root and continue serving the request.
func (t *Tracer) Extract(r *http.Request) (TraceContext, error) {
	extraction := t.ExtractRequest(r)
	return extraction.Context, extraction.Rejection
}

// ParseRequest is an alias for Extract.
func (t *Tracer) ParseRequest(r *http.Request) (TraceContext, error) {
	return t.Extract(r)
}

// ExtractRequest returns the full extraction result, including rejection
// reason and authenticated remote metadata.
func (t *Tracer) ExtractRequest(r *http.Request) Extraction {
	if r == nil {
		tc, err := t.newRoot(Binding{}, false)
		return Extraction{Context: tc, Rejection: err}
	}

	binding, hasBinding, bindingErr := t.bindingForRequest(r)
	remote, err := t.extractHeaders(r.Header, binding, hasBinding)
	if err == nil && remote != nil {
		tc, idErr := t.localizeRemote(remote, binding)
		if idErr != nil {
			root, _ := t.newRoot(binding, hasBinding)
			return Extraction{Context: root, Rejection: idErr}
		}
		return Extraction{Context: tc, Remote: remote, Trusted: true}
	}

	root, rootErr := t.newRoot(binding, hasBinding)
	if err == nil {
		err = bindingErr
	}
	if err == nil {
		err = rootErr
	}
	return Extraction{
		Context:   root,
		Remote:    remote,
		Trusted:   false,
		Rejection: err,
	}
}

// ExtractHeaders validates headers against an explicit authoritative binding.
// An empty binding means no authenticated identity is available.
func (t *Tracer) ExtractHeaders(h http.Header, binding Binding) (TraceContext, error) {
	hasBinding := binding.TenantID != "" || binding.Origin != ""
	if hasBinding {
		if err := binding.Validate(); err != nil {
			root, rootErr := t.newRoot(Binding{}, false)
			if rootErr != nil {
				return TraceContext{}, rootErr
			}
			return root, err
		}
	}
	remote, err := t.extractHeaders(h, binding, hasBinding)
	if err == nil && remote != nil {
		tc, idErr := t.localizeRemote(remote, binding)
		if idErr != nil {
			root, rootErr := t.newRoot(binding, hasBinding)
			if rootErr != nil {
				return TraceContext{}, rootErr
			}
			return root, idErr
		}
		return tc, nil
	}
	root, rootErr := t.newRoot(binding, hasBinding)
	if rootErr != nil {
		return TraceContext{}, rootErr
	}
	return root, err
}

// Inject signs the trace context already attached to ctx into h. Existing
// tracing headers are replaced, never merged.
func (t *Tracer) Inject(ctx context.Context, h http.Header) error {
	tc, ok := FromContext(ctx)
	if !ok {
		return ErrNoTraceContext
	}
	return t.WriteHeaders(h, tc)
}

// InjectContext is an alias for Inject.
func (t *Tracer) InjectContext(ctx context.Context, h http.Header) error {
	return t.Inject(ctx, h)
}

// WriteHeaders writes a fresh signed header set for tc. The local span ID
// becomes the remote parent for the next hop.
func (t *Tracer) WriteHeaders(h http.Header, tc TraceContext) error {
	if h == nil {
		return errors.New("netsec: nil header map")
	}
	// Remove any prior context even if this write fails, so a rejected
	// context can never survive by fallback.
	clearTracingHeaders(h)
	if err := tc.Validate(); err != nil {
		return err
	}
	tp, err := FormatTraceParent(tc.TraceID, tc.SpanID, tc.Sampled)
	if err != nil {
		return err
	}
	traceState := tc.TraceState
	if traceState != "" {
		traceState, err = NormalizeTraceState(traceState)
		if err != nil {
			return err
		}
	}
	now := t.now().UTC()
	timestamp := fmt.Sprintf("%d", now.Unix())
	signature := t.sign(tp, traceState, tc.TenantID, tc.Origin, timestamp)

	h.Set(HeaderTraceParent, tp)
	if traceState != "" {
		h.Set(HeaderTraceState, traceState)
	}
	h.Set(HeaderTenant, tc.TenantID)
	h.Set(HeaderOrigin, tc.Origin)
	h.Set(HeaderTimestamp, timestamp)
	h.Set(HeaderSignature, signature)
	return nil
}

// Headers returns a fresh signed header set without mutating an existing
// header map.
func (t *Tracer) Headers(tc TraceContext) (http.Header, error) {
	h := make(http.Header)
	if err := t.WriteHeaders(h, tc); err != nil {
		return nil, err
	}
	return h, nil
}

// InjectTraceContext writes an explicit context into h.
func (t *Tracer) InjectTraceContext(h http.Header, tc TraceContext) error {
	return t.WriteHeaders(h, tc)
}

func validTraceID(s string) bool {
	if len(s) != 32 || !isLowerHex(s) {
		return false
	}
	return !strings.EqualFold(s, strings.Repeat("0", 32))
}

func validSpanID(s string) bool {
	if len(s) != 16 || !isLowerHex(s) {
		return false
	}
	return !strings.EqualFold(s, strings.Repeat("0", 16))
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}
