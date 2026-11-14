package netsec

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testKeyA    = "0123456789abcdef0123456789abcdef"
	testTenant  = "tenant-a"
	testOrigin  = "edge-a"
	testTimeSec = int64(1_800_000_000)
)

func testTime() time.Time {
	return time.Unix(testTimeSec, 0).UTC()
}

func testBinding() Binding {
	return Binding{TenantID: testTenant, Origin: testOrigin}
}

func newTestTracer(t *testing.T, mutate func(*Config)) *Tracer {
	t.Helper()
	cfg := Config{
		SigningKey: []byte(testKeyA),
		TenantID:   testTenant,
		Origin:     testOrigin,
		Sampler:    AlwaysSample{},
		Now:        testTime,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	tracer, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tracer
}

func newRoot(t *testing.T, tracer *Tracer) TraceContext {
	t.Helper()
	tc, err := tracer.ExtractHeaders(nil, testBinding())
	if err != nil {
		t.Fatalf("ExtractHeaders(root): %v", err)
	}
	return tc
}

func signedHeaders(t *testing.T, tracer *Tracer, tc TraceContext) http.Header {
	t.Helper()
	h, err := tracer.Headers(tc)
	if err != nil {
		t.Fatalf("Headers: %v", err)
	}
	return h
}

func mustExtract(t *testing.T, tracer *Tracer, h http.Header) TraceContext {
	t.Helper()
	tc, err := tracer.ExtractHeaders(h, testBinding())
	if err != nil {
		t.Fatalf("ExtractHeaders: %v", err)
	}
	if !tc.Trusted {
		t.Fatalf("TraceContext.Trusted = false, want true")
	}
	return tc
}

func cloneHeader(h http.Header) http.Header {
	return h.Clone()
}

func TestLegitimateTraceIsAuthenticatedAndRegenerated(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	root.TraceState = "congo=t61rcWkgMzE,rojo=00f067aa0ba902b7"
	if root.Trusted || root.HasRemoteParent() {
		t.Fatalf("root must not be trusted: %+v", root)
	}
	if err := root.Validate(); err != nil {
		t.Fatalf("root Validate: %v", err)
	}

	headers := signedHeaders(t, tracer, root)
	accepted := mustExtract(t, tracer, headers)
	if accepted.TraceID != root.TraceID {
		t.Fatalf("trace ID = %q, want %q", accepted.TraceID, root.TraceID)
	}
	if accepted.ParentSpanID != root.SpanID {
		t.Fatalf("parent span = %q, want remote %q", accepted.ParentSpanID, root.SpanID)
	}
	if accepted.SpanID == root.SpanID {
		t.Fatal("boundary reused the remote span ID")
	}
	if !accepted.Sampled {
		t.Fatal("local AlwaysSample policy was not applied")
	}
	if accepted.TenantID != testTenant || accepted.Origin != testOrigin {
		t.Fatalf("binding = %s/%s, want %s/%s", accepted.TenantID, accepted.Origin, testTenant, testOrigin)
	}
	if accepted.TraceState != root.TraceState {
		t.Fatalf("tracestate = %q, want %q", accepted.TraceState, root.TraceState)
	}

	outbound := signedHeaders(t, tracer, accepted)
	tp, err := ParseTraceParent(outbound.Get(HeaderTraceParent))
	if err != nil {
		t.Fatalf("ParseTraceParent(outbound): %v", err)
	}
	if tp.SpanID != accepted.SpanID {
		t.Fatalf("outbound parent span = %q, want local span %q", tp.SpanID, accepted.SpanID)
	}
}

func TestForgedContextsAreIsolated(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	root.TraceState = "congo=t61rcWkgMzE,rojo=00f067aa0ba902b7"
	valid := signedHeaders(t, tracer, root)

	otherTraceID := strings.Repeat("1", 32)
	if otherTraceID == root.TraceID {
		otherTraceID = strings.Repeat("2", 32)
	}
	mutations := []struct {
		name   string
		mutate func(http.Header)
		reason error
	}{
		{
			name: "parent span",
			mutate: func(h http.Header) {
				tp, _ := ParseTraceParent(h.Get(HeaderTraceParent))
				tp.SpanID = strings.Repeat("2", 16)
				h.Set(HeaderTraceParent, tp.String())
			},
			reason: ErrInvalidSignature,
		},
		{
			name: "sampling flag",
			mutate: func(h http.Header) {
				tp, _ := ParseTraceParent(h.Get(HeaderTraceParent))
				tp.Sampled = !tp.Sampled
				h.Set(HeaderTraceParent, tp.String())
			},
			reason: ErrInvalidSignature,
		},
		{
			name: "trace ID",
			mutate: func(h http.Header) {
				tp, _ := ParseTraceParent(h.Get(HeaderTraceParent))
				tp.TraceID = otherTraceID
				h.Set(HeaderTraceParent, tp.String())
			},
			reason: ErrInvalidSignature,
		},
		{
			name:   "tenant",
			mutate: func(h http.Header) { h.Set(HeaderTenant, "tenant-b") },
			reason: ErrInvalidSignature,
		},
		{
			name:   "origin",
			mutate: func(h http.Header) { h.Set(HeaderOrigin, "edge-b") },
			reason: ErrInvalidSignature,
		},
		{
			name: "tracestate whitespace",
			mutate: func(h http.Header) {
				h.Set(HeaderTraceState, strings.Replace(h.Get(HeaderTraceState), ",", ", ", 1))
			},
			reason: ErrInvalidSignature,
		},
		{
			name: "signature",
			mutate: func(h http.Header) {
				parts := strings.Split(h.Get(HeaderSignature), ".")
				mac, _ := base64.RawURLEncoding.DecodeString(parts[2])
				mac[0] ^= 0xff
				parts[2] = base64.RawURLEncoding.EncodeToString(mac)
				h.Set(HeaderSignature, strings.Join(parts, "."))
			},
			reason: ErrInvalidSignature,
		},
		{
			name:   "missing signature",
			mutate: func(h http.Header) { h.Del(HeaderSignature) },
			reason: ErrMalformedHeader,
		},
		{
			name:   "missing tenant",
			mutate: func(h http.Header) { h.Del(HeaderTenant) },
			reason: ErrMalformedHeader,
		},
		{
			name: "duplicate traceparent",
			mutate: func(h http.Header) {
				h["traceparent"] = []string{h.Get(HeaderTraceParent), h.Get(HeaderTraceParent)}
			},
			reason: ErrDuplicateHeader,
		},
	}

	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			h := cloneHeader(valid)
			test.mutate(h)
			tc, err := tracer.ExtractHeaders(h, testBinding())
			if err == nil {
				t.Fatal("expected forged context to be rejected")
			}
			if test.reason != nil && !errors.Is(err, test.reason) {
				t.Fatalf("error = %v, want errors.Is(%v)", err, test.reason)
			}
			if tc.Trusted {
				t.Fatal("forged context was marked trusted")
			}
			if tc.ParentSpanID != "" {
				t.Fatalf("forged context contributed parent %q", tc.ParentSpanID)
			}
			if tc.TraceID == root.TraceID {
				t.Fatal("forged context preserved the legitimate trace ID")
			}
			if !tc.Sampled {
				t.Fatal("local sampling policy was bypassed by forged flags")
			}
		})
	}
}

func TestUnsignedTraceParentCannotJoinTraceOrDisableSampling(t *testing.T) {
	tracer := newTestTracer(t, nil)
	tpValue := "00-" + strings.Repeat("a", 32) + "-" + strings.Repeat("b", 16) + "-00"
	h := http.Header{HeaderTraceParent: []string{tpValue}}
	tc, err := tracer.ExtractHeaders(h, testBinding())
	if err == nil {
		t.Fatal("unsigned traceparent must be rejected")
	}
	if tc.TraceID == strings.Repeat("a", 32) {
		t.Fatal("attacker-controlled trace ID was preserved")
	}
	if !tc.Sampled {
		t.Fatal("attacker-controlled unsampled flag was honored")
	}
	if tc.ParentSpanID != "" {
		t.Fatal("attacker-controlled parent span was preserved")
	}
}

func TestCrossBoundaryPropagationRegeneratesParent(t *testing.T) {
	tracer := newTestTracer(t, nil)

	var firstCtx TraceContext
	var firstHeaders http.Header
	boundaryOne := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstCtx, _ = FromContext(r.Context())
		firstHeaders = r.Header.Clone()
	}))
	boundaryOne.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service-a/", nil))

	var secondCtx TraceContext
	var secondHeaders http.Header
	boundaryTwo := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondCtx, _ = FromContext(r.Context())
		secondHeaders = r.Header.Clone()
		if RejectionFromContext(r.Context()) != nil {
			t.Fatalf("second boundary rejected valid context: %v", RejectionFromContext(r.Context()))
		}
	}))
	secondRequest := httptest.NewRequest(http.MethodGet, "http://service-b/", nil)
	secondRequest.Header = firstHeaders
	boundaryTwo.ServeHTTP(httptest.NewRecorder(), secondRequest)

	if secondCtx.TraceID != firstCtx.TraceID {
		t.Fatalf("trace changed across trusted boundary: %q -> %q", firstCtx.TraceID, secondCtx.TraceID)
	}
	if secondCtx.ParentSpanID != firstCtx.SpanID {
		t.Fatalf("parent span = %q, want first boundary span %q", secondCtx.ParentSpanID, firstCtx.SpanID)
	}
	if secondCtx.SpanID == firstCtx.SpanID {
		t.Fatal("second boundary reused first boundary span ID")
	}
	if secondCtx.SpanID == secondCtx.ParentSpanID {
		t.Fatal("local span equals parent span")
	}

	tp, err := ParseTraceParent(secondHeaders.Get(HeaderTraceParent))
	if err != nil {
		t.Fatalf("parse propagated traceparent: %v", err)
	}
	if tp.SpanID != secondCtx.SpanID {
		t.Fatalf("propagated parent = %q, want second local span %q", tp.SpanID, secondCtx.SpanID)
	}
	if _, err := tracer.ExtractHeaders(secondHeaders, testBinding()); err != nil {
		t.Fatalf("propagated headers no longer authenticate: %v", err)
	}
}

func TestSameRemoteContextAlwaysProducesFreshLocalSpan(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	headers := signedHeaders(t, tracer, root)

	first := mustExtract(t, tracer, headers)
	second := mustExtract(t, tracer, headers)
	if first.TraceID != second.TraceID || first.TraceID != root.TraceID {
		t.Fatal("trusted trace ID was not preserved")
	}
	if first.ParentSpanID != root.SpanID || second.ParentSpanID != root.SpanID {
		t.Fatal("authenticated remote parent was not retained as link metadata")
	}
	if first.SpanID == second.SpanID {
		t.Fatal("boundary did not regenerate an independent local span")
	}
	if first.SpanID == root.SpanID || second.SpanID == root.SpanID {
		t.Fatal("boundary reused the inbound span")
	}
}

func TestLocalSamplingPolicyOverridesRemoteFlags(t *testing.T) {
	upstreamNever := newTestTracer(t, func(cfg *Config) { cfg.Sampler = NeverSample{} })
	root := newRoot(t, upstreamNever)
	headers := signedHeaders(t, upstreamNever, root)

	receiverAlways := newTestTracer(t, nil)
	accepted := mustExtract(t, receiverAlways, headers)
	if accepted.RemoteSampled {
		t.Fatal("test setup should carry an unsampled remote flag")
	}
	if !accepted.Sampled {
		t.Fatal("remote unsampled flag suppressed local sampling")
	}

	upstreamAlways := newTestTracer(t, nil)
	root = newRoot(t, upstreamAlways)
	headers = signedHeaders(t, upstreamAlways, root)
	receiverNever := newTestTracer(t, func(cfg *Config) { cfg.Sampler = NeverSample{} })
	accepted = mustExtract(t, receiverNever, headers)
	if !accepted.RemoteSampled {
		t.Fatal("test setup should carry a sampled remote flag")
	}
	if accepted.Sampled {
		t.Fatal("remote sampled flag forced local sampling")
	}
}

func TestRateSamplerIsStableAndFlagIndependent(t *testing.T) {
	rate := MustRateSampler(0.5)
	upstream := newTestTracer(t, func(cfg *Config) { cfg.Sampler = AlwaysSample{} })
	receiver := newTestTracer(t, func(cfg *Config) { cfg.Sampler = rate })

	root := newRoot(t, upstream)
	sampledHeaders := signedHeaders(t, upstream, root)
	unsampled := root
	unsampled.Sampled = false
	unsampledHeaders := signedHeaders(t, upstream, unsampled)

	a := mustExtract(t, receiver, sampledHeaders)
	b := mustExtract(t, receiver, unsampledHeaders)
	if a.TraceID != b.TraceID {
		t.Fatal("test contexts unexpectedly differ")
	}
	if a.Sampled != b.Sampled {
		t.Fatal("local sampling decision depended on the remote flag")
	}

	seenSampled, seenUnsampled := false, false
	for i := 0; i < 64; i++ {
		traceIDBytes := sha256.Sum256([]byte{byte(i), 1})
		traceID := hex.EncodeToString(traceIDBytes[:16])
		if rate.Sample(traceID, testTenant, testOrigin) {
			seenSampled = true
		} else {
			seenUnsampled = true
		}
	}
	if !seenSampled || !seenUnsampled {
		t.Fatal("rate sampler did not produce both decisions across a sample set")
	}
}

func TestTenantAndOriginAreBound(t *testing.T) {
	tracerA := newTestTracer(t, nil)
	root := newRoot(t, tracerA)
	headers := signedHeaders(t, tracerA, root)

	for _, binding := range []Binding{
		{TenantID: "tenant-b", Origin: testOrigin},
		{TenantID: testTenant, Origin: "edge-b"},
	} {
		tc, err := tracerA.ExtractHeaders(headers, binding)
		if !errors.Is(err, ErrBindingMismatch) {
			t.Fatalf("binding %+v error = %v, want ErrBindingMismatch", binding, err)
		}
		if tc.Trusted || tc.ParentSpanID != "" || tc.TraceID == root.TraceID {
			t.Fatal("mismatched binding was not isolated")
		}
	}

	tracerB := newTestTracer(t, func(cfg *Config) {
		cfg.TenantID = "tenant-b"
	})
	tc, err := tracerB.ExtractHeaders(headers, Binding{TenantID: "tenant-b", Origin: testOrigin})
	if !errors.Is(err, ErrBindingMismatch) {
		t.Fatalf("wrong signed tenant error = %v", err)
	}
	if tc.Trusted {
		t.Fatal("context signed for another tenant was trusted")
	}
}

func TestDynamicBindingUsesRequestIdentityNotTraceHeaders(t *testing.T) {
	tracer := newTestTracer(t, func(cfg *Config) {
		cfg.TenantID = ""
		cfg.Origin = ""
		cfg.Bind = func(r *http.Request) (Binding, bool) {
			if strings.HasPrefix(r.URL.Path, "/alpha") {
				return Binding{TenantID: "tenant-a", Origin: "edge-a"}, true
			}
			return Binding{TenantID: "tenant-b", Origin: "edge-b"}, true
		}
	})
	source := newTestTracer(t, nil)
	root := newRoot(t, source)
	headers := signedHeaders(t, source, root)

	allowed := httptest.NewRequest(http.MethodGet, "http://host/alpha", nil)
	allowed.Header = headers.Clone()
	extraction := tracer.ExtractRequest(allowed)
	if extraction.Rejection != nil || !extraction.Context.Trusted {
		t.Fatalf("valid alpha request rejected: %+v", extraction)
	}
	if extraction.Context.TenantID != "tenant-a" || extraction.Context.Origin != "edge-a" {
		t.Fatal("binding was not taken from local request identity")
	}

	denied := httptest.NewRequest(http.MethodGet, "http://host/beta", nil)
	denied.Header = headers.Clone()
	extraction = tracer.ExtractRequest(denied)
	if !errors.Is(extraction.Rejection, ErrBindingMismatch) {
		t.Fatalf("alpha context accepted for beta: %v", extraction.Rejection)
	}
	if extraction.Context.Trusted || extraction.Context.ParentSpanID != "" {
		t.Fatal("cross-tenant context was trusted")
	}
}

func TestSignatureFreshness(t *testing.T) {
	signer := newTestTracer(t, nil)
	root := newRoot(t, signer)
	headers := signedHeaders(t, signer, root)

	expired := newTestTracer(t, func(cfg *Config) {
		cfg.Now = func() time.Time { return testTime().Add(defaultMaxAge + time.Second) }
	})
	tc, err := expired.ExtractHeaders(headers, testBinding())
	if !errors.Is(err, ErrExpiredContext) {
		t.Fatalf("expired context error = %v", err)
	}
	if tc.Trusted || tc.TraceID == root.TraceID {
		t.Fatal("expired context was trusted")
	}

	future := newTestTracer(t, func(cfg *Config) {
		cfg.Now = func() time.Time { return testTime().Add(-defaultFutureSkew - time.Second) }
	})
	tc, err = future.ExtractHeaders(headers, testBinding())
	if !errors.Is(err, ErrFutureContext) {
		t.Fatalf("future context error = %v", err)
	}
	if tc.Trusted {
		t.Fatal("future context was trusted")
	}
}

func TestWriteHeadersRequiresBindingAndReplacesForgedHeaders(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	h := http.Header{
		HeaderTraceParent: []string{"00-" + strings.Repeat("f", 32) + "-" + strings.Repeat("e", 16) + "-01"},
		HeaderTenant:      []string{"forged"},
		HeaderSignature:   []string{"forged"},
	}
	if err := tracer.WriteHeaders(h, root); err != nil {
		t.Fatalf("WriteHeaders: %v", err)
	}
	if len(h.Values("traceparent")) != 1 {
		t.Fatal("WriteHeaders did not replace traceparent")
	}
	if h.Get(HeaderTenant) != testTenant || h.Get(HeaderOrigin) != testOrigin {
		t.Fatal("WriteHeaders did not replace binding headers")
	}
	if _, err := tracer.ExtractHeaders(h, testBinding()); err != nil {
		t.Fatalf("written headers do not validate: %v", err)
	}

	noBinding := root
	noBinding.TenantID = ""
	noBinding.Origin = ""
	if err := tracer.WriteHeaders(http.Header{}, noBinding); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("missing binding error = %v, want ErrInvalidContext", err)
	}
	if err := tracer.WriteHeaders(nil, root); err == nil {
		t.Fatal("nil header map was accepted")
	}

	h = http.Header{HeaderTraceParent: []string{"attacker"}}
	if err := tracer.WriteHeaders(h, noBinding); err == nil {
		t.Fatal("invalid context was written")
	}
	if h.Get(HeaderTraceParent) != "" {
		t.Fatal("failed WriteHeaders left an old tracing header behind")
	}
}

func TestStartChildRegeneratesSpan(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	child, err := tracer.StartChild(root)
	if err != nil {
		t.Fatalf("StartChild: %v", err)
	}
	if child.TraceID != root.TraceID {
		t.Fatal("child changed trace ID")
	}
	if child.ParentSpanID != root.SpanID || child.SpanID == root.SpanID {
		t.Fatal("child did not regenerate a span")
	}
	if child.Sampled != root.Sampled {
		t.Fatal("child sampling policy was not locally applied")
	}
}
