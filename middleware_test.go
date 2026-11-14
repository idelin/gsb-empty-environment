package netsec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMiddlewareSanitizesForgedHeaders(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	forged := signedHeaders(t, tracer, root)
	forged.Set(HeaderTenant, "tenant-b")
	forgedSignature := forged.Get(HeaderSignature)

	var got TraceContext
	var gotHeaders http.Header
	var gotRejection error
	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		gotHeaders = r.Header.Clone()
		gotRejection = RejectionFromContext(r.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "http://victim/", nil)
	request.Header = forged
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if !errors.Is(gotRejection, ErrInvalidSignature) {
		t.Fatalf("rejection = %v, want ErrInvalidSignature", gotRejection)
	}
	if got.Trusted || got.ParentSpanID != "" || got.TraceID == root.TraceID {
		t.Fatal("middleware trusted or linked a forged context")
	}
	if gotHeaders.Get(HeaderTenant) != testTenant {
		t.Fatal("forged tenant survived in forwarded headers")
	}
	if gotHeaders.Get(HeaderSignature) == forgedSignature {
		t.Fatal("forged signature was forwarded unchanged")
	}
	tp, err := ParseTraceParent(gotHeaders.Get(HeaderTraceParent))
	if err != nil {
		t.Fatalf("forwarded traceparent: %v", err)
	}
	if tp.SpanID != got.SpanID {
		t.Fatal("forwarded traceparent does not contain the local span")
	}
	next := mustExtract(t, tracer, gotHeaders)
	if next.TraceID != got.TraceID || next.ParentSpanID != got.SpanID {
		t.Fatal("forwarded headers do not authenticate as the local span")
	}
}

func TestMiddlewareAttachesTrustedContext(t *testing.T) {
	tracer := newTestTracer(t, nil)
	source := newRoot(t, tracer)
	headers := signedHeaders(t, tracer, source)

	var got TraceContext
	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		if err := RejectionFromContext(r.Context()); err != nil {
			t.Fatalf("valid headers rejected: %v", err)
		}
	}))
	request := httptest.NewRequest(http.MethodGet, "http://service/", nil)
	request.Header = headers
	handler.ServeHTTP(httptest.NewRecorder(), request)

	if !got.Trusted {
		t.Fatal("valid signed context was not trusted")
	}
	if got.TraceID != source.TraceID || got.ParentSpanID != source.SpanID {
		t.Fatal("trusted context was not linked correctly")
	}
	if got.SpanID == source.SpanID {
		t.Fatal("middleware reused inbound span")
	}
	if !got.Sampled {
		t.Fatal("local sampler was not used")
	}
}

func TestMiddlewareWithoutHeadersCreatesRoot(t *testing.T) {
	tracer := newTestTracer(t, nil)
	var got TraceContext
	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = FromContext(r.Context())
		if RejectionFromContext(r.Context()) != nil {
			t.Fatalf("clean request rejected: %v", RejectionFromContext(r.Context()))
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service/", nil))
	if got.TraceID == "" || got.SpanID == "" || got.Trusted || got.Sampled != true {
		t.Fatalf("unexpected root context: %+v", got)
	}
}

func TestMiddlewareStripsHeadersWhenBindingUnavailable(t *testing.T) {
	tracer := newTestTracer(t, func(cfg *Config) {
		cfg.TenantID = ""
		cfg.Origin = ""
		cfg.Bind = func(*http.Request) (Binding, bool) { return Binding{}, false }
	})
	var gotHeaders http.Header
	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
	}))
	request := httptest.NewRequest(http.MethodGet, "http://service/", nil)
	request.Header.Set(HeaderTraceParent, "00-"+strings.Repeat("a", 32)+"-"+strings.Repeat("b", 16)+"-00")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if gotHeaders.Get(HeaderTraceParent) != "" || gotHeaders.Get(HeaderSignature) != "" {
		t.Fatal("untrusted tracing headers survived when no binding was available")
	}
}

func TestMiddlewareDoesNotTreatMissingTraceAsRejection(t *testing.T) {
	tracer := newTestTracer(t, nil)
	handler := tracer.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := RejectionFromContext(r.Context()); err != nil {
			t.Fatalf("missing trace treated as rejection: %v", err)
		}
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "http://service/", nil))
}
