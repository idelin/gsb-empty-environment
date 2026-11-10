package main

import (
	"fmt"
	"sort"
)

// Event is one version of a key.
type Event struct {
	Seq   uint64
	Del   bool
	Value []byte
}

// History accumulates per-key versions across batches, in commit order.
type History struct {
	events    map[string][]Event
	nextSeq   uint64
	started   bool
	Anomalies []string
}

func NewHistory() *History {
	return &History{events: make(map[string][]Event)}
}

// Apply folds one commit into the history. Within a batch, ops apply in
// order, so a put followed by a delete of the same key leaves the delete
// as the last version. Sequence numbers must move forward; gaps or
// regressions are reported but never fabricated into commits.
func (h *History) Apply(b *Batch) {
	if h.started {
		switch {
		case b.Seq < h.nextSeq:
			h.Anomalies = append(h.Anomalies,
				fmt.Sprintf("batch seq %d regresses below expected %d", b.Seq, h.nextSeq))
		case b.Seq > h.nextSeq:
			h.Anomalies = append(h.Anomalies,
				fmt.Sprintf("sequence gap: expected %d, batch starts at %d (%d missing)",
					h.nextSeq, b.Seq, b.Seq-h.nextSeq))
		}
	}
	h.started = true
	h.nextSeq = b.Seq + uint64(b.Count)
	for _, op := range b.Ops {
		k := string(op.Key)
		h.events[k] = append(h.events[k], Event{Seq: op.Seq, Del: op.Del, Value: op.Value})
	}
}

// KeyReport is the query result for one key.
type KeyReport struct {
	Key        string
	FirstWrite Event
	HasFirst   bool
	Last       Event
	Visible    bool // false when the last version is a deletion
}

// Query returns reports for every key matching match, sorted by key.
func (h *History) Query(match func(string) bool) []KeyReport {
	var keys []string
	for k := range h.events {
		if match(k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	reports := make([]KeyReport, 0, len(keys))
	for _, k := range keys {
		evs := h.events[k]
		rep := KeyReport{Key: k, Last: evs[len(evs)-1], Visible: !evs[len(evs)-1].Del}
		for _, e := range evs {
			if !e.Del {
				rep.FirstWrite = e
				rep.HasFirst = true
				break
			}
		}
		reports = append(reports, rep)
	}
	return reports
}
