package kvsm

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

// Snapshot captures the full engine state at a log slice point (LastSeq).
// Crash recovery restores the snapshot and replays log entries with
// Seq > LastSeq, yielding byte-identical state to a full replay.
type Snapshot struct {
	LastSeq  uint64
	NumParts int
	Parts    []map[string]string
	Pending  []*PendingTxn
	Waiting  []Event // deferred commits, keyed by TxnID on restore
}

// Snapshot captures engine state at the current applied sequence.
func (e *Engine) Snapshot() *Snapshot {
	parts := make([]map[string]string, e.numParts)
	for i, kvs := range e.parts {
		cp := make(map[string]string, len(kvs))
		for k, v := range kvs {
			cp[k] = v
		}
		parts[i] = cp
	}
	pending := make([]*PendingTxn, 0, len(e.pending))
	for _, txn := range e.pending {
		pending = append(pending, txn)
	}
	waiting := make([]Event, 0, len(e.waiting))
	for _, ev := range e.waiting {
		waiting = append(waiting, ev)
	}
	return &Snapshot{
		LastSeq:  e.nextSeq - 1,
		NumParts: e.numParts,
		Parts:    parts,
		Pending:  pending,
		Waiting:  waiting,
	}
}

// Restore builds an engine from a snapshot. The engine expects the next
// event at LastSeq+1, so replaying the log suffix resumes exactly.
func Restore(snap *Snapshot, maxBuf, maxPending int) *Engine {
	e := NewEngine(snap.NumParts, maxBuf, maxPending)
	for i, kvs := range snap.Parts {
		for k, v := range kvs {
			e.parts[i][k] = v
		}
	}
	for _, txn := range snap.Pending {
		e.pending[txn.ID] = txn
	}
	for _, ev := range snap.Waiting {
		e.waiting[ev.TxnID] = ev
	}
	e.nextSeq = snap.LastSeq + 1
	return e
}

// SaveSnapshot writes a snapshot as JSON (stdlib only).
func SaveSnapshot(path string, snap *Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0600)
}

// LoadSnapshot reads a snapshot written by SaveSnapshot.
func LoadSnapshot(path string) (*Snapshot, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, err
	}
	return &snap, nil
}

// SaveLog persists the retained log events as JSON alongside a snapshot.
func SaveLog(path string, events []Event) error {
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0600)
}

// LoadLog reads events persisted by SaveLog.
func LoadLog(path string) ([]Event, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, err
	}
	return events, nil
}

// fileExists reports whether path exists (small helper for demos/tests).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
