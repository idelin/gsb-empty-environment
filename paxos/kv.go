package paxos

import "sync"

// KV is the replicated state machine: a key/value store. Every applied op
// (including noops) advances the applied index so the log and the state
// machine stay slot-aligned. Ops since the last checkpoint are journaled so
// an incremental snapshot is just a cheap slice copy — no full state scan.
type KV struct {
	mu      sync.Mutex
	data    map[string]string
	applied uint64
	base    uint64 // applied index at which the journal starts
	journal []Op
}

func NewKV() *KV {
	return &KV{data: make(map[string]string)}
}

// Apply applies one op and returns the new applied index.
func (kv *KV) Apply(op Op) uint64 {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	switch op.Kind {
	case OpSet:
		kv.data[op.Key] = op.Value
	case OpDel:
		delete(kv.data, op.Key)
	}
	kv.journal = append(kv.journal, op)
	kv.applied++
	return kv.applied
}

func (kv *KV) Get(key string) (string, bool) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	v, ok := kv.data[key]
	return v, ok
}

func (kv *KV) Applied() uint64 {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	return kv.applied
}

// Snapshot returns the applied index and a deep copy of the full state.
func (kv *KV) Snapshot() (uint64, map[string]string) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	state := make(map[string]string, len(kv.data))
	for k, v := range kv.data {
		state[k] = v
	}
	return kv.applied, state
}

// Delta returns the journaled ops since the last ResetJournal: the payload
// of an incremental snapshot covering (from, to].
func (kv *KV) Delta() (from, to uint64, ops []Op) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	ops = make([]Op, len(kv.journal))
	copy(ops, kv.journal)
	return kv.base, kv.applied, ops
}

// ResetJournal drops journaled ops up to the current applied index; called
// after a full checkpoint makes older incrementals redundant.
func (kv *KV) ResetJournal() {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.journal = nil
	kv.base = kv.applied
}

// Restore replaces the whole state (used when loading a snapshot).
func (kv *KV) Restore(index uint64, state map[string]string) {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	kv.data = make(map[string]string, len(state))
	for k, v := range state {
		kv.data[k] = v
	}
	kv.applied = index
	kv.base = index
	kv.journal = nil
}

// Equal reports whether two KVs hold identical state and applied index.
func (kv *KV) Equal(other *KV) bool {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	other.mu.Lock()
	defer other.mu.Unlock()
	if kv.applied != other.applied || len(kv.data) != len(other.data) {
		return false
	}
	for k, v := range kv.data {
		if ov, ok := other.data[k]; !ok || ov != v {
			return false
		}
	}
	return true
}
