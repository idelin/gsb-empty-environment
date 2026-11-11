package lease

// opType is the kind of a journaled state transition.
type opType string

const (
	opProvision opType = "provision"
	opLease     opType = "lease"
	opRelease   opType = "release"
	opHandoff   opType = "handoff"
	opConsume   opType = "consume"
)

// record is one append-only journal entry. The lease ID ties every
// transition to the exact acquire that created it, which is what makes
// cross-key returns and stale-token replays detectable during replay.
type record struct {
	Op       opType     `json:"op"`
	Key      KeyVersion `json:"key"`
	Slot     uint64     `json:"slot"`
	LeaseID  string     `json:"lease_id,omitempty"`
	Instance string     `json:"instance,omitempty"`
}
