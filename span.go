package spandep

import (
	"encoding/json"
	"fmt"
	"io"
)

// Kind is the span kind as exported by the tracer.
type Kind string

const (
	KindClient   Kind = "client"
	KindServer   Kind = "server"
	KindInternal Kind = "internal"
)

// Status is the span status code.
type Status string

const (
	StatusUnset Status = "unset"
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// Span is one telemetry event. Service may be empty when the exporter
// did not set a service name; empty is never resolved to a guessed name.
type Span struct {
	TraceID       string `json:"traceId"`
	SpanID        string `json:"spanId"`
	ParentSpanID  string `json:"parentSpanId,omitempty"`
	Name          string `json:"name"`
	Kind          Kind   `json:"kind"`
	Service       string `json:"service,omitempty"`
	Status        Status `json:"status"`
	StartUnixNano int64  `json:"startUnixNano"`
	EndUnixNano   int64  `json:"endUnixNano"`
}

func (s Span) key() string { return s.TraceID + "/" + s.SpanID }

// Fixture is the on-disk replay format for synthesized telemetry.
type Fixture struct {
	Seed  int64  `json:"seed"`
	Spans []Span `json:"spans"`
}

// LoadFixture decodes a fixture from r and validates the spans.
func LoadFixture(r io.Reader) (Fixture, error) {
	var f Fixture
	dec := json.NewDecoder(r)
	if err := dec.Decode(&f); err != nil {
		return Fixture{}, fmt.Errorf("decode fixture: %w", err)
	}
	for i, s := range f.Spans {
		if s.TraceID == "" || s.SpanID == "" {
			return Fixture{}, fmt.Errorf("span %d: traceId and spanId are required", i)
		}
		switch s.Kind {
		case KindClient, KindServer, KindInternal:
		default:
			return Fixture{}, fmt.Errorf("span %d (%s): unknown kind %q", i, s.SpanID, s.Kind)
		}
		switch s.Status {
		case StatusUnset, StatusOK, StatusError:
		default:
			return Fixture{}, fmt.Errorf("span %d (%s): unknown status %q", i, s.SpanID, s.Status)
		}
	}
	return f, nil
}

// WriteFixture encodes spans as a replayable fixture.
func WriteFixture(w io.Writer, seed int64, spans []Span) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(Fixture{Seed: seed, Spans: spans})
}
