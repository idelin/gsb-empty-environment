package dnswindow

import (
	"fmt"
	"math"
	"sort"
	"testing"
	"time"
)

// oracle recomputes every feature from scratch from the latest accepted
// version of each event. It deliberately shares no aggregation code with the
// Manager: entropy is derived via natural logarithms and all counters are
// rebuilt per sealed window, so any incremental-state bug diverges.
type oracle struct {
	size   time.Duration
	events map[string]Event
	sealed map[time.Time][]Event
}

func newOracle(size time.Duration) *oracle {
	return &oracle{
		size:   size,
		events: make(map[string]Event),
		sealed: make(map[time.Time][]Event),
	}
}

func (o *oracle) windowStart(t time.Time) time.Time {
	return truncateWindowStart(t, o.size)
}

// recompute builds full from-scratch feature rows for one window's events.
func (o *oracle) recompute(events []Event) []GroupFeatures {
	type key struct {
		client, registered string
	}
	type acc struct {
		total, nx, bytes int64
		hist             map[int]int64
		labels           map[string]int64
		freq             [256]int64
	}
	groups := make(map[key]*acc)

	for _, event := range events {
		k := key{event.Client, event.RegisteredDomain}
		state := groups[k]
		if state == nil {
			state = &acc{hist: map[int]int64{}, labels: map[string]int64{}}
			groups[k] = state
		}
		state.total++
		if event.NXDOMAIN {
			state.nx++
		}
		name := trimTrailingDot(event.QueriedName)
		state.bytes += int64(len(name))
		for i := 0; i < len(name); i++ {
			state.freq[name[i]]++
		}
		for _, label := range labelsOf(event.QueriedName) {
			state.hist[len(label)]++
			state.labels[label]++
		}
	}

	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].client != keys[j].client {
			return keys[i].client < keys[j].client
		}
		return keys[i].registered < keys[j].registered
	})

	rows := make([]GroupFeatures, 0, len(keys))
	for _, k := range keys {
		state := groups[k]
		var entropy float64
		if state.bytes > 0 {
			for _, count := range state.freq {
				if count == 0 {
					continue
				}
				p := float64(count) / float64(state.bytes)
				entropy -= p * math.Log(p) / math.Ln2
			}
		}
		hist := make(map[int]int64, len(state.hist))
		for length, count := range state.hist {
			hist[length] = count
		}
		row := GroupFeatures{
			Client:           k.client,
			RegisteredDomain: k.registered,
			TotalEvents:      state.total,
			NXDOMAINEvents:   state.nx,
			LabelLengthHist:  hist,
			DistinctLabels:   int64(len(state.labels)),
			TotalNameBytes:   state.bytes,
			ByteFrequency:    state.freq,
			ByteEntropy:      entropy,
		}
		if state.total > 0 {
			row.NXDOMAINRatio = float64(state.nx) / float64(state.total)
		}
		rows = append(rows, row)
	}
	return rows
}

// seal freezes every window ending at or before t, mirroring Manager.Advance.
func (o *oracle) seal(t time.Time) []WindowResult {
	var starts []time.Time
	seen := map[time.Time]bool{}
	for _, event := range o.events {
		start := o.windowStart(event.Time)
		if !start.Add(o.size).After(t) && !seen[start] {
			seen[start] = true
			starts = append(starts, start)
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Before(starts[j]) })

	results := make([]WindowResult, 0, len(starts))
	for _, start := range starts {
		var members []Event
		for _, event := range o.events {
			if o.windowStart(event.Time).Equal(start) {
				members = append(members, event)
			}
		}
		sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
		o.sealed[start] = append([]Event(nil), members...)
		results = append(results, WindowResult{
			Start:  start,
			End:    start.Add(o.size),
			Groups: o.recompute(members),
		})
	}
	return results
}

func compareGroup(t *testing.T, got, want GroupFeatures, context string) {
	t.Helper()
	if got.Client != want.Client || got.RegisteredDomain != want.RegisteredDomain {
		t.Fatalf("%s: identity (%q,%q) != (%q,%q)", context, got.Client, got.RegisteredDomain, want.Client, want.RegisteredDomain)
	}
	if got.TotalEvents != want.TotalEvents || got.NXDOMAINEvents != want.NXDOMAINEvents {
		t.Fatalf("%s: counts %d/%d != %d/%d", context, got.TotalEvents, got.NXDOMAINEvents, want.TotalEvents, want.NXDOMAINEvents)
	}
	if !approxEqual(got.NXDOMAINRatio, want.NXDOMAINRatio) {
		t.Fatalf("%s: nx ratio %.12f != %.12f", context, got.NXDOMAINRatio, want.NXDOMAINRatio)
	}
	if got.DistinctLabels != want.DistinctLabels {
		t.Fatalf("%s: distinct labels %d != %d", context, got.DistinctLabels, want.DistinctLabels)
	}
	if got.TotalNameBytes != want.TotalNameBytes {
		t.Fatalf("%s: name bytes %d != %d", context, got.TotalNameBytes, want.TotalNameBytes)
	}
	if !approxEqual(got.ByteEntropy, want.ByteEntropy) {
		t.Fatalf("%s: entropy %.12f != %.12f", context, got.ByteEntropy, want.ByteEntropy)
	}
	if fmt.Sprint(got.LabelLengthHist) != fmt.Sprint(want.LabelLengthHist) {
		t.Fatalf("%s: hist %v != %v", context, got.LabelLengthHist, want.LabelLengthHist)
	}
	if got.ByteFrequency != want.ByteFrequency {
		t.Fatalf("%s: byte frequency mismatch", context)
	}
}

func compareResults(t *testing.T, got, want []WindowResult) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sealed %d windows, oracle sealed %d", len(got), len(want))
	}
	for i := range got {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) {
			t.Fatalf("window %d bounds [%v,%v) != [%v,%v)", i, got[i].Start, got[i].End, want[i].Start, want[i].End)
		}
		if len(got[i].Groups) != len(want[i].Groups) {
			t.Fatalf("window %v: %d groups, oracle %d", got[i].Start, len(got[i].Groups), len(want[i].Groups))
		}
		for j := range got[i].Groups {
			compareGroup(t, got[i].Groups[j], want[i].Groups[j],
				fmt.Sprintf("window %v group %d", got[i].Start, j))
		}
	}
}
