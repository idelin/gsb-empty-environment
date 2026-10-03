package metrics

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
)

// ExportText writes the snapshot in a Prometheus-inspired text exposition
// format: one line per series, histograms expanded into bucket rows.
func (s *Snapshot) ExportText(w io.Writer) error {
	for _, k := range sortedMapKeysI64(s.Counters) {
		if _, err := fmt.Fprintf(w, "counter %s %d\n", k, s.Counters[k]); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeysF64(s.Gauges) {
		if _, err := fmt.Fprintf(w, "gauge %s %g\n", k, s.Gauges[k]); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeysHist(s.Histograms) {
		h := s.Histograms[k]
		for _, b := range h.Buckets {
			ub := fmt.Sprintf("%g", b.UpperBound)
			if math.IsInf(b.UpperBound, 1) {
				ub = "+Inf"
			}
			if _, err := fmt.Fprintf(w, "histogram_bucket %s le=%s %d\n", k, ub, b.CumulativeCount); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "histogram_summary %s count=%d sum=%g mean=%g min=%g max=%g p50=%g p90=%g p99=%g\n",
			k, h.Count, h.Sum, h.Mean, h.Min, h.Max, h.P50, h.P90, h.P99); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeysHR(s.HitRates) {
		hr := s.HitRates[k]
		if _, err := fmt.Fprintf(w, "hitrate %s hits=%d misses=%d ratio=%g\n", k, hr.Hits, hr.Misses, hr.Ratio); err != nil {
			return err
		}
	}
	for _, k := range sortedMapKeysEC(s.ErrorCodes) {
		codes := s.ErrorCodes[k]
		for _, c := range sortedMapKeysI64(codes) {
			if _, err := fmt.Fprintf(w, "error_code %s code=%s %d\n", k, c, codes[c]); err != nil {
				return err
			}
		}
	}
	return nil
}

// ExportJSON writes the snapshot as indented JSON.
func (s *Snapshot) ExportJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

func sortedMapKeysI64(m map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeysF64(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeysHist(m map[string]HistogramSnapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeysHR(m map[string]HitRateSnapshot) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedMapKeysEC(m map[string]map[string]int64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
