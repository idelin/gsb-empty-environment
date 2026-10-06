package kvindex

import "sort"

// model is a naive per-key history reference implementation used to verify
// the index. Every mutation that affects a key is appended to that key's
// event list; visibility is decided by replaying the events with the highest
// sequence number at or below the snapshot.
type model struct {
	history map[string][]modelEvent
	keys    map[string]bool // keys that have at least one point write
}

type modelEvent struct {
	seq   uint64
	put   bool
	value string
}

func newModel() *model {
	return &model{
		history: make(map[string][]modelEvent),
		keys:    make(map[string]bool),
	}
}

func (m *model) apply(op Op) {
	switch op.Kind {
	case OpPut:
		m.history[op.Key] = append(m.history[op.Key], modelEvent{seq: op.Seq, put: true, value: op.Value})
		m.keys[op.Key] = true
	case OpDeleteRange:
		for key := range m.keys {
			if op.Start <= key && key < op.End {
				m.history[key] = append(m.history[key], modelEvent{seq: op.Seq})
			}
		}
	}
}

func (m *model) getAt(key string, snapshot uint64) (string, bool) {
	var best modelEvent
	found := false
	for _, ev := range m.history[key] {
		if ev.seq <= snapshot && (!found || ev.seq > best.seq) {
			best = ev
			found = true
		}
	}
	if !found || !best.put {
		return "", false
	}
	return best.value, true
}

func (m *model) scanAt(start, end string, snapshot uint64) []KV {
	var keys []string
	for key := range m.keys {
		if start <= key && key < end {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var out []KV
	for _, key := range keys {
		if value, ok := m.getAt(key, snapshot); ok {
			out = append(out, KV{Key: key, Value: value})
		}
	}
	return out
}
