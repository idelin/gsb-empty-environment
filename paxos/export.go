package paxos

import (
	"encoding/json"
	"io"
	"sort"
	"time"
)

// Snapshot is an immutable, exportable view of one node's Metrics.
type Snapshot struct {
	NodeID     int                    `json:"nodeID"`
	TakenAt    time.Time              `json:"takenAt"`
	Uptime     float64                `json:"uptimeSeconds"`
	Counters   map[string]int64       `json:"counters"`
	Errors     map[string]int64       `json:"errors"`   // keyed by ErrCode string
	HitRates   map[string]float64     `json:"hitRates"` // hits/(hits+misses)
	Histograms map[string]HistSummary `json:"histograms"`
	SeriesLen  int                    `json:"seriesLen"`

	// raw histogram samples retained for cross-node aggregation; not exported.
	raw map[string][]float64
}

// Snapshot captures the current metrics state of the node.
func (m *Metrics) Snapshot(nodeID int) Snapshot {
	m.mu.Lock()
	counters := make(map[string]int64, len(m.counters))
	for k, v := range m.counters {
		counters[k] = v
	}
	errs := make(map[string]int64, len(m.errs))
	for k, v := range m.errs {
		errs[k.String()] = v
	}
	hitRates := make(map[string]float64, len(m.hits)+len(m.misses))
	for k := range m.hits {
		h, mi := m.hits[k], m.misses[k]
		if h+mi > 0 {
			hitRates[k] = float64(h) / float64(h+mi)
		}
	}
	for k := range m.misses {
		if _, ok := hitRates[k]; !ok {
			h, mi := m.hits[k], m.misses[k]
			if h+mi > 0 {
				hitRates[k] = float64(h) / float64(h+mi)
			}
		}
	}
	hs := make(map[string]*Histogram, len(m.hists))
	for k, v := range m.hists {
		hs[k] = v
	}
	seriesLen := len(m.series)
	m.mu.Unlock()

	hists := make(map[string]HistSummary, len(hs))
	raw := make(map[string][]float64, len(hs))
	for k, h := range hs {
		hists[k] = h.Summary()
		raw[k] = h.rawValues()
	}
	return Snapshot{
		NodeID:     nodeID,
		TakenAt:    time.Now(),
		Uptime:     m.Uptime().Seconds(),
		Counters:   counters,
		Errors:     errs,
		HitRates:   hitRates,
		Histograms: hists,
		SeriesLen:  seriesLen,
		raw:        raw,
	}
}

// Aggregate merges per-node snapshots into a cluster-wide statistical view.
// Counters and error tallies are summed; hit rates are sample-weighted;
// histogram percentiles are recomputed from pooled raw samples.
type Aggregate struct {
	Nodes      int                    `json:"nodes"`
	Counters   map[string]int64       `json:"counters"`
	Errors     map[string]int64       `json:"errors"`
	HitRates   map[string]float64     `json:"hitRates"`
	Histograms map[string]HistSummary `json:"histograms"`
}

// AggregateSnapshots combines any number of snapshots (zero is allowed).
func AggregateSnapshots(snaps ...Snapshot) Aggregate {
	agg := Aggregate{
		Nodes:      len(snaps),
		Counters:   make(map[string]int64),
		Errors:     make(map[string]int64),
		HitRates:   make(map[string]float64),
		Histograms: make(map[string]HistSummary),
	}
	pooled := make(map[string][]float64)
	hitW := make(map[string]float64) // weighted hits
	hitN := make(map[string]int64)   // weight (node count contribution)
	for _, s := range snaps {
		for k, v := range s.Counters {
			agg.Counters[k] += v
		}
		for k, v := range s.Errors {
			agg.Errors[k] += v
		}
		for k, r := range s.HitRates {
			hitW[k] += r
			hitN[k]++
		}
		for k, vals := range s.raw {
			pooled[k] = append(pooled[k], vals...)
		}
	}
	for k := range hitW {
		agg.HitRates[k] = hitW[k] / float64(hitN[k])
	}
	for k, vals := range pooled {
		h := NewHistogram(0)
		for _, v := range vals {
			h.Observe(v)
		}
		agg.Histograms[k] = h.Summary()
	}
	return agg
}

// ExportJSON writes a snapshot as indented JSON.
func ExportJSON(w io.Writer, s Snapshot) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(s)
}

// ExportAggregateJSON writes an aggregate as indented JSON.
func ExportAggregateJSON(w io.Writer, a Aggregate) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(a)
}

// SortedCounters returns counter names in deterministic order (for reports).
func SortedCounters(m map[string]int64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
