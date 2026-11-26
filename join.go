package trijoin

import (
	"fmt"
	"sort"
)

type tripleKey struct {
	key string
	id  OutputID
}

type outputState struct {
	key    string
	id     OutputID
	comps  [3]Component
	counts [3]int64
	weight int64
}

// Join is a stateful three-way interval join operator.
//
// Join is not safe for concurrent use. Callers that share one Join should
// serialize calls.
type Join struct {
	cfg Config

	hasWatermark bool
	watermark    int64
	bounds       [3]int64

	streams [3]*streamState

	outputs  map[tripleKey]*outputState
	byRecord [3]map[recordID]map[tripleKey]struct{}

	cleanedRows int64
	lateDrops   int64
}

// New constructs a Join. It panics if any offset is negative.
func New(d1, d2, d3, d4 int64) *Join {
	return NewWithConfig(Config{D1: d1, D2: d2, D3: d3, D4: d4})
}

// NewWithConfig constructs a Join. It panics if any offset is negative.
func NewWithConfig(cfg Config) *Join {
	if cfg.D1 < 0 || cfg.D2 < 0 || cfg.D3 < 0 || cfg.D4 < 0 {
		panic(fmt.Sprintf("trijoin: negative interval offset in %+v", cfg))
	}
	j := &Join{
		cfg:     cfg,
		outputs: make(map[tripleKey]*outputState),
	}
	for i := range j.streams {
		j.streams[i] = newStreamState()
		j.byRecord[i] = make(map[recordID]map[tripleKey]struct{})
	}
	return j
}

// ApplyBatch processes a batch of changelog and watermark events in order.
// Returned deltas are compressed across the entire batch.
func (j *Join) ApplyBatch(events []Event) []Delta {
	acc := newAccumulator()
	for _, event := range events {
		switch event.Kind {
		case EventRecord:
			acc.addAll(j.applyRecord(event.Record))
		case EventWatermark:
			j.advanceWatermark(event.Watermark)
		}
	}
	return acc.finish()
}

// Apply is a variadic convenience wrapper around ApplyBatch.
func (j *Join) Apply(events ...Event) []Delta {
	return j.ApplyBatch(events)
}

// Process applies one changelog row.
func (j *Join) Process(record Record) []Delta {
	return j.ApplyBatch([]Event{RecordEvent(record)})
}

// AdvanceWatermark updates the global watermark and cleans per-stream input
// state. A watermark that does not advance is ignored.
func (j *Join) AdvanceWatermark(w int64) {
	j.advanceWatermark(w)
}

func (j *Join) advanceWatermark(w int64) {
	if j.hasWatermark && w <= j.watermark {
		return
	}
	j.hasWatermark = true
	j.watermark = w
	j.bounds = [3]int64{
		safeSub(w, j.cfg.D2),
		safeSub(w, min64(j.cfg.D1, j.cfg.D4)),
		safeSub(w, j.cfg.D3),
	}
	for i := range j.streams {
		j.cleanedRows += int64(j.streams[i].clean(j.bounds[i]))
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func (j *Join) applyRecord(record Record) []Delta {
	if record.Weight == 0 {
		return nil
	}
	if !record.Stream.valid() {
		return nil
	}
	idx := int(record.Stream)
	if j.hasWatermark && record.Time < j.bounds[idx] {
		j.lateDrops++
		return nil
	}

	id := recordID{stream: record.Stream, key: record.Key, rowID: record.RowID}
	stream := j.streams[idx]
	row := stream.rows[id]
	if row == nil {
		row = &stateRow{
			id:      id,
			time:    record.Time,
			payload: record.Payload,
		}
		stream.insert(row)
	}

	oldActive := activeCount(row.count)
	row.count += record.Weight
	newActive := activeCount(row.count)

	acc := newAccumulator()
	if oldActive != newActive {
		j.updateExistingOutputs(idx, row, newActive, acc)
	}
	if oldActive == 0 && newActive > 0 {
		j.addNewOutputs(idx, row, acc)
	}
	return acc.finish()
}

func (j *Join) updateExistingOutputs(idx int, row *stateRow, active int64, acc *accumulator) {
	byID := j.byRecord[idx][row.id]
	comp := componentFromRow(row)
	for tk := range byID {
		entry := j.outputs[tk]
		if entry == nil {
			continue
		}
		oldWeight := entry.weight
		entry.counts[idx] = active
		entry.comps[idx] = comp
		newWeight := entry.counts[0] * entry.counts[1] * entry.counts[2]
		entry.weight = newWeight
		if newWeight != oldWeight {
			acc.add(entry, newWeight-oldWeight)
		}
	}
}

func (j *Join) addNewOutputs(idx int, row *stateRow, acc *accumulator) {
	switch idx {
	case int(StreamA):
		j.addWithA(row, acc)
	case int(StreamB):
		j.addWithB(row, acc)
	case int(StreamC):
		j.addWithC(row, acc)
	}
}

func (j *Join) addWithA(a *stateRow, acc *accumulator) {
	bRows := j.streams[int(StreamB)].rangeActive(
		a.id.key,
		safeSub(a.time, j.cfg.D1),
		safeAdd(a.time, j.cfg.D2),
	)
	for _, b := range bRows {
		cRows := j.streams[int(StreamC)].rangeActive(
			a.id.key,
			safeSub(b.time, j.cfg.D3),
			safeAdd(b.time, j.cfg.D4),
		)
		for _, c := range cRows {
			j.ensureOutput(a, b, c, acc)
		}
	}
}

func (j *Join) addWithB(b *stateRow, acc *accumulator) {
	aRows := j.streams[int(StreamA)].rangeActive(
		b.id.key,
		safeSub(b.time, j.cfg.D2),
		safeAdd(b.time, j.cfg.D1),
	)
	cRows := j.streams[int(StreamC)].rangeActive(
		b.id.key,
		safeSub(b.time, j.cfg.D3),
		safeAdd(b.time, j.cfg.D4),
	)
	for _, a := range aRows {
		for _, c := range cRows {
			j.ensureOutput(a, b, c, acc)
		}
	}
}

func (j *Join) addWithC(c *stateRow, acc *accumulator) {
	bRows := j.streams[int(StreamB)].rangeActive(
		c.id.key,
		safeSub(c.time, j.cfg.D4),
		safeAdd(c.time, j.cfg.D3),
	)
	for _, b := range bRows {
		aRows := j.streams[int(StreamA)].rangeActive(
			c.id.key,
			safeSub(b.time, j.cfg.D2),
			safeAdd(b.time, j.cfg.D1),
		)
		for _, a := range aRows {
			j.ensureOutput(a, b, c, acc)
		}
	}
}

func (j *Join) ensureOutput(a, b, c *stateRow, acc *accumulator) {
	tk := tripleKey{
		key: a.id.key,
		id: OutputID{
			ARow: a.id.rowID,
			BRow: b.id.rowID,
			CRow: c.id.rowID,
		},
	}
	if _, ok := j.outputs[tk]; ok {
		// An existing triple is already updated by updateExistingOutputs when
		// the row that caused this enumeration changed its active count.
		return
	}

	entry := &outputState{
		key: tk.key,
		id:  tk.id,
		comps: [3]Component{
			componentFromRow(a),
			componentFromRow(b),
			componentFromRow(c),
		},
		counts: [3]int64{
			activeCount(a.count),
			activeCount(b.count),
			activeCount(c.count),
		},
	}
	entry.weight = entry.counts[0] * entry.counts[1] * entry.counts[2]
	j.outputs[tk] = entry
	for i, row := range []*stateRow{a, b, c} {
		byID := j.byRecord[i][row.id]
		if byID == nil {
			byID = make(map[tripleKey]struct{})
			j.byRecord[i][row.id] = byID
		}
		byID[tk] = struct{}{}
	}
	if entry.weight != 0 {
		acc.add(entry, entry.weight)
	}
}

func componentFromRow(row *stateRow) Component {
	return Component{
		Stream:  row.id.stream,
		RowID:   row.id.rowID,
		Time:    row.time,
		Payload: row.payload,
	}
}

// Snapshot returns the current materialized output relation. Triples whose net
// weight is zero are omitted.
func (j *Join) Snapshot() []Delta {
	out := make([]Delta, 0, len(j.outputs))
	for _, entry := range j.outputs {
		if entry.weight == 0 {
			continue
		}
		out = append(out, deltaFromEntry(entry, entry.weight))
	}
	sortDeltas(out)
	return out
}

// Stats returns current operator counters.
func (j *Join) Stats() Stats {
	stats := Stats{
		HasWatermark: j.hasWatermark,
		Watermark:    j.watermark,
		CleanedRows:  j.cleanedRows,
		LateDrops:    j.lateDrops,
	}
	if j.hasWatermark {
		stats.SafeLowerBound = j.bounds
	}
	for i, stream := range j.streams {
		stats.TrackedRows[i] = len(stream.rows)
		for _, row := range stream.rows {
			if row.count > 0 {
				stats.ActiveRows[i]++
			}
		}
	}
	for _, entry := range j.outputs {
		if entry.weight > 0 {
			stats.OutputTriples++
			stats.OutputWeight += entry.weight
		}
	}
	return stats
}

type accumulator struct {
	entries map[tripleKey]*Delta
}

func newAccumulator() *accumulator {
	return &accumulator{entries: make(map[tripleKey]*Delta)}
}

func (a *accumulator) addAll(deltas []Delta) {
	for _, d := range deltas {
		tk := tripleKey{key: d.Key, id: d.ID}
		entry := a.entries[tk]
		if entry == nil {
			copy := d
			a.entries[tk] = &copy
			continue
		}
		entry.Weight += d.Weight
	}
}

func (a *accumulator) add(entry *outputState, weight int64) {
	if weight == 0 {
		return
	}
	tk := tripleKey{key: entry.key, id: entry.id}
	delta := a.entries[tk]
	if delta == nil {
		d := deltaFromEntry(entry, weight)
		a.entries[tk] = &d
		return
	}
	delta.Weight += weight
}

func (a *accumulator) finish() []Delta {
	out := make([]Delta, 0, len(a.entries))
	for _, delta := range a.entries {
		if delta.Weight == 0 {
			continue
		}
		out = append(out, *delta)
	}
	sortDeltas(out)
	return out
}

func deltaFromEntry(entry *outputState, weight int64) Delta {
	return Delta{
		Key:    entry.key,
		ID:     entry.id,
		A:      entry.comps[0],
		B:      entry.comps[1],
		C:      entry.comps[2],
		Weight: weight,
	}
}

func sortDeltas(deltas []Delta) {
	sort.Slice(deltas, func(i, j int) bool {
		if deltas[i].Key != deltas[j].Key {
			return deltas[i].Key < deltas[j].Key
		}
		if deltas[i].ID.ARow != deltas[j].ID.ARow {
			return deltas[i].ID.ARow < deltas[j].ID.ARow
		}
		if deltas[i].ID.BRow != deltas[j].ID.BRow {
			return deltas[i].ID.BRow < deltas[j].ID.BRow
		}
		return deltas[i].ID.CRow < deltas[j].ID.CRow
	})
}
