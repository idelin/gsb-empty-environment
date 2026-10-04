package logasm

// Kind 是本地合成的遥测事件类型。事件不接入任何监控平台，
// 仅通过 Sink 在本进程内分发，可由测试夹具录制与回放。
type Kind string

const (
	EventRecordEmitted      Kind = "record_emitted"
	EventRecordRevised      Kind = "record_revised"
	EventRecordTruncated    Kind = "record_truncated"
	EventSeqGap             Kind = "seq_gap"
	EventDuplicateDropped   Kind = "duplicate_dropped"
	EventLateRevision       Kind = "late_revision"
	EventLateUnowned        Kind = "late_unowned"
	EventRulesSwapped       Kind = "rules_swapped"
	EventBackpressure       Kind = "backpressure"
	EventFlushIncomplete    Kind = "flush_incomplete"
	EventCheckpointSaved    Kind = "checkpoint_saved"
	EventCheckpointRestored Kind = "checkpoint_restored"
)

// Event 是一条遥测事件，携带来源身份与关联记录 ID。
type Event struct {
	Kind     Kind
	Source   string
	Epoch    uint64
	RecordID string
	Detail   string
}

// Sink 接收遥测事件。
type Sink interface {
	Emit(Event)
}

// Recorder 是最简单的 Sink：把事件按序收集，供夹具回放比对。
type Recorder struct {
	Events []Event
}

func (r *Recorder) Emit(e Event) { r.Events = append(r.Events, e) }

// Kinds 返回事件类型序列，便于断言。
func (r *Recorder) Kinds() []Kind {
	out := make([]Kind, len(r.Events))
	for i, e := range r.Events {
		out[i] = e.Kind
	}
	return out
}

// Count 统计某类事件数量。
func (r *Recorder) Count(k Kind) int {
	n := 0
	for _, e := range r.Events {
		if e.Kind == k {
			n++
		}
	}
	return n
}
