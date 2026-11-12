package netsec

import (
	"math/rand"
	"sort"
	"testing"
	"time"
)

// oracleInstance is the oracle's independently maintained per-instance state.
type oracleInstance struct {
	start  time.Time
	source Endpoint
	target Endpoint
	state  instanceState
}

type oracleQuery struct {
	query  Query
	frozen bool
}

// oracle is an independent, deliberately simple reference implementation: it
// keeps every accepted instance and answers every query with a full table
// scan, mirroring the same state-machine, watermark and window rules.
type oracle struct {
	watermark time.Time
	instances map[InstanceID]oracleInstance
	queries   map[QueryID]oracleQuery
	nextID    QueryID
}

func newOracle() *oracle {
	return &oracle{
		instances: make(map[InstanceID]oracleInstance),
		queries:   make(map[QueryID]oracleQuery),
	}
}

func (o *oracle) ingest(e Event) IngestStatus {
	id := InstanceID{Generation: e.Generation, Instance: e.Instance}
	if !e.valid() {
		return IngestInvalid
	}
	existing, known := o.instances[id]
	if known {
		if !(e.Start.Equal(existing.start) && e.Source == existing.source && e.Target == existing.target) {
			return IngestConflict
		}
		late := e.Start.Before(o.watermark)
		if late {
			if nextState(e.Kind, existing.state) != existing.state {
				return IngestLate
			}
			return IngestDuplicate
		}
		if nextState(e.Kind, existing.state) == existing.state {
			return IngestDuplicate
		}
		existing.state = nextState(e.Kind, existing.state)
		o.instances[id] = existing
		return IngestDuplicate
	}
	if e.Start.Before(o.watermark) {
		return IngestLate
	}
	o.instances[id] = oracleInstance{
		start:  e.Start,
		source: e.Source,
		target: e.Target,
		state:  nextState(e.Kind, stateOpen),
	}
	return IngestAccepted
}

func (o *oracle) advance(w time.Time) {
	if w.After(o.watermark) {
		o.watermark = w
	}
	for id, qq := range o.queries {
		if !qq.frozen && !w.Before(qq.query.At) {
			qq.frozen = true
			o.queries[id] = qq
		}
	}
}

func (o *oracle) register(q Query) QueryID {
	o.nextID++
	id := o.nextID
	frozen := !o.watermark.Before(q.At)
	o.queries[id] = oracleQuery{query: q, frozen: frozen}
	return id
}

func (o *oracle) snapshot(id QueryID) (map[alertKey]int, bool) {
	qq, ok := o.queries[id]
	if !ok {
		return nil, false
	}
	table := make(map[InstanceID]*instance, len(o.instances))
	for key, val := range o.instances {
		val := val
		table[key] = &instance{
			start:  val.start,
			source: val.source,
			target: val.target,
			state:  val.state,
		}
	}
	alerts := evaluate(table, qq.query)
	out := make(map[alertKey]int, len(alerts))
	for key, alert := range alerts {
		out[key] = alert.Count
	}
	return out, qq.frozen
}

func detectorAlertMap(t *testing.T, d *Detector, id QueryID) (map[alertKey]int, QuerySnapshot) {
	t.Helper()
	snap, ok := d.Snapshot(id)
	if !ok {
		t.Fatalf("detector lost query %d", id)
	}
	out := make(map[alertKey]int, len(snap.Alerts))
	for _, alert := range snap.Alerts {
		out[alertKey{kind: alert.Kind, source: alert.Source, host: alert.Host, port: alert.Port}] = alert.Count
	}
	return out, snap
}

func TestDifferentialAgainstFullRecomputeOracle(t *testing.T) {
	const iterations = 12
	const eventCount = 1500

	for seed := int64(1); seed <= iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		d := NewDetector()
		o := newOracle()

		type poolEntry struct {
			id     InstanceID
			start  time.Time
			source Endpoint
			target Endpoint
		}
		var pool []poolEntry
		newEntry := func() poolEntry {
			gen := uint64(rng.Intn(4))
			inst := uint64(rng.Intn(40))
			start := t0.Add(time.Duration(rng.Intn(40)) * time.Second)
			source := Endpoint{
				Addr: "10.0.0." + string(rune('1'+rng.Intn(4))),
				Port: uint16(30000 + rng.Intn(2000)),
			}
			target := Endpoint{
				Addr: "192.0.2." + string(rune('1'+rng.Intn(5))),
				Port: uint16(20 + rng.Intn(6)),
			}
			return poolEntry{
				id:     InstanceID{Generation: gen, Instance: inst},
				start:  start,
				source: source,
				target: target,
			}
		}
		for i := 0; i < 60; i++ {
			pool = append(pool, newEntry())
		}

		registerQuery := func() QueryID {
			q := Query{
				Window:        time.Duration(2+rng.Intn(20)) * time.Second,
				PortThreshold: 1 + rng.Intn(4),
				HostThreshold: 1 + rng.Intn(4),
				At:            t0.Add(time.Duration(rng.Intn(45)) * time.Second),
			}
			id, err := d.RegisterQuery(q)
			if err != nil {
				t.Fatalf("seed %d: valid query rejected: %v", seed, err)
			}
			oid := o.register(q)
			if id != oid {
				t.Fatalf("seed %d: query id mismatch %d vs %d", seed, id, oid)
			}
			return id
		}

		var queryIDs []QueryID
		for i := 0; i < 4; i++ {
			queryIDs = append(queryIDs, registerQuery())
		}

		for i := 0; i < eventCount; i++ {
			entry := pool[rng.Intn(len(pool))]
			kind := []EventKind{EventStart, EventFailure, EventSuccess}[rng.Intn(3)]

			e := Event{
				Kind:       kind,
				Generation: entry.id.Generation,
				Instance:   entry.id.Instance,
				Start:      entry.start,
				Source:     entry.source,
				Target:     entry.target,
			}
			// Occasionally mutate metadata to exercise conflict rejection.
			if rng.Intn(20) == 0 {
				switch rng.Intn(3) {
				case 0:
					e.Target.Port++
				case 1:
					e.Start = e.Start.Add(time.Second)
				case 2:
					e.Source.Addr = "10.0.9.9"
				}
			}

			got := d.Ingest(e).Status
			want := o.ingest(e)
			if got != want {
				t.Fatalf("seed %d event %d %+v: status %v, oracle %v", seed, i, e, got, want)
			}

			if rng.Intn(10) == 0 {
				w := t0.Add(time.Duration(rng.Intn(45)) * time.Second)
				d.AdvanceWatermark(w)
				o.advance(w)
			}
			if rng.Intn(30) == 0 {
				queryIDs = append(queryIDs, registerQuery())
			}
			if rng.Intn(25) == 0 && len(queryIDs) > 0 {
				idx := rng.Intn(len(queryIDs))
				id := queryIDs[idx]
				gotAlerts, snap := detectorAlertMap(t, d, id)
				wantAlerts, wantFrozen := o.snapshot(id)
				if snap.Frozen != wantFrozen {
					t.Fatalf("seed %d: query %d frozen %v, oracle %v", seed, id, snap.Frozen, wantFrozen)
				}
				if !alertMapsEqual(gotAlerts, wantAlerts) {
					t.Fatalf("seed %d: query %d alerts %v, oracle %v", seed, id, gotAlerts, wantAlerts)
				}
			}
		}

		for _, id := range queryIDs {
			gotAlerts, snap := detectorAlertMap(t, d, id)
			wantAlerts, wantFrozen := o.snapshot(id)
			if snap.Frozen != wantFrozen {
				t.Fatalf("seed %d final: query %d frozen mismatch", seed, id)
			}
			if !alertMapsEqual(gotAlerts, wantAlerts) {
				t.Fatalf("seed %d final: query %d alerts %v, oracle %v", seed, id, gotAlerts, wantAlerts)
			}
		}
	}
}

func alertMapsEqual(a, b map[alertKey]int) bool {
	if len(a) != len(b) {
		return false
	}
	for key, count := range a {
		if b[key] != count {
			return false
		}
	}
	return true
}

// TestDeterministicFullRecompute covers the three explicitly required audit
// scenarios: a success arriving late, instance-number reuse across generations,
// and differing two-endpoint identities.
func TestDeterministicFullRecompute(t *testing.T) {
	d := NewDetector()
	o := newOracle()

	events := []Event{
		// Generation 1, instance 7: failed then a late success is rejected.
		failAt(1, "10.0.0.1", 4000, "192.0.2.1", 22),
		failAt(2, "10.0.0.1", 4001, "192.0.2.1", 23),
		// Generation 2 reuses instance 7 with entirely different endpoints.
		ev(EventFailure, 2, 7, t0.Add(3*time.Second), "10.0.0.2", 5000, "192.0.2.2", 443),
		// A two-endpoint-distinct failure on another host/port.
		failAt(4, "10.0.0.1", 4002, "192.0.2.3", 80),
		// A second failed target host on port 443 drives the port-target sweep.
		ev(EventFailure, 2, 8, t0.Add(5*time.Second), "10.0.0.2", 5001, "192.0.2.4", 443),
	}
	for _, e := range events {
		if got, want := d.Ingest(e).Status, o.ingest(e); got != want {
			t.Fatalf("ingest mismatch for %+v: %v vs %v", e, got, want)
		}
	}

	q := Query{Window: time.Minute, PortThreshold: 2, HostThreshold: 2, At: t0.Add(time.Minute)}
	id, _ := d.RegisterQuery(q)
	oid := o.register(q)

	// Move watermark past the first start, then attempt its success override.
	w := t0.Add(2 * time.Second)
	d.AdvanceWatermark(w)
	o.advance(w)
	lateSuccess := successOf(events[0])
	if got, want := d.Ingest(lateSuccess).Status, o.ingest(lateSuccess); got != want || got != IngestLate {
		t.Fatalf("late success = %v / %v, want late", got, want)
	}

	got, snap := detectorAlertMap(t, d, id)
	want, frozen := o.snapshot(oid)
	if snap.Frozen != frozen || !alertMapsEqual(got, want) {
		t.Fatalf("deterministic recompute mismatch: %v vs %v (frozen %v/%v)", got, want, snap.Frozen, frozen)
	}

	keys := make([]alertKey, 0, len(got))
	for key := range got {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return alertKeyOrder(keys[i], keys[j]) < 0 })
	if len(keys) < 2 {
		t.Fatalf("expected both sweep signals, got %d alerts: %v", len(keys), got)
	}
	var kinds []AlertKind
	for _, key := range keys {
		kinds = append(kinds, key.kind)
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if kinds[0] != AlertHostPortSweep || kinds[1] != AlertPortTargetSweep {
		t.Fatalf("expected host-port and port-target sweeps, got kinds %v", kinds)
	}
}

func alertKeyOrder(a, b alertKey) int {
	if a.source != b.source {
		if a.source < b.source {
			return -1
		}
		return 1
	}
	if a.host != b.host {
		if a.host < b.host {
			return -1
		}
		return 1
	}
	if a.port != b.port {
		if a.port < b.port {
			return -1
		}
		return 1
	}
	return 0
}
