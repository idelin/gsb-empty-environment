package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ExportJSON writes the registry snapshot as indented JSON.
func (r *Registry) ExportJSON(w io.Writer) error {
	snap := r.Snapshot()
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(snap)
}

// ExportText writes the registry snapshot in a Prometheus-inspired
// line format: one metric per line, histograms decomposed into
// count/sum/quantiles/buckets, errors labelled by code.
func (r *Registry) ExportText(w io.Writer) error {
	snap := r.Snapshot()
	var b strings.Builder

	for _, name := range sortedKeys(snap.Counters) {
		fmt.Fprintf(&b, "%s %d\n", name, snap.Counters[name])
	}
	for _, name := range sortedKeys(snap.Gauges) {
		fmt.Fprintf(&b, "%s %d\n", name, snap.Gauges[name])
	}
	for _, name := range sortedKeysH(snap.Histograms) {
		h := snap.Histograms[name]
		fmt.Fprintf(&b, "%s_count %d\n", name, h.Count)
		fmt.Fprintf(&b, "%s_sum %g\n", name, h.Sum)
		fmt.Fprintf(&b, "%s{quantile=\"0.5\"} %g\n", name, h.P50)
		fmt.Fprintf(&b, "%s{quantile=\"0.9\"} %g\n", name, h.P90)
		fmt.Fprintf(&b, "%s{quantile=\"0.99\"} %g\n", name, h.P99)
		for _, bk := range h.Buckets {
			le := fmt.Sprintf("%g", bk.Le)
			if bk.Le >= 1e308 {
				le = "+Inf"
			}
			fmt.Fprintf(&b, "%s_bucket{le=\"%s\"} %d\n", name, le, bk.Count)
		}
	}
	codes := make([]string, 0, len(snap.Errors))
	for code := range snap.Errors {
		codes = append(codes, string(code))
	}
	sort.Strings(codes)
	for _, code := range codes {
		fmt.Fprintf(&b, "errors_total{code=\"%s\"} %d\n", code, snap.Errors[ErrCode(code)])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func sortedKeys(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedKeysH(m map[string]HistogramSnapshot) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
