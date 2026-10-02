package promlite

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Labels is the label set identifying a series (without the metric name).
type Labels map[string]string

// Key returns a canonical, sorted rendering of the label set.
func (l Labels) Key() string {
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s=%q", k, l[k])
	}
	b.WriteByte('}')
	return b.String()
}

// Sample is a single telemetry event: a value for a series at a time.
type Sample struct {
	Metric string            `json:"metric"`
	Labels map[string]string `json:"labels,omitempty"`
	T      time.Time         `json:"ts"`
	V      float64           `json:"value"`
}

// Matcher is an equality matcher on one label.
type Matcher struct {
	Name  string
	Value string
}

func (m Matcher) matches(l Labels) bool { return l[m.Name] == m.Value }

// Store holds synthesized telemetry events, replayed from a fixture.
type Store struct {
	samples []Sample
	sorted  bool
}

func NewStore() *Store { return &Store{} }

func (s *Store) Add(samples ...Sample) {
	s.samples = append(s.samples, samples...)
	s.sorted = false
}

func (s *Store) sortSamples() {
	if s.sorted {
		return
	}
	sort.SliceStable(s.samples, func(i, j int) bool {
		return s.samples[i].T.Before(s.samples[j].T)
	})
	s.sorted = true
}

// Samples returns all events in time order.
func (s *Store) Samples() []Sample {
	s.sortSamples()
	return s.samples
}

// MaxTime returns the newest event timestamp in the store.
func (s *Store) MaxTime() (time.Time, bool) {
	s.sortSamples()
	if len(s.samples) == 0 {
		return time.Time{}, false
	}
	return s.samples[len(s.samples)-1].T, true
}

func matchesAll(m []Matcher, l Labels) bool {
	for _, mt := range m {
		if !mt.matches(l) {
			return false
		}
	}
	return true
}

// Instant returns, per matching series, the latest sample with T <= t.
func (s *Store) Instant(metric string, matchers []Matcher, t time.Time) []Sample {
	s.sortSamples()
	latest := map[string]int{} // series key -> index into s.samples
	for i, smp := range s.samples {
		if smp.T.After(t) {
			break
		}
		if smp.Metric != metric || !matchesAll(matchers, smp.Labels) {
			continue
		}
		latest[seriesKey(smp)] = i
	}
	out := make([]Sample, 0, len(latest))
	for _, i := range latest {
		out = append(out, s.samples[i])
	}
	sort.Slice(out, func(i, j int) bool { return seriesKey(out[i]) < seriesKey(out[j]) })
	return out
}

// Window returns matching samples with start < T <= end (left-open, like
// PromQL range selectors), grouped per series and sorted by time.
func (s *Store) Window(metric string, matchers []Matcher, start, end time.Time) [][]Sample {
	s.sortSamples()
	bySeries := map[string][]Sample{}
	var order []string
	for _, smp := range s.samples {
		if smp.T.After(end) {
			break
		}
		if !smp.T.After(start) {
			continue
		}
		if smp.Metric != metric || !matchesAll(matchers, smp.Labels) {
			continue
		}
		k := seriesKey(smp)
		if _, ok := bySeries[k]; !ok {
			order = append(order, k)
		}
		bySeries[k] = append(bySeries[k], smp)
	}
	sort.Strings(order)
	out := make([][]Sample, 0, len(order))
	for _, k := range order {
		out = append(out, bySeries[k])
	}
	return out
}

func seriesKey(smp Sample) string {
	return smp.Metric + Labels(smp.Labels).Key()
}
