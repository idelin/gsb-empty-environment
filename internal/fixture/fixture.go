// Package fixture persists synthesized telemetry as JSON fixtures and
// replays them, so analysis runs are reproducible offline.
package fixture

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"depgraph/internal/telemetry"
)

// Write stores spans as a JSON array.
func Write(w io.Writer, spans []telemetry.Span) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(spans)
}

// Read loads spans from a JSON fixture.
func Read(r io.Reader) ([]telemetry.Span, error) {
	var spans []telemetry.Span
	if err := json.NewDecoder(r).Decode(&spans); err != nil {
		return nil, fmt.Errorf("fixture: %w", err)
	}
	return spans, nil
}

// Replay orders events by start time, simulating ingestion order.
// The sort is stable and independent of the input order.
func Replay(spans []telemetry.Span) []telemetry.Span {
	out := make([]telemetry.Span, len(spans))
	copy(out, spans)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].StartUnixNano < out[j].StartUnixNano
	})
	return out
}
