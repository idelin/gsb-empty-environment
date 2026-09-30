package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
)

// 本地合成的遥测事件类型。不接入任何监控平台，事件仅在本地产生、
// 可编码为 JSONL 夹具并回放。
const (
	EvRulesetPublished = "ruleset_published"
	EvSeriesUpserted   = "series_upserted"
	EvSeriesRetracted  = "series_retracted"
	EvConflictDetected = "conflict_detected"
	EvSeriesMerged     = "series_merged"
	EvCacheIsolated    = "cache_isolated"
)

// Event 一条本地遥测事件。操作类事件在 Detail 中携带可回放的载荷。
type Event struct {
	Seq     uint64            `json:"seq"`
	Type    string            `json:"type"`
	Version uint64            `json:"version"`
	Detail  map[string]string `json:"detail,omitempty"`
}

// Telemetry 本地事件日志。
type Telemetry struct {
	mu     sync.Mutex
	events []Event
}

// NewTelemetry 创建空的本地遥测。
func NewTelemetry() *Telemetry {
	return &Telemetry{}
}

// Emit 追加一条事件。
func (t *Telemetry) Emit(typ string, version uint64, detail map[string]string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, Event{
		Seq:     uint64(len(t.events)) + 1,
		Type:    typ,
		Version: version,
		Detail:  detail,
	})
}

// Events 返回全部事件副本。
func (t *Telemetry) Events() []Event {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Event(nil), t.events...)
}

// EncodeJSONL 将事件编码为 JSONL，可保存为回放夹具。
func (t *Telemetry) EncodeJSONL() ([]byte, error) {
	var buf bytes.Buffer
	for _, ev := range t.Events() {
		line, err := json.Marshal(ev)
		if err != nil {
			return nil, err
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes(), nil
}

// DecodeEvents 从 JSONL 夹具解码事件。
func DecodeEvents(data []byte) ([]Event, error) {
	var events []Event
	for i, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var ev Event
		if err := json.Unmarshal(line, &ev); err != nil {
			return nil, fmt.Errorf("fixture line %d: %w", i+1, err)
		}
		events = append(events, ev)
	}
	return events, nil
}

// Replay 在全新引擎上按序回放操作类事件，重建最终状态。
// 派生事件（冲突、合并、缓存隔离）在回放中由引擎重新合成。
func Replay(events []Event) (*Engine, error) {
	e := New(NewTelemetry())
	for _, ev := range events {
		switch ev.Type {
		case EvRulesetPublished:
			var rs RuleSet
			if err := json.Unmarshal([]byte(ev.Detail["ruleset"]), &rs); err != nil {
				return nil, fmt.Errorf("replay seq %d: %w", ev.Seq, err)
			}
			if err := e.PublishSet(&rs); err != nil {
				return nil, fmt.Errorf("replay seq %d: %w", ev.Seq, err)
			}
		case EvSeriesUpserted:
			var s Series
			if err := json.Unmarshal([]byte(ev.Detail["series"]), &s); err != nil {
				return nil, fmt.Errorf("replay seq %d: %w", ev.Seq, err)
			}
			if err := e.Upsert(s); err != nil {
				return nil, fmt.Errorf("replay seq %d: %w", ev.Seq, err)
			}
		case EvSeriesRetracted:
			e.Retract(ev.Detail["id"])
		}
	}
	return e, nil
}
