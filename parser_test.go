package promlite

import (
	"testing"
	"time"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{`http_requests_total`, `http_requests_total`},
		{`http_requests_total{job="api", instance="a"}`, `http_requests_total{job="api", instance="a"}`},
		{`rate(http_requests_total[5m])`, `rate(http_requests_total[5m0s])`},
		{`rate(http_requests_total{job="api"}[1h30m])`, `rate(http_requests_total{job="api"}[1h30m0s])`},
		{`sum by (job) (rate(http_requests_total[5m]))`, `sum by (job) (rate(http_requests_total[5m0s]))`},
		{`http_requests_total offset 10m`, `http_requests_total offset 10m0s`},
		{`rate(http_requests_total[5m]) offset 1h`, `rate(http_requests_total[5m0s]) offset 1h0m0s`},
		{`a / on(job) b`, `a / on(job) b`},
		{`a + ignoring(instance) b`, `a + ignoring(instance) b`},
		{`a + b * 2`, `a + (b * 2)`},
		{`(a + b) * 2`, `(a + b) * 2`},
	}
	for _, c := range cases {
		e, err := Parse(c.src)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", c.src, err)
			continue
		}
		if got := Describe(e); got != c.want {
			t.Errorf("Parse(%q) round-trip = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestParseInvalid(t *testing.T) {
	cases := []string{
		`rate(http_requests_total)`,      // missing range
		`rate(sum by (job) (m)[5m])`,     // rate over non-selector
		`http_requests_total offset -5m`, // negative offset not lexable as duration
		`http_requests_total offset 0s`,  // zero offset
		`rate(m[0s])`,                    // zero window
		`sum by (job) m`,                 // missing parens around expr
		`m{job=api}`,                     // unquoted label value
		`a group_left b`,                 // unsupported modifier
		`a > b`,                          // comparison ops unsupported
		`m{job="a"`,                      // unclosed brace
		``,
	}
	for _, src := range cases {
		if e, err := Parse(src); err == nil {
			t.Errorf("Parse(%q) unexpectedly succeeded: %v", src, Describe(e))
		}
	}
}

func TestParseOffsetDuration(t *testing.T) {
	e, err := Parse(`m offset 1h30m`)
	if err != nil {
		t.Fatal(err)
	}
	o, ok := e.(OffsetExpr)
	if !ok {
		t.Fatalf("expected OffsetExpr, got %T", e)
	}
	if o.Offset != 90*time.Minute {
		t.Fatalf("offset = %v, want 90m", o.Offset)
	}
}
