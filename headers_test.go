package netsec

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestParseTraceParentStrict(t *testing.T) {
	valid := "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
	tp, err := ParseTraceParent(valid)
	if err != nil {
		t.Fatalf("ParseTraceParent: %v", err)
	}
	if tp.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || tp.SpanID != "00f067aa0ba902b7" || !tp.Sampled {
		t.Fatalf("unexpected parse: %+v", tp)
	}
	if tp.String() != valid {
		t.Fatalf("round trip = %q, want %q", tp.String(), valid)
	}

	cases := []string{
		"",
		"00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-" + strings.Repeat("0", 16) + "-01",
		"01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01-extra",
		"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-0g",
		" 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"00-" + strings.Repeat("4", 32) + "-00f067aa0ba902b7-01" + strings.Repeat("x", 80),
	}
	for _, value := range cases {
		if _, err := ParseTraceParent(value); err == nil {
			t.Errorf("ParseTraceParent(%q) succeeded", value)
		}
	}
}

func TestTraceStateValidationAndLimits(t *testing.T) {
	valid := "congo=t61rcWkgMzE,rojo=00f067aa0ba902b7"
	normalized, err := NormalizeTraceState(" congo = t61rcWkgMzE , rojo = 00f067aa0ba902b7 ")
	if err != nil {
		t.Fatalf("NormalizeTraceState: %v", err)
	}
	if normalized != valid {
		t.Fatalf("normalized = %q, want %q", normalized, valid)
	}

	invalid := []string{
		"",
		"congo",
		"=value",
		"key=",
		"Key=value",
		"congo=a,b",
		"congo=a=c",
		"congo=a,congo=b",
		"congo=a\nb",
		strings.Repeat("a=1,", 33) + "z=1",
		"congo=" + strings.Repeat("a", 600),
	}
	for _, value := range invalid {
		if err := ValidateTraceState(value); err == nil {
			t.Errorf("ValidateTraceState(%q) succeeded", value)
		}
	}
}

func TestHeaderSizeAndAggregateLimits(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	headers := signedHeaders(t, tracer, root)

	tooLongTP := cloneHeader(headers)
	tooLongTP.Set(HeaderTraceParent, headers.Get(HeaderTraceParent)+strings.Repeat("x", MaxTraceParentBytes))
	if _, err := tracer.ExtractHeaders(tooLongTP, testBinding()); !errors.Is(err, ErrHeaderTooLong) {
		t.Fatalf("oversized traceparent error = %v", err)
	}

	tooLongState := cloneHeader(headers)
	tooLongState.Set(HeaderTraceState, "a="+strings.Repeat("x", MaxTraceStateBytes))
	if _, err := tracer.ExtractHeaders(tooLongState, testBinding()); !errors.Is(err, ErrHeaderTooLong) {
		t.Fatalf("oversized tracestate error = %v", err)
	}

	tooLongTenant := cloneHeader(headers)
	tooLongTenant.Set(HeaderTenant, strings.Repeat("a", MaxBindingBytes+1))
	if _, err := tracer.ExtractHeaders(tooLongTenant, testBinding()); !errors.Is(err, ErrHeaderTooLong) {
		t.Fatalf("oversized tenant error = %v", err)
	}

	smallAggregate := newTestTracer(t, func(cfg *Config) {
		cfg.MaxHeaderBytes = 256
	})
	aggregateHeaders := cloneHeader(headers)
	aggregateHeaders.Set(HeaderTraceState, "vendor="+strings.Repeat("x", 100))
	if _, err := smallAggregate.ExtractHeaders(aggregateHeaders, testBinding()); !errors.Is(err, ErrHeaderTooLong) {
		t.Fatalf("aggregate limit error = %v", err)
	}

	badTimestamp := cloneHeader(headers)
	badTimestamp.Set(HeaderTimestamp, strings.Repeat("9", MaxTimestampBytes+1))
	if _, err := tracer.ExtractHeaders(badTimestamp, testBinding()); !errors.Is(err, ErrHeaderTooLong) {
		t.Fatalf("oversized timestamp error = %v", err)
	}
}

func TestHeaderCaseAndDuplicateHandling(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	headers := signedHeaders(t, tracer, root)

	lower := http.Header{}
	for key, values := range headers {
		lower[strings.ToLower(key)] = values
	}
	tc := mustExtract(t, tracer, lower)
	if tc.TraceID != root.TraceID {
		t.Fatal("case-insensitive headers were not accepted")
	}

	duplicate := cloneHeader(headers)
	duplicate["traceparent"] = []string{duplicate.Get(HeaderTraceParent)}
	if _, err := tracer.ExtractHeaders(duplicate, testBinding()); !errors.Is(err, ErrDuplicateHeader) {
		t.Fatalf("case-variant duplicate error = %v", err)
	}
}

func TestSignatureWireFormat(t *testing.T) {
	tracer := newTestTracer(t, nil)
	root := newRoot(t, tracer)
	headers := signedHeaders(t, tracer, root)
	signature := headers.Get(HeaderSignature)
	parts := strings.Split(signature, ".")
	if len(parts) != 3 || parts[0] != "v1" || parts[1] != "default" {
		t.Fatalf("signature format = %q", signature)
	}
	if len(signature) > MaxSignatureBytes {
		t.Fatal("signature exceeds limit")
	}

	for _, malformed := range []string{
		"",
		"v1.default",
		"v2.default." + parts[2],
		"v1.bad!id." + parts[2],
		"v1.default.not-base64!",
		"v1.default." + strings.Repeat("A", 10),
	} {
		h := cloneHeader(headers)
		h.Set(HeaderSignature, malformed)
		if _, err := tracer.ExtractHeaders(h, testBinding()); !errors.Is(err, ErrMalformedHeader) && !errors.Is(err, ErrInvalidSignature) {
			t.Errorf("signature %q error = %v", malformed, err)
		}
	}
}

func TestConfigRejectsUnsafeValues(t *testing.T) {
	valid := Config{
		SigningKey: []byte(testKeyA),
		TenantID:   testTenant,
		Origin:     testOrigin,
		Now:        testTime,
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "short key", mutate: func(c *Config) { c.SigningKey = []byte("short") }},
		{name: "bad key id", mutate: func(c *Config) { c.KeyID = "bad.id" }},
		{name: "partial binding", mutate: func(c *Config) { c.Origin = "" }},
		{name: "invalid tenant", mutate: func(c *Config) { c.TenantID = "tenant a" }},
		{name: "negative age", mutate: func(c *Config) { c.MaxAge = -1 }},
		{name: "negative skew", mutate: func(c *Config) { c.FutureSkew = -1 }},
		{name: "skew greater than age", mutate: func(c *Config) { c.MaxAge = 1; c.FutureSkew = 2 }},
		{name: "tiny header budget", mutate: func(c *Config) { c.MaxHeaderBytes = 10 }},
		{name: "oversized tracestate", mutate: func(c *Config) { c.MaxTraceStateBytes = 600 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := valid
			test.mutate(&cfg)
			if _, err := New(cfg); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}
