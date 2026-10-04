package logasm

import (
	"encoding/json"
	"regexp"
)

// snapshot 是 Mux 的可序列化状态：半行、待定记录、重排缓冲、
// 序号游标、行号归属与已闭合记录全部保留，恢复后可无缝续跑。
type snapshot struct {
	Sources []sourceSnap `json:"sources"`
}

type sourceSnap struct {
	Source             string         `json:"source"`
	Epoch              uint64         `json:"epoch"`
	NextSeq            uint64         `json:"next_seq"`
	MaxSeen            uint64         `json:"max_seen"`
	Started            bool           `json:"started"`
	Reorder            []Line         `json:"reorder,omitempty"`
	Partial            string         `json:"partial,omitempty"`
	Pending            *Record        `json:"pending,omitempty"`
	LineNo             int            `json:"line_no"`
	RecSeq             int            `json:"rec_seq"`
	StartPattern       string         `json:"start_pattern"`
	ContinuationIndent bool           `json:"continuation_indent"`
	MaxRecordBytes     int            `json:"max_record_bytes"`
	Owner              map[int]string `json:"owner,omitempty"`
	Closed             []Record       `json:"closed,omitempty"`
}

// Snapshot 导出检查点（JSON）。回调函数不可序列化，恢复时重新注入。
func (m *Mux) Snapshot() ([]byte, error) {
	snap := snapshot{}
	for _, k := range m.sortedKeys() {
		st := m.sources[k]
		ss := sourceSnap{
			Source:             st.source,
			Epoch:              st.epoch,
			NextSeq:            st.nextSeq,
			MaxSeen:            st.maxSeen,
			Started:            st.started,
			Partial:            st.asm.partial,
			LineNo:             st.asm.lineNo,
			RecSeq:             st.asm.recSeq,
			ContinuationIndent: st.asm.rules.ContinuationIndent,
			MaxRecordBytes:     st.asm.rules.MaxRecordBytes,
			Owner:              st.owner,
		}
		if st.asm.rules.Start != nil {
			ss.StartPattern = st.asm.rules.Start.String()
		}
		if st.asm.pending != nil {
			p := *st.asm.pending
			ss.Pending = &p
		}
		for _, l := range st.reorder {
			ss.Reorder = append(ss.Reorder, l)
		}
		for _, r := range st.closed {
			ss.Closed = append(ss.Closed, r)
		}
		snap.Sources = append(snap.Sources, ss)
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return nil, err
	}
	m.sink.Emit(Event{Kind: EventCheckpointSaved, Detail: "checkpoint saved"})
	return data, nil
}

// Restore 从检查点重建 Mux。遥测与记录回调由调用方重新提供。
func Restore(data []byte, budget int, sink Sink, onRecord func(Record)) (*Mux, error) {
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	m := NewMux(Rules{}, budget, sink, onRecord)
	for _, ss := range snap.Sources {
		rules := Rules{
			ContinuationIndent: ss.ContinuationIndent,
			MaxRecordBytes:     ss.MaxRecordBytes,
		}
		if ss.StartPattern != "" {
			re, err := regexp.Compile(ss.StartPattern)
			if err != nil {
				return nil, err
			}
			rules.Start = re
		}
		st := &sourceState{
			source:  ss.Source,
			epoch:   ss.Epoch,
			nextSeq: ss.NextSeq,
			maxSeen: ss.MaxSeen,
			started: ss.Started,
			reorder: make(map[uint64]Line),
			owner:   ss.Owner,
			closed:  make(map[string]Record),
		}
		if st.owner == nil {
			st.owner = make(map[int]string)
		}
		st.asm = newAssembler(ss.Source, ss.Epoch, rules, func(r Record) {
			st.closed[r.ID] = r
			m.emitRecord(r)
		}, m.sink.Emit)
		st.asm.partial = ss.Partial
		st.asm.lineNo = ss.LineNo
		st.asm.recSeq = ss.RecSeq
		if ss.Pending != nil {
			p := *ss.Pending
			st.asm.pending = &p
		}
		for _, l := range ss.Reorder {
			st.reorder[l.Seq] = l
		}
		for _, r := range ss.Closed {
			st.closed[r.ID] = r
		}
		m.sources[muxKey(ss.Source, ss.Epoch)] = st
	}
	m.sink.Emit(Event{Kind: EventCheckpointRestored, Detail: "checkpoint restored"})
	return m, nil
}
