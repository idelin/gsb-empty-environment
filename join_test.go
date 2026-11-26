package trijoin

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func mustRecord(stream Stream, key string, time, row int64, weight int64) Record {
	return Record{
		Stream:  stream,
		Key:     key,
		Time:    time,
		RowID:   row,
		Payload: fmt.Sprintf("%s:%d", stream, row),
		Weight:  weight,
	}
}

func tripleWeights(deltas []Delta) map[tripleKey]int64 {
	out := make(map[tripleKey]int64, len(deltas))
	for _, delta := range deltas {
		out[tripleKey{key: delta.Key, id: delta.ID}] += delta.Weight
	}
	return out
}

func snapshotWeights(deltas []Delta) map[tripleKey]int64 {
	return tripleWeights(deltas)
}

func applyDeltas(snapshot map[tripleKey]int64, deltas []Delta) {
	for key, weight := range tripleWeights(deltas) {
		snapshot[key] += weight
		if snapshot[key] == 0 {
			delete(snapshot, key)
		}
	}
}

func requireWeights(t *testing.T, got map[tripleKey]int64, want map[tripleKey]int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("weight map size = %d, want %d\ngot:  %v\nwant: %v", len(got), len(want), got, want)
	}
	for key, weight := range want {
		if got[key] != weight {
			t.Fatalf("weight for %+v = %d, want %d", key, got[key], weight)
		}
	}
}

func TestThreeWayJoinAndRetraction(t *testing.T) {
	join := New(2, 3, 1, 2)

	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 11, 3, 1)),
	)
	if got := join.Process(mustRecord(B, "k", 12, 2, 1)); len(got) != 1 || got[0].Weight != 1 {
		t.Fatalf("B insertion deltas = %+v, want one insert", got)
	}
	if got := join.Snapshot(); len(got) != 1 || got[0].ID != (OutputID{ARow: 1, BRow: 2, CRow: 3}) {
		t.Fatalf("snapshot = %+v", got)
	}

	if got := join.Process(mustRecord(B, "k", 12, 2, -1)); len(got) != 1 || got[0].Weight != -1 {
		t.Fatalf("B retraction deltas = %+v, want one retraction", got)
	}
	if got := join.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot after retraction = %+v, want empty", got)
	}
}

func TestIntervalBoundariesAreInclusive(t *testing.T) {
	join := New(2, 3, 1, 2)
	join.Apply(
		RecordEvent(mustRecord(A, "low", 10, 1, 1)),
		RecordEvent(mustRecord(B, "low", 8, 2, 1)),
		RecordEvent(mustRecord(C, "low", 7, 3, 1)),
		RecordEvent(mustRecord(A, "high", 10, 4, 1)),
		RecordEvent(mustRecord(B, "high", 13, 5, 1)),
		RecordEvent(mustRecord(C, "high", 15, 6, 1)),
	)
	want := map[tripleKey]int64{
		{key: "low", id: OutputID{ARow: 1, BRow: 2, CRow: 3}}:  1,
		{key: "high", id: OutputID{ARow: 4, BRow: 5, CRow: 6}}: 1,
	}
	requireWeights(t, snapshotWeights(join.Snapshot()), want)
}

func TestRetractBeforeInsertCancels(t *testing.T) {
	join := New(2, 3, 1, 2)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 11, 3, 1)),
		RecordEvent(mustRecord(B, "k", 12, 2, -1)),
	)
	if got := join.Process(mustRecord(B, "k", 12, 2, 1)); len(got) != 0 {
		t.Fatalf("compensating insertion produced %+v, want no output", got)
	}
	if got := join.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot = %+v, want empty", got)
	}

	// The same behavior holds when the retraction and insertion share a batch.
	join = New(2, 3, 1, 2)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 11, 3, 1)),
		RecordEvent(mustRecord(B, "k", 12, 2, 1)),
	)
	got := join.Apply(
		RecordEvent(mustRecord(B, "k", 12, 2, -1)),
		RecordEvent(mustRecord(B, "k", 12, 2, +1)),
	)
	if len(got) != 0 {
		t.Fatalf("same-batch retract/insert deltas = %+v, want empty", got)
	}
}

func TestSameBatchDeltaCompression(t *testing.T) {
	join := New(2, 3, 1, 2)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 11, 3, 1)),
		RecordEvent(mustRecord(B, "k", 12, 2, 1)),
	)
	got := join.Apply(
		RecordEvent(mustRecord(B, "k", 12, 2, -1)),
		RecordEvent(mustRecord(B, "k", 12, 2, +1)),
		RecordEvent(mustRecord(A, "k", 10, 1, -1)),
	)
	want := []Delta{{
		Key:    "k",
		ID:     OutputID{ARow: 1, BRow: 2, CRow: 3},
		A:      Component{Stream: A, RowID: 1, Time: 10, Payload: "A:1"},
		B:      Component{Stream: B, RowID: 2, Time: 12, Payload: "B:2"},
		C:      Component{Stream: C, RowID: 3, Time: 11, Payload: "C:3"},
		Weight: -1,
	}}
	if len(got) != 1 || got[0].ID != want[0].ID || got[0].Weight != -1 {
		t.Fatalf("compressed deltas = %+v, want %+v", got, want)
	}
}

func TestWatermarkAsymmetricSafeBoundsAndLateDrops(t *testing.T) {
	join := New(5, 4, 2, 1)
	join.AdvanceWatermark(20)
	stats := join.Stats()
	if stats.SafeLowerBound != [3]int64{16, 19, 18} {
		t.Fatalf("safe bounds = %v, want [16 19 18]", stats.SafeLowerBound)
	}

	join.Apply(
		RecordEvent(mustRecord(A, "k", 15, 1, 1)), // late
		RecordEvent(mustRecord(A, "k", 16, 2, 1)), // retained boundary
		RecordEvent(mustRecord(B, "k", 18, 3, 1)), // late
		RecordEvent(mustRecord(B, "k", 19, 4, 1)), // retained boundary
		RecordEvent(mustRecord(C, "k", 17, 5, 1)), // late
		RecordEvent(mustRecord(C, "k", 18, 6, 1)), // retained boundary
	)
	stats = join.Stats()
	if stats.LateDrops != 3 {
		t.Fatalf("late drops = %d, want 3", stats.LateDrops)
	}
	if stats.TrackedRows != [3]int{1, 1, 1} {
		t.Fatalf("tracked rows = %v, want [1 1 1]", stats.TrackedRows)
	}

	// Boundary rows are exactly compatible with a future event at W.
	join.AdvanceWatermark(20)
	if got := join.Snapshot(); len(got) != 1 || got[0].Weight != 1 {
		t.Fatalf("snapshot at boundaries = %+v, want one triple", got)
	}
}

func TestCleanupKeepsMaterializedOutputAndDropsLateRetraction(t *testing.T) {
	join := New(5, 4, 2, 1)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(B, "k", 12, 2, 1)),
		RecordEvent(mustRecord(C, "k", 11, 3, 1)),
	)
	if got := join.Snapshot(); len(got) != 1 {
		t.Fatalf("initial snapshot = %+v", got)
	}

	join.AdvanceWatermark(20)
	stats := join.Stats()
	if stats.CleanedRows != 3 || stats.TrackedRows != [3]int{} {
		t.Fatalf("cleanup stats = %+v, want all three input rows cleaned", stats)
	}
	if got := join.Snapshot(); len(got) != 1 {
		t.Fatalf("cleanup changed materialized output to %+v", got)
	}

	join.Apply(RecordEvent(mustRecord(A, "k", 10, 1, -1)))
	stats = join.Stats()
	if stats.LateDrops != 1 {
		t.Fatalf("late drops = %d, want 1", stats.LateDrops)
	}
	if got := join.Snapshot(); len(got) != 1 {
		t.Fatalf("late retraction changed materialized output to %+v", got)
	}
}

func TestRetainedBStillRetractsOutputWithExpiredAAndC(t *testing.T) {
	join := New(10, 1, 1, 10)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 10, 3, 1)),
		RecordEvent(mustRecord(B, "k", 11, 2, 1)),
	)
	join.AdvanceWatermark(20)
	if got := join.Stats(); got.SafeLowerBound != [3]int64{19, 10, 19} ||
		got.TrackedRows != [3]int{0, 1, 0} {
		t.Fatalf("unexpected cleanup state: %+v", got)
	}

	if got := join.Process(mustRecord(B, "k", 11, 2, -1)); len(got) != 1 || got[0].Weight != -1 {
		t.Fatalf("retained B retraction = %+v, want one negative delta", got)
	}
	if got := join.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot = %+v, want empty", got)
	}
}

func TestAcceptedLateRowCanCatchUpWithExpiredHistory(t *testing.T) {
	join := New(3, 4, 2, 5)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(C, "k", 12, 3, 1)),
	)
	join.AdvanceWatermark(15)
	if got := join.Stats(); got.SafeLowerBound != [3]int64{11, 12, 13} {
		t.Fatalf("safe bounds = %v", got.SafeLowerBound)
	}

	// B at 12 is exactly on its retained boundary. A and C have expired from
	// active state, but their accepted changelog history still forms the triple.
	got := join.Process(mustRecord(B, "k", 12, 2, 1))
	if len(got) != 1 || got[0].Weight != 1 ||
		got[0].ID != (OutputID{ARow: 1, BRow: 2, CRow: 3}) {
		t.Fatalf("late catch-up deltas = %+v", got)
	}
	if join.Stats().LateDrops != 0 {
		t.Fatalf("boundary row was counted late")
	}
}

func TestOneBRetractionCancelsManyTriples(t *testing.T) {
	join := New(3, 3, 3, 3)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(A, "k", 11, 2, 1)),
		RecordEvent(mustRecord(C, "k", 12, 3, 1)),
		RecordEvent(mustRecord(C, "k", 13, 4, 1)),
		RecordEvent(mustRecord(B, "k", 11, 5, 1)),
	)
	if got := join.Snapshot(); len(got) != 4 {
		t.Fatalf("initial triples = %d, want 4", len(got))
	}

	got := join.Process(mustRecord(B, "k", 11, 5, -1))
	if len(got) != 4 {
		t.Fatalf("B retraction emitted %d deltas, want 4", len(got))
	}
	for _, delta := range got {
		if delta.Weight != -1 {
			t.Fatalf("B retraction delta weight = %d, want -1", delta.Weight)
		}
	}
	if got := join.Snapshot(); len(got) != 0 {
		t.Fatalf("snapshot after B retraction = %+v", got)
	}

	// Retract and reinsert B around other changes in one batch. The B
	// retract/insert cancels exactly, while removing one A leaves two net
	// retractions.
	join = New(3, 3, 3, 3)
	join.Apply(
		RecordEvent(mustRecord(A, "k", 10, 1, 1)),
		RecordEvent(mustRecord(A, "k", 11, 2, 1)),
		RecordEvent(mustRecord(C, "k", 12, 3, 1)),
		RecordEvent(mustRecord(C, "k", 13, 4, 1)),
		RecordEvent(mustRecord(B, "k", 11, 5, 1)),
	)
	got = join.Apply(
		RecordEvent(mustRecord(B, "k", 11, 5, -1)),
		RecordEvent(mustRecord(B, "k", 11, 5, +1)),
		RecordEvent(mustRecord(A, "k", 11, 2, -1)),
	)
	if len(got) != 2 {
		t.Fatalf("net deltas = %+v, want two retractions", got)
	}
	for _, delta := range got {
		if delta.Weight != -1 || delta.ID.ARow != 2 || delta.ID.BRow != 5 {
			t.Fatalf("unexpected net delta %+v", delta)
		}
	}
	requireWeights(t, snapshotWeights(join.Snapshot()), map[tripleKey]int64{
		{key: "k", id: OutputID{ARow: 1, BRow: 5, CRow: 3}}: 1,
		{key: "k", id: OutputID{ARow: 1, BRow: 5, CRow: 4}}: 1,
	})
}

type oracleRow struct {
	record Record
	count  int64
}

type oracle struct {
	cfg          Config
	hasWatermark bool
	watermark    int64
	bounds       [3]int64
	rows         map[recordID]*oracleRow
	lateDrops    int64
}

func newOracle(cfg Config) *oracle {
	return &oracle{cfg: cfg, rows: make(map[recordID]*oracleRow)}
}

func (o *oracle) advance(w int64) {
	if o.hasWatermark && w <= o.watermark {
		return
	}
	o.hasWatermark = true
	o.watermark = w
	o.bounds = [3]int64{
		oracleSafeSub(w, o.cfg.D2),
		oracleSafeSub(w, min64(o.cfg.D1, o.cfg.D4)),
		oracleSafeSub(w, o.cfg.D3),
	}
}

func (o *oracle) apply(record Record) {
	if record.Weight == 0 {
		return
	}
	idx := int(record.Stream)
	if o.hasWatermark && record.Time < o.bounds[idx] {
		o.lateDrops++
		return
	}
	id := recordID{stream: record.Stream, key: record.Key, rowID: record.RowID}
	row := o.rows[id]
	if row == nil {
		row = &oracleRow{record: record}
		o.rows[id] = row
	}
	row.count += record.Weight
}

func (o *oracle) snapshot() map[tripleKey]int64 {
	out := make(map[tripleKey]int64)
	var aRows, bRows, cRows []*oracleRow
	for _, row := range o.rows {
		if row.count <= 0 {
			continue
		}
		switch row.record.Stream {
		case A:
			aRows = append(aRows, row)
		case B:
			bRows = append(bRows, row)
		case C:
			cRows = append(cRows, row)
		}
	}
	for _, a := range aRows {
		for _, b := range bRows {
			if a.record.Key != b.record.Key ||
				b.record.Time < a.record.Time-o.cfg.D1 ||
				b.record.Time > a.record.Time+o.cfg.D2 {
				continue
			}
			for _, c := range cRows {
				if b.record.Key != c.record.Key ||
					c.record.Time < b.record.Time-o.cfg.D3 ||
					c.record.Time > b.record.Time+o.cfg.D4 {
					continue
				}
				key := tripleKey{
					key: a.record.Key,
					id: OutputID{
						ARow: a.record.RowID,
						BRow: b.record.RowID,
						CRow: c.record.RowID,
					},
				}
				out[key] += a.count * b.count * c.count
			}
		}
	}
	return out
}

func oracleSafeSub(a, b int64) int64 {
	// Test inputs are tiny, so no saturation is needed here.
	return a - b
}

func TestRandomDifferentialAgainstFullHistoryOracle(t *testing.T) {
	cfg := Config{D1: 3, D2: 4, D3: 2, D4: 5}
	join := NewWithConfig(cfg)
	oracle := newOracle(cfg)
	rng := rand.New(rand.NewSource(20261126))

	times := make(map[recordID]int64)
	nextRow := [3]int64{1, 1, 1}
	now := int64(0)
	expected := make(map[tripleKey]int64)

	for step := 0; step < 250; step++ {
		if rng.Intn(12) == 0 {
			now += int64(rng.Intn(6))
			join.AdvanceWatermark(now)
			oracle.advance(now)
			if join.Stats().LateDrops != oracle.lateDrops {
				t.Fatalf("step %d late drops = %d, oracle %d", step, join.Stats().LateDrops, oracle.lateDrops)
			}
			continue
		}

		stream := Stream(rng.Intn(3))
		var rowID int64
		if rng.Intn(3) == 0 {
			rowID = nextRow[stream]
			nextRow[stream]++
			times[recordID{stream: stream, key: fmt.Sprintf("k%d", rng.Intn(2)), rowID: rowID}] =
				int64(rng.Intn(24))
		} else {
			// Pick any already assigned identity for this stream.
			var ids []recordID
			for id := range times {
				if id.stream == stream {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				rowID = nextRow[stream]
				nextRow[stream]++
				key := fmt.Sprintf("k%d", rng.Intn(2))
				time := int64(rng.Intn(24))
				times[recordID{stream: stream, key: key, rowID: rowID}] = time
			} else {
				sort.Slice(ids, func(i, j int) bool {
					if ids[i].key != ids[j].key {
						return ids[i].key < ids[j].key
					}
					return ids[i].rowID < ids[j].rowID
				})
				id := ids[rng.Intn(len(ids))]
				rowID = id.rowID
			}
			_ = rowID
		}

		// Recreate the selected identity. The loop above may have selected one.
		var id recordID
		found := false
		for candidate := range times {
			if candidate.stream == stream && candidate.rowID == rowID {
				id = candidate
				found = true
				break
			}
		}
		if !found {
			continue
		}
		weight := int64(1)
		if rng.Intn(2) == 0 {
			weight = -1
		}
		record := mustRecord(stream, id.key, times[id], id.rowID, weight)

		deltas := join.Process(record)
		oracle.apply(record)
		applyDeltas(expected, deltas)
		requireWeights(t, expected, snapshotWeights(join.Snapshot()))
		requireWeights(t, expected, oracle.snapshot())
		if join.Stats().LateDrops != oracle.lateDrops {
			t.Fatalf("step %d late drops = %d, oracle %d", step, join.Stats().LateDrops, oracle.lateDrops)
		}
	}
}

func TestBatchDeltasEqualSnapshotTransition(t *testing.T) {
	cfg := Config{D1: 2, D2: 3, D3: 1, D4: 2}
	join := NewWithConfig(cfg)
	oracle := newOracle(cfg)
	expected := make(map[tripleKey]int64)
	rng := rand.New(rand.NewSource(7))

	ids := make([]recordID, 0)
	times := make(map[recordID]int64)
	next := [3]int64{1, 1, 1}
	for round := 0; round < 40; round++ {
		events := make([]Event, 0, 6)
		for n := 0; n < 1+rng.Intn(5); n++ {
			stream := Stream(rng.Intn(3))
			if rng.Intn(3) == 0 || len(ids) == 0 {
				id := recordID{stream: stream, key: fmt.Sprintf("k%d", rng.Intn(2)), rowID: next[stream]}
				next[stream]++
				times[id] = int64(rng.Intn(12))
				ids = append(ids, id)
			}
			id := ids[rng.Intn(len(ids))]
			stream = id.stream
			weight := int64(1)
			if rng.Intn(2) == 0 {
				weight = -1
			}
			events = append(events, RecordEvent(mustRecord(stream, id.key, times[id], id.rowID, weight)))
		}
		deltas := join.ApplyBatch(events)
		for _, event := range events {
			oracle.apply(event.Record)
		}
		applyDeltas(expected, deltas)
		requireWeights(t, expected, snapshotWeights(join.Snapshot()))
		requireWeights(t, expected, oracle.snapshot())
	}
}
