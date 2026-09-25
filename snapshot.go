package kvimport

// snapshotValue is the published state of one key: last value and the
// number of confirmed puts (version).
type snapshotValue struct {
	Value   string `json:"value"`
	Version uint64 `json:"version"`
}

type snapshotMeta struct {
	Records uint64 `json:"records"`
	Rejects uint64 `json:"rejects"`
	Keys    int    `json:"keys"`
	Offset  int64  `json:"offset"`
	Digest  string `json:"digest"`
}

// snapshot is the published store.json document. Map keys are marshaled in
// sorted order by encoding/json, so identical imports produce identical
// bytes.
type snapshot struct {
	Meta snapshotMeta             `json:"meta"`
	Keys map[string]snapshotValue `json:"keys"`
}
