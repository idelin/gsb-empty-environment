package logrec

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// snapshotVersion guards the checkpoint format.
const snapshotVersion = 1

// Snapshot serializes the full multiplexer state: every stream's
// partial line buffer, pending (undecided) record, closed-record
// history for late merges, sequence index, buffer accounting and the
// telemetry log. Custom ContinuationIndent functions cannot be
// serialized; restored streams fall back to the default indent rule.
func (m *Multiplexer) Snapshot() ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	snap := snapshot{
		Version:   snapshotVersion,
		Budget:    m.budget,
		Used:      m.used,
		Rules:     rulesJSONFrom(m.rules),
		Telemetry: m.telemetry.Events(),
	}
	for _, a := range m.assemblers {
		as := assemblerSnapshot{
			Source:     a.source,
			Epoch:      a.epoch,
			Rules:      rulesJSONFrom(a.rules),
			LineBuf:    a.lineBuf,
			NextAutoSq: a.nextAutoSq,
			MaxSeq:     a.maxSeq,
			HaveSeq:    a.haveSeq,
			BySeq:      a.bySeq,
		}
		if a.pending != nil {
			as.PendingID = a.pending.ID
		}
		for _, rec := range a.records {
			as.Records = append(as.Records, *rec)
		}
		snap.Streams = append(snap.Streams, as)
	}
	m.telemetry.Emit(Event{Kind: EvCheckpointSaved,
		Detail: fmt.Sprintf("streams=%d used=%d", len(snap.Streams), snap.Used)})
	snap.Telemetry = m.telemetry.Events()
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	return data, nil
}

// Restore rebuilds a multiplexer from a snapshot. Partial lines and
// pending records survive, so feeding can resume exactly where the
// checkpoint was taken.
func Restore(data []byte) (*Multiplexer, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("logrec: bad checkpoint: %w", err)
	}
	if snap.Version != snapshotVersion {
		return nil, fmt.Errorf("logrec: unsupported checkpoint version %d", snap.Version)
	}
	m := NewMultiplexer(
		WithBudget(snap.Budget),
		WithRules(snap.Rules.rules()),
	)
	m.used = snap.Used
	for _, e := range snap.Telemetry {
		m.telemetry.Emit(e)
	}
	for _, as := range snap.Streams {
		a := newAssembler(m, as.Source, as.Epoch, as.Rules.rules())
		a.lineBuf = as.LineBuf
		a.nextAutoSq = as.NextAutoSq
		a.maxSeq = as.MaxSeq
		a.haveSeq = as.HaveSeq
		a.bySeq = as.BySeq
		if a.bySeq == nil {
			a.bySeq = map[uint64]seqRef{}
		}
		for i := range as.Records {
			rec := as.Records[i]
			rec.closed = rec.Reason != ""
			a.records[rec.ID] = &rec
			if rec.ID == as.PendingID {
				a.pending = &rec
			}
		}
		m.assemblers[streamKey{as.Source, as.Epoch}] = a
	}
	m.telemetry.Emit(Event{Kind: EvCheckpointRestore,
		Detail: fmt.Sprintf("streams=%d used=%d", len(snap.Streams), snap.Used)})
	return m, nil
}

type snapshot struct {
	Version   int                 `json:"version"`
	Budget    int64               `json:"budget"`
	Used      int64               `json:"used"`
	Rules     rulesJSON           `json:"rules"`
	Streams   []assemblerSnapshot `json:"streams"`
	Telemetry []Event             `json:"telemetry,omitempty"`
}

type assemblerSnapshot struct {
	Source     string            `json:"source"`
	Epoch      uint64            `json:"epoch"`
	Rules      rulesJSON         `json:"rules"`
	LineBuf    []byte            `json:"line_buf,omitempty"`
	NextAutoSq uint64            `json:"next_auto_seq"`
	MaxSeq     uint64            `json:"max_seq"`
	HaveSeq    bool              `json:"have_seq"`
	PendingID  string            `json:"pending_id,omitempty"`
	Records    []Record          `json:"records,omitempty"`
	BySeq      map[uint64]seqRef `json:"by_seq,omitempty"`
}

type rulesJSON struct {
	Pattern        string `json:"pattern,omitempty"`
	MaxRecordBytes int    `json:"max_record_bytes,omitempty"`
	MaxRecordLines int    `json:"max_record_lines,omitempty"`
}

func rulesJSONFrom(r Rules) rulesJSON {
	j := rulesJSON{MaxRecordBytes: r.MaxRecordBytes, MaxRecordLines: r.MaxRecordLines}
	if r.StartPattern != nil {
		j.Pattern = r.StartPattern.String()
	}
	return j
}

func (j rulesJSON) rules() Rules {
	r := Rules{MaxRecordBytes: j.MaxRecordBytes, MaxRecordLines: j.MaxRecordLines}
	if j.Pattern != "" {
		r.StartPattern = regexp.MustCompile(j.Pattern)
	}
	return r
}
