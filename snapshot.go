package netsec

import "encoding/json"

// SnapshotVersion is bumped only when the on-disk shape of Snapshot changes in
// a backward-incompatible way. New optional fields do not bump it.
const SnapshotVersion = 1

// Snapshot is the persistable form of a query and its decision.
//
// Hosts store snapshots at decision time and re-read them after organization
// migrations or policy edits: because evaluation is a pure function of the
// snapshot input, re-decoding and re-evaluating reproduces the stored verdict
// byte-for-byte at the decision level.
type Snapshot struct {
	Version  int       `json:"version"`
	Input    EvalInput `json:"input"`
	Decision Decision  `json:"decision"`
}

// NewSnapshot pairs a query with its evaluated decision.
func NewSnapshot(in EvalInput, decision *Decision) Snapshot {
	return Snapshot{Version: SnapshotVersion, Input: in, Decision: *decision}
}

// MarshalSnapshot serializes a snapshot deterministically.
func MarshalSnapshot(snap Snapshot) ([]byte, error) {
	return json.Marshal(snap)
}

// UnmarshalSnapshot decodes a previously stored snapshot.
func UnmarshalSnapshot(data []byte) (Snapshot, error) {
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}
